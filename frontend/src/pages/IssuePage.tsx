import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { EditorContent, useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { ArrowLeft, BookOpen, ClipboardCheck, Eye, Send, Wrench } from "lucide-react";
import { api, ApiRequestError, type Check, type IssueDocument, type IssueSummary, type RemovedSentence } from "../api";
import { docToTiptap, nextPendingSentence, readyFromChecks } from "../docModel";
import { Commentary, DocActionsCtx, ItemHeading, Sentence, type DocActions } from "../editor/nodes";
import { useT } from "../i18n";
import { cn } from "../lib/cn";
import { errText } from "../lib/errors";
import { fmtDate } from "../lib/fmt";

// datetime-local inputs work in local time without a zone; the API gets
// the same instant as RFC3339 via new Date(v).toISOString().
function localInput(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}
import { Badge, Button, KDialog, KPopover } from "../ui";
import { CoverageCounter, EvidencePane, PautasPane, PreviewPane, ReadinessList, scrollInPane } from "../components/Panes";
import { BoardSkeleton, BuildBoard, StepRail } from "./Board";

// scroll the editor pane to a sentence/item element and flash it —
// constrained to the pane so other panes don't move
function flashEl(docEl: HTMLElement | null, sel: string) {
  const el = docEl?.querySelector<HTMLElement>(sel);
  if (!el || !docEl) return;
  scrollInPane(docEl, el);
  el.classList.remove("flash");
  void el.offsetWidth;
  el.classList.add("flash");
}

// statuses before the draft exists — the build board owns them
const BOARD_STATUSES = new Set(["draft", "reviewing", ""]);

export function IssuePage() {
  const { id } = useParams();
  const issueID = Number(id);
  const { t } = useT();
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const runID = params.get("run");
  const [runDone, setRunDone] = useState(!runID);
  const [runSummary, setRunSummary] = useState<string | null>(null);
  const issueQ = useQuery({ queryKey: ["issue", issueID], queryFn: () => api.issue(issueID) });
  const itemsQ = useQuery({ queryKey: ["items", issueID], queryFn: () => api.issueItems(issueID) });
  const docQ = useQuery({ queryKey: ["document", issueID], queryFn: () => api.document(issueID) });
  const readinessQ = useQuery({
    queryKey: ["readiness", issueID],
    queryFn: () => api.readiness(issueID),
    // readiness is cheap; refetch whenever doc/version may have changed
    refetchInterval: false,
  });

  const doc = docQ.data;
  const versionRef = useRef("");
  useEffect(() => {
    if (doc) versionRef.current = doc.version;
  }, [doc]);

  const [toast, setToast] = useState<{ text: string; undo?: RemovedSentence } | null>(null);
  const toastRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const showToast = useCallback((text: string, undo?: RemovedSentence) => {
    setToast({ text, undo });
    if (toastRef.current) clearTimeout(toastRef.current);
    toastRef.current = setTimeout(() => setToast(null), 8000);
  }, []);

  // a pending Undo dies with the context it belonged to — approval or
  // sending makes "restore that sentence" meaningless
  const issueStatus = issueQ.data?.status;
  useEffect(() => { setToast(null); }, [issueStatus]);

  // resync: a 409 carries the fresh document — accept it and tell the user
  const resync = useCallback(
    (err: unknown) => {
      if (err instanceof ApiRequestError && err.document) {
        qc.setQueryData(["document", issueID], err.document);
        showToast(t.errors.conflict);
      } else {
        showToast(errText(t, err));
      }
    },
    [qc, issueID, showToast, t],
  );

  const invalidate = useCallback(() => {
    qc.invalidateQueries({ queryKey: ["document", issueID] });
    qc.invalidateQueries({ queryKey: ["readiness", issueID] });
    qc.invalidateQueries({ queryKey: ["issue", issueID] });
  }, [qc, issueID]);

  // evidence pane selection
  const [sel, setSel] = useState<{ ref: number; issueItemId: number; citeRef?: number; sid?: string } | null>(null);
  const [rightTab, setRightTab] = useState<"evidence" | "preview">("evidence");
  const [mobileTab, setMobileTab] = useState<"list" | "doc" | "side">("doc");
  const editorWrapRef = useRef<HTMLDivElement>(null);
  const [saved, setSaved] = useState<"" | "saving" | "saved">("");

  const undoRemove = useCallback(
    async (removed: RemovedSentence) => {
      try {
        const r = await api.restoreSentence(issueID, removed, versionRef.current);
        versionRef.current = r.version;
        setToast(null);
        invalidate();
      } catch (e) {
        resync(e);
      }
    },
    [issueID, invalidate, resync],
  );

  const actions: DocActions = useMemo(
    () => ({
      readOnly: doc?.status === "sent",
      showEvidence: (ref, opts) => {
        const item = findItem(doc ?? null, ref);
        setSel({ ref, issueItemId: item?.issue_item_id ?? 0, citeRef: opts?.citeRef, sid: opts?.sid });
        setRightTab("evidence");
        if (window.innerWidth < 768) setMobileTab("side");
      },
      editSentence: async (sid, text, citations) => {
        try {
          const r = await api.editSentence(issueID, sid, text, citations, versionRef.current);
          versionRef.current = r.version;
          invalidate();
          return null;
        } catch (e) {
          if (e instanceof ApiRequestError && e.document) {
            // version went stale — accept the fresh document, still show the reason
            qc.setQueryData(["document", issueID], e.document);
          }
          return errText(t, e);
        }
      },
      interpret: async (sid, text) => {
        try {
          const r = await api.markInterpretation(issueID, sid, text, versionRef.current);
          versionRef.current = r.version;
          invalidate();
          return true;
        } catch (e) {
          resync(e);
          return false;
        }
      },
      removeSentence: async (sid) => {
        try {
          const r = await api.removeSentence(issueID, sid, versionRef.current);
          versionRef.current = r.version;
          invalidate();
          showToast(t.doc.removed, r.removed);
          return true;
        } catch (e) {
          resync(e);
          return false;
        }
      },
    }),
    [doc, issueID, invalidate, resync, qc, showToast, t],
  );

  // --- commentary autosave: watch editor updates, diff commentary
  // nodes against what the server last saw, PUT debounced.
  // Saves are serialized through one drain worker — concurrent PUTs
  // carrying the same version token race on the server (both read the
  // same base state, each writes only its own key → lost update). ---
  const savedCommentary = useRef<Map<string, string>>(new Map());
  const dirtyCommentary = useRef<Map<string, string>>(new Map());
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const draining = useRef(false);
  const drainSaves = useCallback(async () => {
    if (draining.current) return;
    draining.current = true;
    try {
      for (;;) {
        const next = dirtyCommentary.current.entries().next();
        if (next.done) return;
        const [section, text] = next.value;
        dirtyCommentary.current.delete(section);
        try {
          setSaved("saving");
          const r = await api.commentary(issueID, section, text, versionRef.current);
          versionRef.current = r.version;
          savedCommentary.current.set(section, text);
          setSaved("saved");
          qc.invalidateQueries({ queryKey: ["readiness", issueID] });
        } catch (e) {
          setSaved("");
          resync(e);
          return;
        }
      }
    } finally {
      draining.current = false;
    }
  }, [issueID, qc, resync]);
  const scheduleDrain = useCallback(() => {
    if (saveTimer.current) clearTimeout(saveTimer.current);
    saveTimer.current = setTimeout(() => {
      saveTimer.current = null;
      drainSaves();
    }, 900);
  }, [drainSaves]);

  const editor = useEditor(
    {
      extensions: [
        StarterKit.configure({ heading: { levels: [2] } }),
        Sentence,
        Commentary,
        ItemHeading,
      ],
      content: doc ? docToTiptap(doc) : { type: "doc", content: [] },
      editable: false, // enabled by the effect below once the document loads
      editorProps: { attributes: { class: "focus:outline-none px-4 py-2" } },
      onUpdate: ({ editor: ed }) => {
        let dirty = false;
        ed.state.doc.descendants((n) => {
          if (n.type.name !== "commentary") return true;
          const section = String(n.attrs.section);
          const text = n.textContent;
          if (savedCommentary.current.get(section) === text) return true;
          dirtyCommentary.current.set(section, text);
          dirty = true;
          return true;
        });
        if (dirty) scheduleDrain();
      },
    },
    [issueID],
  );

  // reset baseline when a fresh document arrives (version changes)
  useEffect(() => {
    if (!editor || !doc) return;
    savedCommentary.current = new Map(doc.sections.map((s) => [s.title, s.commentary.text]));
    dirtyCommentary.current.clear();
    editor.commands.setContent(docToTiptap(doc));
    // setContent leaves a selection spanning the whole fresh document — a
    // keystroke arriving before a click's selection sync would replace the
    // entire issue. Collapse it to a cursor at the start.
    editor.commands.setTextSelection(0);
  }, [editor, doc?.version]); // eslint-disable-line react-hooks/exhaustive-deps

  // contenteditable follows the issue status once the document arrives —
  // the editor instance is created before the first fetch resolves
  useEffect(() => {
    editor?.setEditable(!!doc && doc.status !== "sent");
  }, [editor, doc?.status]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    (window as unknown as Record<string, unknown>).__ed = editor;
  }, [editor]);

  const goToSentence = useCallback(
    (selStr: string) => {
      if (window.innerWidth < 768) setMobileTab("doc");
      setTimeout(() => flashEl(editorWrapRef.current, selStr), 60);
    },
    [setMobileTab],
  );

  const fixNow = () => {
    if (!doc) return;
    const sid = nextPendingSentence(doc);
    if (sid) goToSentence(`[data-sid="${sid}"]`);
  };

  const onCheckTarget = (c: Check) => {
    if (!c.target) {
      fixNow();
      return;
    }
    if (c.target.kind === "sentence") goToSentence(`[data-sid="${c.target.ref}"]`);
    else if (c.target.kind === "item") goToSentence(`[data-item-ref="${c.target.ref}"]`);
    else if (c.target.kind === "section") goToSentence(`[data-commentary="${c.target.ref}"]`);
    else if (c.target.kind === "issue") {
      // open story decisions live in the list pane
      if (window.innerWidth < 768) setMobileTab("list");
    }
  };

  if (issueQ.isLoading || !issueQ.data) {
    return <p className="p-6 text-sm text-muted">…</p>;
  }
  const issue = issueQ.data;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* top bar */}
      <header className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-2.5">
        <Link to={`/publications/${issue.publication_id}`} className="inline-flex items-center gap-1 text-xs text-muted hover:text-ink">
          <ArrowLeft size={13} /> {t.back}
        </Link>
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold">
            #{issue.number} · {issue.subject}
          </p>
        </div>
        <ol className="flex items-center gap-1 text-[11px]" aria-label={(t.status as Record<string, string>)[issue.status]}>
          {t.board.steps.map((label, i) => {
            const step =
              issue.status === "drafted" ? 2
              : issue.status === "approved" || issue.status === "sent" || issue.status === "scheduled" ? 3
              : 1;
            return (
              <li key={label} className="flex items-center gap-1">
                {i > 0 && <span className="mx-0.5 text-muted/40">·</span>}
                <span
                  className={cn(
                    "inline-flex h-4 w-4 items-center justify-center rounded-full text-[10px]",
                    i + 1 < step ? "bg-teal-soft text-teal" : i + 1 === step ? "bg-ink text-paper" : "bg-line text-muted",
                  )}
                >
                  {i + 1}
                </span>
                <span className={i + 1 === step ? "font-semibold text-ink" : i + 1 < step ? "text-teal" : "text-muted"}>
                  {label}
                </span>
              </li>
            );
          })}
        </ol>
        <span className="flex-1" />
        {saved && (
          <span className="rounded-full bg-teal-soft px-2 py-0.5 text-[11px] text-teal">
            {saved === "saving" ? t.commentary.saving : t.commentary.saved}
          </span>
        )}
        {!BOARD_STATUSES.has(issue.status) && doc && (
          <CoverageCounter coverage={doc.coverage} onFixNow={fixNow} />
        )}
        {issue.status === "approved" && (
          <KPopover
            trigger={
              <Button size="sm" variant="danger">
                <Send size={12} /> {t.send.action}
              </Button>
            }
          >
            {issue.receipt && (
              <p className="mb-2 text-xs text-muted">
                {t.receipt.by} {issue.receipt.approver} · {t.receipt.version}{" "}
                {issue.receipt.document_version.slice(0, 8)}
              </p>
            )}
            <SendRow issue={issue} onDone={() => invalidate()} onToast={showToast} />
          </KPopover>
        )}
        {readinessQ.data && !BOARD_STATUSES.has(issue.status) && issue.status !== "sent" && (
          <ApproveBar
            issue={issue}
            checks={readinessQ.data.checks}
            coverage={doc?.coverage ?? readinessQ.data.coverage}
            onTarget={onCheckTarget}
            onApproved={() => invalidate()}
          />
        )}
        {issue.status === "scheduled" && <HoldBar issue={issue} onDone={() => invalidate()} />}
        {issue.status === "sent" && <Badge tone="ink">{t.send.sent}</Badge>}
      </header>

      {BOARD_STATUSES.has(issue.status) ? (
        runID && !runDone ? (
          <div className="flex min-h-0 flex-1 flex-col">
            <div className="mx-auto w-full max-w-md p-6">
              <h2 className="mb-4 text-lg font-semibold">{t.board.preparing}</h2>
              <StepRail
                runID={runID}
                steps={["collect", "dedupe", "triage", "storylist", "dossier", "deepdive"]}
                onDone={(run, events) => {
                  const bits: string[] = [];
                  const labels = t.run.counts as Record<string, string | ((n: number) => string)>;
                  for (const key of ["items_read", "duplicates_merged", "ranked", "selected"]) {
                    const ev = [...events].reverse().find((e) => e.counts && key in e.counts);
                    if (ev?.counts) {
                      const lbl = labels[key] ?? key;
                      bits.push(`${ev.counts[key]} ${typeof lbl === "function" ? lbl(ev.counts[key]) : lbl}`);
                    }
                  }
                  if (bits.length) setRunSummary(bits.join(" · "));
                  setRunDone(true);
                  setParams((p) => { p.delete("run"); return p; }, { replace: true });
                  qc.invalidateQueries({ queryKey: ["candidates", issueID] });
                  qc.invalidateQueries({ queryKey: ["issue", issueID] });
                  if (run.no_new_items) showToast(t.run.noNewItems);
                  if (run.error) showToast(run.error);
                }}
              />
            </div>
            <BoardSkeleton />
          </div>
        ) : (
          <div className="flex min-h-0 flex-1 flex-col">
            {runSummary && <p className="border-b border-line px-4 py-1.5 text-xs text-muted">{runSummary}</p>}
            <BuildBoard issue={issue} onDrafted={() => invalidate()} />
          </div>
        )
      ) : (
        doc && (
          <>
            {/* all three panes stay mounted — visibility toggles per
                breakpoint so the editor is never double-mounted */}
            <div className="flex min-h-0 flex-1 flex-col md:flex-row">
              <aside
                className={cn(
                  "min-h-0 shrink-0 overflow-y-auto border-line md:w-64 md:flex-none md:border-r",
                  mobileTab === "list" ? "block flex-1" : "hidden md:block",
                )}
              >
                <PautasPane
                  doc={doc}
                  items={itemsQ.data?.items}
                  selectedRef={sel?.ref ?? null}
                  onSelect={(ref, iid) => actions.showEvidence(ref, { issueItemId: iid })}
                />
              </aside>
              <main
                ref={editorWrapRef}
                className={cn(
                  "min-h-0 min-w-0 flex-1 overflow-y-auto",
                  mobileTab === "doc" ? "block" : "hidden md:block",
                )}
              >
                <DocActionsCtx.Provider value={actions}>
                  <EditorContent editor={editor} />
                </DocActionsCtx.Provider>
              </main>
              <aside
                className={cn(
                  "min-h-0 shrink-0 flex-col border-line md:flex md:w-80 md:flex-none md:border-l",
                  mobileTab === "side" ? "flex flex-1" : "hidden",
                )}
              >
                <div className="flex border-b border-line text-xs">
                  {(["evidence", "preview"] as const).map((tb) => (
                    <button
                      key={tb}
                      onClick={() => setRightTab(tb)}
                      className={cn(
                        "flex-1 px-3 py-2 font-medium",
                        rightTab === tb ? "border-b-2 border-teal text-ink" : "text-muted",
                      )}
                    >
                      {tb === "evidence" ? t.evidence.title : t.preview.title}
                    </button>
                  ))}
                </div>
                <div className="min-h-0 flex-1">
                  {rightTab === "evidence" ? (
                    <EvidencePane
                      item={sel ? findItem(doc, sel.ref) : null}
                      citeRef={sel?.citeRef}
                      sid={sel?.sid}
                    />
                  ) : (
                    <PreviewPane issueID={issueID} />
                  )}
                </div>
              </aside>
            </div>

            {/* phone tab bar — bottom, per spec */}
            <nav className="flex border-t border-line text-xs md:hidden">
              {(
                [
                  ["list", <BookOpen key="i" size={15} />, t.pautas],
                  ["doc", <ClipboardCheck key="i" size={15} />, t.issue],
                  ["side", <Eye key="i" size={15} />, rightTab === "evidence" ? t.evidence.title : t.preview.title],
                ] as const
              ).map(([k, icon, label]) => (
                <button
                  key={k}
                  onClick={() => setMobileTab(k)}
                  aria-pressed={mobileTab === k}
                  className={cn(
                    "flex flex-1 flex-col items-center gap-0.5 py-2",
                    mobileTab === k ? "text-teal" : "text-muted",
                  )}
                >
                  {icon}
                  {label}
                </button>
              ))}
              <button
                onClick={() => {
                  setRightTab(rightTab === "evidence" ? "preview" : "evidence");
                  setMobileTab("side");
                }}
                className="flex flex-1 flex-col items-center gap-0.5 py-2 text-muted"
              >
                <Wrench size={15} />
                {rightTab === "evidence" ? t.preview.title : t.evidence.title}
              </button>
            </nav>
          </>
        )
      )}

      {(issue.status === "sent" || issue.status === "scheduled") && <AfterSend issue={issue} />}
      {toast && (
        <div className="fixed bottom-4 left-1/2 z-50 flex -translate-x-1/2 items-center gap-3 rounded-full bg-ink px-4 py-2 text-xs text-paper shadow-lg">
          {toast.text}
          {toast.undo && (
            <button className="font-semibold underline" onClick={() => undoRemove(toast.undo!)}>
              {t.doc.undo}
            </button>
          )}
          {toast.undo && (
            <button className="text-muted" aria-label="dismiss" onClick={() => setToast(null)}>
              ✕
            </button>
          )}
        </div>
      )}
    </div>
  );
}

function findItem(doc: IssueDocument | null, ref: number) {
  return doc?.sections.flatMap((s) => s.items ?? []).find((i) => i.ref === ref) ?? null;
}

// --- approve popover + dialog ---

function ApproveBar({
  issue,
  checks,
  coverage,
  onTarget,
  onApproved,
}: {
  issue: IssueSummary;
  checks: Check[];
  coverage: Parameters<typeof CoverageCounter>[0]["coverage"];
  onTarget: (c: Check) => void;
  onApproved: () => void;
}) {
  const { t } = useT();
  const ready = readyFromChecks(checks);
  const [confirm, setConfirm] = useState(false);
  const [audience, setAudience] = useState<"nobody" | "readers">("nobody");
  const [when, setWhen] = useState<"now" | "schedule">("now");
  const [at, setAt] = useState(() => localInput(new Date(Date.now() + 24 * 3600e3)));
  const pubQ = useQuery({
    queryKey: ["publication", issue.publication_id],
    queryFn: () => api.publication(issue.publication_id),
  });
  const readerQ = useQuery({
    queryKey: ["reader", issue.publication_id],
    queryFn: () => api.reader(issue.publication_id),
    enabled: audience === "readers",
  });
  const pub = pubQ.data?.publication;
  const atOK = when !== "schedule" || new Date(at).getTime() > Date.now();
  // the dialog approves first, then sends per the chosen audience —
  // "Nobody (test only)" only approves; readers send now or schedules.
  const flow = useMutation({
    mutationFn: async () => {
      await api.approve(issue.id);
      if (audience === "readers") {
        await api.send(issue.id, "readers", when, undefined,
          when === "schedule" ? new Date(at).toISOString() : undefined);
      }
    },
    onSuccess: () => {
      setConfirm(false);
      onApproved();
    },
  });

  return (
    <>
      <KPopover
        trigger={
          <Button size="sm" variant={ready ? "teal" : "outline"}>
            {issue.status === "approved" ? `${t.readiness.approvedAt}` : t.readiness.approve}
            {issue.status === "approved" && issue.receipt && (
              <span className="text-[10px] opacity-80">#{issue.receipt.number}</span>
            )}
          </Button>
        }
      >
        <ReadinessList checks={checks} coverage={coverage} onTarget={onTarget} />
        {ready && issue.status !== "approved" && (
          <Button className="mt-3 w-full" variant="teal" size="sm" onClick={() => setConfirm(true)}>
            {t.readiness.approve}
          </Button>
        )}
        {issue.status === "approved" && issue.receipt && (
          <div className="mt-3 border-t border-line pt-2 text-xs text-muted">
            {t.receipt.by} {issue.receipt.approver} · {t.receipt.version} {issue.receipt.document_version.slice(0, 8)}
          </div>
        )}
      </KPopover>

      <KDialog open={confirm} onOpenChange={setConfirm} title={t.send.approveSend}>
        <p className="mb-3 text-sm font-semibold">
          #{issue.number} · {issue.subject}
        </p>
        <div className="mb-3">
          <p className="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">{t.send.audience}</p>
          <label className="flex items-center gap-2 py-1 text-sm">
            <input type="radio" name="aud" checked={audience === "nobody"} onChange={() => setAudience("nobody")} />
            {t.send.nobody}
          </label>
          <label className="flex items-center gap-2 py-1 text-sm">
            <input type="radio" name="aud" checked={audience === "readers"} onChange={() => setAudience("readers")} />
            {t.send.allReaders}
          </label>
        </div>
        {audience === "readers" && (
          <div className="mb-3">
            <p className="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">{t.send.when}</p>
            <label className="flex items-center gap-2 py-1 text-sm">
              <input type="radio" name="when" checked={when === "now"} onChange={() => setWhen("now")} />
              {t.send.now}
            </label>
            <label className="flex items-center gap-2 py-1 text-sm">
              <input type="radio" name="when" checked={when === "schedule"} onChange={() => setWhen("schedule")} />
              {t.send.schedule}
            </label>
            {when === "schedule" && (
              <label className="mt-1 flex items-center gap-2 pl-6 text-xs text-muted">
                {t.send.at}
                <input
                  type="datetime-local"
                  className="rounded border border-line bg-paper px-2 py-1 text-xs text-ink"
                  min={localInput(new Date())}
                  value={at}
                  onChange={(e) => setAt(e.target.value)}
                />
                {!atOK && <span className="text-danger">{t.send.pickTime}</span>}
              </label>
            )}
          </div>
        )}
        <dl className="mb-4 space-y-1 border-t border-line pt-3 text-xs text-muted">
          <div className="flex justify-between">
            <dt>{t.send.version}</dt>
            <dd className="font-mono">{(issue.document_version ?? "").slice(0, 8)}</dd>
          </div>
          {coverage && (
            <div className="flex justify-between">
              <dt>{t.send.sourced}</dt>
              <dd>
                {coverage.cited}/{coverage.total}
              </dd>
            </div>
          )}
          {audience === "readers" && (
            <div className="flex justify-between">
              <dt>{t.send.recipients}</dt>
              <dd>{t.send.subscriberCount(readerQ.data?.subscribers ?? 0)}</dd>
            </div>
          )}
          <div className="flex justify-between">
            <dt>{t.send.sender}</dt>
            <dd>{pub?.name} &lt;{pub?.slug}@kloxus.demo&gt;</dd>
          </div>
        </dl>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setConfirm(false)}>
            {t.doc.cancel}
          </Button>
          <Button
            variant={audience === "readers" ? "danger" : "teal"}
            disabled={flow.isPending || !atOK}
            onClick={() => flow.mutate()}
          >
            {t.send.confirm}
          </Button>
        </div>
        {flow.error && <p className="mt-2 text-xs text-danger">{errText(t, flow.error)}</p>}
      </KDialog>
    </>
  );
}

function SendRow({ issue, onDone, onToast }: {
  issue: IssueSummary; onDone: () => void; onToast: (text: string) => void;
}) {
  const { t } = useT();
  const [at, setAt] = useState(() => localInput(new Date(Date.now() + 24 * 3600e3)));
  const [confirmSend, setConfirmSend] = useState<null | { when: "now" | "schedule" }>(null);
  const atOK = new Date(at).getTime() > Date.now();
  const pubQ = useQuery({
    queryKey: ["publication", issue.publication_id],
    queryFn: () => api.publication(issue.publication_id),
  });
  const readerQ = useQuery({
    queryKey: ["reader", issue.publication_id],
    queryFn: () => api.reader(issue.publication_id),
  });
  const sendMut = useMutation({
    mutationFn: ({ aud, when }: { aud: "test" | "readers"; when: "now" | "schedule" }) =>
      api.send(issue.id, aud, when, undefined,
        when === "schedule" ? new Date(at).toISOString() : undefined),
    onSuccess: (r, { aud }) => {
      setConfirmSend(null);
      // a test send lands in the outbox — say where it went
      if (r.to?.length) onToast(aud === "test" ? t.send.testSentTo(r.to.join(", ")) : t.send.sentTo(r.to.join(", ")));
      onDone();
    },
  });
  const pub = pubQ.data?.publication;
  return (
    <div className="mt-2 space-y-2">
      <div className="flex gap-2">
        <Button size="sm" variant="outline" disabled={sendMut.isPending} onClick={() => sendMut.mutate({ aud: "test", when: "now" })}>
          <Send size={12} /> {t.send.test}
        </Button>
        <Button size="sm" variant="danger" disabled={sendMut.isPending} onClick={() => setConfirmSend({ when: "now" })}>
          {t.send.readers}
        </Button>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <input
          type="datetime-local"
          aria-label={t.send.at}
          className="rounded border border-line bg-paper px-2 py-1 text-xs text-ink"
          min={localInput(new Date())}
          value={at}
          onChange={(e) => setAt(e.target.value)}
        />
        <Button
          size="sm" variant="ghost"
          disabled={sendMut.isPending || !atOK}
          onClick={() => setConfirmSend({ when: "schedule" })}
        >
          {t.send.schedule}
        </Button>
        {!atOK && <span className="text-[11px] text-danger">{t.send.pickTime}</span>}
      </div>
      {sendMut.error && <p className="text-xs text-danger">{errText(t, sendMut.error)}</p>}

      <KDialog open={confirmSend !== null} onOpenChange={(o) => !o && setConfirmSend(null)} title={t.send.confirmSend}>
        <dl className="mb-4 space-y-1 text-sm">
          <div className="flex justify-between gap-4">
            <dt className="text-muted">{t.send.subject}</dt>
            <dd className="text-right font-medium">#{issue.number} · {issue.subject}</dd>
          </div>
          <div className="flex justify-between gap-4">
            <dt className="text-muted">{t.send.recipients}</dt>
            <dd>{t.send.subscriberCount(readerQ.data?.subscribers ?? 0)}</dd>
          </div>
          <div className="flex justify-between gap-4">
            <dt className="text-muted">{t.send.sender}</dt>
            <dd>{pub?.name} &lt;{pub?.slug}@kloxus.demo&gt;</dd>
          </div>
          {confirmSend?.when === "schedule" && (
            <div className="flex justify-between gap-4">
              <dt className="text-muted">{t.send.at}</dt>
              <dd>{new Date(at).toLocaleString()}</dd>
            </div>
          )}
        </dl>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setConfirmSend(null)}>{t.doc.cancel}</Button>
          <Button
            variant="danger"
            disabled={sendMut.isPending}
            onClick={() => sendMut.mutate({ aud: "readers", when: confirmSend!.when })}
          >
            {confirmSend?.when === "schedule" ? t.send.schedule : t.send.now}
          </Button>
        </div>
      </KDialog>
    </div>
  );
}

// HoldBar surfaces the scheduled state in the header: the issue is
// locked until it ships; Hold returns it to draft for re-approval.
function HoldBar({ issue, onDone }: { issue: IssueSummary; onDone: () => void }) {
  const { t, lang } = useT();
  const holdMut = useMutation({
    mutationFn: () => api.hold(issue.id),
    onSuccess: onDone,
  });
  return (
    <KPopover
      trigger={
        <Button size="sm" variant="outline">
          {t.send.scheduled}
          {issue.scheduled_at && (
            <span className="text-[10px] opacity-80">· {fmtDate(issue.scheduled_at, lang, true)}</span>
          )}
        </Button>
      }
    >
      <p className="mb-2 text-xs text-muted">
        {issue.scheduled_at && <>{t.send.scheduledFor} {fmtDate(issue.scheduled_at, lang, true)} — </>}
        {t.send.scheduledHint}
      </p>
      <Button size="sm" variant="danger" disabled={holdMut.isPending} onClick={() => holdMut.mutate()}>
        {t.send.hold}
      </Button>
      {holdMut.error && <p className="mt-2 text-xs text-danger">{errText(t, holdMut.error)}</p>}
    </KPopover>
  );
}

// AfterSend is the "after sending" surface: public link, downloads, the
// receipt, what the guide learned and the way to the next issue.
function AfterSend({ issue }: { issue: IssueSummary }) {
  const { t, lang } = useT();
  const nav = useNavigate();
  const afterQ = useQuery({ queryKey: ["after", issue.id], queryFn: () => api.after(issue.id) });
  const next = useMutation({
    mutationFn: () => api.startRun(issue.publication_id),
    onSuccess: (r) => nav(`/runs/${r.id}`),
  });
  const a = afterQ.data;
  const r = issue.receipt;
  return (
    <div className="border-t border-line bg-paper-raised px-4 py-3">
      <div className="mx-auto flex max-w-3xl flex-col gap-3">
        <div className="flex flex-wrap items-center gap-x-6 gap-y-1 text-xs">
          {r && (
            <span className="text-muted">
              {t.receipt.by} {r.approver} · {fmtDate(r.approved_at, lang, true)} · {t.receipt.version}{" "}
              {r.document_version.slice(0, 8)}
            </span>
          )}
          {issue.status === "scheduled" && issue.scheduled_at && (
            <span className="font-semibold text-ink">
              {t.send.scheduledFor} {fmtDate(issue.scheduled_at, lang, true)}
            </span>
          )}
          {a && (
            <a href={a.public_url} target="_blank" rel="noreferrer" className="font-semibold text-teal hover:underline">
              {t.after.open} ↗
            </a>
          )}
          {a && (
            <span className="text-muted">
              {t.after.downloads}:{" "}
              <a className="text-ink underline" href={a.export_html}>HTML</a>
              {" · "}
              <a className="text-ink underline" href={a.export_md}>Markdown</a>
            </span>
          )}
          <span className="flex-1" />
          <Button size="sm" variant="teal" disabled={next.isPending} onClick={() => next.mutate()}>
            {t.after.next}
          </Button>
        </div>
        {a && (a.learned.length > 0 ? (
          <div className="text-xs">
            <p className="mb-1 font-semibold uppercase tracking-wide text-muted">
              {t.after.learned} · {t.guide.title} v{a.guide.version}
            </p>
            <ul className="list-disc space-y-0.5 pl-5 text-ink/80">
              {a.learned.map((rule, i) => <li key={i}>{rule}</li>)}
            </ul>
          </div>
        ) : (
          <p className="text-xs text-muted">{t.after.learnedEmpty}</p>
        ))}
        {next.error && <p className="text-xs text-danger">{errText(t, next.error)}</p>}
      </div>
    </div>
  );
}
