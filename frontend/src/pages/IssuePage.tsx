import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { EditorContent, useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { ArrowLeft, BookOpen, ClipboardCheck, Eye, Send, Wrench } from "lucide-react";
import { api, ApiRequestError, type Check, type IssueDocument, type IssueItem, type IssueSummary, type RunEvent } from "../api";
import { docToTiptap, nextPendingSentence, readyFromChecks } from "../docModel";
import { Commentary, DocActionsCtx, ItemHeading, Sentence, type DocActions } from "../editor/nodes";
import { useT } from "../i18n";
import { cn } from "../lib/cn";
import { Badge, Button, Card, KDialog, KPopover } from "../ui";
import { CoverageCounter, EvidencePane, PautasPane, PreviewPane, ReadinessList } from "../components/Panes";

// scroll the editor to a sentence/item element and flash it
function flashEl(docEl: HTMLElement | null, sel: string) {
  const el = docEl?.querySelector<HTMLElement>(sel);
  if (!el) return;
  el.scrollIntoView({ block: "center", behavior: "smooth" });
  el.classList.remove("flash");
  void el.offsetWidth;
  el.classList.add("flash");
}

export function IssuePage() {
  const { id } = useParams();
  const issueID = Number(id);
  const { t } = useT();
  const qc = useQueryClient();
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

  const [toast, setToast] = useState("");
  const toastRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const showToast = useCallback((s: string) => {
    setToast(s);
    if (toastRef.current) clearTimeout(toastRef.current);
    toastRef.current = setTimeout(() => setToast(""), 3000);
  }, []);

  // resync: a 409 carries the fresh document — accept it and tell the user
  const resync = useCallback(
    (err: unknown) => {
      if (err instanceof ApiRequestError && err.document) {
        qc.setQueryData(["document", issueID], err.document);
        showToast(t.errors.conflict);
      } else {
        showToast(err instanceof ApiRequestError ? err.body.message : t.errors.generic);
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

  const actions: DocActions = useMemo(
    () => ({
      readOnly: doc?.status === "sent",
      showEvidence: (ref, opts) => {
        const item = doc?.sections.flatMap((s) => s.items).find((i) => i.ref === ref);
        setSel({ ref, issueItemId: item?.issue_item_id ?? 0, citeRef: opts?.citeRef, sid: opts?.sid });
        setRightTab("evidence");
        if (window.innerWidth < 768) setMobileTab("side");
      },
      editSentence: async (sid, text, citations) => {
        try {
          const r = await api.editSentence(issueID, sid, text, citations, versionRef.current);
          versionRef.current = r.version;
          invalidate();
          return true;
        } catch (e) {
          resync(e);
          return false;
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
          return true;
        } catch (e) {
          resync(e);
          return false;
        }
      },
    }),
    [doc, issueID, invalidate, resync],
  );

  // --- commentary autosave: watch editor updates, diff commentary
  // nodes against what the server last saw, PUT debounced ---
  const savedCommentary = useRef<Map<string, string>>(new Map());
  const pendingSave = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map());
  const saveCommentary = useCallback(
    (section: string, text: string) => {
      setSaved("saving");
      api
        .commentary(issueID, section, text, versionRef.current)
        .then((r) => {
          versionRef.current = r.version;
          savedCommentary.current.set(section, text);
          setSaved("saved");
          qc.invalidateQueries({ queryKey: ["readiness", issueID] });
        })
        .catch((e) => {
          setSaved("");
          resync(e);
        });
    },
    [issueID, qc, resync],
  );

  const editor = useEditor(
    {
      extensions: [
        StarterKit.configure({ heading: { levels: [2] } }),
        Sentence,
        Commentary,
        ItemHeading,
      ],
      content: doc ? docToTiptap(doc) : { type: "doc", content: [] },
      editable: !!doc && doc.status !== "sent",
      editorProps: { attributes: { class: "focus:outline-none px-4 py-2" } },
      onUpdate: ({ editor: ed }) => {
        ed.state.doc.descendants((n) => {
          if (n.type.name !== "commentary") return true;
          const section = String(n.attrs.section);
          const text = n.textContent;
          if (savedCommentary.current.get(section) === text) return true;
          const prev = pendingSave.current.get(section);
          if (prev) clearTimeout(prev);
          pendingSave.current.set(
            section,
            setTimeout(() => saveCommentary(section, text), 900),
          );
          return true;
        });
      },
    },
    [issueID],
  );

  // reset baseline when a fresh document arrives (version changes)
  useEffect(() => {
    if (!editor || !doc) return;
    savedCommentary.current = new Map(doc.sections.map((s) => [s.title, s.commentary.text]));
    editor.commands.setContent(docToTiptap(doc));
  }, [editor, doc?.version]); // eslint-disable-line react-hooks/exhaustive-deps

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
    else if (c.target.kind === "section" || c.target.kind === "issue") {
      // commentary section — scroll to its commentary node
      goToSentence(`.ProseMirror`);
    } else if (c.target.kind === "decision") {
      // nothing in the doc — the list pane carries pending items
      if (window.innerWidth < 768) setMobileTab("list");
    }
  };

  if (issueQ.isLoading || !issueQ.data) {
    return <p className="p-6 text-sm text-muted">…</p>;
  }
  const issue = issueQ.data;

  return (
    <div className="flex h-full flex-col">
      {/* top bar */}
      <header className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-2.5">
        <Link to={`/pub/${issue.publication_id}`} className="inline-flex items-center gap-1 text-xs text-muted hover:text-ink">
          <ArrowLeft size={13} /> {t.back}
        </Link>
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold">
            #{issue.number} · {issue.subject}
          </p>
        </div>
        <Badge tone={issue.status === "approved" ? "teal" : issue.status === "sent" ? "ink" : "warn"}>
          {(t.status as Record<string, string>)[issue.status]}
        </Badge>
        {saved && (
          <span className="text-[11px] text-muted">
            {saved === "saving" ? t.commentary.saving : t.commentary.saved}
          </span>
        )}
        <span className="flex-1" />
        {doc && <CoverageCounter coverage={doc.coverage} onFixNow={fixNow} />}
        {readinessQ.data && issue.status !== "sent" && (
          <ApproveBar
            issue={issue}
            checks={readinessQ.data.checks}
            coverage={doc?.coverage ?? readinessQ.data.coverage}
            onTarget={onCheckTarget}
            onApproved={() => invalidate()}
          />
        )}
        {issue.status === "sent" && <Badge tone="ink">{t.send.sent}</Badge>}
      </header>

      {issue.status === "reviewing" || issue.status === "" ? (
        <DecisionsView
          issue={issue}
          items={itemsQ.data?.items ?? []}
          pending={itemsQ.data?.pending ?? 0}
          onChanged={() => {
            invalidate();
            qc.invalidateQueries({ queryKey: ["items", issueID] });
          }}
        />
      ) : (
        doc && (
          <>
            {/* desktop three-pane */}
            <div className="hidden min-h-0 flex-1 md:flex">
              <aside className="w-64 shrink-0 overflow-y-auto border-r border-line">
                <PautasPane
                  doc={doc}
                  items={itemsQ.data?.items}
                  selectedRef={sel?.ref ?? null}
                  onSelect={(ref, iid) => actions.showEvidence(ref, { issueItemId: iid })}
                />
              </aside>
              <main ref={editorWrapRef} className="min-w-0 flex-1 overflow-y-auto">
                <DocActionsCtx.Provider value={actions}>
                  <EditorContent editor={editor} />
                </DocActionsCtx.Provider>
              </main>
              <aside className="flex w-80 shrink-0 flex-col border-l border-line">
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
                <div className="min-h-0 flex-1 overflow-y-auto">
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

            {/* mobile: one pane at a time + bottom tabs */}
            <div className="flex min-h-0 flex-1 flex-col md:hidden">
              <div className="min-h-0 flex-1 overflow-y-auto" ref={mobileTab === "doc" ? editorWrapRef : undefined}>
                {mobileTab === "list" && (
                  <PautasPane
                    doc={doc}
                    items={itemsQ.data?.items}
                    selectedRef={sel?.ref ?? null}
                    onSelect={(ref, iid) => actions.showEvidence(ref, { issueItemId: iid })}
                  />
                )}
                {mobileTab === "doc" && (
                  <DocActionsCtx.Provider value={actions}>
                    <EditorContent editor={editor} />
                  </DocActionsCtx.Provider>
                )}
                {mobileTab === "side" &&
                  (rightTab === "evidence" ? (
                    <EvidencePane item={sel ? findItem(doc, sel.ref) : null} citeRef={sel?.citeRef} sid={sel?.sid} />
                  ) : (
                    <PreviewPane issueID={issueID} />
                  ))}
              </div>
              <nav className="flex border-t border-line text-xs">
                {(
                  [
                    ["list", <BookOpen key="i" size={15} />, t.pautas],
                    ["doc", <ClipboardCheck key="i" size={15} />, t.issue],
                    ["side", <Eye key="i" size={15} />, rightTab === "evidence" ? t.evidence.title : t.preview.title],
                  ] as const
                ).map(([k, icon, label]) => (
                  <button
                    key={k}
                    onClick={() => {
                      if (k === "side" && rightTab === "preview" && !sel) setRightTab("preview");
                      setMobileTab(k);
                    }}
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
                  onClick={() => setRightTab(rightTab === "evidence" ? "preview" : "evidence")}
                  className={cn("flex flex-1 flex-col items-center gap-0.5 py-2", "text-muted")}
                >
                  <Wrench size={15} />
                  {rightTab === "evidence" ? t.preview.title : t.evidence.title}
                </button>
              </nav>
            </div>
          </>
        )
      )}

      {issue.status === "sent" && issue.receipt && <ReceiptCard issue={issue} />}
      {toast && (
        <div className="fixed bottom-4 left-1/2 z-50 -translate-x-1/2 rounded-full bg-ink px-4 py-2 text-xs text-paper shadow-lg">
          {toast}
        </div>
      )}
    </div>
  );
}

function findItem(doc: IssueDocument, ref: number) {
  return doc.sections.flatMap((s) => s.items).find((i) => i.ref === ref) ?? null;
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
  const approveMut = useMutation({
    mutationFn: () => api.approve(issue.id),
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
            <SendRow issue={issue} onDone={onApproved} />
          </div>
        )}
      </KPopover>

      <KDialog open={confirm} onOpenChange={setConfirm} title={t.readiness.approve}>
        <p className="mb-4 text-sm text-muted">
          #{issue.number} · {issue.subject}
        </p>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setConfirm(false)}>
            {t.doc.cancel}
          </Button>
          <Button variant="teal" disabled={approveMut.isPending} onClick={() => approveMut.mutate()}>
            {t.readiness.approve}
          </Button>
        </div>
        {approveMut.error && (
          <p className="mt-2 text-xs text-danger">
            {approveMut.error instanceof ApiRequestError ? approveMut.error.body.message : t.errors.generic}
          </p>
        )}
      </KDialog>
    </>
  );
}

function SendRow({ issue, onDone }: { issue: IssueSummary; onDone: () => void }) {
  const { t } = useT();
  const sendMut = useMutation({
    mutationFn: (aud: "test" | "readers") => api.send(issue.id, aud),
    onSuccess: onDone,
  });
  return (
    <div className="mt-2 flex gap-2">
      <Button size="sm" variant="outline" disabled={sendMut.isPending} onClick={() => sendMut.mutate("test")}>
        <Send size={12} /> {t.send.test}
      </Button>
      <Button size="sm" variant="danger" disabled={sendMut.isPending} onClick={() => sendMut.mutate("readers")}>
        {t.send.readers}
      </Button>
      {sendMut.error && (
        <p className="text-xs text-danger">
          {sendMut.error instanceof ApiRequestError ? sendMut.error.body.message : ""}
        </p>
      )}
    </div>
  );
}

function ReceiptCard({ issue }: { issue: IssueSummary }) {
  const { t } = useT();
  const r = issue.receipt!;
  return (
    <div className="border-t border-line bg-teal-soft/40 px-4 py-2 text-xs">
      {t.receipt.title}: {t.receipt.by} {r.approver} · {new Date(r.approved_at).toLocaleString()} ·{" "}
      {t.receipt.version} {r.document_version.slice(0, 8)}
    </div>
  );
}

// --- reviewing: pautas decisions (triaged cards) ---

function DecisionsView({
  issue,
  items,
  pending,
  onChanged,
}: {
  issue: IssueSummary;
  items: IssueItem[];
  pending: number;
  onChanged: () => void;
}) {
  const { t } = useT();
  const qc = useQueryClient();
  const [rejectReason, setRejectReason] = useState<Record<number, string>>({});
  const [draftRun, setDraftRun] = useState<string | null>(null);

  const decide = useMutation({
    mutationFn: ({ itemID, kind, reason }: { itemID: number; kind: string; reason?: string }) =>
      api.decide(issue.id, itemID, kind, reason),
    onSuccess: () => {
      onChanged();
    },
  });

  const draft = useMutation({
    mutationFn: () => api.draft(issue.id),
    onSuccess: (run) => setDraftRun(run.id),
    onError: (e) => {
      if (e instanceof ApiRequestError && e.body.reasons) {
        // surface each blocked reason instead of a generic message
        alert(e.body.reasons.map((r) => `${r.code}: ${r.message}`).join("\n"));
      }
    },
  });

  return (
    <div className="min-h-0 flex-1 overflow-y-auto p-4">
      <div className="mx-auto max-w-2xl">
        <div className="mb-4 flex items-center gap-3">
          <h2 className="text-lg font-semibold">{t.pautas}</h2>
          <Badge tone={pending > 0 ? "warn" : "teal"}>
            {pending} {t.decisions.pending}
          </Badge>
          <span className="flex-1" />
          <Button
            variant="teal"
            size="sm"
            disabled={draft.isPending || !!draftRun}
            onClick={() => draft.mutate()}
          >
            {draft.isPending || draftRun ? t.draft.running : t.draft.action}
          </Button>
        </div>

        {draftRun && <DraftProgress runID={draftRun} issueID={issue.id} onDone={() => qc.invalidateQueries()} />}

        <div className="space-y-3">
          {items.map((it) => (
            <Card key={it.id} className="p-4">
              <div className="flex items-start justify-between gap-3">
                <div>
                  <p className="font-doc font-semibold leading-6">{it.title}</p>
                  <p className="mt-0.5 text-xs text-muted">
                    {it.outlets.join(", ")} · {it.score.toFixed(1)}/10
                    {it.reason ? ` — ${it.reason}` : ""}
                  </p>
                  <p className="text-[11px] uppercase tracking-wide text-muted">{it.section}</p>
                </div>
                <Badge
                  tone={
                    it.status === "approved" ? "teal" : it.status === "rejected" ? "danger" : it.status === "alternative" ? "ink" : "warn"
                  }
                >
                  {it.status || "pending"}
                </Badge>
              </div>
              <div className="mt-3 flex flex-wrap items-center gap-2">
                {it.status === "rejected" ? (
                  <Button size="sm" variant="outline" onClick={() => decide.mutate({ itemID: it.id, kind: "undo" })}>
                    {t.decisions.undo}
                  </Button>
                ) : (
                  <>
                    <Button size="sm" variant="teal" onClick={() => decide.mutate({ itemID: it.id, kind: "approve" })}>
                      {t.decisions.approve}
                    </Button>
                    <Button size="sm" variant="outline" onClick={() => decide.mutate({ itemID: it.id, kind: "alternative" })}>
                      {t.decisions.alternative}
                    </Button>
                  </>
                )}
                {it.status !== "rejected" && (
                  <span className="flex items-center gap-1">
                    <input
                      value={rejectReason[it.id] ?? ""}
                      onChange={(e) => setRejectReason((m) => ({ ...m, [it.id]: e.target.value }))}
                      placeholder={t.reasonRequired}
                      className="h-8 w-56 rounded-[var(--radius-input)] border border-line bg-paper px-2 text-xs"
                    />
                    <Button
                      size="sm"
                      variant="danger"
                      disabled={!rejectReason[it.id]?.trim()}
                      onClick={() => decide.mutate({ itemID: it.id, kind: "reject", reason: rejectReason[it.id] })}
                    >
                      {t.decisions.reject}
                    </Button>
                  </span>
                )}
              </div>
              {decide.error instanceof ApiRequestError && (
                <p className="mt-1 text-xs text-danger">{decide.error.body.message}</p>
              )}
            </Card>
          ))}
        </div>
      </div>
    </div>
  );
}

// SSE-driven draft progress — reuses the run-events stream
function DraftProgress({ runID, issueID, onDone }: { runID: string; issueID: number; onDone: () => void }) {
  const { t } = useT();
  const qc = useQueryClient();
  const [events, setEvents] = useState<RunEvent[]>([]);
  useEffect(() => {
    const es = new EventSource(`/api/v1/runs/${runID}/events`);
    es.onmessage = (ev) => {
      const e = JSON.parse(ev.data) as RunEvent & { status?: string; issue_id?: number; error?: string };
      setEvents((prev) => (prev.some((p) => p.seq === e.seq) ? prev : [...prev, e]));
      if (e.step === "done" || e.status === "done") {
        es.close();
        onDone();
        qc.invalidateQueries({ queryKey: ["document", issueID] });
        qc.invalidateQueries({ queryKey: ["issue", issueID] });
      }
      if (e.status === "failed") es.close();
    };
    return () => es.close();
  }, [runID, issueID, onDone, qc]);

  return (
    <Card className="mb-4 p-3 text-sm">
      {events.map((e) => {
        const steps = t.run.step as Record<string, string>;
        const counts = t.run.counts as Record<string, string>;
        const label = steps[e.step] ?? e.step;
        const bits = Object.entries(e.counts ?? {}).map(([k, v]) => `${v} ${counts[k] ?? k}`);
        return (
          <p key={e.seq} className="text-muted">
            {label} {bits.length ? `— ${bits.join(", ")}` : ""}
          </p>
        );
      })}
      <p className="text-xs text-muted">{t.draft.running}</p>
    </Card>
  );
}
