import { defineConfig } from "@playwright/test";

// E2E runs against the real Go server — it serves the embedded build
// under /app. Build the frontend first (npm run build) so the binary
// has real assets; demo mode replays the fixture pipeline.
//
// E2E_BASE_URL points the suite at an already-running server (e.g. the
// Fly deployment) instead of booting a local one — required by the
// deploy-and-verify routine (D35).
const baseURL = process.env.E2E_BASE_URL ?? "http://localhost:8137";

export default defineConfig({
  testDir: "./e2e",
  timeout: 180_000,
  retries: 0,
  workers: 1,
  use: {
    baseURL,
    locale: "en-US",
  },
  webServer: process.env.E2E_BASE_URL
    ? undefined
    : {
        command: "go run ../cmd/kloxus demo",
        url: "http://localhost:8137/healthz",
        env: {
          KLOXUS_PORT: "8137",
          KLOXUS_DATA_DIR: ".e2e-data",
          KLOXUS_NO_BROWSER: "1",
        },
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
      },
});
