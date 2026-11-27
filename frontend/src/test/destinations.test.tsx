import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api, type Rendition } from "../api";
import { DestinationStep } from "../components/Destinations";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const mk = (id: number, format: Rendition["format"], over: Partial<Rendition> = {}): Rendition => ({
  id, issue_id: 1, format, body_text: "", body_html: "", body_md: "",
  created_at: "", updated_at: "", stale: false, ...over,
});

function setup(rs: Rendition[], stories = [{ headline: "Principal" }]) {
  vi.spyOn(api, "renditions").mockResolvedValue({ renditions: rs, stories: stories.length });
  const create = vi.spyOn(api, "renditionCreate").mockImplementation(async (_i, f) => mk(99, f));
  const save = vi.spyOn(api, "renditionSave").mockImplementation(async (_i, id, body) => mk(id, "linkedin", { body_text: body }));
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <DestinationStep issueID={1} stories={stories} />
    </QueryClientProvider>,
  );
  return { create, save };
}

describe("DestinationStep", () => {
  it("forks a rendition when a destination is ticked", async () => {
    const { create } = setup([]);
    const box = await screen.findByRole("checkbox", { name: /Markdown/ });
    fireEvent.click(box);
    await waitFor(() => expect(create).toHaveBeenCalledWith(1, "markdown"));
  });

  it("counts LinkedIn characters with line breaks and shows the see-more fold", async () => {
    const text = "Título\n\n" + "palavra ".repeat(60).trim();
    setup([mk(1, "linkedin", { body_text: text })]);
    const counter = await screen.findByTestId("counter");
    expect(counter.textContent).toContain(String(Array.from(text).length).replace(/\B(?=(\d{3})+(?!\d))/g, ","));
    expect(counter.textContent).toContain("3,000");
    // the fold mark follows the interface language — the test browser
    // is en-US, so it reads "… see more" (KLOX-232)
    expect(screen.getByTestId("fold-preview").textContent).toContain("see more");
  });

  it("suggests a main story when several stories meet a social format", async () => {
    setup([mk(1, "linkedin", { body_text: "oi" })], [{ headline: "A" }, { headline: "B" }]);
    expect((await screen.findByRole("note")).textContent).toContain("2 stories");
  });

  it("edits a rendition through the autosave without touching the others", async () => {
    const { save } = setup([mk(1, "linkedin", { body_text: "um" }), mk(2, "markdown", { body_md: "# md" })]);
    const ta = await screen.findByRole("textbox", { name: /LinkedIn/ });
    fireEvent.change(ta, { target: { value: "dois" } });
    await waitFor(() => expect(save).toHaveBeenCalledWith(1, 1, "dois"), { timeout: 2000 });
    expect(save).toHaveBeenCalledTimes(1);
  });

  // KLOX-196: a failed save used to drop the body and go back to idle
  it("keeps the edit queued and retries when a save fails", async () => {
    const { save } = setup([mk(1, "linkedin", { body_text: "um" })]);
    save.mockRejectedValueOnce(new Error("boom"));
    const ta = await screen.findByRole("textbox", { name: /LinkedIn/ });
    fireEvent.change(ta, { target: { value: "dois" } });
    expect(await screen.findByText("Couldn't save — retrying")).toBeTruthy();
    await waitFor(() => expect(save).toHaveBeenCalledTimes(2), { timeout: 4000 });
    expect(save).toHaveBeenLastCalledWith(1, 1, "dois");
    await screen.findByText("Saved");
  });

  it("saves on blur without waiting for the debounce", async () => {
    const { save } = setup([mk(1, "linkedin", { body_text: "um" })]);
    const ta = await screen.findByRole("textbox", { name: /LinkedIn/ });
    fireEvent.change(ta, { target: { value: "dois" } });
    fireEvent.blur(ta);
    await waitFor(() => expect(save).toHaveBeenCalledWith(1, 1, "dois"), { timeout: 1000 });
  });

  it("flushes the pending edit when the editor unmounts", async () => {
    const { save } = setup([mk(1, "linkedin", { body_text: "um" }), mk(2, "markdown", { body_md: "# md" })]);
    const ta = await screen.findByRole("textbox", { name: /LinkedIn/ });
    fireEvent.change(ta, { target: { value: "dois" } });
    fireEvent.click(screen.getByRole("tab", { name: /Markdown/ }));
    await waitFor(() => expect(save).toHaveBeenCalledWith(1, 1, "dois"), { timeout: 1000 });
  });

  // KLOX-214: unticking deletes at once — a toast carries the undo
  // (re-fork + write the old body back), never a confirm dialog.
  it("deletes an unticked destination at once and offers undo on a toast", async () => {
    const del = vi.spyOn(api, "renditionDelete").mockResolvedValue(undefined);
    const confirmSpy = vi.spyOn(window, "confirm");
    const { create, save } = setup([mk(1, "markdown", { body_md: "# md" })]);
    const box = await screen.findByRole("checkbox", { name: /Markdown/ });
    await waitFor(() => expect(box).toBeEnabled());

    fireEvent.click(box);
    await waitFor(() => expect(del).toHaveBeenCalledWith(1, 1));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(confirmSpy).not.toHaveBeenCalled();

    const toast = await screen.findByRole("status");
    expect(toast).toHaveTextContent("Version removed");
    fireEvent.click(within(toast).getByRole("button", { name: "Undo" }));
    await waitFor(() => expect(create).toHaveBeenCalledWith(1, "markdown"));
    await waitFor(() => expect(save).toHaveBeenCalledWith(1, 99, "# md"));
  });

  // KLOX-214: a thread suggestion is not "over the limit" — the global
  // counter says how many posts the text becomes, not a red number.
  it("reads a split X text as a thread, not an over-limit post", async () => {
    const para = "Uma frase razoavelmente longa para encher espaço no post. ".repeat(12).trim();
    setup([mk(1, "x", { body_text: `${para}\n\n${para}` })]);
    expect(await screen.findByTestId("thread-note")).toHaveTextContent(/thread of \d+ posts/);
    expect(screen.queryByTestId("counter")).toBeNull();
  });

  // KLOX-214: the many-stories note is dismissable.
  it("lets the author dismiss the many-stories note", async () => {
    setup([mk(1, "x", { body_text: "oi" })], [{ headline: "A" }, { headline: "B" }]);
    const note = await screen.findByRole("note");
    fireEvent.click(within(note).getByRole("button", { name: "Dismiss" }));
    await waitFor(() => expect(screen.queryByRole("note")).toBeNull());
  });

  it("offers a manual refresh when the base changed", async () => {
    setup([mk(1, "markdown", { body_md: "# md", stale: true })]);
    expect(await screen.findByRole("button", { name: "Update this version?" })).toBeTruthy();
  });

  // KLOX-213: a failed delete used to vanish behind a closing confirm
  // dialog. The instant-delete flow (KLOX-214) surfaces it on the
  // action error line — and offers no undo toast for a rendition
  // that still exists.
  it("shows the delete error when removing a destination fails", async () => {
    const del = vi.spyOn(api, "renditionDelete").mockRejectedValue(new Error("boom"));
    setup([mk(1, "markdown", { body_md: "# md" })]);
    const box = await screen.findByRole("checkbox", { name: /Markdown/ });
    await waitFor(() => expect(box).toBeEnabled());

    fireEvent.click(box);
    await waitFor(() => expect(del).toHaveBeenCalledWith(1, 1));
    expect(await screen.findByRole("alert")).toHaveTextContent("Something went wrong");
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.getByRole("checkbox", { name: /Markdown/ })).toBeChecked();
  });

  it("shows the refresh error inside its dialog when updating fails", async () => {
    const refresh = vi.spyOn(api, "renditionRefresh").mockRejectedValue(new Error("boom"));
    setup([mk(1, "markdown", { body_md: "# md", stale: true })]);
    fireEvent.click(await screen.findByRole("button", { name: "Update this version?" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Update" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Something went wrong");
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it("shows the create error under the destinations when forking fails", async () => {
    const { create } = setup([]);
    create.mockRejectedValueOnce(new Error("boom"));
    fireEvent.click(await screen.findByRole("checkbox", { name: /Markdown/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Something went wrong");
  });

  // KLOX-184: copying or downloading marks the piece "Exportada".
  it("marks the piece exported when the author copies", async () => {
    const mark = vi.spyOn(api, "markExported").mockResolvedValue({ exported: true, formats: ["linkedin"] });
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      configurable: true,
    });
    setup([mk(1, "linkedin", { body_text: "oi" })]);
    fireEvent.click(await screen.findByRole("button", { name: "Copy" }));
    await waitFor(() => expect(mark).toHaveBeenCalledWith(1, "linkedin"));
  });

  // KLOX-202: a refused clipboard write must not fail silently — warn
  // and leave the text selected so Ctrl+C works.
  it("warns and pre-selects the text when copy is refused", async () => {
    const mark = vi.spyOn(api, "markExported").mockResolvedValue({ exported: true, formats: [] });
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText: vi.fn().mockRejectedValue(new Error("denied")) },
      configurable: true,
    });
    setup([mk(1, "linkedin", { body_text: "oi" })]);
    fireEvent.click(await screen.findByRole("button", { name: "Copy" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/Couldn't copy/);
    const ta = screen.getByRole("textbox", { name: /LinkedIn/ }) as HTMLTextAreaElement;
    expect(ta.selectionStart).toBe(0);
    expect(ta.selectionEnd).toBe(2);
    expect(mark).not.toHaveBeenCalled();
  });
});
