import { defineConfig } from "@playwright/test";
import { appEnv, APP_ORIGIN, FAKE_ORIGIN, FAKE_PORT, resolveMode } from "../scripts/e2e-harness.mjs";

// E2E runs against the real Go server — it serves the embedded build
// under /app. Build the frontend first (npm run build) so the binary
// has real assets; demo mode replays the fixture pipeline.
//
// Three mutually exclusive modes (scripts/e2e-harness.mjs):
//   local     — default: the app boots with the outbox transport only.
//   fake-live — E2E_LIVE_MAIL=1: the app boots against the loopback
//               fake provider (synthetic credentials) so live-mail
//               rules run end to end locally.
//   hosted    — E2E_BASE_URL=<url>: run against a deployed target.
//               Specs preflight the server's own transport evidence and
//               refuse anything but the deterministic demo pipeline
//               with outbox mail — a hosted target can never send.
//
// E2E_BASE_URL and E2E_LIVE_MAIL together are a hard error.
const mode = resolveMode();
const baseURL = process.env.E2E_BASE_URL ?? APP_ORIGIN;

export default defineConfig({
  testDir: "./e2e",
  // Hosted targets run the same journey against remote Postgres — two
  // pipeline runs plus editor ops need a much larger budget than local.
  timeout: mode === "hosted" ? 600_000 : 180_000,
  retries: 0,
  workers: 1,
  use: {
    baseURL,
    locale: "en-US",
  },
  webServer:
    mode === "hosted"
      ? undefined
      : [
          {
            command: "go run ../cmd/kloxus demo",
            url: `${APP_ORIGIN}/healthz`,
            // appEnv is the complete environment: deterministic pipeline,
            // .env disabled, credentials scrubbed — the fake-live branch
            // adds the synthetic loopback endpoint and never a real key.
            env: appEnv(mode),
            // Never adopt an already-running server: a reused process
            // could carry a live transport this suite cannot detect
            // until it sends. An occupied port must fail the run.
            reuseExistingServer: false,
            timeout: 120_000,
          },
          ...(mode === "fake-live"
            ? [
                {
                  command: "node ../scripts/fake-resend.mjs",
                  url: `${FAKE_ORIGIN}/healthz`,
                  env: { FAKE_RESEND_PORT: String(FAKE_PORT) },
                  reuseExistingServer: false,
                  timeout: 30_000,
                },
              ]
            : []),
        ],
});
