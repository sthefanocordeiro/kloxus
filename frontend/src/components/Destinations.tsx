import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEditor, EditorContent, type Editor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import Image from "@tiptap/extension-image";
import { api, type Rendition, type RenditionFormat } from "../api";
import { useT } from "../i18n";
import { Button, Tick } from "../ui";
import { KDialog } from "../ui-overlays";
import { Markdown } from "../markdown";
import { cn } from "../lib/cn";
import { errText } from "../lib/errors";
import {
  LINKEDIN_LIMIT, X_LIMIT, absolutizeHtml, charCount, foldIndex, htmlToPlain, splitForX,
  standaloneHtml, xCount,
} from "../lib/renditions";

// "Para onde vai?" (KLOX-125): after "Concluir revisão" the author picks
// destinations; each one forks the approved piece through a fixed
// template and opens its own editor. Kloxus never publishes — the
// author copies or downloads and pastes into the destination.

const en = {
  title: "Where is it going?",
  hint: "Pick one or more destinations. Each destination gets its own editable copy.",
  formats: {
    linkedin: { name: "LinkedIn post", sub: "Plain text · 3,000 characters" },
    x: { name: "X post", sub: "Plain text · 280 per post" },
    html: { name: "Newsletter / blog / intranet", sub: "Rich text (HTML)" },
    markdown: { name: "Markdown", sub: "Ghost, Notion, Obsidian" },
  } as Record<RenditionFormat, { name: string; sub: string }>,
  manyStories: (n: number, head: string) =>
    `This brief has ${n} stories. Social posts read better with one main story${head ? ` — maybe “${head}”` : ""}. You can go ahead as is.`,
  stale: "The brief changed since this version was made.",
  refresh: "Update this version?",
  refreshConfirm: "Updating replaces your edits in this version with the current brief. Continue?",
  threadNote: (n: number) => `Becomes a thread of ${n} posts`,
  removed: "Version removed.",
  undo: "Undo",
  dismiss: "Dismiss",
  update: "Update",
  cancel: "Cancel",
  saving: "Saving…",
  saved: "Saved",
  saveError: "Couldn't save — retrying",
  copy: "Copy",
  copied: "Copied",
  copyFailed: "Couldn't copy — the text is selected; press Ctrl+C",
  copyRich: "Copy to paste",
  copyHtml: "Copy HTML",
  downloadHtml: "Download .html",
  downloadMd: "Download .md",
  chars: (n: number, max: number) => `${n.toLocaleString("en-US")} / ${max.toLocaleString("en-US")}`,
  over: "Over the limit",
  fold: "“See more” cuts here in the feed (~210 characters)",
  splitTitle: "Suggested thread",
  splitNote: (n: number) => `${n} posts — copy each one separately.`,
  preview: "Preview",
  h2: "Heading", h3: "Subheading", bold: "Bold", italic: "Italic", link: "Link",
  bullets: "Bullets", numbers: "Numbered list", quote: "Quote", image: "Image",
  linkPrompt: "Link URL",
  uploadErr: "Image upload failed — PNG, JPEG or WebP up to 2 MB.",
  remove: "Remove",
  loading: "Preparing your versions…",
  none: "Pick a destination above to start.",
};
type Strings = typeof en;

const pt: Strings = {
  title: "Para onde vai?",
  hint: "Escolha um ou mais destinos. Cada destino ganha sua própria cópia editável.",
  formats: {
    linkedin: { name: "Post LinkedIn", sub: "Texto puro · 3.000 caracteres" },
    x: { name: "Post X", sub: "Texto puro · 280 por post" },
    html: { name: "Newsletter / blog / intranet", sub: "Texto rico (HTML)" },
    markdown: { name: "Markdown", sub: "Ghost, Notion, Obsidian" },
  },
  manyStories: (n, head) =>
    `Esta pauta tem ${n} notícias. Posts sociais funcionam melhor com uma principal${head ? ` — talvez “${head}”` : ""}. Você pode seguir assim.`,
  stale: "A pauta mudou desde que esta versão foi feita.",
  refresh: "Atualizar esta versão?",
  refreshConfirm: "Atualizar troca suas edições desta versão pelo texto atual da pauta. Continuar?",
  threadNote: (n) => `Vira uma thread de ${n} posts`,
  removed: "Versão removida.",
  undo: "Desfazer",
  dismiss: "Dispensar",
  update: "Atualizar",
  cancel: "Cancelar",
  saving: "Salvando…",
  saved: "Salvo",
  saveError: "Não salvou — tentando de novo",
  copy: "Copiar",
  copied: "Copiado",
  copyFailed: "Não deu para copiar — selecione o texto e use Ctrl+C",
  copyRich: "Copiar p/ colar",
  copyHtml: "Copiar HTML",
  downloadHtml: "Baixar .html",
  downloadMd: "Baixar .md",
  chars: (n, max) => `${n.toLocaleString("pt-BR")} / ${max.toLocaleString("pt-BR")}`,
  over: "Acima do limite",
  fold: "“Ver mais” corta aqui no feed (~210 caracteres)",
  splitTitle: "Sugestão de thread",
  splitNote: (n) => `${n} posts — copie um de cada vez.`,
  preview: "Prévia",
  h2: "Título", h3: "Subtítulo", bold: "Negrito", italic: "Itálico", link: "Link",
  bullets: "Lista", numbers: "Lista numerada", quote: "Citação", image: "Imagem",
  linkPrompt: "URL do link",
  uploadErr: "Falha no envio da imagem — PNG, JPEG ou WebP até 2 MB.",
  remove: "Remover",
  loading: "Preparando suas versões…",
  none: "Escolha um destino acima para começar.",
};

const ORDER: RenditionFormat[] = ["linkedin", "x", "html", "markdown"];
const isSocial = (f: RenditionFormat) => f === "linkedin" || f === "x";

function download(name: string, mime: string, content: string) {
  const url = URL.createObjectURL(new Blob([content], { type: `${mime};charset=utf-8` }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

async function copyText(text: string) {
  await navigator.clipboard.writeText(text);
}

// copyRich puts text/html and text/plain on the clipboard so a paste
// into an email composer or CMS keeps headings, links and images.
async function copyRich(html: string) {
  const abs = absolutizeHtml(html, window.location.origin);
  if (typeof ClipboardItem !== "undefined" && navigator.clipboard.write) {
    await navigator.clipboard.write([
      new ClipboardItem({
        "text/html": new Blob([abs], { type: "text/html" }),
        "text/plain": new Blob([htmlToPlain(abs)], { type: "text/plain" }),
      }),
    ]);
    return;
  }
  await navigator.clipboard.writeText(abs);
}

// KLOX-202: when the browser refuses the clipboard write (e.g. tab
// without focus), the fallback message says Ctrl+C — this leaves the
// text already selected so the shortcut just works.
function selectEl(el: HTMLElement | null) {
  if (!el) return;
  if (el instanceof HTMLTextAreaElement || el instanceof HTMLInputElement) {
    el.focus();
    el.select();
    return;
  }
  const r = document.createRange();
  r.selectNodeContents(el);
  const sel = window.getSelection();
  sel?.removeAllRanges();
  sel?.addRange(r);
}

// KLOX-184: copying or downloading a rendition marks the piece
// "Exportada" — fire-and-forget so a failed mark never breaks the copy.
function useExportMark(issueID: number, format: RenditionFormat) {
  const qc = useQueryClient();
  return () => {
    api.markExported(issueID, format)
      .then(() => {
        for (const key of ["issues", "today", "issue", "pauta"]) {
          void qc.invalidateQueries({ queryKey: [key] });
        }
      })
      .catch(() => {});
  };
}

// useCopied returns [copiedKey, failed, run]. A refused clipboard
// write must not stay silent (KLOX-202): run takes an optional
// select() that leaves the text selected for a manual Ctrl+C.
function useCopied() {
  const [done, setDone] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);
  const timer = useRef<number>(0);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  return [
    done,
    failed,
    async (key: string, fn: () => Promise<void>, select?: () => void) => {
      try {
        await fn();
        setDone(key);
        setFailed(false);
        window.clearTimeout(timer.current);
        timer.current = window.setTimeout(() => setDone(null), 1800);
      } catch {
        setDone(null);
        setFailed(true);
        select?.();
      }
    },
  ] as const;
}

// useAutosave debounces edits into PUT /renditions/{id}. The first
// render never saves — only real edits do. A failed save keeps the
// body queued and retries instead of dropping it on the floor
// (KLOX-196); the badge shows "error" rather than going back to idle.
function useAutosave(issueID: number, id: number, onSaved: (r: Rendition) => void) {
  const [state, setState] = useState<"idle" | "saving" | "saved" | "error">("idle");
  const timer = useRef<number>(0);
  const pending = useRef<string | null>(null);
  const inFlight = useRef(false);
  const retry = useRef(0);
  const flush = async () => {
    const body = pending.current;
    if (body === null || inFlight.current) return;
    pending.current = null;
    inFlight.current = true;
    setState("saving");
    let failed = false;
    try {
      onSaved(await api.renditionSave(issueID, id, body));
      retry.current = 0;
      setState(pending.current === null ? "saved" : "saving");
    } catch {
      // re-queue the body — a newer push already holds the newest text
      failed = true;
      pending.current = pending.current ?? body;
      setState("error");
      retry.current = Math.min(retry.current + 1, 5);
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => void flushRef.current(), 1000 * retry.current);
    } finally {
      inFlight.current = false;
    }
    // edits queued while this save flew flush right after, in order
    if (!failed && pending.current !== null) {
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => void flushRef.current(), 0);
    }
  };
  const flushRef = useRef(flush);
  flushRef.current = flush;
  useEffect(() => () => {
    window.clearTimeout(timer.current);
    void flushRef.current();
  }, []);
  // a reload/close while a save is pending would drop it — warn first
  useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => {
      if (pending.current !== null || inFlight.current) {
        e.preventDefault();
        e.returnValue = "";
      }
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, []);
  return {
    state,
    push: (body: string) => {
      pending.current = body;
      setState("saving");
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => void flushRef.current(), 800);
    },
    flush: () => void flushRef.current(),
  };
}

function SaveBadge({ state, s }: { state: "idle" | "saving" | "saved" | "error"; s: Strings }) {
  if (state === "idle") return null;
  return (
    <span className={cn(
      "rounded-full px-2 py-0.5 text-[11px]",
      state === "error" ? "bg-danger-soft text-danger" : "bg-teal-soft text-teal",
    )}>
      {state === "saving" ? s.saving : state === "error" ? s.saveError : s.saved}
    </span>
  );
}

function Counter({ n, max, s }: { n: number; max: number; s: Strings }) {
  return (
    <span className={cn("text-xs tabular-nums", n > max ? "font-semibold text-danger" : "text-muted")} data-testid="counter">
      {s.chars(n, max)}
      {n > max && ` · ${s.over}`}
    </span>
  );
}

// ---------- plain text (LinkedIn / X) ----------

// dropLegacySeparators removes the "---" post separators old X bodies
// carry — a blank line is the post boundary now.
function dropLegacySeparators(text: string): string {
  return text
    .split("\n")
    .filter((l) => l.trim() !== "---")
    .join("\n")
    .replace(/\n{3,}/g, "\n\n");
}

function PlainEditor({
  r, issueID, s, onSaved,
}: { r: Rendition; issueID: number; s: Strings; onSaved: (r: Rendition) => void }) {
  const [text, setText] = useState(() =>
    r.format === "x" ? dropLegacySeparators(r.body_text) : r.body_text);
  const save = useAutosave(issueID, r.id, onSaved);
  const [copied, copyFailed, copy] = useCopied();
  const markExported = useExportMark(issueID, r.format);
  const textRef = useRef<HTMLTextAreaElement>(null);
  const postRefs = useRef<(HTMLElement | null)[]>([]);
  const linkedin = r.format === "linkedin";
  const fold = linkedin ? foldIndex(text) : -1;
  const posts = useMemo(() => (linkedin ? [] : splitForX(text)), [linkedin, text]);
  const count = linkedin ? charCount(text) : xCount(text);
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        {/* KLOX-214: a suggested thread makes the "420 / 280 over the
            limit" counter a false alarm — say what it really is */}
        {linkedin || posts.length <= 1 ? (
          <Counter n={count} max={linkedin ? LINKEDIN_LIMIT : X_LIMIT} s={s} />
        ) : (
          <span className="text-xs text-muted" data-testid="thread-note">{s.threadNote(posts.length)}</span>
        )}
        <SaveBadge state={save.state} s={s} />
        <Button size="sm" className="ml-auto" onClick={() => copy("text", async () => { await copyText(text); markExported(); }, () => selectEl(textRef.current))}>
          {copied === "text" ? s.copied : s.copy}
        </Button>
      </div>
      {copyFailed && <p role="alert" className="text-sm text-danger">{s.copyFailed}</p>}
      <textarea
        ref={textRef}
        aria-label={s.formats[r.format].name}
        value={text}
        onChange={(e) => { setText(e.target.value); save.push(e.target.value); }}
        onBlur={save.flush}
        rows={16}
        className="w-full whitespace-pre-wrap rounded-[var(--radius-input)] border border-line bg-paper p-3 text-sm leading-relaxed"
      />
      {linkedin && fold >= 0 && (
        <div className="rounded-[var(--radius-card)] border border-line bg-surface p-3 text-sm">
          <p className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted">{s.fold}</p>
          <p className="whitespace-pre-wrap" data-testid="fold-preview">
            {text.slice(0, fold)}
            <span className="font-medium text-teal"> … ver mais</span>
          </p>
        </div>
      )}
      {!linkedin && posts.length > 1 && (
        <div className="space-y-2">
          <p className="text-xs text-muted">{s.splitTitle} — {s.splitNote(posts.length)}</p>
          {posts.map((p, i) => (
            <div key={i} className="rounded-[var(--radius-card)] border border-line bg-paper-raised p-3">
              <p ref={(el) => { postRefs.current[i] = el; }} className="whitespace-pre-wrap text-sm">{p}</p>
              <div className="mt-2 flex items-center gap-3">
                <span className="text-xs tabular-nums text-muted">{s.chars(xCount(p), X_LIMIT)}</span>
                <Button size="sm" variant="outline" className="ml-auto" onClick={() => copy(`p${i}`, async () => { await copyText(p); markExported(); }, () => selectEl(postRefs.current[i]))}>
                  {copied === `p${i}` ? s.copied : s.copy}
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// ---------- Markdown ----------

function MarkdownEditor({
  r, issueID, s, onSaved,
}: { r: Rendition; issueID: number; s: Strings; onSaved: (r: Rendition) => void }) {
  const [md, setMd] = useState(r.body_md);
  const save = useAutosave(issueID, r.id, onSaved);
  const [copied, copyFailed, copy] = useCopied();
  const markExported = useExportMark(issueID, r.format);
  const mdRef = useRef<HTMLTextAreaElement>(null);
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <SaveBadge state={save.state} s={s} />
        <div className="ml-auto flex flex-wrap gap-2">
          <Button size="sm" onClick={() => copy("md", async () => { await copyText(md); markExported(); }, () => selectEl(mdRef.current))}>
            {copied === "md" ? s.copied : s.copy}
          </Button>
          <Button size="sm" variant="outline" onClick={() => { download("peca.md", "text/markdown", md); markExported(); }}>
            {s.downloadMd}
          </Button>
        </div>
      </div>
      {copyFailed && <p role="alert" className="text-sm text-danger">{s.copyFailed}</p>}
      <div className="grid gap-3 md:grid-cols-2">
        <textarea
          ref={mdRef}
          aria-label="Markdown"
          value={md}
          onChange={(e) => { setMd(e.target.value); save.push(e.target.value); }}
          onBlur={save.flush}
          rows={20}
          className="w-full rounded-[var(--radius-input)] border border-line bg-paper p-3 font-mono text-xs leading-relaxed"
        />
        <div className="max-h-[32rem] overflow-y-auto rounded-[var(--radius-card)] border border-line bg-paper-raised p-4" aria-label={s.preview}>
          <Markdown md={md} />
        </div>
      </div>
    </div>
  );
}

// ---------- rich HTML (TipTap) ----------

const MAX_IMAGE = 2 << 20;
const IMAGE_TYPES = ["image/png", "image/jpeg", "image/webp"];

function fileToB64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const fr = new FileReader();
    fr.onload = () => resolve(String(fr.result).split(",")[1] ?? "");
    fr.onerror = () => reject(fr.error);
    fr.readAsDataURL(file);
  });
}

function HtmlEditor({
  r, issueID, s, onSaved,
}: { r: Rendition; issueID: number; s: Strings; onSaved: (r: Rendition) => void }) {
  const save = useAutosave(issueID, r.id, onSaved);
  const [copied, copyFailed, copy] = useCopied();
  const markExported = useExportMark(issueID, r.format);
  const [err, setErr] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);
  const editorRef = useRef<Editor | null>(null);

  const upload = async (file: File) => {
    setErr("");
    if (!IMAGE_TYPES.includes(file.type) || file.size > MAX_IMAGE) {
      setErr(s.uploadErr);
      return;
    }
    try {
      const m = await api.uploadMedia(issueID, {
        content_type: file.type, data_b64: await fileToB64(file), name: file.name,
      });
      editorRef.current?.chain().focus().setImage({ src: m.media.url, alt: file.name.replace(/\.[^.]+$/, "") }).run();
    } catch {
      setErr(s.uploadErr);
    }
  };
  const imageFrom = (items: DataTransferItemList | FileList | undefined | null): File | null => {
    if (!items) return null;
    for (let i = 0; i < items.length; i++) {
      const it = items[i];
      const f = it instanceof File ? it : it.kind === "file" ? it.getAsFile() : null;
      if (f && f.type.startsWith("image/")) return f;
    }
    return null;
  };

  const editor = useEditor({
    extensions: [StarterKit.configure({ link: { openOnClick: false } }), Image],
    content: r.body_html,
    editorProps: {
      attributes: { class: "tiptap-body min-h-[18rem] px-4 py-3 focus:outline-none", "aria-label": s.formats.html.name },
      handlePaste: (_v, ev) => {
        const f = imageFrom(ev.clipboardData?.items);
        if (!f) return false;
        void upload(f);
        return true;
      },
      handleDrop: (_v, ev) => {
        const f = imageFrom(ev.dataTransfer?.files);
        if (!f) return false;
        ev.preventDefault();
        void upload(f);
        return true;
      },
    },
    onUpdate: ({ editor: e }) => save.push(e.getHTML()),
    onBlur: () => save.flush(),
  });
  editorRef.current = editor;
  if (!editor) return null;
  const html = () => editor.getHTML();
  const tb = (label: string, active: boolean, run: () => void, text: string) => (
    <button
      type="button"
      aria-label={label}
      title={label}
      aria-pressed={active}
      onMouseDown={(e) => e.preventDefault()}
      onClick={run}
      className={cn(
        "h-9 min-w-9 rounded-[var(--radius-btn)] px-2 text-sm hover:bg-surface",
        active && "bg-teal-soft font-semibold text-teal",
      )}
    >
      {text}
    </button>
  );
  const setLink = () => {
    const prev = editor.getAttributes("link").href as string | undefined;
    const url = window.prompt(s.linkPrompt, prev ?? "https://");
    if (url === null) return;
    if (url.trim() === "") editor.chain().focus().extendMarkRange("link").unsetLink().run();
    else editor.chain().focus().extendMarkRange("link").setLink({ href: url.trim() }).run();
  };
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <SaveBadge state={save.state} s={s} />
        <div className="ml-auto flex flex-wrap gap-2">
          <Button size="sm" onClick={() => copy("rich", async () => { await copyRich(html()); markExported(); }, () => editor.chain().focus().selectAll().run())}>
            {copied === "rich" ? s.copied : s.copyRich}
          </Button>
          <Button size="sm" variant="outline" onClick={() => copy("html", async () => { await copyText(absolutizeHtml(html(), window.location.origin)); markExported(); }, () => editor.chain().focus().selectAll().run())}>
            {copied === "html" ? s.copied : s.copyHtml}
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => { download("pauta.html", "text/html", standaloneHtml(
              editor.getText().split("\n")[0] || "Pauta", absolutizeHtml(html(), window.location.origin))); markExported(); }}
          >
            {s.downloadHtml}
          </Button>
        </div>
      </div>
      <div className="rounded-[var(--radius-card)] border border-line bg-paper">
        <div role="toolbar" className="flex flex-wrap gap-1 border-b border-line p-1">
          {tb(s.h2, editor.isActive("heading", { level: 2 }), () => editor.chain().focus().toggleHeading({ level: 2 }).run(), "H2")}
          {tb(s.h3, editor.isActive("heading", { level: 3 }), () => editor.chain().focus().toggleHeading({ level: 3 }).run(), "H3")}
          {tb(s.bold, editor.isActive("bold"), () => editor.chain().focus().toggleBold().run(), "B")}
          {tb(s.italic, editor.isActive("italic"), () => editor.chain().focus().toggleItalic().run(), "I")}
          {tb(s.link, editor.isActive("link"), setLink, "🔗")}
          {tb(s.bullets, editor.isActive("bulletList"), () => editor.chain().focus().toggleBulletList().run(), "•")}
          {tb(s.numbers, editor.isActive("orderedList"), () => editor.chain().focus().toggleOrderedList().run(), "1.")}
          {tb(s.quote, editor.isActive("blockquote"), () => editor.chain().focus().toggleBlockquote().run(), "❝")}
          {tb(s.image, false, () => fileRef.current?.click(), "🖼")}
          <input
            ref={fileRef}
            type="file"
            accept={IMAGE_TYPES.join(",")}
            className="hidden"
            data-testid="image-input"
            onChange={(e) => {
              const f = e.target.files?.[0];
              e.target.value = "";
              if (f) void upload(f);
            }}
          />
        </div>
        <EditorContent editor={editor} className="rendition-html" />
      </div>
      {copyFailed && <p role="alert" className="text-sm text-danger">{s.copyFailed}</p>}
      {err && <p role="alert" className="text-xs text-danger">{err}</p>}
    </div>
  );
}

// ---------- the step ----------

export function DestinationStep({
  issueID, stories,
}: { issueID: number; stories: { headline: string }[] }) {
  const { t, lang } = useT();
  const s = lang === "pt" ? pt : en;
  const qc = useQueryClient();
  const key = ["renditions", issueID];
  const q = useQuery({ queryKey: key, queryFn: () => api.renditions(issueID) });
  const [active, setActive] = useState<RenditionFormat | null>(null);
  const [refreshing, setRefreshing] = useState(0);
  const [confirmRefresh, setConfirmRefresh] = useState(false);
  const [noteClosed, setNoteClosed] = useState(false);
  // KLOX-214: unticking a destination deletes at once and offers undo
  // on a toast — no modal confirm in the way of every checkbox
  const [undo, setUndo] = useState<Rendition | null>(null);
  const undoTimer = useRef(0);
  const [actionErr, setActionErr] = useState("");
  useEffect(() => () => window.clearTimeout(undoTimer.current), []);

  const rs = q.data?.renditions ?? [];
  const byFormat = new Map(rs.map((r) => [r.format, r]));
  const put = (r: Rendition) =>
    qc.setQueryData<{ renditions: Rendition[]; stories: number }>(key, (old) =>
      old ? { ...old, renditions: old.renditions.map((x) => (x.id === r.id ? r : x)) } : old);

  const create = useMutation({
    mutationFn: (f: RenditionFormat) => api.renditionCreate(issueID, f),
    onSuccess: (r) => { setActive(r.format); qc.invalidateQueries({ queryKey: key }); },
  });
  const del = useMutation({
    mutationFn: (r: Rendition) => api.renditionDelete(issueID, r.id),
    onError: (e) => setActionErr(errText(t, e)),
    onSuccess: (_v, r) => {
      setActive((a) => (a === r.format ? null : a));
      qc.invalidateQueries({ queryKey: key });
      window.clearTimeout(undoTimer.current);
      setUndo(r);
      undoTimer.current = window.setTimeout(() => setUndo(null), 8000);
    },
  });
  const refresh = useMutation({
    mutationFn: (r: Rendition) => api.renditionRefresh(issueID, r.id),
    onSuccess: (r) => { put(r); setRefreshing((n) => n + 1); },
  });

  const toggle = (f: RenditionFormat) => {
    const cur = byFormat.get(f);
    if (!cur) { create.mutate(f); return; }
    del.mutate(cur);
  };

  // undo re-forks the format and writes the deleted body back, so the
  // author's edits survive an accidental untick
  const undoRemove = async () => {
    const r = undo;
    if (!r) return;
    window.clearTimeout(undoTimer.current);
    setUndo(null);
    try {
      const fresh = await api.renditionCreate(issueID, r.format);
      const body = r.format === "html" ? r.body_html : r.format === "markdown" ? r.body_md : r.body_text;
      if (body) await api.renditionSave(issueID, fresh.id, body);
      void qc.invalidateQueries({ queryKey: key });
      setActive(r.format);
    } catch (e) {
      setActionErr(errText(t, e));
    }
  };

  const current = (active && byFormat.get(active)) || (rs.length ? rs[0] : undefined);
  const socialChosen = rs.some((r) => isSocial(r.format));
  const social = stories.length > 1 && socialChosen;

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto w-full max-w-4xl space-y-4 p-4">
        <section className="rounded-[var(--radius-card)] border border-line bg-paper-raised p-4">
          <h2 className="mb-1 text-base font-semibold">{s.title}</h2>
          <p className="mb-3 text-sm text-muted">{s.hint}</p>
          <div className="grid gap-2 sm:grid-cols-2" role="group" aria-label={s.title}>
            {ORDER.map((f) => {
              const on = byFormat.has(f);
              return (
                <label
                  key={f}
                  className={cn(
                    "flex cursor-pointer items-start gap-3 rounded-[var(--radius-card)] border p-3",
                    on ? "border-teal bg-teal-soft/40" : "border-line bg-paper",
                  )}
                >
                  <Tick
                    checked={on}
                    disabled={q.isLoading || create.isPending || del.isPending}
                    onChange={() => toggle(f)}
                    className="mt-0.5"
                    aria-label={s.formats[f].name}
                  />
                  <span>
                    <span className="block text-sm font-medium">{s.formats[f].name}</span>
                    <span className="text-xs text-muted">{s.formats[f].sub}</span>
                  </span>
                </label>
              );
            })}
          </div>
          {social && !noteClosed && (
            <p role="note" className="mt-3 flex items-start gap-2 rounded-[var(--radius-btn)] bg-warn-soft/60 px-3 py-2 text-xs text-warn">
              <span className="flex-1">{s.manyStories(stories.length, stories[0]?.headline ?? "")}</span>
              <button
                type="button"
                aria-label={s.dismiss}
                className="shrink-0 cursor-pointer px-1 leading-none hover:text-ink"
                onClick={() => setNoteClosed(true)}
              >
                ✕
              </button>
            </p>
          )}
          {actionErr && <p role="alert" className="mt-3 text-xs text-danger">{actionErr}</p>}
        </section>

        {q.isLoading && <p className="text-sm text-muted">{s.loading}</p>}
        {!q.isLoading && rs.length === 0 && <p className="text-sm text-muted">{s.none}</p>}

        {rs.length > 0 && current && (
          <section className="rounded-[var(--radius-card)] border border-line bg-paper-raised p-4">
            <div role="tablist" className="mb-4 flex flex-wrap gap-1 border-b border-line">
              {ORDER.filter((f) => byFormat.has(f)).map((f) => (
                <button
                  key={f}
                  role="tab"
                  aria-selected={current.format === f}
                  onClick={() => setActive(f)}
                  className={cn(
                    "-mb-px border-b-2 px-3 py-2 text-sm",
                    current.format === f ? "border-teal font-medium text-teal" : "border-transparent text-muted hover:text-ink",
                  )}
                >
                  {s.formats[f].name}
                  {byFormat.get(f)?.stale && <span aria-hidden className="ml-1 text-warn">●</span>}
                </button>
              ))}
            </div>
            {current.stale && (
              <div role="status" className="mb-3 flex flex-wrap items-center gap-3 rounded-[var(--radius-btn)] bg-warn-soft/60 px-3 py-2 text-xs text-warn">
                <span>{s.stale}</span>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={refresh.isPending}
                  onClick={() => setConfirmRefresh(true)}
                >
                  {s.refresh}
                </Button>
              </div>
            )}
            {/* keyed by id + refresh count so a refresh remounts the editor with the new body */}
            <div key={`${current.id}-${refreshing}`}>
              {current.format === "html" ? (
                <HtmlEditor r={current} issueID={issueID} s={s} onSaved={put} />
              ) : current.format === "markdown" ? (
                <MarkdownEditor r={current} issueID={issueID} s={s} onSaved={put} />
              ) : (
                <PlainEditor r={current} issueID={issueID} s={s} onSaved={put} />
              )}
            </div>
            <div className="mt-4 text-right">
              <Button size="sm" variant="dangerGhost" onClick={() => toggle(current.format)}>
                {s.remove}
              </Button>
            </div>
          </section>
        )}
      </div>

      {/* delete is instant — the undo toast is the safety net; sits
          above the mobile bottom nav like the editor action bar */}
      {undo && (
        <div
          role="status"
          className="fixed inset-x-4 bottom-[calc(4.5rem+env(safe-area-inset-bottom))] z-50 mx-auto flex w-[min(24rem,100%)] items-center justify-between gap-3 rounded-[var(--radius-btn)] bg-ink px-4 py-3 text-sm text-paper shadow-lg md:bottom-6"
        >
          <span>{s.removed}</span>
          <button
            type="button"
            className="shrink-0 cursor-pointer font-semibold text-accent hover:underline"
            onClick={() => void undoRemove()}
          >
            {s.undo}
          </button>
        </div>
      )}

      <KDialog
        open={confirmRefresh}
        onOpenChange={setConfirmRefresh}
        title={s.refresh}
      >
        <p className="text-sm text-muted">{s.refreshConfirm}</p>
        <div className="mt-5 flex gap-3">
          <Button
            disabled={refresh.isPending}
            onClick={() => { if (current) refresh.mutate(current); setConfirmRefresh(false); }}
          >
            {s.update}
          </Button>
          <Button variant="outline" onClick={() => setConfirmRefresh(false)}>
            {s.cancel}
          </Button>
        </div>
      </KDialog>
    </div>
  );
}
