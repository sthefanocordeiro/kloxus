import { useRef, useState } from "react";
import {
  ArrowDown, ArrowUp, GripVertical, Heading4, Image as ImageIcon, Link2,
  ListOrdered, MessageSquareText, Minus, Newspaper, SquareCode, Trash2, Type,
} from "lucide-react";
import { api, type NewsletterSection, type StdMessage, type TemplateBlock } from "../api";
import { useComposer } from "../editor/composerStrings";
import { useT } from "../i18n";
import { cn } from "../lib/cn";
import { Button, SkeletonLine } from "../ui";

// TemplateBuilder (KLOX-72): the newsletter's default edition layout —
// an ordered block list reusing the composer's vocabulary. Fixed
// blocks (banner art, sponsor message, footer) copy into every new
// edition; a "section" slot is where that section's pautas land when
// the edition materializes. Saving stores the list on the newsletter
// — editions already in progress keep their own copy.

const FIXED_KINDS = ["text", "heading", "link", "divider", "image", "embed"] as const;
type FixedKind = (typeof FIXED_KINDS)[number];

let seq = 0;
function newBlock(kind: TemplateBlock["kind"], section = ""): TemplateBlock & { id: string } {
  seq += 1;
  return { id: `tb-${Date.now().toString(36)}-${seq}`, kind, text: "", url: "", section };
}

// Row is the editable view of a stored block — the id only keys the
// list; it is stripped before saving.
export type TemplateRow = TemplateBlock & { id: string };

export function toRows(blocks: TemplateBlock[]): TemplateRow[] {
  return blocks.map((b) => ({ ...b, id: `tb-${Date.now().toString(36)}-${(seq += 1)}` }));
}

function kindIcon(kind: TemplateBlock["kind"]) {
  switch (kind) {
    case "section": return Newspaper;
    case "heading": return Heading4;
    case "link": return Link2;
    case "image": return ImageIcon;
    case "embed": return SquareCode;
    case "divider": return Minus;
    default: return Type;
  }
}

export function TemplateBuilder({
  value,
  onChange,
  sections,
  pubID,
}: {
  value: TemplateRow[];
  onChange: (rows: TemplateRow[]) => void;
  sections: NewsletterSection[];
  pubID: number;
}) {
  const { t } = useT();
  const b = useComposer();
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [overIndex, setOverIndex] = useState<number | null>(null);
  const [msgOpen, setMsgOpen] = useState(false);
  const [msgs, setMsgs] = useState<StdMessage[] | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const [uploadFor, setUploadFor] = useState<number | null>(null);
  const [uploadErr, setUploadErr] = useState("");

  const update = (i: number, patch: Partial<TemplateBlock>) =>
    onChange(value.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const move = (from: number, to: number) => {
    if (to < 0 || to >= value.length || from === to) return;
    const next = [...value];
    const [r] = next.splice(from, 1);
    next.splice(to, 0, r);
    onChange(next);
  };
  const remove = (i: number) => onChange(value.filter((_, j) => j !== i));
  const add = (kind: TemplateBlock["kind"]) => {
    setMsgOpen(false);
    onChange([...value, newBlock(kind, sections[0]?.title ?? "")]);
  };

  const openMessages = async () => {
    setMsgOpen((o) => !o);
    if (msgs === null) {
      try {
        setMsgs(await api.messages(pubID));
      } catch {
        setMsgs([]);
      }
    }
  };
  const insertMessage = (m: StdMessage) => {
    setMsgOpen(false);
    const row = newBlock("text");
    row.text = m.body;
    onChange([...value, row]);
  };

  const onFile = async (f: File | undefined) => {
    if (!f || uploadFor === null) return;
    const buf = await f.arrayBuffer();
    let bin = "";
    new Uint8Array(buf).forEach((x) => (bin += String.fromCharCode(x)));
    try {
      const res = await api.uploadPubMedia(pubID, {
        content_type: f.type || "image/png", data_b64: btoa(bin), name: f.name,
      });
      update(uploadFor, { url: res.media.url });
      setUploadErr("");
    } catch {
      setUploadErr(b.uploadErr);
    }
  };

  const kindLabel = (kind: TemplateBlock["kind"]): string => {
    if (kind === "section") return t.templatePage.sectionSlot;
    const map: Record<FixedKind, string> = {
      text: b.text, heading: b.heading, link: b.link,
      divider: b.divider, image: b.image, embed: b.embed,
    };
    return map[kind as FixedKind] ?? kind;
  };

  return (
    <div>
      <ul className="grid gap-3">
        {value.map((r, i) => {
          const Icon = kindIcon(r.kind);
          return (
            <li
              key={r.id}
              onDragOver={(e) => { e.preventDefault(); setOverIndex(i); }}
              onDrop={(e) => {
                e.preventDefault();
                if (dragIndex !== null) move(dragIndex, i);
                setDragIndex(null);
                setOverIndex(null);
              }}
              className={cn(
                "rounded-[var(--radius-card)] border border-line bg-paper-raised p-3",
                overIndex === i && dragIndex !== null && dragIndex !== i && "ring-2 ring-teal",
              )}
            >
              <div className="flex items-center gap-2">
                <span
                  draggable
                  onDragStart={() => setDragIndex(i)}
                  onDragEnd={() => { setDragIndex(null); setOverIndex(null); }}
                  aria-label={t.builder.drag}
                  className="flex h-11 w-6 cursor-grab items-center justify-center text-muted active:cursor-grabbing md:h-10"
                >
                  <GripVertical size={16} aria-hidden />
                </span>
                <span className="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-muted">
                  <Icon size={14} aria-hidden />
                  {kindLabel(r.kind)}
                </span>
                <span className="flex-1" />
                <div className="flex shrink-0">
                  <Button variant="ghost" size="icon" onClick={() => move(i, i - 1)}
                    disabled={i === 0} aria-label={t.builder.moveUp}>
                    <ArrowUp size={16} />
                  </Button>
                  <Button variant="ghost" size="icon" onClick={() => move(i, i + 1)}
                    disabled={i === value.length - 1} aria-label={t.builder.moveDown}>
                    <ArrowDown size={16} />
                  </Button>
                  <Button variant="dangerGhost" size="icon" onClick={() => remove(i)}
                    aria-label={t.builder.remove}>
                    <Trash2 size={16} />
                  </Button>
                </div>
              </div>

              <div className="mt-2">
                {r.kind === "section" && (
                  <select
                    value={r.section ?? ""}
                    onChange={(e) => update(i, { section: e.target.value })}
                    aria-label={t.templatePage.sectionSlot}
                    className="w-full rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
                  >
                    {r.section && !sections.some((s) => s.title === r.section) && (
                      <option value={r.section}>{r.section}</option>
                    )}
                    {sections.map((s) => (
                      <option key={s.id} value={s.title}>{s.title}</option>
                    ))}
                  </select>
                )}
                {r.kind === "section" && (
                  <p className="mt-1 text-[11px] text-muted">{t.templatePage.sectionHint}</p>
                )}
                {r.kind === "heading" && (
                  <input
                    value={r.text ?? ""}
                    onChange={(e) => update(i, { text: e.target.value })}
                    placeholder={b.headingPh}
                    className="w-full rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
                  />
                )}
                {r.kind === "text" && (
                  <textarea
                    value={r.text ?? ""}
                    onChange={(e) => update(i, { text: e.target.value })}
                    rows={3}
                    placeholder={b.textPh}
                    className="w-full resize-y rounded border border-line bg-paper px-3 py-2 font-doc text-sm outline-none focus:border-teal"
                  />
                )}
                {r.kind === "link" && (
                  <div className="grid gap-1.5">
                    <input
                      value={r.text ?? ""}
                      onChange={(e) => update(i, { text: e.target.value })}
                      placeholder={t.templatePage.linkLabelPh}
                      className="w-full rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
                    />
                    <input
                      value={r.url ?? ""}
                      onChange={(e) => update(i, { url: e.target.value })}
                      placeholder="https://"
                      className="w-full rounded border border-line bg-paper px-3 py-2 text-xs outline-none focus:border-teal"
                    />
                  </div>
                )}
                {r.kind === "image" && (
                  <div className="grid gap-1.5">
                    <div className="flex gap-1.5">
                      <input
                        value={r.url ?? ""}
                        onChange={(e) => update(i, { url: e.target.value })}
                        placeholder={b.imageUrlPh}
                        className="min-w-0 flex-1 rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
                      />
                      <Button size="sm" variant="outline"
                        onClick={() => { setUploadFor(i); fileRef.current?.click(); }}>
                        {b.upload}
                      </Button>
                    </div>
                    <input
                      value={r.text ?? ""}
                      onChange={(e) => update(i, { text: e.target.value })}
                      placeholder={b.altPh}
                      className="w-full rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
                    />
                    {r.url && (
                      <img src={r.url} alt={r.text ?? ""} className="max-h-24 rounded border border-line object-contain" />
                    )}
                  </div>
                )}
                {r.kind === "embed" && (
                  <div className="grid gap-1">
                    <textarea
                      value={r.text ?? ""}
                      onChange={(e) => update(i, { text: e.target.value })}
                      rows={4}
                      placeholder={b.embedPh}
                      className="w-full resize-y rounded border border-line bg-paper p-2 font-mono text-xs leading-5 outline-none focus:border-teal"
                    />
                    <p className="text-[11px] text-muted">{b.embedHint}</p>
                  </div>
                )}
              </div>
            </li>
          );
        })}
      </ul>

      <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/gif,image/webp" className="hidden"
        onChange={(e) => { void onFile(e.target.files?.[0]); e.target.value = ""; }} />
      {uploadErr && <p className="mt-2 text-xs text-danger" role="alert">{uploadErr}</p>}

      <div className="mt-3 flex flex-wrap gap-1.5">
        {FIXED_KINDS.map((k) => (
          <button key={k} type="button" onClick={() => add(k)}
            className="inline-flex min-h-11 items-center rounded-full border border-line bg-paper-raised px-3 text-xs font-medium text-ink hover:border-teal hover:text-teal md:min-h-10">
            {kindLabel(k)}
          </button>
        ))}
        <button type="button" onClick={() => add("section")}
          className="inline-flex min-h-11 items-center rounded-full border border-line bg-paper-raised px-3 text-xs font-medium text-ink hover:border-teal hover:text-teal md:min-h-10">
          {t.templatePage.sectionSlot}
        </button>
        <button type="button" onClick={() => void openMessages()}
          className="inline-flex min-h-11 items-center gap-1 rounded-full border border-teal px-3 text-xs font-medium text-teal hover:bg-teal hover:text-paper md:min-h-10">
          <MessageSquareText size={12} aria-hidden />
          {b.message}
        </button>
      </div>

      {msgOpen && (
        <div className="mt-2 max-h-56 space-y-1 overflow-y-auto rounded-[var(--radius-card)] border border-line bg-paper-raised p-2">
          {msgs === null && <SkeletonLine />}
          {msgs !== null && msgs.length === 0 && (
            <p className="text-sm text-muted">{b.msgEmpty}</p>
          )}
          {(msgs ?? []).map((m) => (
            <button key={m.id} type="button" onClick={() => insertMessage(m)}
              className="block w-full rounded-[var(--radius-btn)] px-2 py-1.5 text-left hover:bg-surface">
              <span className="block text-sm font-medium">{m.name}</span>
              <span className="block truncate text-xs text-muted">{m.body}</span>
            </button>
          ))}
        </div>
      )}

      <p className="mt-3 flex items-start gap-1.5 text-xs text-muted">
        <ListOrdered size={13} className="mt-0.5 shrink-0" aria-hidden />
        {t.templatePage.hint}
      </p>
    </div>
  );
}
