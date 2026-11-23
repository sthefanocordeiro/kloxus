// Pure helpers for the per-format renditions (KLOX-125): character
// counting, the LinkedIn "ver mais" fold, the X thread split and the
// absolute-URL pass that makes exported HTML self-contained.

export const LINKEDIN_LIMIT = 3000;
export const LINKEDIN_FOLD = 210;
export const X_LIMIT = 280;
// X counts every link as 23 characters whatever its length.
const X_URL_WEIGHT = 23;
const URL_RE = /https?:\/\/[^\s]+/g;

const chars = (s: string): string[] => Array.from(s);

// charCount counts what LinkedIn counts: characters (code points), with
// line breaks included.
export function charCount(s: string): number {
  return chars(s.replace(/\r\n/g, "\n")).length;
}

// xCount is the length X would charge for the text.
export function xCount(s: string): number {
  const t = s.replace(/\r\n/g, "\n");
  let n = 0;
  let last = 0;
  for (const m of t.matchAll(URL_RE)) {
    n += chars(t.slice(last, m.index)).length + X_URL_WEIGHT;
    last = (m.index ?? 0) + m[0].length;
  }
  return n + chars(t.slice(last)).length;
}

// foldIndex returns the UTF-16 offset where LinkedIn truncates the feed
// preview ("ver mais"): the last word boundary at or before ~210
// characters. -1 when the whole text fits above the fold.
export function foldIndex(text: string, fold = LINKEDIN_FOLD): number {
  const cs = chars(text);
  if (cs.length <= fold) return -1;
  let cut = fold;
  while (cut > 0 && !/\s/.test(cs[cut])) cut--;
  if (cut === 0) cut = fold;
  return cs.slice(0, cut).join("").length;
}

// splitForX proposes a thread: paragraphs are packed into posts of at
// most 280 characters, over-long paragraphs break at sentence ends and
// then at words. Each post of a multi-post thread gets a " n/N" suffix
// budgeted into the limit.
export function splitForX(text: string): string[] {
  const clean = text.replace(/\r\n/g, "\n").trim();
  if (!clean) return [];
  if (xCount(clean) <= X_LIMIT) return [clean];
  const budget = X_LIMIT - 7; // room for " 12/12"
  const pieces: string[] = [];
  for (const para of clean.split(/\n{2,}/)) {
    if (xCount(para) <= budget) {
      pieces.push(para.trim());
      continue;
    }
    for (const sentence of para.split(/(?<=[.!?])\s+/)) {
      if (xCount(sentence) <= budget) {
        pieces.push(sentence.trim());
        continue;
      }
      let cur = "";
      for (const word of sentence.split(/\s+/)) {
        const next = cur ? `${cur} ${word}` : word;
        if (cur && xCount(next) > budget) {
          pieces.push(cur);
          cur = word;
        } else cur = next;
      }
      if (cur) pieces.push(cur);
    }
  }
  const posts: string[] = [];
  let cur = "";
  for (const p of pieces) {
    const next = cur ? `${cur}\n\n${p}` : p;
    if (cur && xCount(next) > budget) {
      posts.push(cur);
      cur = p;
    } else cur = next;
  }
  if (cur) posts.push(cur);
  return posts.length === 1 ? posts : posts.map((p, i) => `${p} ${i + 1}/${posts.length}`);
}

// absolutizeHtml rewrites root-relative src/href (uploaded images live at
// /m/{id}-{token}) to the app's public origin, so the exported HTML works
// pasted or opened anywhere.
export function absolutizeHtml(html: string, origin: string): string {
  const base = origin.replace(/\/$/, "");
  return html.replace(/(\s(?:src|href)=")(\/(?!\/)[^"]*)"/g, (_m, pre: string, path: string) => `${pre}${base}${path}"`);
}

// standaloneHtml wraps a body fragment as a complete downloadable file.
export function standaloneHtml(title: string, body: string): string {
  const esc = title.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return `<!doctype html>\n<html lang="pt-BR">\n<head>\n<meta charset="utf-8">\n<meta name="viewport" content="width=device-width, initial-scale=1">\n<title>${esc}</title>\n</head>\n<body>\n${body}\n</body>\n</html>\n`;
}

// htmlToPlain is the text/plain companion of a rich copy.
export function htmlToPlain(html: string): string {
  const doc = new DOMParser().parseFromString(html, "text/html");
  doc.querySelectorAll("br").forEach((b) => b.replaceWith("\n"));
  doc.querySelectorAll("p,h1,h2,h3,h4,li,blockquote").forEach((e) => e.append("\n"));
  return (doc.body.textContent ?? "").replace(/\n{3,}/g, "\n\n").trim();
}
