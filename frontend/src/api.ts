import { clearSession, markSession } from "./lib/session";

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
  // the editorias the theme names (cadastro chips, KLOX-207)
  niches?: string[];
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
  // KLOX-184: list-friendly name (first story title, opening or
  // subject) plus the export mark — copied/downloaded formats.
  title?: string;
  exported?: boolean;
  exported_formats?: string[];
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

export interface ApprovalReceipt {
  issue_id: number;
  number: number;
  document_version: string;
  approver: string;
  approved_at: string;
  checks: Check[];
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

export interface Check {
  code: string;
  ok: boolean;
  count?: number;
  message?: string;
  target?: { kind: string; ref: string };
}

export class ApiRequestError extends Error {
  status: number;
  body: ApiError;
  // seconds the server asked us to wait before retrying (429 Retry-After)
  retryAfter?: number;
  constructor(status: number, body: ApiError, retryAfter?: number) {
    super(body.message || `request failed: ${status}`);
    this.status = status;
    this.body = body;
    this.retryAfter = retryAfter;
  }
}

// Retry-After as delta-seconds or an HTTP-date; anything unreadable
// (or absurd) collapses to undefined so callers fall back to a plain
// retry affordance.
export function parseRetryAfter(v: string | null): number | undefined {
  const s = v?.trim();
  if (!s) return undefined;
  const n = Number(s);
  if (Number.isFinite(n)) return n >= 0 ? Math.min(Math.ceil(n), 3600) : undefined;
  const at = Date.parse(s);
  if (!Number.isNaN(at)) return Math.min(Math.max(0, Math.ceil((at - Date.now()) / 1000)), 3600);
  return undefined;
}

// request is the single fetch path: JSON in, JSON out, envelope errors
// become ApiRequestError. Conflict responses may carry the fresh
// document — callers use it to resync. keepalive lets a save fired on
// pagehide outlive the page itself (KLOX-233); bodies are capped at
// ~64 KB by the browser.
export async function request<T>(method: string, path: string, body?: unknown, keepalive = false): Promise<T> {
  const mutating = method !== "GET" && method !== "HEAD";
  const res = await fetch(path, {
    method,
    headers: mutating ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    keepalive,
  });
  const data = res.status === 204 ? {} : await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = (data as { error?: ApiError }).error ??
      { code: res.status === 429 ? "rate_limited" : "internal", message: `HTTP ${res.status}` };
    throw new ApiRequestError(
      res.status, err,
      res.status === 429 ? parseRetryAfter(res.headers.get("Retry-After")) : undefined,
    );
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
  title?: string;
  created_at?: string;
  exported_formats?: string[];
}

export interface TodayData {
  cards: PautaCard[];
  pieces: PieceRow[];
  open_piece: number;
  window_hours: number;
}

// /issues/:id is gone (KLOX-179): every pauta opens in the pauta
// editor — straight to the export step once it left ("exported" on
// Hoje; "sent"/"scheduled" on the legacy send flow).
export const pautaPath = (id: number | string, status?: string) =>
  status === "exported" || status === "sent" || status === "scheduled"
    ? `/pauta/${id}/export`
    : `/pauta/${id}`;

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
  // KLOX-199: the same name the lists show — the first story's title
  // until the author renames it in the editor
  title: string;
}
export interface PautaSave {
  opening: string;
  entries: { story_id: number; text: string }[];
  // omitted leaves the name alone; "" restores the automatic name
  title?: string;
}

export const api = {
  pautaCreate: (pubID: number | string, storyIDs: number[]) =>
    request<{ issue_id: number }>("POST", `/api/v1/publications/${pubID}/pauta`, { story_ids: storyIDs }),
  pauta: (issueID: number | string) => request<PautaState>("GET", `/api/v1/issues/${issueID}/pauta`),
  pautaSave: (issueID: number | string, body: PautaSave, keepalive = false) =>
    request<{ saved: boolean }>("PUT", `/api/v1/issues/${issueID}/pauta`, body, keepalive),
  pautaFinish: (issueID: number | string) =>
    request<{ ready: boolean; missing_text: number }>("POST", `/api/v1/issues/${issueID}/pauta/finish`, {}),
  logout: () =>
    request<{ ok: boolean }>("POST", "/api/v1/logout", {}).then((r) => {
      clearSession();
      return r;
    }),
  // resolve-or-create: a successful answer means this browser holds a
  // session, so the marker goes down for the public pages to read
  workspace: () =>
    request<{ workspace: Workspace }>("GET", "/api/v1/workspace").then((r) => {
      const w = r.workspace ?? (r as unknown as Workspace);
      markSession();
      return w;
    }),
  publications: () => request<{ publications: Publication[] }>("GET", "/api/v1/publications").then((r) => r.publications),
  publication: (id: string | number) => request<{ publication: Publication; latest_issue?: IssueSummary }>("GET", `/api/v1/publications/${id}`),
  issues: (pubID: number) => request<{ issues: IssueSummary[] }>("GET", `/api/v1/publications/${pubID}/issues`).then((r) => r.issues),
  issue: (id: number) => request<{ issue: IssueSummary }>("GET", `/api/v1/issues/${id}`).then((r) => r.issue),
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
  // KLOX-184: the author copied or downloaded a format — the piece
  // reads "Exportada" and the format is remembered.
  markExported: (issueID: number | string, format: RenditionFormat) =>
    request<{ exported: boolean; formats: string[] }>("POST", `/api/v1/issues/${issueID}/exported`, { format }),
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
  publicIssue: (slug: string, num: string | number, query = "") =>
    request<PublicIssue>("GET", `/api/v1/p/${slug}/${num}${query}`),
  // The /p/{slug} landing page: brand + editions archive (KLOX-102).
  publicNewsletter: (slug: string, query = "") =>
    request<PublicNewsletter>("GET", `/api/v1/p/${slug}${query}`),
  today: (pubID: number | string) => request<TodayData>("GET", `/api/v1/publications/${pubID}/today`),
  createRadar: (name: string, topics: string, niches: string[]) =>
    request<{ publication: Publication; sources: SourceRow[] }>("POST", "/api/v1/radar", { name, about: topics, niches }),
  calibration: (pubID: number | string) =>
    request<{ cards: PautaCard[]; window_hours: number }>("POST", `/api/v1/publications/${pubID}/calibration`).then((r) => r.cards),
  calibrationAnswer: (pubID: number | string, storyID: number, answer: "yes" | "no") =>
    request<{ ok: boolean }>("POST", `/api/v1/publications/${pubID}/calibration/answer`, { story_id: storyID, answer }),
  skipTopic: (pubID: number | string, topic: string) =>
    request<{ publication: Publication }>("POST", `/api/v1/publications/${pubID}/skip`, { topic }),
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
    request<{ workspace: { id: string; label: string } }>("POST", "/api/v1/demo/new", {}).then((r) => {
      markSession();
      return r;
    }),
};
