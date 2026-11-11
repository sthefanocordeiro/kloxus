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
  status: "" | "reviewing" | "drafted" | "approved" | "sent";
  subject: string;
  created_at: string;
  items: Record<string, number>;
  flags: number;
  missing_required_commentary: number;
  receipt?: ApprovalReceipt;
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
  message?: string;
  target?: { kind: string; ref: string };
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
  editSentence: (issueID: number, sid: string, text: string, citations: number[], version: string) =>
    request<{ sentence?: DocSentence; coverage: Coverage; flags: number; version: string }>(
      "PATCH", `/api/v1/issues/${issueID}/sentences/${sid}`, { text, citations, version },
    ),
  removeSentence: (issueID: number, sid: string, version: string) =>
    request<{ coverage: Coverage; flags: number; version: string }>(
      "DELETE", `/api/v1/issues/${issueID}/sentences/${sid}`, { version },
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
  send: (issueID: number, audience: "test" | "readers", to?: string) =>
    request<{ issue: IssueSummary; outbox_email_id: number; audience: string }>(
      "POST", `/api/v1/issues/${issueID}/send`, { audience, to },
    ),
  preview: (issueID: number, reader: string, surface: string) =>
    request<{ body_md: string; gated: boolean; reader: string; surface: string }>(
      "GET", `/api/v1/issues/${issueID}/preview?reader=${reader}&surface=${surface}`,
    ),
};
