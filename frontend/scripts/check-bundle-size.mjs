// Asserts the "/" landing payload stays under budget (KLOX-16): the
// JS and CSS referenced by the built index.html must gzip to under
// 150 KiB combined. Lazy route chunks load on demand and are not
// measured.
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";

const BUDGET = 150 * 1024;
const dist = join(dirname(fileURLToPath(import.meta.url)), "../../internal/web/appdist");
const html = readFileSync(join(dist, "index.html"), "utf8");

const refs = [
  ...[...html.matchAll(/<script[^>]+src="([^"]+\.js)"/g)].map((m) => m[1]),
  ...[...html.matchAll(/<link[^>]+href="([^"]+\.css)"/g)].map((m) => m[1]),
  ...[...html.matchAll(/<link[^>]+rel="modulepreload"[^>]+href="([^"]+)"/g)].map((m) => m[1]),
];
if (refs.length === 0) {
  console.error("bundle-size: no JS/CSS assets found in index.html");
  process.exit(1);
}

let total = 0;
for (const ref of refs) {
  const raw = readFileSync(join(dist, ref));
  const gz = gzipSync(raw).length;
  total += gz;
  console.log(`bundle-size: ${ref}  ${raw.length} B raw / ${gz} B gzip`);
}

const kib = (n) => (n / 1024).toFixed(1) + " KiB";
console.log(`bundle-size: landing payload ${kib(total)} gzip (budget ${kib(BUDGET)})`);
if (total > BUDGET) {
  console.error("bundle-size: FAIL — landing payload exceeds the 150 KiB gzip budget");
  process.exit(1);
}
