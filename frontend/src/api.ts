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
  // KLOXUS_PUBLISHING (KLOX-122): the reader-facing surface (send,
  // readers, public pages, paywall) is hidden unless the server says on
  publishing?: boolean;
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
  // topic declares the pauta the section wants — keywords that steer
  // which suggestions land here (KLOX-94); may be empty
  topic?: string;
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
  // reject_terms are the editorial line's no-go themes — the aderência
  // ranking demotes matching stories and never auto-slots them (KLOX-94)
  reject_terms?: string;
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
  // a post-approval edit reverted the issue to drafted — the header
  // shows "re-approve to send" until a fresh approval lands (KLOX-66)
  edited_after_approval?: boolean;
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
  paywall?: { enabled: boolean; price: string; test_mode?: boolean; simulated?: boolean };
}

// The /p/{slug} newsletter landing page (KLOX-102): the publication's
// own brand plus its public editions archive — the same sent/scheduled/
// test-sent visibility the issue page enforces. Reading an edition
// stays on /p/{slug}/{num}, where the paywall gating applies.
export interface PublicNewsletter {
  publication: { name: string; slug: string; language: string };
  editions: { number: number; subject: string; created_at: string }[];
  paywall?: { enabled: boolean; price: string };
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
  /** KLOX-95: enrichment card fields — article type slug, detected
   * language, topic tags, newest member publish time. */
  kind?: string;
  language?: string;
  topics?: string[];
  published_at?: string;
  /** KLOX-95: hidden by default — different language / off-topic. */
  foreign?: boolean;
  off_topic?: boolean;
  /** KLOX-67: lineup card whose pauta body is still AI-written. */
  ai_draft?: boolean;
  /** KLOX-94: bench story the newsletter's reject terms exclude — it
      stays visible at the bench tail, never auto-slotted. */
  rejected_editorial?: boolean;
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
  // KLOX-95: how many bench suggestions the default filter hides
  // (foreign-language / off-topic) — the toggle's honest count.
  hidden_bench?: number;
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
  publication_id?: number;
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
  /** KLOX-181: the item's written text as one field, exactly as the
   * author typed it — the sentence-split model drops the period at
   * every '. ' boundary, so pauta entries and exports use this. */
  text?: string;
  sentences: DocSentence[];
  sources: { ref: number; name: string; url: string; item_id: number; excerpt: string; date: string }[];
  flags?: { sentence: string; note: string }[];
  /** KLOX-67: pauta body still AI-written — the composer shows "Gerado
   * por IA" until the author edits it. */
  ai_draft?: boolean;
  // link-only pauta (KLOX-66 D2): ships the story link with zero
  // written text — no commentary or citation is expected for it
  link_only?: boolean;
}

// One ordered unit inside a section (KLOX-66): a pauta (kind "item")
// or an author-inserted heading / divider / link / image / embed /
// text block. The block's index inside section.blocks addresses it in
// block ops. image → url is the src and text the alt; embed → text is
// the sanitized HTML.
export interface DocBlock {
  kind: "item" | "heading" | "divider" | "link" | "image" | "embed" | "text";
  ref?: number; // item → dossier ref
  text?: string;
  url?: string; // link → href, image → src
}

export interface DocSection {
  title: string;
  // featured | news | links — a links section's items are link-only:
  // the link is the source, no written text is expected (KLOX-65/D2)
  format?: string;
  commentary: { mode: string; text: string };
  items: DocItem[];
  // ordered layout — items in body order plus author blocks; absent on
  // documents written before the block model, callers fall back to items
  blocks?: DocBlock[];
}

// One reusable standard message (greeting/footer/signature) — author
// text inserted into an edition without retyping (KLOX-66 D3).
export interface StdMessage {
  id: number;
  name: string;
  body: string;
}

// One entry in the newsletter's default edition template (KLOX-72): a
// fixed composer block copied into every new edition, or a "section"
// slot that materializes the week's pautas of that section. Same
// kind/text/url shape as the composer blocks.
export interface TemplateBlock {
  kind: "section" | "heading" | "divider" | "link" | "image" | "embed" | "text";
  text?: string;
  url?: string;
  section?: string; // kind=section → section title
}

export interface Template {
  blocks: TemplateBlock[];
  version: string;
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

// What the propose scrape reports alongside the proposal (KLOX-99) —
// the honest "what we extracted" summary the creator reviews.
export interface ScrapeInfo {
  url: string;
  title: string;
  provider: string;
  simulated: boolean;
  words: number;
  warnings: string[];
}

// The proposal is the five editable blocks plus carried settings and the
// sources offered next on the sources screen.
export interface Proposal {
  name: string;
  topic: string;
  audience: string;
  editorial_line: string;
  reject_terms?: string;
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

// Onboarding from zero (KLOX-123): the radar, the calibration deck and
// the Sim/Não answers that train its ranking.
export interface PautaCard {
  story_id: number;
  title: string;
  outlet: string;
  outlets: number;
  summary: string;
  topics: string[];
  url: string;
  age_hours: number | null;
}

export type RenditionFormat = "linkedin" | "x" | "html" | "markdown";
export interface Rendition {
  id: number; issue_id: number; format: RenditionFormat;
  body_text: string; body_html: string; body_md: string;
  created_at: string; updated_at: string;
  // the piece changed since this version was forked/refreshed
  stale: boolean;
}

export interface PieceRow {
  id: number;
  number: number;
  status: "draft" | "reviewed" | "exported";
}

export interface TodayData {
  cards: PautaCard[];
  pieces: PieceRow[];
  open_piece: number;
  window_hours: number;
}

export interface TodayWrite {
  issue_id: number;
  created?: boolean;
  add_block?: boolean;
  section?: string;
}

// KLOX-168: the main flow — ticked stories become a pauta of free text.
export interface PautaEntry {
  story_id: number;
  issue_item_id?: number;
  title: string;
  url: string;
  outlet: string;
  date: string;
  text: string;
}
export interface PautaState {
  issue_id: number;
  number: number;
  status: string;
  opening: string;
  entries: PautaEntry[];
}
export interface PautaSave {
  opening: string;
  entries: { story_id: number; text: string }[];
}

export const api = {
  pautaCreate: (pubID: number | string, storyIDs: number[]) =>
    request<{ issue_id: number }>("POST", `/api/v1/publications/${pubID}/pauta`, { story_ids: storyIDs }),
  pauta: (issueID: number | string) => request<PautaState>("GET", `/api/v1/issues/${issueID}/pauta`),
  pautaSave: (issueID: number | string, body: PautaSave) =>
    request<{ saved: boolean }>("PUT", `/api/v1/issues/${issueID}/pauta`, body),
  pautaFinish: (issueID: number | string) =>
    request<{ ready: boolean; missing_text: number }>("POST", `/api/v1/issues/${issueID}/pauta/finish`, {}),
  logout: () => request<{ ok: boolean }>("POST", "/api/v1/logout", {}),
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
  // all=true serves the default-filtered (foreign-language / off-topic)
  // suggestions back — the board's "show hidden" toggle.
  candidates: (issueID: number, page = 0, all = false) =>
    request<CandidatesResponse>("GET", `/api/v1/issues/${issueID}/candidates?page=${page}${all ? "&all=1" : ""}`),
  // editorial rerank ("Ordenar pela minha linha editorial") — capped to
  // 2 paid calls per newsletter per day; {reranked:false} keeps the
  // free order and is not an error.
  rerank: (issueID: number) =>
    request<{ reranked: boolean }>("POST", `/api/v1/issues/${issueID}/rerank`, {}),
  lineup: (issueID: number, entries: LineupEntry[], all = false) =>
    request<{
      candidates: Candidate[];
      bench_total?: number; has_more?: boolean; page?: number; page_size?: number; hidden_bench?: number;
      guide?: { version: number; added?: string[] };
    }>(
      "PUT", `/api/v1/issues/${issueID}/lineup${all ? "?all=1" : ""}`, { entries },
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
  // composer block ops (KLOX-66): every mutation answers with the fresh
  // document — callers swap it into the cache and take its version
  blockInsert: (issueID: number, body: {
    section: string; kind: "item" | "heading" | "divider" | "link" | "image" | "embed" | "text";
    text?: string; url?: string; story_id?: number; position?: number; version: string;
  }) => request<{ document: IssueDocument }>("POST", `/api/v1/issues/${issueID}/blocks`, body),
  blockUpdate: (issueID: number, body: {
    section: string; index: number; text?: string; url?: string; version: string;
  }) => request<{ document: IssueDocument }>("PUT", `/api/v1/issues/${issueID}/blocks`, body),
  blockMove: (issueID: number, section: string, from: number, to: number, version: string) =>
    request<{ document: IssueDocument }>("POST", `/api/v1/issues/${issueID}/blocks/move`,
      { section, from, to, version }),
  blockRemove: (issueID: number, section: string, index: number, version: string) =>
    request<{ document: IssueDocument }>("DELETE", `/api/v1/issues/${issueID}/blocks`,
      { section, index, version }),
  // link-only toggle (KLOX-66 D2): marks/unmarks a pauta as shipping
  // the bare story link — no written text required
  itemLinkOnly: (issueID: number, ref: number, linkOnly: boolean, version: string) =>
    request<{ document: IssueDocument }>("PUT", `/api/v1/issues/${issueID}/items/${ref}/link`,
      { link_only: linkOnly, version }),
  // media upload (KLOX-66 D3): JSON base64 stays inside the API's
  // JSON-only contract — returns the public /m/{id}-{token} URL
  uploadMedia: (issueID: number, body: { content_type: string; data_b64: string; name: string }) =>
    request<{ media: { id: number; url: string; content_type: string; bytes: number } }>(
      "POST", `/api/v1/issues/${issueID}/media`, body),
  // renditions (KLOX-125): per-format forks of an approved piece
  renditions: (issueID: number) =>
    request<{ renditions: Rendition[]; stories: number }>("GET", `/api/v1/issues/${issueID}/renditions`),
  renditionCreate: (issueID: number, format: RenditionFormat) =>
    request<{ rendition: Rendition; stories: number }>("POST", `/api/v1/issues/${issueID}/renditions`, { format })
      .then((r) => r.rendition),
  renditionSave: (issueID: number, id: number, body: string) =>
    request<{ rendition: Rendition }>("PUT", `/api/v1/issues/${issueID}/renditions/${id}`, { body })
      .then((r) => r.rendition),
  renditionRefresh: (issueID: number, id: number) =>
    request<{ rendition: Rendition }>("POST", `/api/v1/issues/${issueID}/renditions/${id}/refresh`, {})
      .then((r) => r.rendition),
  renditionDelete: (issueID: number, id: number) =>
    request<void>("DELETE", `/api/v1/issues/${issueID}/renditions/${id}`),
  // standard-message library (KLOX-66 D3): reusable author text
  messages: (pubID: number) =>
    request<{ messages: StdMessage[] }>("GET", `/api/v1/publications/${pubID}/messages`).then((r) => r.messages),
  messageCreate: (pubID: number, body: { name: string; body: string }) =>
    request<{ message: StdMessage }>("POST", `/api/v1/publications/${pubID}/messages`, body).then((r) => r.message),
  messageDelete: (pubID: number, mid: number) =>
    request<void>("DELETE", `/api/v1/publications/${pubID}/messages/${mid}`),
  // newsletter template (KLOX-72): the default block layout every new
  // edition materializes — saves carry the version like document ops
  template: (pubID: number) =>
    request<{ template: Template }>("GET", `/api/v1/publications/${pubID}/template`).then((r) => r.template),
  saveTemplate: (pubID: number, blocks: TemplateBlock[], version: string) =>
    request<{ template: Template }>("PUT", `/api/v1/publications/${pubID}/template`, { blocks, version }).then((r) => r.template),
  // template image upload — same JSON base64 contract as the issue's,
  // scoped to the publication since no edition exists yet
  uploadPubMedia: (pubID: number, body: { content_type: string; data_b64: string; name: string }) =>
    request<{ media: { id: number; url: string; content_type: string; bytes: number } }>(
      "POST", `/api/v1/publications/${pubID}/media`, body),
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
  // Paid-section checkout (KLOX-13): opens an AbacatePay PIX billing —
  // the reader is redirected to checkout_url. provider "fake" is the
  // documented fallback the UI labels as simulated; test_mode is the
  // provider's own sandbox flag ("no real charge").
  payCheckout: (slug: string, email: string, issue: string, query: string) =>
    request<{
      ref: string; status: string; provider: string; checkout_url?: string;
      test_mode: boolean; simulated: boolean; already_subscribed?: boolean;
    }>("POST", `/api/v1/p/${slug}/checkout${query}`, { email, issue }),
  // The return page polls this until the webhook-confirmed grant lands
  // — also the fallback path when the webhook never arrives.
  payStatus: (slug: string, ref: string, query: string) =>
    request<{
      ref: string; status: string; provider: string; test_mode: boolean;
      simulated: boolean; checkout_url?: string; paid_until?: string; magic_link?: string;
    }>("GET", `/api/v1/p/${slug}/pay/${ref}${query}`),
  hold: (issueID: number) =>
    request<{ issue: IssueSummary }>("POST", `/api/v1/issues/${issueID}/hold`, {}),
  after: (issueID: number) => request<AfterSend>("GET", `/api/v1/issues/${issueID}/after`),
  publicIssue: (slug: string, num: string | number, query = "") =>
    request<PublicIssue>("GET", `/api/v1/p/${slug}/${num}${query}`),
  // The /p/{slug} landing page: brand + editions archive (KLOX-102).
  publicNewsletter: (slug: string, query = "") =>
    request<PublicNewsletter>("GET", `/api/v1/p/${slug}${query}`),
  outbox: (pubID: number) =>
    request<{ emails: OutboxEmail[] }>("GET", `/api/v1/publications/${pubID}/outbox`).then((r) => r.emails),
  outboxEmail: (id: number) =>
    request<{ email: OutboxEmail }>("GET", `/api/v1/outbox/${id}`).then((r) => r.email),
  reader: (pubID: number) =>
    request<{ reader: { email: string; name: string }; subscribed: boolean; subscribers?: number; readers?: number; confirmed?: number; public_url?: string; issue?: IssueSummary }>(
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
    request<{ proposal: Proposal; scrape: ScrapeInfo }>("POST", "/api/v1/publications/propose", { material }),
  today: (pubID: number | string) => request<TodayData>("GET", `/api/v1/publications/${pubID}/today`),
  todayWrite: (pubID: number | string, storyID: number) =>
    request<TodayWrite>("POST", `/api/v1/publications/${pubID}/today/write`, { story_id: storyID }),
  createRadar: (name: string, topics: string, niches: string[]) =>
    request<{ publication: Publication; sources: SourceRow[] }>("POST", "/api/v1/radar", { name, about: topics, niches }),
  calibration: (pubID: number | string) =>
    request<{ cards: PautaCard[]; window_hours: number }>("POST", `/api/v1/publications/${pubID}/calibration`).then((r) => r.cards),
  calibrationAnswer: (pubID: number | string, storyID: number, answer: "yes" | "no") =>
    request<{ ok: boolean }>("POST", `/api/v1/publications/${pubID}/calibration/answer`, { story_id: storyID, answer }),
  skipTopic: (pubID: number | string, topic: string) =>
    request<{ publication: Publication }>("POST", `/api/v1/publications/${pubID}/skip`, { topic }),
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
