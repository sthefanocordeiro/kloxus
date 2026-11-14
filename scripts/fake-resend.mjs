// Minimal fake Resend API for local verification — point the app at it
// with KLOXUS_FAKE_RESEND_URL=http://127.0.0.1:8399 so live-mail rules
// (claim gate, confirmed readers, caps, outbox ledger) run end to end
// with no provider key and zero spend. Loopback only: the fake never
// binds a reachable interface.
//
//   node scripts/fake-resend.mjs          # listens on 127.0.0.1:8399
//   FAKE_RESEND_PORT=8410 node scripts/fake-resend.mjs
//
// Verification surface (for specs, not part of the Resend protocol):
//   GET  /requests   → {count, requests:[{method,path,to,from,subject,auth,idem,text}]}
//   POST /__fail     → body {"on":true|false} — while on, provider calls answer 500
//   GET  /healthz    → "ok"

import { createServer } from "node:http";

const HOST = "127.0.0.1"; // literal loopback — never expose the fake
const port = Number(process.env.FAKE_RESEND_PORT || 8399);
let n = 0;
let failing = false;
const requests = [];

function body(req) {
  return new Promise((resolve) => {
    let s = "";
    req.on("data", (d) => (s += d)).on("end", () => resolve(s));
  });
}

createServer(async (req, res) => {
  const path = (req.url || "").split("?")[0];
  if (req.method === "GET" && path === "/healthz") {
    res.writeHead(200, { "content-type": "text/plain" });
    res.end("ok\n");
    return;
  }
  if (req.method === "GET" && path === "/requests") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ count: requests.length, requests }));
    return;
  }
  if (req.method === "POST" && path === "/__fail") {
    let on = true;
    try {
      on = JSON.parse(await body(req)).on !== false;
    } catch { /* toggle default */ }
    failing = on;
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ failing }));
    return;
  }
  // everything else is a provider call — record it, then answer like
  // the Resend API (or a broken provider while /__fail is on).
  const raw = await body(req);
  let parsed = {};
  try {
    parsed = JSON.parse(raw);
  } catch { /* non-JSON provider call */ }
  n += 1;
  const entry = {
    n,
    method: req.method,
    path,
    auth: req.headers.authorization || "",
    idem: req.headers["idempotency-key"] || "",
    to: parsed.to || null,
    from: parsed.from || null,
    subject: parsed.subject || null,
    text: parsed.text || null,
  };
  requests.push(entry);
  if (failing) {
    res.writeHead(500, { "content-type": "application/json" });
    res.end(JSON.stringify({ statusCode: 500, name: "internal_error", message: "fake provider failure" }));
    console.log(`fake-resend: ${req.method} ${path} -> 500 (forced failure)`);
    return;
  }
  const id = `re_local_${n}`;
  res.writeHead(200, { "content-type": "application/json" });
  res.end(JSON.stringify({ id }));
  console.log(`fake-resend: ${req.method} ${path} -> ${id}`);
}).listen(port, HOST, () => console.log(`fake-resend listening on http://${HOST}:${port}`));
