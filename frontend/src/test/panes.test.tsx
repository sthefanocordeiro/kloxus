// KLOX-65/D2: a pauta inside a links-format section is link-only — the
// link is the source, so nothing in the UI may press the author to
// write for it. These tests pin the no-pressure contract on the two
// panes that surface per-item writing affordances.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DocItem, IssueDocument, IssueItem } from "../api";
import { EvidencePane, PautasPane } from "../components/Panes";
import { DocActionsCtx, type DocActions } from "../editor/nodes";

afterEach(cleanup);

const item = (over: Partial<DocItem> = {}): DocItem => ({
  ref: 7,
  issue_item_id: 70,
  headline: "A cool link",
  sentences: [],
  sources: [
    { ref: 1, name: "Radar", url: "/a/1", item_id: 1, excerpt: "excerpt one", date: "" },
  ],
  ...over,
});

const doc = (): IssueDocument => ({
  issue_id: 1,
  number: 3,
  status: "drafted",
  subject: "Issue",
  version: "v1",
  coverage: { ok: 0, cited: 0, total: 0, flagged: 0 },
  sections: [
    {
      title: "Quick links",
      format: "links",
      commentary: { mode: "none", text: "" },
      items: [item()],
    },
    {
      title: "Worth knowing",
      format: "news",
      commentary: { mode: "optional", text: "" },
      items: [item({ ref: 8, issue_item_id: 80, headline: "A story", sources: [
        { ref: 1, name: "Radar", url: "/a/1", item_id: 1, excerpt: "excerpt one", date: "" },
        { ref: 2, name: "Pulso", url: "/a/2", item_id: 2, excerpt: "excerpt two", date: "" },
      ] })],
    },
  ],
});

// readOnly: false is what isolates linkOnly as the hiding cause — the
// default context is read-only, which would hide the box either way.
const writableActions: DocActions = {
  editSentence: async () => null,
  interpret: async () => false,
  removeSentence: async () => false,
  removeFlagged: async () => false,
  showEvidence: () => {},
  addSentence: async () => null,
  moveBlock: async () => false,
  removeBlock: async () => false,
  updateBlock: async () => null,
  insertBlock: async () => null,
  openInsert: () => {},
  uploadImage: async () => null,
  toggleLinkOnly: async () => false,
  pickPauta: () => {},
  composeFor: () => {},
  composeClose: () => {},
  composeRef: null,
  composeInsert: null,
  consumeInsert: () => {},
  composeDraftTake: () => null,
  composeDraftSave: () => {},
  readOnly: false,
};

function wrap(ui: React.ReactElement, actions: DocActions = writableActions) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <DocActionsCtx.Provider value={actions}>{ui}</DocActionsCtx.Provider>
    </QueryClientProvider>,
  );
  return qc;
}

describe("link-only pautas (KLOX-65/D2)", () => {
  it("EvidencePane shows no compose or insert affordances for a links item", () => {
    wrap(<EvidencePane item={item()} linkOnly />);
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByRole("button", { name: /insert source/ })).toBeNull();
    expect(screen.queryByText(/Nothing written yet/)).toBeNull();
    // the link itself stays — its source is the point of the item
    expect(screen.getByText("Radar")).toBeVisible();
  });

  it("PautasPane drops the '0 sentences' signal on links items only", () => {
    wrap(<PautasPane doc={doc()} selectedRef={null} onSelect={() => {}} />);
    expect(screen.getByText("1 source")).toBeVisible();
    expect(screen.getByText("0 sentences · 2 sources")).toBeVisible();
    expect(screen.queryByText(/0 sentences · 1 source/)).toBeNull();
  });
});

// KLOX-96: writing moved into the document — the rail is supporting
// material. Clicks on facts and source excerpts feed the pauta's inline
// composer through composeFor instead of a side-panel box.
describe("supporting rail (KLOX-96)", () => {
  const dossierData = {
    dossier: {
      ref: 7,
      summary: "",
      facts: [
        { text: "Pix hit 4 billion transactions", ref: 1 },
        { text: "The bank denied the incident", ref: 2 },
      ],
      quote: { text: "", attribution: "" },
      // the deterministic pipeline parks the triage rank note here —
      // jargon, not editorial guidance (audit P0-3)
      interpretation: ["Nota de relevância: 6.9/10.", ""],
      unknowns: [],
      sources: [],
    },
  };

  function seededPane(acts: Partial<DocActions> = {}) {
    const qc = wrap(<EvidencePane item={item()} />, { ...writableActions, ...acts });
    qc.setQueryData(["dossier", 70], dossierData);
    return qc;
  }

  it("a fact click opens the item's in-document composer prefilled with its citation", async () => {
    const composeFor = vi.fn();
    seededPane({ composeFor });
    const fact = await screen.findByRole("button", { name: /Pix hit 4 billion transactions \[1\]/ });
    fireEvent.click(fact);
    expect(composeFor).toHaveBeenCalledWith(7, { text: "Pix hit 4 billion transactions", cite: 1, seq: 1 });
  });

  it("the 'Nota de relevância' rank note never renders as interpretation", async () => {
    seededPane();
    await screen.findByRole("button", { name: /Pix hit 4 billion transactions \[1\]/ });
    expect(screen.queryByText(/Nota de relevância/)).toBeNull();
    expect(screen.queryByText(/6\.9\/10/)).toBeNull();
    expect(screen.queryByText("Interpretation")).toBeNull();
  });

  it("a source insert click feeds the inline composer instead of a side box", () => {
    const composeFor = vi.fn();
    seededPane({ composeFor });
    fireEvent.click(screen.getByRole("button", { name: /insert source \[1\] excerpt/ }));
    expect(composeFor).toHaveBeenCalledWith(
      7,
      expect.objectContaining({ cite: 1 }),
    );
    // no writing surface lives in the rail anymore
    expect(screen.queryByRole("textbox")).toBeNull();
  });

  it("walks pautas one at a time with prev/next", () => {
    const onGo = vi.fn();
    wrap(<EvidencePane item={item()} pos={{ index: 0, total: 3 }} onGo={onGo} />);
    expect(screen.getByText("Story 1 of 3")).toBeVisible();
    expect(screen.getByRole("button", { name: "Previous story" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Next story" }));
    expect(onGo).toHaveBeenCalledWith(1);
  });
});

// KLOX-100: the badge reads only the items list — an unloaded or stale
// list must show no badge, never a fallback "to decide" that flips
// once the real status lands.
describe("PautasPane status badges (KLOX-100)", () => {
  const ii = (over: Partial<IssueItem> = {}): IssueItem => ({
    id: 70, issue_id: 1, story_id: 7, section: "Quick links",
    score: 1, reason: "", status: "approved", origin: "", title: "A cool link",
    outlets: [], ...over,
  });

  it("shows the item's real status when the list is loaded", () => {
    wrap(<PautasPane doc={doc()} items={[ii(), ii({ id: 80, status: "pending" })]} selectedRef={null} onSelect={() => {}} />);
    expect(screen.getByText("approved")).toBeVisible();
    expect(screen.getByText("to decide")).toBeVisible();
  });

  it("renders no badge while the items list is not in", () => {
    wrap(<PautasPane doc={doc()} selectedRef={null} onSelect={() => {}} />);
    expect(screen.queryByText("approved")).toBeNull();
    expect(screen.queryByText("to decide")).toBeNull();
  });

  it("renders no badge for an item missing from a stale list", () => {
    // a swap creates a new issue_item the cached list lacks — the old
    // fallback painted that as "to decide" until the refetch landed
    wrap(<PautasPane doc={doc()} items={[ii({ id: 999 })]} selectedRef={null} onSelect={() => {}} />);
    expect(screen.queryByText("approved")).toBeNull();
    expect(screen.queryByText("to decide")).toBeNull();
  });
});
