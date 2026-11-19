// Shared e2e verification harness (KLOX-32). The suite runs in exactly
// one of three mutually exclusive modes:
//
//   "local"     — default: Playwright boots the app locally, outbox
//                 transport only. No mail ever leaves the process.
//   "fake-live" — E2E_LIVE_MAIL=1: Playwright also boots the loopback
//                 fake provider (scripts/fake-resend.mjs) and the app
//                 uses KLOXUS_FAKE_RESEND_URL with synthetic
//                 credentials. Live-mail rules (claim, confirmed
//                 readers, caps, ledger) are exercised end to end.
//   "hosted"    — E2E_BASE_URL=<url>: the suite runs against an
//                 already-running target, and ONLY when the target's
//                 own /api/v1/workspace proves the deterministic demo
//                 pipeline plus an outbox transport. A live or unknown
//                 transport fails the preflight before any mail- or
//                 model-capable call — server-side evidence, never a
//                 client flag.
//
// E2E_BASE_URL and E2E_LIVE_MAIL together are a hard error.

export const FAKE_PORT = 8399;
export const FAKE_ORIGIN = `http://127.0.0.1:${FAKE_PORT}`;
export const APP_PORT = 8137;
export const APP_ORIGIN = `http://localhost:${APP_PORT}`;

export function resolveMode(env = process.env) {
  if (env.E2E_BASE_URL && env.E2E_LIVE_MAIL) {
    throw new Error(
      "E2E_BASE_URL and E2E_LIVE_MAIL are mutually exclusive — " +
        "a hosted target must never run the fake-live suite",
    );
  }
  if (env.E2E_BASE_URL) return "hosted";
  if (env.E2E_LIVE_MAIL === "1") return "fake-live";
  return "local";
}

// Credentials and endpoints a spawned server must never inherit:
// explicit empty strings beat real env vars and (with
// KLOXUS_NO_DOTENV) .env leakage — the verification process gets them
// only from this table.
const SCRUBBED = {
  RESEND_API_KEY: "",
  RESEND_BASE_URL: "",
  RESEND_WEBHOOK_SECRET: "",
  KLOXUS_FAKE_RESEND_URL: "",
  KLOXUS_FROM_ADDRESS: "",
  KLOXUS_PUBLIC_URL: "",
  KLOXUS_MASTER_KEY: "",
  ANTHROPIC_API_KEY: "",
  ABACATEPAY_API_KEY: "",
  FIRECRAWL_API_KEY: "",
};

// appEnv is the complete environment for the spawned app server:
// deterministic pipeline (llm off), .env disabled, every credential
// scrubbed — plus, in fake-live mode only, the synthetic-transport
// endpoint.
export function appEnv(mode) {
  const env = {
    KLOXUS_PORT: String(APP_PORT),
    KLOXUS_DATA_DIR: ".e2e-data",
    KLOXUS_NO_BROWSER: "1",
    KLOXUS_NO_DOTENV: "1",
    KLOXUS_LLM_MODE: "off",
    ...SCRUBBED,
  };
  if (mode === "fake-live") {
    env.KLOXUS_FROM_ADDRESS = "Kloxus E2E <e2e@kloxus.test>";
    env.KLOXUS_FAKE_RESEND_URL = FAKE_ORIGIN;
  }
  return env;
}

// assertTransport enforces the server-side evidence a mode requires.
// ws is the /api/v1/workspace payload. Throws on anything unexpected —
// callers must run this before any mail- or model-capable request.
export function assertTransport(ws, mode) {
  const mail = ws?.mail;
  const transport = ws?.mail_transport;
  const serverMode = ws?.mode;
  const describe = `mail=${mail} mail_transport=${transport} mode=${serverMode}`;
  if (serverMode !== "demo") {
    throw new Error(
      `e2e transport preflight failed: the target must run the deterministic demo pipeline (${describe})`,
    );
  }
  switch (mode) {
    case "hosted":
    case "local":
      if (mail !== "outbox" || transport !== "outbox") {
        throw new Error(
          `e2e transport preflight failed: this suite is outbox-only; refusing a mail-capable target (${describe})`,
        );
      }
      break;
    case "fake-live":
      if (mail !== "live" || transport !== "synthetic") {
        throw new Error(
          `e2e transport preflight failed: fake-live needs the loopback synthetic transport (${describe})`,
        );
      }
      break;
    default:
      throw new Error(`e2e transport preflight failed: unknown suite mode ${mode}`);
  }
}
