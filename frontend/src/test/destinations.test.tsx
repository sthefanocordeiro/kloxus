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

  it("counts LinkedIn characters with line breaks and shows the ver-mais fold", async () => {
    const text = "Título\n\n" + "palavra ".repeat(60).trim();
    setup([mk(1, "linkedin", { body_text: text })]);
    const counter = await screen.findByTestId("counter");
    expect(counter.textContent).toContain(String(Array.from(text).length).replace(/\B(?=(\d{3})+(?!\d))/g, ","));
    expect(counter.textContent).toContain("3,000");
    expect(screen.getByTestId("fold-preview").textContent).toContain("ver mais");
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

  it("confirms removal in-app instead of window.confirm", async () => {
    const del = vi.spyOn(api, "renditionDelete").mockResolvedValue(undefined);
    const confirmSpy = vi.spyOn(window, "confirm");
    setup([mk(1, "markdown", { body_md: "# md" })]);
    const box = await screen.findByRole("checkbox", { name: /Markdown/ });
    await waitFor(() => expect(box).toBeEnabled());

    fireEvent.click(box);
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/Remove this version and its edits/)).toBeTruthy();

    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(del).not.toHaveBeenCalled();
    expect(confirmSpy).not.toHaveBeenCalled();

    fireEvent.click(box);
    const again = await screen.findByRole("dialog");
    fireEvent.click(within(again).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(del).toHaveBeenCalledWith(1, 1));
    expect(confirmSpy).not.toHaveBeenCalled();
  });

  it("offers a manual refresh when the base changed", async () => {
    setup([mk(1, "markdown", { body_md: "# md", stale: true })]);
    expect(await screen.findByRole("button", { name: "Update this version?" })).toBeTruthy();
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
});
