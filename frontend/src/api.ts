// Types mirroring docs/api.md (the /api/v1 contract).
export interface Workspace {
  id: string;
  label: string;
  mode: string;
  hosted: boolean;
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
  sections: { title: string; items: number; commentary: string }[];
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
  document_version?: string;
  scheduled_at?: string;
  receipt?: ApprovalReceipt;
}

export interface OutboxEmail {
  id: number;
  kind: string;
  to: string;
  subject: string;
  body_md?: string;
  body_html?: string;
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
}

export interface BoardSection {
  title: string;
  capacity: number;
  commentary: string;
}

export interface CandidatesResponse {
  candidates: Candidate[];
  sections: BoardSection[];
  demo: boolean;
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
  status: "running" | "done" | "failed";
  step: string;
  issue_id?: number;
  no_new_items: boolean;
  error?: string;
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
}

export interface DocSection {
  title: string;
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
  sections: { title: string; items: number; commentary: string }[];
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
  const res = await fetch(path, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const data = res.status === 204 ? {} : await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = (data as { error?: ApiError }).error ?? { code: "internal", message: `HTTP ${res.status}` };
    throw new ApiRequestError(res.status, err, (data as { document?: IssueDocument }).document);
  }
  return data as T;
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
  decide: (issueID: number, itemID: number, kind: string, reason?: string) =>
    request<{ item: IssueItem; pending: number; guide?: { version: number; added?: string[] } }>(
      "POST", `/api/v1/issues/${issueID}/items/${itemID}/decisions`, { kind, reason },
    ),
  candidates: (issueID: number) =>
    request<CandidatesResponse>("GET", `/api/v1/issues/${issueID}/candidates`),
  lineup: (issueID: number, entries: LineupEntry[]) =>
    request<{ candidates: Candidate[]; guide?: { version: number; added?: string[] } }>(
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
  restoreSentence: (issueID: number, removed: RemovedSentence, version: string) =>
    request<{ coverage: Coverage; flags: number; version: string }>(
      "POST", `/api/v1/issues/${issueID}/items/${removed.ref}/sentences`,
      { index: removed.index, text: removed.text, kind: removed.kind, citations: removed.citations, flag: removed.flag, version },
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
    request<{ issue: IssueSummary; outbox_email_id?: number; audience?: string; scheduled?: boolean; scheduled_at?: string; to?: string[]; subject?: string }>(
      "POST", `/api/v1/issues/${issueID}/send`, { audience, when, to, at },
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
    request<{ reader: { email: string; name: string }; subscribed: boolean; subscribers?: number; issue?: IssueSummary }>(
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
  sources: (pubID: number | string) =>
    request<{ sources: SourceRow[] }>("GET", `/api/v1/publications/${pubID}/sources`).then((r) => r.sources),
  sourceCatalog: (pubID: number | string) =>
    request<{ offered: { type: string; url: string; name?: string }[] }>("GET", `/api/v1/publications/${pubID}/sources/catalog`).then((r) => r.offered),
  sourcePreview: (pubID: number | string, type: string, url: string) =>
    request<{ items: PreviewItem[]; note?: string }>(
      "GET", `/api/v1/publications/${pubID}/sources/preview?type=${type}&url=${encodeURIComponent(url)}`),
  sourceAdd: (pubID: number | string, type: string, url: string) =>
    request<{ source: SourceRow }>("POST", `/api/v1/publications/${pubID}/sources`, { type, url }).then((r) => r.source),
  sourceConfirm: (pubID: number | string) =>
    request<{ sources: SourceRow[] }>("POST", `/api/v1/publications/${pubID}/sources/confirm`, {}).then((r) => r.sources),
  sourceDelete: (pubID: number | string, sid: number) =>
    request<{ ok: boolean }>("DELETE", `/api/v1/publications/${pubID}/sources/${sid}`),
  demoReset: () => request<{ ok: boolean }>("POST", "/api/v1/demo/reset", {}),
  demoNew: () =>
    request<{ workspace: { id: string; label: string } }>("POST", "/api/v1/demo/new", {}),
};
