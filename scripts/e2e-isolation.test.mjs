// Isolation regression tests for the e2e harness (KLOX-32 remediation):
// conflicting flags, inherited credentials, occupied ports and wrong
// server identity must all fail closed — never silently reuse a server
// or leak a real credential into a verification run.
//
//   node --test scripts/e2e-isolation.test.mjs

import { test } from "node:test";
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { resolveMode, assertTransport, appEnv, FAKE_ORIGIN } from "./e2e-harness.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(here, "..");
const FAKE = path.join(here, "fake-resend.mjs");
const FRONTEND = path.join(ROOT, "frontend");
// Invoke the CLI's JS entry directly — spawning the .cmd shim without a
// shell is blocked on Windows and needs no npm indirection anywhere.
const PW_CLI = path.join(FRONTEND, "node_modules", "playwright", "cli.js");

// startFake spawns fake-resend.mjs and resolves once it reports its
// loopback bind, or rejects on a nonzero exit (e.g. occupied port).
function startFake(port, { expectExit = false } = {}) {
  const env = { ...process.env, FAKE_RESEND_PORT: String(port) };
  const child = spawn(process.execPath, [FAKE], { env });
  let stdout = "", stderr = "";
  child.stdout.on("data", (d) => (stdout += d));
  child.stderr.on("data", (d) => (stderr += d));
  const ready = new Promise((resolve, reject) => {
    child.stdout.on("data", (d) => {
      if (String(d).includes("listening")) resolve(child);
    });
    // 'close' fires after stdio flushes — stderr is complete by then
    child.on("close", (code) => {
      if (expectExit) resolve({ code, stdout, stderr });
      else reject(new Error(`fake exited ${code}: ${stderr}`));
    });
    setTimeout(() => reject(new Error(`fake never answered: ${stdout} ${stderr}`)), 15000);
  });
  return { child, ready, output: () => ({ stdout, stderr }) };
}

async function waitExit(child) {
  return new Promise((resolve) => child.on("exit", resolve));
}

test("conflicting E2E flags are a hard error, never a silent precedence", () => {
  assert.throws(
    () => resolveMode({ E2E_BASE_URL: "https://kloxus.fly.dev", E2E_LIVE_MAIL: "1" }),
    /mutually exclusive/,
  );
  assert.equal(resolveMode({ E2E_BASE_URL: "https://kloxus.fly.dev" }), "hosted");
  assert.equal(resolveMode({ E2E_LIVE_MAIL: "1" }), "fake-live");
  assert.equal(resolveMode({}), "local");
});

test("app env never carries inherited credentials", () => {
  // The spawned server's env holds only the scrubbed/synthetic values —
  // appEnv builds the table, it never copies the parent environment.
  const env = appEnv("local");
  assert.equal(env.RESEND_API_KEY, "");
  assert.equal(env.KLOXUS_FAKE_RESEND_URL, "");
  assert.equal(env.KLOXUS_FROM_ADDRESS, "");
  assert.equal(env.KLOXUS_NO_DOTENV, "1");
  assert.equal(env.KLOXUS_LLM_MODE, "off");
  assert.ok(!("DATABASE_URL" in env), "DATABASE_URL stays caller-provided, never synthesized");

  const live = appEnv("fake-live");
  assert.equal(live.RESEND_API_KEY, ""); // fake never carries a real key
  assert.equal(live.KLOXUS_FAKE_RESEND_URL, FAKE_ORIGIN);
  assert.match(live.KLOXUS_FAKE_RESEND_URL, /^http:\/\/127\.0\.0\.1:/);
});

test("transport preflight fails closed on wrong server identity", () => {
  const liveTarget = { mode: "demo", mail: "live", mail_transport: "resend" };
  for (const mode of ["hosted", "local", "fake-live"]) {
    assert.throws(() => assertTransport(liveTarget, mode), /preflight/);
  }
  // a live-mail server masquerading under a client flag is still refused
  assert.throws(
    () => assertTransport({ mode: "demo", mail: "outbox", mail_transport: "resend" }, "local"),
    /preflight/,
  );
  // missing/unknown fields fail closed — an old or wrong server gives none
  assert.throws(() => assertTransport({ mail: "outbox" }, "hosted"), /preflight/);
  assert.throws(() => assertTransport({}, "hosted"), /preflight/);
  assert.throws(() => assertTransport(null, "local"), /preflight/);
  // the synthetic transport is only accepted in fake-live
  const synthetic = { mode: "demo", mail: "live", mail_transport: "synthetic" };
  assert.throws(() => assertTransport(synthetic, "hosted"), /preflight/);
  assert.throws(() => assertTransport(synthetic, "local"), /preflight/);
  assert.doesNotThrow(() => assertTransport(synthetic, "fake-live"));
  assert.doesNotThrow(() => assertTransport({ mode: "demo", mail: "outbox", mail_transport: "outbox" }, "hosted"));
  assert.doesNotThrow(() => assertTransport({ mode: "demo", mail: "outbox", mail_transport: "outbox" }, "local"));
});

test("fake server binds loopback and records provider calls", async () => {
  const origin = "http://127.0.0.1:8531";
  const { child, ready, output } = startFake(8531);
  try {
    await ready;
    assert.match(output().stdout, /listening on http:\/\/127\.0\.0\.1:8531/);
    const health = await fetch(`${origin}/healthz`);
    assert.equal(health.status, 200);

    const send = await fetch(`${origin}/emails`, {
      method: "POST",
      headers: { "content-type": "application/json", authorization: "Bearer re_local_verify_synthetic" },
      body: JSON.stringify({ from: "Kloxus E2E <e2e@kloxus.test>", to: ["smoke@kloxus.dev"], subject: "Edição 1", text: "body [1](/a/3)" }),
    });
    assert.equal(send.status, 200);
    const receipt = await send.json();
    assert.equal(receipt.id, "re_local_1");

    const reqs = await (await fetch(`${origin}/requests`)).json();
    assert.equal(reqs.count, 1);
    assert.deepEqual(reqs.requests[0].to, ["smoke@kloxus.dev"]);
    assert.equal(reqs.requests[0].subject, "Edição 1");
    assert.equal(reqs.requests[0].auth, "Bearer re_local_verify_synthetic");
  } finally {
    child.kill();
  }
});

test("fake provider failure is a separate, intentional state", async () => {
  const { child, ready } = startFake(8532);
  const origin = "http://127.0.0.1:8532";
  try {
    await ready;
    const call = () =>
      fetch(`${origin}/emails`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ to: ["a@b.c"], subject: "s" }),
      });
    await fetch(`${origin}/__fail`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ on: true }) });
    const failed = await call();
    assert.equal(failed.status, 500);
    await fetch(`${origin}/__fail`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ on: false }) });
    const ok = await call();
    assert.equal(ok.status, 200);
  } finally {
    child.kill();
  }
});

test("an occupied fake port exits nonzero instead of being silently reused", async () => {
  const first = startFake(8533);
  try {
    await first.ready;
    const second = startFake(8533, { expectExit: true });
    const result = await second.ready;
    assert.notEqual(result.code, 0);
    assert.match(result.stderr, /EADDRINUSE|address already in use/i);
  } finally {
    first.child.kill();
    await waitExit(first.child);
  }
});

test("a wrong-identity server fails the preflight, not just the health check", async () => {
  // A server answering /healthz but without the app's workspace
  // contract must be rejected by assertTransport — this is what stops
  // reuseExistingServer-style mistakes.
  const { child, ready } = startFake(8534);
  const origin = "http://127.0.0.1:8534";
  try {
    await ready;
    const res = await fetch(`${origin}/api/v1/workspace`);
    const ws = await res.json(); // the fake answers a provider-style body, not a workspace
    assert.throws(() => assertTransport(ws, "local"), /preflight/);
    assert.throws(() => assertTransport(ws, "fake-live"), /preflight/);
    assert.throws(() => assertTransport(ws, "hosted"), /preflight/);
  } finally {
    child.kill();
  }
});

test("playwright config hard-fails on conflicting flags", (t) => {
  const probe = spawnSync(process.execPath, [PW_CLI, "--version"], { cwd: FRONTEND });
  if (probe.status !== 0) {
    t.skip("playwright not installed — run `npm ci` in frontend first");
    return;
  }
  const res = spawnSync(process.execPath, [PW_CLI, "test", "--list"], {
    cwd: FRONTEND,
    timeout: 120_000,
    env: { ...process.env, E2E_BASE_URL: "https://kloxus.fly.dev", E2E_LIVE_MAIL: "1" },
    encoding: "utf8",
  });
  assert.notEqual(res.status, 0, `expected failure, got stdout: ${res.stdout}`);
  assert.match(String(res.stderr) + String(res.stdout), /mutually exclusive/);
});
