import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { EditorContent, useEditor } from "@tiptap/react";
import { TextSelection } from "@tiptap/pm/state";
import StarterKit from "@tiptap/starter-kit";
import { BookOpen, CalendarClock, ChevronDown, ClipboardCheck, Download, ExternalLink, Eye, Pencil, Plus, Send, Trash2, Wrench } from "lucide-react";
import { api, ApiRequestError, type Candidate, type Check, type IssueDocument, type IssueSummary, type RemovedSentence } from "../api";
import { blockTextMd, docToTiptap, nextPendingSentence, readyFromChecks } from "../docModel";
import { BlockAdd, BlockDivider, BlockEmbed, BlockHeading, BlockImage, BlockLink, BlockText, Commentary, DocActionsCtx, FIX_SENTENCE_EVENT, insideEditable, IssueDoc, ItemHeading, SectionHeading, Sentence, type DocActions } from "../editor/nodes";
import { useT } from "../i18n";
import { useComposer } from "../editor/composerStrings";
import { cn } from "../lib/cn";
import { errText } from "../lib/errors";
import { fmtDate } from "../lib/fmt";

// datetime-local inputs work in local time without a zone; the API gets
// the same instant as RFC3339 via new Date(v).toISOString().
function localInput(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}
import { Badge, Button, buttonVariants } from "../ui";
import { KDialog, KPopover } from "../ui-overlays";
import { ClaimEmail } from "../components/ClaimEmail";
import { StepCounter } from "../components/StepCounter";
import { CoverageCounter, EvidencePane, PautasPane, PreviewPane, ReadinessList, scrollInPane } from "../components/Panes";
import { BlockedCard, BoardSkeleton, BuildBoard, StepRail } from "./Board";
import { NotFound } from "./ErrorPage";

// scroll the editor pane to a sentence/item element and flash it —
// constrained to the pane so other panes don't move. Returns the
// element so callers can open its fix affordance too (KLOX-52).
function flashEl(docEl: HTMLElement | null, sel: string): HTMLElement | null {
  const el = docEl?.querySelector<HTMLElement>(sel);
  if (!el || !docEl) return null;
  scrollInPane(docEl, el);
  el.classList.remove("flash");
  void el.offsetWidth;
  el.classList.add("flash");
  return el;
}

// statuses before the draft exists — the build board owns them; a
// 'blocked' issue keeps whatever the interrupted run produced.
const BOARD_STATUSES = new Set(["draft", "reviewing", "blocked", ""]);

export function IssuePage() {
  const { id } = useParams();
  const issueID = Number(id);
  const { t } = useT();
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const runID = params.get("run");
  const [runDone, setRunDone] = useState(!runID);
  const [runBlocked, setRunBlocked] = useState<string | null>(null);
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

  const [toast, setToast] = useState<{ text: string; undo?: RemovedSentence[] } | null>(null);
  const toastRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const showToast = useCallback((text: string, undo?: RemovedSentence[]) => {
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
    // a send can change the confirmed-reader count the dialog shows
    // (a test send confirms the claimed owner as a reader)
    qc.invalidateQueries({ queryKey: ["reader"] });
  }, [qc, issueID]);

  // evidence pane selection
  const [sel, setSel] = useState<{ ref: number; issueItemId: number; citeRef?: number; sid?: string } | null>(null);
  const [rightTab, setRightTab] = useState<"evidence" | "preview">("evidence");
  const [mobileTab, setMobileTab] = useState<"list" | "doc" | "side">("doc");
  const editorWrapRef = useRef<HTMLDivElement>(null);
  const [saved, setSaved] = useState<"" | "saving" | "saved">("");

  const undoRemove = useCallback(
    async (removed: RemovedSentence[]) => {
      try {
        const r = await api.restoreSentences(issueID, removed[0].ref, removed, versionRef.current);
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
          showToast(t.doc.removed, r.removed ? [r.removed] : undefined);
          return true;
        } catch (e) {
          resync(e);
          return false;
        }
      },
      removeFlagged: async (itemRef) => {
        try {
          const r = await api.removeFlagged(issueID, itemRef, versionRef.current);
          versionRef.current = r.version;
          invalidate();
          showToast(t.doc.removedFlagged(r.removed?.length ?? 0), r.removed);
          return true;
        } catch (e) {
          resync(e);
          return false;
        }
      },
      // manual compose: append editor text to an item — same refusal
      // contract as editSentence (null ok, reason string to show inline)
      addSentence: async (itemRef, text, citations, kind) => {
        try {
          const r = await api.composeSentence(issueID, itemRef, text, citations, kind, versionRef.current);
          versionRef.current = r.version;
          invalidate();
          return null;
        } catch (e) {
          if (e instanceof ApiRequestError && e.document) {
            qc.setQueryData(["document", issueID], e.document);
          }
          return errText(t, e);
        }
      },
      // --- composer blocks (KLOX-66): every op answers with the fresh
      // document — swap it into the cache and keep its version
      moveBlock: async (section, index, dir) => {
        // a move past the section's edge is a no-op — sending it anyway
        // answered 400 validation and showed an unexplained error toast
        const sec = doc?.sections.find((s) => s.title === section);
        const n = sec?.blocks?.length ?? sec?.items?.length ?? 0;
        const to = index + dir;
        if (to < 0 || to >= n) return true;
        try {
          await flushRef.current();
          const r = await api.blockMove(issueID, section, index, to, versionRef.current);
          versionRef.current = r.document.version;
          qc.setQueryData(["document", issueID], r.document);
          invalidate();
          return true;
        } catch (e) {
          resync(e);
          return false;
        }
      },
      removeBlock: async (section, index) => {
        try {
          await flushRef.current();
          const r = await api.blockRemove(issueID, section, index, versionRef.current);
          versionRef.current = r.document.version;
          qc.setQueryData(["document", issueID], r.document);
          invalidate();
          showToast(t.doc.removed);
          return true;
        } catch (e) {
          resync(e);
          return false;
        }
      },
      updateBlock: async (section, index, text, url) => {
        try {
          await flushRef.current();
          const r = await api.blockUpdate(issueID, { section, index, text, url, version: versionRef.current });
          versionRef.current = r.document.version;
          qc.setQueryData(["document", issueID], r.document);
          invalidate();
          return null;
        } catch (e) {
          if (e instanceof ApiRequestError && e.document) {
            qc.setQueryData(["document", issueID], e.document);
          }
          return errText(t, e);
        }
      },
      insertBlock: async (section, kind, position, text, url) => {
        try {
          await flushRef.current();
          const r = await api.blockInsert(issueID, { section, kind, text, url, position, version: versionRef.current });
          versionRef.current = r.document.version;
          qc.setQueryData(["document", issueID], r.document);
          invalidate();
          return null;
        } catch (e) {
          if (e instanceof ApiRequestError && e.document) {
            qc.setQueryData(["document", issueID], e.document);
          }
          return errText(t, e);
        }
      },
      openInsert: (section, index, kind) => setInsertDlg({ section, index, kind }),
      uploadImage: async (dataB64, contentType, name) => {
        try {
          const r = await api.uploadMedia(issueID, { content_type: contentType, data_b64: dataB64, name });
          return r.media.url;
        } catch {
          return null;
        }
      },
      toggleLinkOnly: async (ref, linkOnly) => {
        try {
          await flushRef.current();
          const r = await api.itemLinkOnly(issueID, ref, linkOnly, versionRef.current);
          versionRef.current = r.document.version;
          qc.setQueryData(["document", issueID], r.document);
          invalidate();
          return true;
        } catch (e) {
          resync(e);
          return false;
        }
      },
      pickPauta: (section, position) => setPautaPick({ section, position }),
    }),
    [doc, issueID, invalidate, resync, qc, showToast, t],
  );

  // the "+ Pauta" affordance opens a picker over the bench — stories
  // already in the edition (or rejected) never appear (KLOX-66)
  const [pautaPick, setPautaPick] = useState<{ section: string; position?: number } | null>(null);
  // the image / embed / standard-message insert dialogs the gap menu
  // opens — the page owns them like the pauta picker (KLOX-66 D3)
  const [insertDlg, setInsertDlg] = useState<
    { section: string; index: number; kind: "image" | "embed" | "message" } | null
  >(null);

  // --- commentary autosave: watch editor updates, diff commentary
  // nodes against what the server last saw, PUT debounced.
  // Saves are serialized through one drain worker — concurrent PUTs
  // carrying the same version token race on the server (both read the
  // same base state, each writes only its own key → lost update). ---
  const savedCommentary = useRef<Map<string, string>>(new Map());
  const dirtyCommentary = useRef<Map<string, string>>(new Map());
  // same pattern for rich-text blocks: `sec:index` → markdown baseline
  // and dirty value, PUT through the block update endpoint (D3)
  const savedText = useRef<Map<string, string>>(new Map());
  const dirtyText = useRef<Map<string, string>>(new Map());
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const draining = useRef(false);
  // the DocActions memo is created before the drain machinery — calls
  // go through this ref, wired to flushSaves once it exists
  const flushRef = useRef<() => Promise<void>>(async () => {});
  const drainSaves = useCallback(async () => {
    if (draining.current) return;
    draining.current = true;
    try {
      for (;;) {
        const nextC = dirtyCommentary.current.entries().next();
        const nextT = nextC.done ? dirtyText.current.entries().next() : null;
        if (nextC.done && (!nextT || nextT.done)) return;
        const isText = nextC.done;
        const [key, text] = (isText ? nextT!.value : nextC.value) as [string, string];
        if (isText) dirtyText.current.delete(key);
        else dirtyCommentary.current.delete(key);
        try {
          setSaved("saving");
          if (isText) {
            const [section, idx] = key.split(":");
            const r = await api.blockUpdate(issueID, {
              section, index: Number(idx), text, version: versionRef.current,
            });
            versionRef.current = r.document.version;
            savedText.current.set(key, text);
          } else {
            const r = await api.commentary(issueID, key, text, versionRef.current);
            versionRef.current = r.version;
            savedCommentary.current.set(key, text);
          }
          setSaved("saved");
          qc.invalidateQueries({ queryKey: ["readiness", issueID] });
        } catch (e) {
          // re-queue so a later drain retries instead of dropping the
          // typed text — a conflict carries the fresh document, so once
          // resync lands the new version the retry can save it. A
          // deterministic 4xx (validation, not_found) can never succeed
          // — drop it instead of re-toasting on every later drain and
          // spinning flush-and-mutate ops forever.
          const permanent =
            e instanceof ApiRequestError && e.status >= 400 && e.status < 500 && !e.document;
          if (!permanent) {
            if (isText) dirtyText.current.set(key, text);
            else dirtyCommentary.current.set(key, text);
          }
          setSaved("");
          resync(e);
          if (e instanceof ApiRequestError && e.document) {
            setTimeout(() => drainSaves(), 1000);
          }
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
  // structural actions (block insert/move/delete, item edits) must see
  // typed-but-unsaved text already persisted, so they flush the queue
  // first instead of racing the debounce
  const flushSaves = useCallback(async () => {
    if (saveTimer.current) {
      clearTimeout(saveTimer.current);
      saveTimer.current = null;
    }
    for (;;) {
      await drainSaves();
      if (!dirtyCommentary.current.size && !dirtyText.current.size) return;
    }
  }, [drainSaves]);
  flushRef.current = flushSaves;

  const editor = useEditor(
    {
      extensions: [
        // no editable heading node, no gap cursor at atom boundaries and
        // a top-level schema that admits only the issue's real nodes —
        // free paragraphs simply cannot be created in this editor
        // StarterKit registers bold/italic/link already — link clicks
        // must not navigate inside the editor
        StarterKit.configure({
          heading: false, gapcursor: false, document: false,
          link: { openOnClick: false },
        }),
        IssueDoc,
        Sentence,
        Commentary,
        ItemHeading,
        SectionHeading,
        BlockHeading,
        BlockDivider,
        BlockLink,
        BlockImage,
        BlockEmbed,
        BlockText,
        BlockAdd,
      ],
      content: doc ? docToTiptap(doc) : { type: "doc", content: [] },
      editable: false, // enabled by the effect below once the document loads
      editorProps: {
        attributes: { class: "focus:outline-none px-4 py-2" },
        // the schema already forbids free paragraphs; these guards make
        // it airtight — a click landing on a boundary between atoms, or
        // a stray selection, can never turn a keystroke into text outside
        // an editable region (commentary or a rich-text block)
        handleTextInput: (view, from) =>
          !insideEditable(view.state.doc.resolve(from)),
        handleKeyDown: (view, event) =>
          ["Enter", "Backspace", "Delete"].includes(event.key) &&
          !insideEditable(view.state.selection.$from),
        handlePaste: (view) =>
          !insideEditable(view.state.selection.$from),
      },
      onUpdate: ({ editor: ed }) => {
        let dirty = false;
        ed.state.doc.descendants((n) => {
          if (n.type.name === "commentary") {
            const section = String(n.attrs.section);
            const text = n.textContent;
            if (savedCommentary.current.get(section) === text) return true;
            dirtyCommentary.current.set(section, text);
            dirty = true;
            return true;
          }
          if (n.type.name === "blockText") {
            const key = `${n.attrs.section}:${n.attrs.index}`;
            const md = blockTextMd(n.toJSON() as Parameters<typeof blockTextMd>[0]);
            if (savedText.current.get(key) === md) return true;
            dirtyText.current.set(key, md);
            dirty = true;
            return true;
          }
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
    // rich-text baselines keyed `sec:index` — the same identity the
    // block update endpoint consumes
    savedText.current = new Map(
      doc.sections.flatMap((s) =>
        (s.blocks ?? [])
          .map((b, i) => [b, i] as const)
          .filter(([b]) => b.kind === "text")
          .map(([b, i]) => [`${s.title}:${i}`, b.text ?? ""]),
      ),
    );
    dirtyText.current.clear();
    editor.commands.setContent(docToTiptap(doc));
    // setContent leaves a selection spanning the whole fresh document — a
    // keystroke arriving before a click's selection sync would replace the
    // entire issue. Collapse it to the nearest text position from the
    // start (a plain TextSelection at a node boundary isn't valid now
    // that the schema forbids top-level text).
    editor.commands.command(({ tr, dispatch }) => {
      if (dispatch) tr.setSelection(TextSelection.near(tr.doc.resolve(0), 1));
      return true;
    });
  }, [editor, doc?.version]); // eslint-disable-line react-hooks/exhaustive-deps

  // contenteditable follows the issue status once the document arrives —
  // the editor instance is created before the first fetch resolves
  useEffect(() => {
    editor?.setEditable(!!doc && doc.status !== "sent" && doc.status !== "scheduled");
  }, [editor, doc?.status]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    (window as unknown as Record<string, unknown>).__ed = editor;
  }, [editor]);

  const goToSentence = useCallback(
    (selStr: string, opts?: { fix?: boolean; click?: boolean }) => {
      if (window.innerWidth < 768) setMobileTab("doc");
      setTimeout(() => {
        const el = flashEl(editorWrapRef.current, selStr);
        if (!el) return;
        if (opts?.fix) el.dispatchEvent(new CustomEvent(FIX_SENTENCE_EVENT));
        else if (opts?.click) el.click();
      }, 60);
    },
    [setMobileTab],
  );

  const fixNow = () => {
    if (!doc) return;
    const sid = nextPendingSentence(doc);
    if (sid) {
      goToSentence(`[data-sid="${sid}"]`, { fix: true });
      return;
    }
    // nothing sentence-shaped is pending — point at the first failing
    // check's own affordance (an empty item's compose box, a missing
    // commentary) instead of a dead-end flash (KLOX-56)
    const check = readinessQ.data?.checks.find((c) => !c.ok && c.target);
    if (check) onCheckTarget(check);
  };

  const onCheckTarget = (c: Check) => {
    if (!c.target) {
      fixNow();
      return;
    }
    if (c.target.kind === "sentence") goToSentence(`[data-sid="${c.target.ref}"]`, { fix: true });
    else if (c.target.kind === "item") {
      goToSentence(`[data-item-ref="${c.target.ref}"]`);
      // the fix surface for an item is its evidence pane — the compose
      // box lives there, so select the item instead of only flashing
      actions.showEvidence(Number(c.target.ref));
    } else if (c.target.kind === "section") {
      // the commentary card is the affordance — a click focuses it
      goToSentence(`[data-commentary="${c.target.ref}"]`, { click: true });
    } else if (c.target.kind === "issue") {
      // open story decisions live in the list pane
      if (window.innerWidth < 768) setMobileTab("list");
    }
  };

  if (issueQ.isLoading || !issueQ.data) {
    // a missing issue settles as a 404 — show "not found", never an
    // endless loader (KLOX-49)
    if (issueQ.error instanceof ApiRequestError && issueQ.error.status === 404) {
      return <NotFound />;
    }
    if (issueQ.isError && !issueQ.isLoading) {
      return <p className="p-6 text-sm text-danger">{errText(t, issueQ.error)}</p>;
    }
    return <p className="p-6 text-sm text-muted">…</p>;
  }
  const issue = issueQ.data;

  // Edition steps (KLOX-66): Pautas → Escrever → Revisar e enviar. The
  // counter navigates between visited steps; the board owns step 1
  // until the edition exists (board statuses), writing owns step 2,
  // review owns step 3 — a step can always be revisited.
  const boardPhase = BOARD_STATUSES.has(issue.status);
  const stepParam = params.get("step");
  const step = boardPhase ? "pautas"
    : stepParam === "pautas" || stepParam === "revisar" || stepParam === "escrever" ? stepParam
    : issue.status === "drafted" ? "escrever" : "revisar";
  const setStep = (s: string) =>
    setParams((p) => { p.set("step", s); return p; }, { replace: true });
  const stepNum = step === "pautas" ? 1 : step === "escrever" ? 2 : 3;
  // validated = approved or past it: the edition is final on this
  // screen, so the checklist and the empty-coverage badge go away
  // (KLOX-80). A post-approval edit drops the status back to drafted,
  // which reinstates the pendency affordances.
  const validated = issue.status === "approved" || issue.status === "scheduled" || issue.status === "sent";

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* top bar */}
      <header className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-2.5">
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold">
            #{issue.number} · {issue.subject}
          </p>
        </div>
        <StepCounter
          steps={t.board.steps}
          current={stepNum}
          onStep={(i) => setStep(["pautas", "escrever", "revisar"][i])}
          className="w-full shrink-0 sm:w-96"
        />
        {/* chrome convention: header/action clusters sit right-aligned —
            ml-auto keeps the whole group on the right edge even when it
            wraps to its own line (owner note on KLOX-80) */}
        <div className="ml-auto flex flex-wrap items-center justify-end gap-3">
          {saved && (
            <span className="rounded-full bg-teal-soft px-2 py-0.5 text-[11px] text-teal">
              {saved === "saving" ? t.commentary.saving : t.commentary.saved}
            </span>
          )}
          {!BOARD_STATUSES.has(issue.status) && doc && !(validated && doc.coverage.total === 0) && (
            <span className="flex shrink-0 items-center gap-2 whitespace-nowrap">
              <CoverageCounter coverage={doc.coverage} onFixNow={fixNow} />
            </span>
          )}
          {issue.status === "approved" && (
            // the action cluster lives in the header so it never scrolls
            // out of reach, whatever the step body renders (KLOX-80)
            <ReviewActions
              issue={issue}
              onDone={() => invalidate()}
              onToast={showToast}
              onEdit={step === "escrever" ? undefined : () => setStep("escrever")}
            />
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
        </div>
      </header>

      {issue.edited_after_approval && (
        <div className="border-b border-warn/40 bg-warn-soft/60 px-4 py-2 text-xs text-warn">
          {t.review.editedAfterApproval}
        </div>
      )}

      {step === "revisar" && doc ? (
        <ReviewStep
          issue={issue}
          issueID={issueID}
          checks={readinessQ.data?.checks ?? []}
          onTarget={(c) => { setStep("escrever"); onCheckTarget(c); }}
          onChanged={() => invalidate()}
        />
      ) : step === "pautas" || boardPhase ? (
        runID && !runDone ? (
          <div className="flex min-h-0 flex-1 flex-col">
            <div className="mx-auto w-full max-w-md p-6">
              <h2 className="mb-4 text-lg font-semibold">{t.board.preparing}</h2>
              <StepRail
                runID={runID}
                steps={["collect", "dedupe", "triage", "storylist", "dossier", "deepdive"]}
                onDone={(run) => {
                  setRunDone(true);
                  setParams((p) => { p.delete("run"); return p; }, { replace: true });
                  qc.invalidateQueries({ queryKey: ["candidates", issueID] });
                  qc.invalidateQueries({ queryKey: ["issue", issueID] });
                  if (run.no_new_items) showToast(t.run.noNewItems);
                  if (run.blocked || run.sample) setRunBlocked(run.error ?? null);
                  else if (run.error) showToast(run.error);
                }}
              />
            </div>
            <BoardSkeleton />
          </div>
        ) : (
          <div className="flex min-h-0 flex-1 flex-col">
            {runBlocked && (
              <div className="relative border-b border-line">
                <button
                  className="absolute right-3 top-3 z-10 text-xs text-muted hover:text-ink"
                  onClick={() => setRunBlocked(null)}
                  aria-label={t.dismiss}
                >
                  ✕
                </button>
                <BlockedCard message={runBlocked} />
              </div>
            )}
            <BuildBoard issue={issue} onDrafted={() => invalidate()} />
          </div>
        )
      ) : (
        doc && (
          <>
            {/* all three panes stay mounted — visibility toggles per
                breakpoint so the editor is never double-mounted */}
            <DocActionsCtx.Provider value={actions}>
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
                  <EditorContent editor={editor} />
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
                        linkOnly={!!sel && findItemFormat(doc, sel.ref) === "links"}
                      />
                    ) : (
                      <PreviewPane issueID={issueID} />
                    )}
                  </div>
                </aside>
              </div>
            </DocActionsCtx.Provider>

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

      {pautaPick && (
        <PautaPicker
          issueID={issueID}
          section={pautaPick.section}
          position={pautaPick.position}
          version={versionRef.current}
          onPicked={(d) => {
            versionRef.current = d.version;
            qc.setQueryData(["document", issueID], d);
            setPautaPick(null);
            invalidate();
          }}
          onError={(e) => resync(e)}
          onClose={() => setPautaPick(null)}
        />
      )}
      {insertDlg && (
        <InsertBlockDialog
          pubID={issue.publication_id}
          dlg={insertDlg}
          onInsert={(kind, text, url) => actions.insertBlock(insertDlg.section, kind, insertDlg.index, text, url)}
          onUploaded={actions.uploadImage}
          onDone={() => { setInsertDlg(null); }}
        />
      )}
      {(issue.status === "sent" || issue.status === "scheduled" || !!issue.test_sent_at) && (
        <AfterSend issue={issue} onToast={showToast} />
      )}
      {toast && (
        <div className="fixed bottom-4 left-1/2 z-50 flex -translate-x-1/2 items-center gap-3 rounded-full bg-ink px-4 py-2 text-xs text-paper shadow-lg">
          {toast.text}
          {toast.undo && (
            <button className="font-semibold underline" onClick={() => undoRemove(toast.undo!)}>
              {t.doc.undo}
            </button>
          )}
          {toast.undo && (
            <button className="text-muted" aria-label={t.dismiss} onClick={() => setToast(null)}>
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

// findItemFormat returns the format of the section holding item ref —
// links-format sections are link-only (KLOX-65/D2).
function findItemFormat(doc: IssueDocument | null, ref: number): string | undefined {
  return doc?.sections.find((s) => (s.items ?? []).some((i) => i.ref === ref))?.format;
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
  const wsQ = useQuery({ queryKey: ["workspace"], queryFn: api.workspace });
  const liveUnclaimed = wsQ.data?.mail === "live" && !wsQ.data?.claimed;
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
          <Button size="sm" variant={ready ? "primary" : "outline"}>
            {issue.status === "approved" ? `${t.readiness.approvedAt}` : t.readiness.approve}
            {issue.status === "approved" && issue.receipt && (
              <span className="text-[10px] opacity-80">#{issue.receipt.number}</span>
            )}
          </Button>
        }
      >
        <ReadinessList checks={checks} onTarget={onTarget} />
        {ready && issue.status !== "approved" && (
          <Button className="mt-3 w-full" size="sm" onClick={() => setConfirm(true)}>
            {t.readiness.approve}
          </Button>
        )}
        {issue.status === "approved" && issue.receipt?.approver && (
          <div className="mt-3 border-t border-line pt-2 text-xs text-muted">
            {t.receipt.by} {issue.receipt.approver}
          </div>
        )}
      </KPopover>

      <KDialog open={confirm} onOpenChange={setConfirm} title={t.send.approveSend}>
        <p className="mb-3 text-sm font-semibold">
          #{issue.number} · {issue.subject}
        </p>
        <fieldset className="mb-3">
          <legend className="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">{t.send.audience}</legend>
          <label className="flex items-center gap-2 py-1 text-sm">
            <input type="radio" name="aud" checked={audience === "nobody"} onChange={() => setAudience("nobody")} />
            {t.send.nobody}
          </label>
          <label className="flex items-center gap-2 py-1 text-sm">
            <input type="radio" name="aud" checked={audience === "readers"} onChange={() => setAudience("readers")} />
            {t.send.allReaders}
          </label>
        </fieldset>
        {audience === "readers" && (
          <fieldset className="mb-3">
            <legend className="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">{t.send.when}</legend>
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
          </fieldset>
        )}
        {audience === "readers" && liveUnclaimed && (
          <div className="mb-3">
            <p className="mb-1 text-xs text-danger">{t.claim.hint}</p>
            <ClaimEmail />
          </div>
        )}
        <dl className="mb-4 space-y-1 border-t border-line pt-3 text-xs text-muted">
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
              <dd>
                {readerQ.data
                  ? wsQ.data?.mail === "live"
                    ? t.send.readerCount(readerQ.data.confirmed ?? 0, readerQ.data.subscribers ?? 0)
                    : t.send.readerCount(readerQ.data.readers ?? 0, readerQ.data.subscribers ?? 0)
                  : "…"}
              </dd>
            </div>
          )}
          <div className="flex justify-between">
            <dt>{t.send.sender}</dt>
            <dd>{pub?.name}</dd>
          </div>
        </dl>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setConfirm(false)}>
            {t.doc.cancel}
          </Button>
          <Button
            variant={audience === "readers" ? "danger" : "primary"}
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

// exportIssuePDF is the honest PDF path (KLOX-80): the approved HTML
// export prints from a hidden frame and the browser's Save-as-PDF
// produces the file — no dead-end menu item, no new dependency.
async function exportIssuePDF(issueID: number) {
  const res = await fetch(`/api/v1/issues/${issueID}/export?fmt=html`);
  if (!res.ok) throw new Error(`export ${res.status}`);
  const html = await res.text();
  const f = document.createElement("iframe");
  f.setAttribute("aria-hidden", "true");
  f.style.cssText =
    "position:fixed;right:0;bottom:0;width:1px;height:1px;border:0;opacity:0;pointer-events:none";
  f.srcdoc = html;
  const done = () => f.remove();
  f.onload = () => {
    const w = f.contentWindow;
    if (!w) return done();
    w.addEventListener("afterprint", done);
    setTimeout(done, 60_000);
    w.focus();
    w.print();
  };
  document.body.appendChild(f);
}

// ReviewActions is the approved edition's header cluster (KLOX-80):
// edit, test send, send now, schedule, export and reader preview as
// first-class buttons that never scroll out of reach — instead of an
// action row buried at the bottom of the review column. Sends keep
// the same confirm dialog, and the live-mail claim gate rides along
// inside it.
function ReviewActions({ issue, onDone, onToast, onEdit }: {
  issue: IssueSummary;
  onDone: () => void;
  onToast: (text: string) => void;
  onEdit?: () => void;
}) {
  const { t } = useT();
  const [dlg, setDlg] = useState<null | "now" | "schedule">(null);
  const [at, setAt] = useState(() => localInput(new Date(Date.now() + 24 * 3600e3)));
  const atOK = new Date(at).getTime() > Date.now();
  const pubQ = useQuery({
    queryKey: ["publication", issue.publication_id],
    queryFn: () => api.publication(issue.publication_id),
  });
  const wsQ = useQuery({ queryKey: ["workspace"], queryFn: api.workspace });
  const readerQ = useQuery({
    queryKey: ["reader", issue.publication_id],
    queryFn: () => api.reader(issue.publication_id),
  });
  // the reader page only exists once a test send stamps test_sent_at
  // (api.after 409s before that) — the link enables exactly when the
  // page is real, so the button never dead-ends
  const publicQ = useQuery({
    queryKey: ["after", issue.id],
    queryFn: () => api.after(issue.id),
    enabled: !!issue.test_sent_at,
    retry: false,
  });
  const unclaimed = wsQ.data?.mail === "live" && !wsQ.data?.claimed;
  const sendMut = useMutation({
    mutationFn: ({ aud, when }: { aud: "test" | "readers"; when: "now" | "schedule" }) =>
      api.send(issue.id, aud, when, undefined,
        when === "schedule" ? new Date(at).toISOString() : undefined),
    onSuccess: (r, { aud }) => {
      setDlg(null);
      // a test send lands in the outbox — say where it went
      if (r.fallback) onToast(t.send.fallbackNote);
      else if (r.to?.length) onToast(aud === "test" ? t.send.testSentTo(r.to.join(", ")) : t.send.sentTo(r.to.join(", ")));
      if (r.refused?.length) onToast(t.send.refusedNote(r.refused.length));
      if (r.warnings?.length) onToast(r.warnings[0]);
      onDone();
    },
  });
  const pub = pubQ.data?.publication;
  const recipients = readerQ.data
    ? wsQ.data?.mail === "live"
      ? t.send.readerCount(readerQ.data.confirmed ?? 0, readerQ.data.subscribers ?? 0)
      : t.send.readerCount(readerQ.data.readers ?? 0, readerQ.data.subscribers ?? 0)
    : "…";
  const publicURL = publicQ.data?.public_url;
  const exportURL = (fmt: string) => `/api/v1/issues/${issue.id}/export?fmt=${fmt}`;
  const menuItem =
    "block w-full rounded-[var(--radius-btn)] px-2 py-1.5 text-left text-sm text-ink hover:bg-surface";
  return (
    <span className="flex flex-wrap items-center justify-end gap-1.5">
      {onEdit && (
        <Button size="sm" variant="ghost" onClick={onEdit}>
          <Pencil size={12} /> {t.review.edit}
        </Button>
      )}
      <Button
        size="sm" variant="outline" disabled={sendMut.isPending}
        onClick={() => sendMut.mutate({ aud: "test", when: "now" })}
      >
        <Send size={12} /> {t.send.test}
      </Button>
      <Button size="sm" variant="danger" disabled={sendMut.isPending} onClick={() => setDlg("now")}>
        {t.send.action}
      </Button>
      <Button size="sm" variant="outline" disabled={sendMut.isPending} onClick={() => setDlg("schedule")}>
        <CalendarClock size={12} /> {t.review.scheduleSend}
      </Button>
      <KPopover
        trigger={
          <Button size="sm" variant="outline">
            <Download size={12} /> {t.review.export} <ChevronDown size={11} />
          </Button>
        }
      >
        <a className={menuItem} href={exportURL("html")} download>HTML</a>
        <a className={menuItem} href={exportURL("markdown")} download>Markdown</a>
        <button
          type="button"
          className={menuItem}
          onClick={() => void exportIssuePDF(issue.id).catch(() => onToast(t.review.exportFailed))}
        >
          PDF
        </button>
        <p className="mt-1 px-2 text-[11px] text-muted">{t.review.pdfHint}</p>
      </KPopover>
      {publicURL ? (
        <a
          href={publicURL}
          target="_blank"
          rel="noreferrer"
          className={buttonVariants({ variant: "outline", size: "sm" })}
        >
          <ExternalLink size={12} /> {t.preview.title}
        </a>
      ) : (
        <Button size="sm" variant="outline" disabled title={t.review.previewHint}>
          <Eye size={12} /> {t.preview.title}
        </Button>
      )}
      {sendMut.error && <span className="text-xs text-danger">{errText(t, sendMut.error)}</span>}

      <KDialog
        open={dlg !== null}
        onOpenChange={(o) => !o && setDlg(null)}
        title={dlg === "schedule" ? t.review.scheduleSend : t.send.confirmSend}
      >
        {unclaimed && (
          <div className="mb-3">
            <p className="mb-1 text-xs text-muted">{t.claim.hint}</p>
            <ClaimEmail />
          </div>
        )}
        <dl className="mb-4 space-y-1 text-sm">
          <div className="flex justify-between gap-4">
            <dt className="text-muted">{t.send.subject}</dt>
            <dd className="text-right font-medium">#{issue.number} · {issue.subject}</dd>
          </div>
          <div className="flex justify-between gap-4">
            <dt className="text-muted">{t.send.recipients}</dt>
            <dd>{recipients}</dd>
          </div>
          <div className="flex justify-between gap-4">
            <dt className="text-muted">{t.send.sender}</dt>
            <dd>{pub?.name}</dd>
          </div>
        </dl>
        {dlg === "schedule" && (
          <label className="mb-4 flex items-center gap-2 text-xs text-muted">
            {t.send.at}
            <input
              type="datetime-local"
              aria-label={t.send.at}
              className="rounded border border-line bg-paper px-2 py-1 text-xs text-ink"
              min={localInput(new Date())}
              value={at}
              onChange={(e) => setAt(e.target.value)}
            />
            {!atOK && <span className="text-[11px] text-danger">{t.send.pickTime}</span>}
          </label>
        )}
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setDlg(null)}>{t.doc.cancel}</Button>
          <Button
            variant="danger"
            disabled={sendMut.isPending || (dlg === "schedule" && !atOK)}
            onClick={() => sendMut.mutate({ aud: "readers", when: dlg! })}
          >
            {dlg === "schedule" ? t.send.schedule : t.send.now}
          </Button>
        </div>
      </KDialog>
    </span>
  );
}

// ReviewStep is the "Revisar e enviar" screen (KLOX-66, redesigned in
// KLOX-80): before approval the checklist and its linked pendencies
// sit beside the preview with the approve action under them; once the
// edition is validated (approved, scheduled or sent) the checklist is
// redundant — the middle column holds only the validated edition with
// its preview variables and every action lives in the header cluster.
// A post-approval edit banner shows at the page level until the issue
// is approved again.
function ReviewStep({
  issue, issueID, checks, onTarget, onChanged,
}: {
  issue: IssueSummary;
  issueID: number;
  checks: Check[];
  onTarget: (c: Check) => void;
  onChanged: () => void;
}) {
  const { t } = useT();
  const ready = checks.length > 0 && checks.every((c) => c.ok);
  const approveMut = useMutation({
    mutationFn: () => api.approve(issueID),
    onSuccess: onChanged,
  });
  const validated = issue.status === "approved" || issue.status === "scheduled" || issue.status === "sent";
  if (validated) {
    return (
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-3xl p-4">
          <section className="rounded-[var(--radius-card)] border border-line bg-paper-raised p-4">
            <PreviewPane issueID={issueID} />
          </section>
        </div>
      </div>
    );
  }
  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto grid w-full max-w-5xl gap-4 p-4 md:grid-cols-2">
        <section className="space-y-3">
          <div className="rounded-[var(--radius-card)] border border-line bg-paper-raised p-4">
            <h2 className="mb-2 text-sm font-semibold">{t.readiness.title}</h2>
            <ReadinessList checks={checks} onTarget={onTarget} />
          </div>
          <div className="rounded-[var(--radius-card)] border border-line bg-paper-raised p-4">
            <h2 className="mb-2 text-sm font-semibold">{t.send.approveSend}</h2>
            <Button
              size="sm"
              disabled={!ready || approveMut.isPending}
              title={ready ? undefined : t.readiness.blockedHint}
              onClick={() => approveMut.mutate()}
            >
              {t.readiness.approve}
            </Button>
            {approveMut.error && <p className="mt-2 text-xs text-danger">{errText(t, approveMut.error)}</p>}
          </div>
        </section>
        <section className="rounded-[var(--radius-card)] border border-line bg-paper-raised p-4">
          <PreviewPane issueID={issueID} />
        </section>
      </div>
    </div>
  );
}

// PautaPicker lists bench candidates for "+ Pauta" — stories in the
// edition and rejected ones never appear as suggestions. `position`
// is the gap the picker opened from (append when undefined).
function PautaPicker({
  issueID, section, position, version, onPicked, onError, onClose,
}: {
  issueID: number;
  section: string;
  position?: number;
  version: string;
  onPicked: (d: IssueDocument) => void;
  onError: (e: unknown) => void;
  onClose: () => void;
}) {
  const { t } = useT();
  const b = useComposer();
  const [q, setQ] = useState("");
  const candsQ = useQuery({
    queryKey: ["candidates", issueID],
    queryFn: () => api.candidates(issueID),
  });
  const pick = useMutation({
    mutationFn: (c: Candidate) =>
      api.blockInsert(issueID, { section, kind: "item", story_id: c.story_id, position, version }),
    onSuccess: (r) => onPicked(r.document),
    onError: onError,
  });
  const bench = (candsQ.data?.candidates ?? []).filter(
    (c) => !c.in_issue && c.available && (!q || c.title.toLowerCase().includes(q.toLowerCase())),
  );
  return (
    <KDialog open onOpenChange={(o) => !o && onClose()} title={b.pautaTitle(section)}>
      <input
        autoFocus
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder={b.pautaSearch}
        className="mb-3 w-full rounded-[var(--radius-input)] border border-line bg-paper px-3 py-2 text-sm"
      />
      <div className="max-h-72 space-y-1 overflow-y-auto">
        {candsQ.isLoading && <p className="text-sm text-muted">…</p>}
        {!candsQ.isLoading && bench.length === 0 && (
          <p className="text-sm text-muted">{b.pautaEmpty}</p>
        )}
        {bench.map((c) => (
          <button
            key={c.story_id}
            type="button"
            disabled={pick.isPending}
            onClick={() => pick.mutate(c)}
            className="flex w-full items-start justify-between gap-2 rounded-[var(--radius-btn)] px-2 py-1.5 text-left hover:bg-surface disabled:opacity-50"
          >
            <span className="min-w-0">
              <span className="block truncate text-sm font-medium">{c.title}</span>
              <span className="text-xs text-muted">{c.outlets.join(" · ")}</span>
            </span>
            <span className="shrink-0 text-teal"><Plus size={14} /></span>
          </button>
        ))}
      </div>
      {!!pick.error && <p className="mt-2 text-xs text-danger">{errText(t, pick.error)}</p>}
    </KDialog>
  );
}

// InsertBlockDialog is the page-owned half of the gap menu's image /
// embed / standard-message entries (KLOX-66 D3): the node views open
// it via `openInsert`, it gathers the payload and delegates the real
// insert — and its version/conflict handling — to `insertBlock`.
function InsertBlockDialog({
  pubID, dlg, onInsert, onUploaded, onDone,
}: {
  pubID: number;
  dlg: { section: string; index: number; kind: "image" | "embed" | "message" };
  onInsert: (kind: "image" | "embed" | "text", text: string, url?: string) => Promise<string | null>;
  onUploaded: (dataB64: string, contentType: string, name: string) => Promise<string | null>;
  onDone: () => void;
}) {
  const { t } = useT();
  const b = useComposer();
  const qc = useQueryClient();
  const [url, setUrl] = useState("");
  const [alt, setAlt] = useState("");
  const [html, setHtml] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const msgsQ = useQuery({
    queryKey: ["messages", pubID],
    queryFn: () => api.messages(pubID),
    enabled: dlg.kind === "message",
  });
  const saveMsg = useMutation({
    mutationFn: (body: { name: string; body: string }) => api.messageCreate(pubID, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["messages", pubID] }),
  });
  const delMsg = useMutation({
    mutationFn: (mid: number) => api.messageDelete(pubID, mid),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["messages", pubID] }),
  });
  const [newName, setNewName] = useState("");
  const [newBody, setNewBody] = useState("");
  const [newOpen, setNewOpen] = useState(false);

  const insert = async (kind: "image" | "embed" | "text", text: string, u?: string) => {
    setBusy(true);
    const refused = await onInsert(kind, text, u);
    setBusy(false);
    if (refused === null) onDone();
    else setErr(refused);
  };
  const onFile = async (f: File | undefined) => {
    if (!f) return;
    setBusy(true);
    const buf = await f.arrayBuffer();
    let bin = "";
    new Uint8Array(buf).forEach((b) => (bin += String.fromCharCode(b)));
    const res = await onUploaded(btoa(bin), f.type || "image/png", f.name);
    setBusy(false);
    if (res) setUrl(res);
    else setErr(b.uploadErr);
  };
  const title =
    dlg.kind === "image" ? b.imageTitle : dlg.kind === "embed" ? b.embedTitle : b.msgTitle;
  return (
    <KDialog open onOpenChange={(o) => !o && onDone()} title={title}>
      {dlg.kind === "image" && (
        <div className="space-y-2">
          <div className="flex gap-1">
            <input
              autoFocus
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder={b.imageUrlPh}
              className="min-w-0 flex-1 rounded-[var(--radius-input)] border border-line bg-paper px-2 py-1.5 text-sm"
            />
            <Button size="sm" variant="outline" disabled={busy} onClick={() => fileRef.current?.click()}>
              {b.upload}
            </Button>
            <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/gif,image/webp" className="hidden"
              onChange={(e) => { void onFile(e.target.files?.[0]); e.target.value = ""; }} />
          </div>
          <input
            value={alt}
            onChange={(e) => setAlt(e.target.value)}
            placeholder={b.altPh}
            className="w-full rounded-[var(--radius-input)] border border-line bg-paper px-2 py-1.5 text-sm"
          />
          {err && <p className="text-xs text-danger" role="alert">{err}</p>}
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="ghost" onClick={onDone}>{t.doc.cancel}</Button>
            <Button size="sm" disabled={busy || !url.trim()} onClick={() => void insert("image", alt, url.trim())}>
              {b.insert}
            </Button>
          </div>
        </div>
      )}
      {dlg.kind === "embed" && (
        <div className="space-y-2">
          <textarea
            autoFocus
            value={html}
            onChange={(e) => setHtml(e.target.value)}
            rows={7}
            placeholder={b.embedPh}
            className="w-full resize-y rounded-[var(--radius-input)] border border-line bg-paper p-2 font-mono text-xs leading-5"
          />
          <p className="text-[11px] text-muted">{b.embedHint}</p>
          {err && <p className="text-xs text-danger" role="alert">{err}</p>}
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="ghost" onClick={onDone}>{t.doc.cancel}</Button>
            <Button size="sm" disabled={busy || !html.trim()} onClick={() => void insert("embed", html)}>
              {b.insert}
            </Button>
          </div>
        </div>
      )}
      {dlg.kind === "message" && (
        <div className="space-y-2">
          <div className="max-h-64 space-y-1 overflow-y-auto">
            {msgsQ.isLoading && <p className="text-sm text-muted">…</p>}
            {!msgsQ.isLoading && !(msgsQ.data ?? []).length && !newOpen && (
              <p className="text-sm text-muted">{b.msgEmpty}</p>
            )}
            {(msgsQ.data ?? []).map((m) => (
              <div key={m.id} className="group flex items-start gap-2 rounded-[var(--radius-btn)] px-2 py-1.5 hover:bg-surface">
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => void insert("text", m.body)}
                  className="min-w-0 flex-1 text-left"
                >
                  <span className="block text-sm font-medium">{m.name}</span>
                  <span className="block truncate text-xs text-muted">{m.body}</span>
                </button>
                <button type="button" aria-label={b.msgDelete}
                  className="rounded p-1 text-muted opacity-0 hover:text-danger group-hover:opacity-100"
                  onClick={() => delMsg.mutate(m.id)}>
                  <Trash2 size={12} />
                </button>
              </div>
            ))}
          </div>
          {!newOpen ? (
            <button type="button"
              className="inline-flex items-center gap-1 text-xs text-teal hover:underline"
              onClick={() => setNewOpen(true)}>
              <Plus size={12} /> {b.msgNew}
            </button>
          ) : (
            <div className="space-y-1.5 rounded-[var(--radius-card)] border border-line bg-paper-raised p-2">
              <input
                autoFocus
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder={b.msgNamePh}
                className="w-full rounded-[var(--radius-input)] border border-line bg-paper px-2 py-1 text-xs"
              />
              <textarea
                value={newBody}
                onChange={(e) => setNewBody(e.target.value)}
                rows={4}
                placeholder={b.msgBodyPh}
                className="w-full resize-y rounded-[var(--radius-input)] border border-line bg-paper p-2 font-doc text-sm"
              />
              <div className="flex gap-2">
                <Button size="sm" disabled={saveMsg.isPending || !newName.trim() || !newBody.trim()}
                  onClick={() => saveMsg.mutate({ name: newName.trim(), body: newBody }, {
                    onSuccess: () => { setNewName(""); setNewBody(""); setNewOpen(false); },
                  })}>
                  {t.doc.save}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setNewOpen(false)}>{t.doc.cancel}</Button>
              </div>
            </div>
          )}
          {err && <p className="text-xs text-danger" role="alert">{err}</p>}
        </div>
      )}
    </KDialog>
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
function AfterSend({ issue, onToast }: { issue: IssueSummary; onToast: (text: string) => void }) {
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
              {r.approver ? `${t.receipt.by} ${r.approver} · ` : ""}
              {fmtDate(r.approved_at, lang, true)}
            </span>
          )}
          {issue.status === "scheduled" && issue.scheduled_at && (
            <span className="font-semibold text-ink">
              {t.send.scheduledFor} {fmtDate(issue.scheduled_at, lang, true)}
            </span>
          )}
          {issue.test_sent_at && issue.status !== "sent" && (
            <span className="text-muted">
              {t.after.testSent} · {fmtDate(issue.test_sent_at, lang, true)}
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
              {" · "}
              <button
                type="button"
                className="text-ink underline"
                onClick={() => void exportIssuePDF(issue.id).catch(() => onToast(t.review.exportFailed))}
              >
                PDF
              </button>
            </span>
          )}
          <Button size="sm" className="ml-auto" disabled={next.isPending} onClick={() => next.mutate()}>
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
