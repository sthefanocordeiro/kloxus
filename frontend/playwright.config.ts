import { defineConfig } from "@playwright/test";

// E2E runs against the real Go server — it serves the embedded build
// under /app. Build the frontend first (npm run build) so the binary
// has real assets; demo mode replays the fixture pipeline.
//
// E2E_BASE_URL points the suite at an already-running server (e.g. the
// Fly deployment) instead of booting a local one — required by the
// deploy-and-verify routine (D35).
const baseURL = process.env.E2E_BASE_URL ?? "http://localhost:8137";

// E2E_LIVE_MAIL=1 boots the local server with mail "live" against a
// fake Resend transport (scripts/fake-resend.mjs via RESEND_BASE_URL):
// the claim gate, confirmed-reader sends and the delivery:"none"
// contract are exercised end to end with zero provider calls. Specs
// branch on the workspace's `mail` field, so the default outbox mode
// keeps working unchanged.
const liveMail = !process.env.E2E_BASE_URL && process.env.E2E_LIVE_MAIL === "1";

export default defineConfig({
  testDir: "./e2e",
  // Hosted targets run the same journey against remote Postgres — two
  // pipeline runs plus editor ops need a much larger budget than local.
  timeout: process.env.E2E_BASE_URL ? 600_000 : 180_000,
  retries: 0,
  workers: 1,
  use: {
    baseURL,
    locale: "en-US",
  },
  webServer: process.env.E2E_BASE_URL
    ? undefined
    : [
        {
          command: "go run ../cmd/kloxus demo",
          url: "http://localhost:8137/healthz",
          env: {
            KLOXUS_PORT: "8137",
            KLOXUS_DATA_DIR: ".e2e-data",
            KLOXUS_NO_BROWSER: "1",
            ...(liveMail
              ? {
                  RESEND_API_KEY: "re_e2e_local",
                  KLOXUS_FROM_ADDRESS: "Kloxus E2E <e2e@kloxus.test>",
                  RESEND_BASE_URL: "http://localhost:8399",
                }
              : {}),
          },
          reuseExistingServer: !process.env.CI,
          timeout: 120_000,
        },
        ...(liveMail
          ? [
              {
                command: "node ../scripts/fake-resend.mjs",
                url: "http://localhost:8399/healthz",
                reuseExistingServer: !process.env.CI,
              },
            ]
          : []),
      ],
});
