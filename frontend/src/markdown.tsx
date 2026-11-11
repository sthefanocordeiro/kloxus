import type { ReactNode } from "react";

// Minimal renderer for our own server-generated markdown (headings,
// paragraphs, emphasis, links, lists, quotes, hr). The input is
// trusted — it comes from our API — so inline HTML is not a concern.
function inline(text: string, key: string | number): ReactNode {
  const parts: ReactNode[] = [];
  const re = /(\*\*[^*]+\*\*|\*[^*]+\*|\[[^\]]+\]\([^)]+\))/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let i = 0;
  while ((m = re.exec(text))) {
    if (m.index > last) parts.push(text.slice(last, m.index));
    const s = m[0];
    if (s.startsWith("**")) parts.push(<strong key={i++}>{s.slice(2, -2)}</strong>);
    else if (s.startsWith("*")) parts.push(<em key={i++}>{s.slice(1, -1)}</em>);
    else {
      const [, label, url] = s.match(/\[([^\]]+)\]\(([^)]+)\)/)!;
      parts.push(
        <a key={i++} href={url} className="text-teal underline" target="_blank" rel="noreferrer">
          {label}
        </a>,
      );
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
  for (const line of lines) {
    const t = line.trim();
    if (t.startsWith("- ")) {
      list.push(t.slice(2));
      continue;
    }
    flush();
    if (!t) continue;
    if (t.startsWith("### ")) out.push(<h3 key={k++} className="font-doc font-semibold mt-4 mb-1">{inline(t.slice(4), k)}</h3>);
    else if (t.startsWith("## ")) out.push(<h2 key={k++} className="font-doc text-xl font-semibold mt-5 mb-2">{inline(t.slice(3), k)}</h2>);
    else if (t.startsWith("# ")) out.push(<h1 key={k++} className="font-doc text-2xl font-semibold mb-3">{inline(t.slice(2), k)}</h1>);
    else if (t.startsWith("> ")) out.push(<blockquote key={k++} className="border-l-2 border-line pl-3 italic text-muted my-2">{inline(t.slice(2), k)}</blockquote>);
    else if (t === "---") out.push(<hr key={k++} className="my-4 border-line" />);
    else out.push(<p key={k++} className="mb-2 font-doc leading-7">{inline(t, k)}</p>);
  }
  flush();
  return <div>{out}</div>;
}
