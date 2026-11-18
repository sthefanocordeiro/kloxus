// Types mirroring docs/api.md (the /api/v1 contract).
export interface Workspace {
  id: string;
  label: string;
  mode: string;
  hosted: boolean;
  llm_mode: string; // "auto" | "off" — off = deterministic pipeline, no AI calls
  // live mail (a sender is configured) requires a claim before any send
  claimed?: boolean;
  owner_email?: string;
  mail?: "live" | "outbox";
  // the sender's wire identity — server-side evidence for verification
  // preflights: outbox | resend | synthetic (loopback fake) | custom
  mail_transport?: "outbox" | "resend" | "synthetic" | "custom";
}

// One newsletter section as the builder edits it: id is the stable
// identity that survives renames, format shapes the issue slot, access
// marks subscriber-only sections (KLOX-65).
export interface NewsletterSection {
  id: string;
  title: string;
  format: "featured" | "news" | "links";
  items: number;
  commentary: string; // required | optional | none
  access: "all" | "subscribers";
}

export interface Publication {
  id: number;
  slug: string;
  name: string;
  custom?: boolean;
  language: string;
  topic: string;
  audience: string;
  editorial_line: string;
  tone: string;
  frequency: string;
  ai_disclosure: string;
  sections: NewsletterSection[];
  paywall: { enabled: boolean; paid_sections: string[]; price: string };
}

export interface IssueSummary {
  id: number;
  publication_id: number;
  number: number;
  status: "" | "reviewing" | "drafted" | "approved" | "sent" | "scheduled";
  subject: string;
  created_at: string;
  items: Record<string, number>;
  flags: number;
  missing_required_commentary: number;
  // recorded model cost of the run steps attached to this issue
  cost_usd?: number;
  document_version?: string;
  scheduled_at?: string;
  // stamped by a test send — the issue stays in its lifecycle status
  // (approved/editable); the marker unlocks the after-sending surface
  test_sent_at?: string;
  receipt?: ApprovalReceipt;
}

export interface OutboxEmail {
  id: number;
  kind: string;
  to: string;
  subject: string;
  body_md?: string;
  body_html?: string;
  // delivery-ledger fields: provider resend|outbox, status
  // queued|sent|delivered|bounced|complained|refused|fallback
  provider?: string;
  provider_id?: string;
  status?: string;
  error?: string;
  created_at: string;
}

export interface PublicIssue {
  publication: { name: string; slug: string; language: string };
  issue: { number: number; subject: string; created_at: string };
  body_md: string;
  gated: boolean;
  subscribed: boolean;
  reader: { name: string };
  paid_sections: string[];
}

export interface AfterSend {
  issue: IssueSummary;
  public_url: string;
  learned: string[];
  guide: { version: number };
  export_html: string;
  export_md: string;
}

export interface ApprovalReceipt {
  issue_id: number;
  number: number;
  document_version: string;
  approver: string;
  approved_at: string;
  checks: Check[];
}

export interface IssueItem {
  id: number;
  issue_id: number;
  story_id: number;
  section: string;
  score: number;
  reason: string;
  status: string;
  origin: string;
  title: string;
  outlets: string[];
  reject_reason?: string;
  /** KLOX-67: the pauta body arrived AI-written; cleared on author edits. */
  ai_draft?: boolean;
}

export interface Dossier {
  ref: number;
  title?: string;
  summary: string;
  facts: { text: string; ref: number }[];
  deep_facts?: { text: string; ref: number }[];
  quote: { text: string; attribution: string };
  unknowns: string[];
  interpretation: string[];
  sources: { ref: number; url: string; item_id?: number; name?: string }[];
}

export interface RunEvent {
  seq: number;
  step: string;
  counts?: Record<string, number>;
}

export interface Candidate {
  story_id: number;
  item_id?: number;
  title: string;
  outlets: string[];
  sources: number;
  duplicates: number;
  score: number;
  reason: string;
  rank: number;
  in_issue: boolean;
  section?: string;
  suggested_section?: string;
  status?: string;
  origin?: string;
  position: number;
  dossier: boolean;
  available: boolean;
  /** KLOX-67 §7.2: one-line AI summary; empty until the sweep runs. */
  summary?: string;
  /** KLOX-67: lineup card whose pauta body is still AI-written. */
  ai_draft?: boolean;
}

export interface BoardSection {
  id?: string;
  title: string;
  capacity: number;
  commentary: string;
  format?: string;
  access?: string;
}

export interface CandidatesResponse {
  candidates: Candidate[];
  sections: BoardSection[];
  // true when the builder was edited after this edition's run — the
  // edition keeps its own sections (KLOX-65)
  sections_changed?: boolean;
  demo: boolean;
  llm_mode: string;
  // bench pagination (KLOX-45): the ranked pool is capped at the top
  // 150 and the bench pages at page_size — in-issue stories ride along
  // on every page so the lineup column is always complete.
  page: number;
  page_size: number;
  bench_total: number;
  has_more: boolean;
}

export interface StoryItem {
  id: number;
  title: string;
  url: string;
  outlet: string;
  published_at: string;
  snippet: string;
}

export interface StoryDetail {
  id: number;
  title: string;
  score: number;
  reason: string;
  outlets: string[];
  items: StoryItem[];
}

export interface LineupEntry {
  story_id: number;
  section: string;
}

export interface Run {
  id: string;
  kind: "prepare" | "draft";
  status: "running" | "done" | "failed" | "blocked";
  step: string;
  issue_id?: number;
  no_new_items: boolean;
  error?: string;
  blocked?: boolean;
  blocked_reason?: string;
  sample?: boolean;
  events: RunEvent[];
}

export interface DocSentence {
  id: string;
  text: string;
  kind: "fact" | "interpretation";
  citations: number[];
  evidence?: { ref: number; excerpt: string }[];
  flag?: { note: string };
}

export interface DocItem {
  ref: number;
  issue_item_id: number;
  headline: string;
  sentences: DocSentence[];
  sources: { ref: number; name: string; url: string; item_id: number; excerpt: string; date: string }[];
  flags?: { sentence: string; note: string }[];
  /** KLOX-67: pauta body still AI-written — the composer shows "Gerado
   * por IA" until the author edits it. */
  ai_draft?: boolean;
}

export interface DocSection {
  title: string;
  // featured | news | links — a links section's items are link-only:
  // the link is the source, no written text is expected (KLOX-65/D2)
  format?: string;
  commentary: { mode: string; text: string };
  items: DocItem[];
}

export interface Coverage {
  ok: number;
  cited: number;
  total: number;
  flagged: number;
}

export interface IssueDocument {
  issue_id: number;
  number: number;
  status: string;
  subject: string;
  version: string;
  sections: DocSection[];
  coverage: Coverage;
}

export interface Check {
  code: string;
  ok: boolean;
  count?: number;
  message?: string;
  target?: { kind: string; ref: string };
}

// The DELETE response's undo payload — POST it back to restore.
export interface RemovedSentence {
  ref: number;
  index: number;
  text: string;
  kind: "fact" | "interpretation";
  citations: number[];
  flag?: string;
}

// The landing page's cited sample — workspace-free demo content.
export interface SampleDossier extends Dossier {
  title: string;
}
export interface Sample {
  publication: string;
  issue_number: number;
  date: string;
  dossiers: SampleDossier[];
  how_it_works: string[];
}

// The proposal is the five editable blocks plus carried settings and the
// sources offered next on the sources screen.
export interface Proposal {
  name: string;
  topic: string;
  audience: string;
  editorial_line: string;
  tone: string;
  language: string;
  frequency: string;
  ai_disclosure: string;
  sections: NewsletterSection[];
  sources: { type: string; url: string }[];
  paywall: { enabled: boolean; paid_sections: string[]; price: string };
}

export interface SourceRow {
  id: number;
  name: string;
  type: string;
  url: string;
  canonical_url: string;
  last_fetch_at?: string;
  last_status?: string;
  items_per_week: number;
  monitor: { provider?: string; id?: string; status?: string };
}

export interface PreviewItem {
  title: string;
  url: string;
  outlet: string;
  published_at?: string;
}

export interface ApiError {
  code: string;
  message: string;
  reasons?: { code: string; message: string; target?: { kind: string; ref: string } }[];
}

export class ApiRequestError extends Error {
  status: number;
  body: ApiError;
  document?: IssueDocument;
  constructor(status: number, body: ApiError, document?: IssueDocument) {
    super(body.message || `request failed: ${status}`);
    this.status = status;
    this.body = body;
    this.document = document;
  }
}

// request is the single fetch path: JSON in, JSON out, envelope errors
// become ApiRequestError. Conflict responses may carry the fresh
// document — callers use it to resync.
export async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const mutating = method !== "GET" && method !== "HEAD";
  const res = await fetch(path, {
    method,
    headers: mutating ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const data = res.status === 204 ? {} : await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = (data as { error?: ApiError }).error ?? { code: "internal", message: `HTTP ${res.status}` };
    throw new ApiRequestError(res.status, err, (data as { document?: IssueDocument }).document);
  }
  return data as T;
}

// The real-mode catalog lists only offered rows (launch/signal —
// pickable). hold/blocked sources are never fetched and never appear
// in user-facing source lists (KLOX-45).
export interface CatalogOffer {
  type: string;
  url: string;
  name?: string;
  items_per_week?: number;
  policy?: string;
  kind?: string;
  suspended?: boolean;
  note?: string;
}
export interface SourceCatalog {
  offered: CatalogOffer[];
}

export const api = {
  workspace: () => request<{ workspace: Workspace }>("GET", "/api/v1/workspace").then((r) => r.workspace ?? (r as unknown as Workspace)),
  publications: () => request<{ publications: Publication[] }>("GET", "/api/v1/publications").then((r) => r.publications),
  publication: (id: string | number) => request<{ publication: Publication; latest_issue?: IssueSummary }>("GET", `/api/v1/publications/${id}`),
  issues: (pubID: number) => request<{ issues: IssueSummary[] }>("GET", `/api/v1/publications/${pubID}/issues`).then((r) => r.issues),
  issue: (id: number) => request<{ issue: IssueSummary }>("GET", `/api/v1/issues/${id}`).then((r) => r.issue),
  issueItems: (id: number) => request<{ items: IssueItem[]; pending: number }>("GET", `/api/v1/issues/${id}/items`),
  dossier: (itemID: number) => request<{ item: IssueItem; dossier: Dossier }>("GET", `/api/v1/items/${itemID}/dossier`),
  document: (id: number) => request<{ document: IssueDocument }>("GET", `/api/v1/issues/${id}/document`).then((r) => r.document),
  readiness: (id: number) => request<{ ready: boolean; coverage: Coverage; checks: Check[] }>("GET", `/api/v1/issues/${id}/readiness`),
  startRun: (pubID: number) => request<{ run: Run }>("POST", `/api/v1/publications/${pubID}/runs`, {}).then((r) => r.run),
  run: (id: string) => request<{ run: Run }>("GET", `/api/v1/runs/${id}`).then((r) => r.run),
  draft: (issueID: number) => request<{ run: Run }>("POST", `/api/v1/issues/${issueID}/draft`, {}).then((r) => r.run),
  // manual compose: the lineup becomes empty item slots, no model calls
  compose: (issueID: number) =>
    request<{ issue: IssueSummary; selected: number }>("POST", `/api/v1/issues/${issueID}/compose`, {}),
  composeSentence: (issueID: number, ref: number, text: string, citations: number[], kind: "fact" | "interpretation", version: string) =>
    request<{ coverage: Coverage; flags: number; version: string; added?: number }>(
      "POST", `/api/v1/issues/${issueID}/items/${ref}/compose`, { text, kind, citations, version },
    ),
  decide: (issueID: number, itemID: number, kind: string, reason?: string) =>
    request<{ item: IssueItem; pending: number; guide?: { version: number; added?: string[] } }>(
      "POST", `/api/v1/issues/${issueID}/items/${itemID}/decisions`, { kind, reason },
    ),
  candidates: (issueID: number, page = 0) =>
    request<CandidatesResponse>("GET", `/api/v1/issues/${issueID}/candidates?page=${page}`),
  // editorial rerank ("Ordenar pela minha linha editorial") — capped to
  // 2 paid calls per newsletter per day; {reranked:false} keeps the
  // free order and is not an error.
  rerank: (issueID: number) =>
    request<{ reranked: boolean }>("POST", `/api/v1/issues/${issueID}/rerank`, {}),
  lineup: (issueID: number, entries: LineupEntry[]) =>
    request<{
      candidates: Candidate[];
      bench_total?: number; has_more?: boolean; page?: number; page_size?: number;
      guide?: { version: number; added?: string[] };
    }>(
      "PUT", `/api/v1/issues/${issueID}/lineup`, { entries },
    ),
  story: (sid: number) => request<{ story: StoryDetail }>("GET", `/api/v1/stories/${sid}`).then((r) => r.story),
  editSentence: (issueID: number, sid: string, text: string, citations: number[], version: string) =>
    request<{ sentence?: DocSentence; coverage: Coverage; flags: number; version: string }>(
      "PATCH", `/api/v1/issues/${issueID}/sentences/${sid}`, { text, citations, version },
    ),
  removeSentence: (issueID: number, sid: string, version: string) =>
    request<{ coverage: Coverage; flags: number; version: string; removed?: RemovedSentence }>(
      "DELETE", `/api/v1/issues/${issueID}/sentences/${sid}`, { version },
    ),
  removeFlagged: (issueID: number, ref: number, version: string) =>
    request<{ coverage: Coverage; flags: number; version: string; removed?: RemovedSentence[] }>(
      "DELETE", `/api/v1/issues/${issueID}/items/${ref}/sentences`, { version },
    ),
  restoreSentence: (issueID: number, removed: RemovedSentence, version: string) =>
    request<{ coverage: Coverage; flags: number; version: string }>(
      "POST", `/api/v1/issues/${issueID}/items/${removed.ref}/sentences`,
      { index: removed.index, text: removed.text, kind: removed.kind, citations: removed.citations, flag: removed.flag, version },
    ),
  // batch undo for the bulk remove-flagged action: one POST restores the list
  restoreSentences: (issueID: number, ref: number, removed: RemovedSentence[], version: string) =>
    request<{ coverage: Coverage; flags: number; version: string }>(
      "POST", `/api/v1/issues/${issueID}/items/${ref}/sentences`, { sentences: removed, version },
    ),
  markInterpretation: (issueID: number, sid: string, text: string | undefined, version: string) =>
    request<{ coverage: Coverage; flags: number; version: string }>(
      "POST", `/api/v1/issues/${issueID}/sentences/${sid}/interpretation`, { text, version },
    ),
  commentary: (issueID: number, section: string, text: string, version: string) =>
    request<{ commentary: { section: string; text: string }; version: string }>(
      "PUT", `/api/v1/issues/${issueID}/commentary`, { section, text, version },
    ),
  approve: (issueID: number) =>
    request<{ issue: IssueSummary; receipt: ApprovalReceipt }>("POST", `/api/v1/issues/${issueID}/approve`, {}),
  send: (issueID: number, audience: "test" | "readers", when: "now" | "schedule" = "now", to?: string, at?: string) =>
    request<{
      issue: IssueSummary; outbox_email_id?: number; audience?: string; scheduled?: boolean; scheduled_at?: string;
      to?: string[]; subject?: string; delivery?: string; sent?: number; refused?: string[]; deduped?: number;
      fallback?: boolean; warnings?: string[];
    }>(
      "POST", `/api/v1/issues/${issueID}/send`, { audience, when, to, at },
    ),
  // Workspace claim: mails a single-use magic link to the creator.
  // Outbox mode answers with the link itself so the demo can open it.
  claim: (email: string, lang: string) =>
    request<{ sent: boolean; email: string; delivery?: string; claim_url?: string; refused?: string; outbox_email_id?: number }>(
      "POST", "/api/v1/workspace/claim", { email, lang },
    ),
  // Reader double opt-in: mails a confirm link; nothing is sent to the
  // reader until they open it. query carries the sealed ?tok.
  subscribe: (slug: string, email: string, query = "") =>
    request<{ sent: boolean; already_confirmed?: boolean; delivery?: string; confirm_url?: string; refused?: string }>(
      "POST", `/api/v1/p/${slug}/subscribe${query}`, { email },
    ),
  hold: (issueID: number) =>
    request<{ issue: IssueSummary }>("POST", `/api/v1/issues/${issueID}/hold`, {}),
  after: (issueID: number) => request<AfterSend>("GET", `/api/v1/issues/${issueID}/after`),
  publicIssue: (slug: string, num: string | number, query = "") =>
    request<PublicIssue>("GET", `/api/v1/p/${slug}/${num}${query}`),
  outbox: (pubID: number) =>
    request<{ emails: OutboxEmail[] }>("GET", `/api/v1/publications/${pubID}/outbox`).then((r) => r.emails),
  outboxEmail: (id: number) =>
    request<{ email: OutboxEmail }>("GET", `/api/v1/outbox/${id}`).then((r) => r.email),
  reader: (pubID: number) =>
    request<{ reader: { email: string; name: string }; subscribed: boolean; subscribers?: number; readers?: number; confirmed?: number; issue?: IssueSummary }>(
      "GET", `/api/v1/publications/${pubID}/reader`),
  paywallSimulate: (pubID: number | string, query = "") =>
    request<{ subscribed: boolean; outbox_email_id: number; magic_link: string }>(
      "POST", `/api/v1/publications/${pubID}/paywall/simulate${query}`, {}),
  paywallCancel: (pubID: number | string, query = "") =>
    request<{ subscribed: boolean }>("POST", `/api/v1/publications/${pubID}/paywall/cancel${query}`, {}),
  preview: (issueID: number, reader: string, surface: string) =>
    request<{ body_md: string; gated: boolean; reader: string; surface: string }>(
      "GET", `/api/v1/issues/${issueID}/preview?reader=${reader}&surface=${surface}`,
    ),
  sample: () => request<Sample>("GET", "/api/v1/sample"),
  proposal: () => request<{ proposal: Proposal }>("GET", "/api/v1/proposal").then((r) => r.proposal),
  propose: (material: string) =>
    request<{ proposal: Proposal }>("POST", "/api/v1/publications/propose", { material }).then((r) => r.proposal),
  createPublication: (p: Omit<Proposal, "sources">) =>
    request<{ publication: Publication }>("POST", "/api/v1/publications", p).then((r) => r.publication),
  updatePublication: (id: string | number, p: Partial<Omit<Proposal, "sources">>) =>
    request<{ publication: Publication }>("PUT", `/api/v1/publications/${id}`, p).then((r) => r.publication),
  deletePublication: (id: string | number) =>
    request<void>("DELETE", `/api/v1/publications/${id}`, { confirm: true }),
  sources: (pubID: number | string) =>
    request<{ sources: SourceRow[] }>("GET", `/api/v1/publications/${pubID}/sources`).then((r) => r.sources),
  sourceCatalog: (pubID: number | string) =>
    request<SourceCatalog>("GET", `/api/v1/publications/${pubID}/sources/catalog`),
  sourcePreview: (pubID: number | string, type: string, url: string) =>
    request<{ items: PreviewItem[]; note?: string }>(
      "GET", `/api/v1/publications/${pubID}/sources/preview?type=${type}&url=${encodeURIComponent(url)}`),
  sourceAdd: (pubID: number | string, type: string, url: string, name?: string) =>
    request<{ source: SourceRow }>("POST", `/api/v1/publications/${pubID}/sources`, { type, url, name }).then((r) => r.source),
  sourceConfirm: (pubID: number | string, include?: string[]) =>
    request<{ sources: SourceRow[] }>("POST", `/api/v1/publications/${pubID}/sources/confirm`, { include }).then((r) => r.sources),
  sourceDelete: (pubID: number | string, sid: number) =>
    request<{ ok: boolean }>("DELETE", `/api/v1/publications/${pubID}/sources/${sid}`),
  guide: (pubID: number | string) =>
    request<{ guide: { version: number; content: string } }>("GET", `/api/v1/publications/${pubID}/guide`).then((r) => r.guide),
  guideSave: (pubID: number | string, content: string) =>
    request<{ guide: { version: number; content: string } }>("PUT", `/api/v1/publications/${pubID}/guide`, { content }).then((r) => r.guide),
  guideRebuild: (pubID: number | string) =>
    request<{ guide: { version: number; content: string } }>("POST", `/api/v1/publications/${pubID}/guide/rebuild`, {}).then((r) => r.guide),
  demoReset: () => request<{ ok: boolean }>("POST", "/api/v1/demo/reset", {}),
  demoNew: () =>
    request<{ workspace: { id: string; label: string } }>("POST", "/api/v1/demo/new", {}),
};
