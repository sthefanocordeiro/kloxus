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
  // a post-approval edit reverted the issue to drafted — the header
  // shows "re-approve to send" until a fresh approval lands (KLOX-66)
  edited_after_approval?: boolean;
}

export interface RunEvent {
  seq: number;
  step: string;
  counts?: Record<string, number>;
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
  constructor(status: number, body: ApiError) {
    super(body.message || `request failed: ${status}`);
    this.status = status;
    this.body = body;
  }
}

// request is the single fetch path: JSON in, JSON out, envelope errors
// become ApiRequestError.
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
    throw new ApiRequestError(res.status, err);
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
  startRun: (pubID: number) => request<{ run: Run }>("POST", `/api/v1/publications/${pubID}/runs`, {}).then((r) => r.run),
  run: (id: string) => request<{ run: Run }>("GET", `/api/v1/runs/${id}`).then((r) => r.run),
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
  // Workspace claim: mails a single-use magic link to the creator.
  // Outbox mode answers with the link itself so the demo can open it.
  claim: (email: string, lang: string) =>
    request<{ sent: boolean; email: string; delivery?: string; claim_url?: string; refused?: string; outbox_email_id?: number }>(
      "POST", "/api/v1/workspace/claim", { email, lang },
    ),
  proposal: () => request<{ proposal: Proposal }>("GET", "/api/v1/proposal").then((r) => r.proposal),
  propose: (material: string) =>
    request<{ proposal: Proposal; scrape: ScrapeInfo }>("POST", "/api/v1/publications/propose", { material }),
  today: (pubID: number | string) => request<TodayData>("GET", `/api/v1/publications/${pubID}/today`),
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
  demoReset: () => request<{ ok: boolean }>("POST", "/api/v1/demo/reset", {}),
  demoNew: () =>
    request<{ workspace: { id: string; label: string } }>("POST", "/api/v1/demo/new", {}),
};
