// Minimal fake Resend API for local verification — point the app at it
// with RESEND_BASE_URL=http://localhost:8399 so live-mail rules (claim
// gate, confirmed readers, caps, outbox ledger) run end to end with no
// provider key and zero spend. Every request gets a synthetic provider
// id; nothing leaves the machine.
//
//   node scripts/fake-resend.mjs          # listens on :8399
//   FAKE_RESEND_PORT=8410 node scripts/fake-resend.mjs

import { createServer } from "node:http";

const port = Number(process.env.FAKE_RESEND_PORT || 8399);
let n = 0;

createServer((req, res) => {
  if (req.url === "/healthz") {
    res.writeHead(200, { "content-type": "text/plain" });
    res.end("ok\n");
    return;
  }
  req.resume();
  req.on("end", () => {
    n += 1;
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ id: `re_local_${n}` }));
    console.log(`fake-resend: ${req.method} ${req.url} -> re_local_${n}`);
  });
}).listen(port, () => console.log(`fake-resend listening on :${port}`));
