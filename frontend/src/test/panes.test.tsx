// KLOX-65/D2: a pauta inside a links-format section is link-only — the
// link is the source, so nothing in the UI may press the author to
// write for it. These tests pin the no-pressure contract on the two
// panes that surface per-item writing affordances.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { DocItem, IssueDocument } from "../api";
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
  readOnly: false,
};

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <DocActionsCtx.Provider value={writableActions}>{ui}</DocActionsCtx.Provider>
    </QueryClientProvider>,
  );
}

describe("link-only pautas (KLOX-65/D2)", () => {
  it("EvidencePane shows no compose box or insert buttons for a links item", () => {
    wrap(<EvidencePane item={item()} linkOnly />);
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByRole("button", { name: /insert source/ })).toBeNull();
    expect(screen.queryByText(/Nothing written yet/)).toBeNull();
    // the link itself stays — its source is the point of the item
    expect(screen.getByText("Radar")).toBeVisible();
  });

  it("EvidencePane still offers the compose box for a writable item", () => {
    wrap(<EvidencePane item={item()} />);
    expect(screen.getByRole("textbox")).toBeVisible();
    expect(screen.getByRole("button", { name: /insert source \[1\] excerpt/ })).toBeVisible();
  });

  it("PautasPane drops the '0 sentences' signal on links items only", () => {
    wrap(<PautasPane doc={doc()} selectedRef={null} onSelect={() => {}} />);
    expect(screen.getByText("1 source")).toBeVisible();
    expect(screen.getByText("0 sentences · 2 sources")).toBeVisible();
    expect(screen.queryByText(/0 sentences · 1 source/)).toBeNull();
  });
});
