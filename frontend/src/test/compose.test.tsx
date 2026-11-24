import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DocItem } from "../api";
import { ComposeBox, type ComposeInsert } from "../components/Compose";
import { DocActionsCtx, type DocActions } from "../editor/nodes";

afterEach(cleanup);

const item = (over: Partial<DocItem> = {}): DocItem => ({
  ref: 3,
  issue_item_id: 10,
  headline: "Story",
  sentences: [],
  sources: [
    { ref: 1, name: "Radar", url: "/a/1", item_id: 1, excerpt: "excerpt one", date: "" },
    { ref: 2, name: "Pulso", url: "/a/2", item_id: 2, excerpt: "excerpt two", date: "" },
  ],
  ...over,
});

function renderBox(actions: Partial<DocActions>, it: DocItem = item(), insert: ComposeInsert | null = null) {
  const full: DocActions = {
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
    ...actions,
  };
  return render(
    <DocActionsCtx.Provider value={full}>
      <ComposeBox item={it} insert={insert} />
    </DocActionsCtx.Provider>,
  );
}

describe("ComposeBox", () => {
  it("adds a fact with the chosen citations", async () => {
    const add = vi.fn(async () => null);
    renderBox({ addSentence: add });
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "The thing happened." } });
    fireEvent.click(screen.getByRole("button", { name: "[1] Radar" }));
    fireEvent.click(screen.getByRole("button", { name: "Add to brief" }));
    await waitFor(() => expect(add).toHaveBeenCalledWith(3, "The thing happened.", [1], "fact"));
    // the box clears for the next paragraph
    await waitFor(() => expect(screen.getByRole("textbox")).toHaveValue(""));
  });

  it("sends an uncited fact when no chip is picked — citecheck flags it", async () => {
    const add = vi.fn(async () => null);
    renderBox({ addSentence: add });
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Uncited claim." } });
    fireEvent.click(screen.getByRole("button", { name: "Add to brief" }));
    await waitFor(() => expect(add).toHaveBeenCalledWith(3, "Uncited claim.", [], "fact"));
  });

  it("a single-source pauta cites that source by default (KLOX-124)", async () => {
    const add = vi.fn(async () => null);
    renderBox({ addSentence: add }, item({ sources: [item().sources[0]] }));
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Typed freely." } });
    fireEvent.click(screen.getByRole("button", { name: "Add to brief" }));
    await waitFor(() => expect(add).toHaveBeenCalledWith(3, "Typed freely.", [1], "fact"));
  });

  it("stores an interpretation with no citations when 'my take' is on", async () => {
    const add = vi.fn(async () => null);
    renderBox({ addSentence: add });
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "My read of it." } });
    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "Add to brief" }));
    await waitFor(() => expect(add).toHaveBeenCalledWith(3, "My read of it.", [], "interpretation"));
  });

  it("prefills from an insert request: excerpt text plus its citation", async () => {
    renderBox({}, item(), { text: "excerpt one", cite: 1, seq: 1 });
    await waitFor(() => expect(screen.getByRole("textbox")).toHaveValue("excerpt one"));
    expect(screen.getByRole("button", { name: "[1] Radar" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "[2] Pulso" })).toHaveAttribute("aria-pressed", "false");
  });

  it("shows the refusal inline and keeps the draft", async () => {
    const add = vi.fn(async () => "That source number does not exist for this item");
    renderBox({ addSentence: add });
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Keep me." } });
    fireEvent.click(screen.getByRole("button", { name: "Add to brief" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("does not exist"));
    expect(screen.getByRole("textbox")).toHaveValue("Keep me.");
  });

  it("does not submit empty text", () => {
    const add = vi.fn(async () => null);
    renderBox({ addSentence: add });
    expect(screen.getByRole("button", { name: "Add to brief" })).toBeDisabled();
  });
});
