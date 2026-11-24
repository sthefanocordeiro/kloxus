import type { ReactNode } from "react";

// Minimal renderer for our own server-generated markdown (headings,
// paragraphs, emphasis, links, lists, quotes, hr, images, embed
// fences). Embed HTML inside <klox-embed>…</klox-embed> is already
// allowlist-sanitized at write and again at render on the server —
// this surface only re-renders that sanitized subset; ordinary lines
// are always escaped, so raw HTML never reaches the DOM as markup.
// Structural HTML comments (e.g. <!-- item 2 link -->) are document
// markers, never reader content — they're skipped.
// Web-safe URL guards: non-web schemes (javascript:, data:) never
// become clickable href/src — the label renders as plain text instead.
const safeHref = (u: string) => /^(https?:|mailto:|\/|#)/i.test(u.trim());
const safeImgSrc = (u: string) => /^(https?:|\/)/i.test(u.trim());
function inline(text: string, key: string | number): ReactNode {
  const parts: ReactNode[] = [];
  const re = /(\*\*[^*]+\*\*|\*[^*]+\*|_[^_]+_|\[[^\]]+\]\([^)]+\))/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let i = 0;
  while ((m = re.exec(text))) {
    if (m.index > last) parts.push(text.slice(last, m.index));
    const s = m[0];
    if (s.startsWith("**")) parts.push(<strong key={i++}>{s.slice(2, -2)}</strong>);
    else if (s.startsWith("*") || s.startsWith("_")) parts.push(<em key={i++}>{s.slice(1, -1)}</em>);
    else {
      const [, label, url] = s.match(/\[([^\]]+)\]\(([^)]+)\)/)!;
      if (safeHref(url)) {
        parts.push(
          <a key={i++} href={url} className="text-teal underline" target="_blank" rel="noreferrer">
            {label}
          </a>,
        );
      } else {
        parts.push(label);
      }
    }
    last = m.index + s.length;
  }
  if (last < text.length) parts.push(text.slice(last));
  return <span key={key}>{parts}</span>;
}

export function Markdown({ md }: { md: string }) {
  const out: ReactNode[] = [];
  const lines = md.split("\n");
  let list: string[] = [];
  let k = 0;
  const flush = () => {
    if (list.length) {
      out.push(
        <ul key={k++} className="mb-3 list-disc pl-6 space-y-1">
          {list.map((li, j) => (
            <li key={j}>{inline(li, j)}</li>
          ))}
        </ul>,
      );
      list = [];
    }
  };
  let embed: string[] | null = null;
  let quote: string[] = [];
  const flushQuote = () => {
    if (!quote.length) return;
    const q = quote.join(" ");
    quote = [];
    out.push(<blockquote key={k++} className="border-l-2 border-line pl-3 italic text-muted my-2">{inline(q, k)}</blockquote>);
  };
  for (const line of lines) {
    const t = line.trim();
    if (embed !== null) {
      if (t === "</klox-embed>") {
        out.push(<div key={k++} className="embed-preview my-3" dangerouslySetInnerHTML={{ __html: embed.join("\n") }} />);
        embed = null;
      } else {
        embed.push(line);
      }
      continue;
    }
    if (t === "<klox-embed>") {
      flush();
      flushQuote();
      embed = [];
      continue;
    }
    if (t.startsWith("<!--")) continue; // structural markers are not content
    if (t.startsWith("> ")) {
      flush();
      quote.push(t.slice(2));
      continue;
    }
    flushQuote();
    if (t.startsWith("- ")) {
      list.push(t.slice(2));
      continue;
    }
    flush();
    if (!t) continue;
    const img = t.match(/^!\[([^\]]*)\]\(([^)\s]+)\)$/);
    if (img && safeImgSrc(img[2])) {
      out.push(
        <figure key={k++} className="my-3">
          <img src={img[2]} alt={img[1]} className="w-full rounded-[var(--radius-card)]" />
          {img[1] && <figcaption className="mt-1 text-center text-xs text-muted">{img[1]}</figcaption>}
        </figure>,
      );
      continue;
    }
    if (t.startsWith("### ")) out.push(<h3 key={k++} className="font-doc font-semibold mt-4 mb-1">{inline(t.slice(4), k)}</h3>);
    else if (t.startsWith("## ")) out.push(<h2 key={k++} className="font-doc text-xl font-semibold mt-5 mb-2">{inline(t.slice(3), k)}</h2>);
    else if (t.startsWith("# ")) out.push(<h1 key={k++} className="font-doc text-2xl font-semibold mb-3">{inline(t.slice(2), k)}</h1>);
    else if (t === "---") out.push(<hr key={k++} className="my-4 border-line" />);
    else out.push(<p key={k++} className="mb-2 font-doc leading-7">{inline(t, k)}</p>);
  }
  flushQuote();
  flush();
  if (embed !== null) {
    out.push(<div key={k++} className="embed-preview my-3" dangerouslySetInnerHTML={{ __html: embed.join("\n") }} />);
  }
  return <div>{out}</div>;
}
