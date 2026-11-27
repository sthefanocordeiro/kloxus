import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, type PautaState } from "../api";
import { LangProvider } from "../i18n";
import { PautaPage, PautaExport } from "../pages/PautaPage";

vi.mock("../api", async (orig) => {
  const real = await orig<typeof import("../api")>();
  return {
    ...real,
    api: {
      ...real.api,
      pauta: vi.fn(),
      pautaSave: vi.fn(),
      pautaFinish: vi.fn(),
      issue: vi.fn(),
      today: vi.fn(),
      renditions: vi.fn(),
    },
  };
});

const state = (): PautaState => ({
  issue_id: 5, number: 1, status: "drafted", opening: "", title: "Primeira",
  entries: [
    { story_id: 1, title: "Primeira", url: "https://a.test/1", outlet: "A", date: "2026-11-23T10:00:00Z", text: "" },
    { story_id: 2, title: "Segunda", url: "https://b.test/2", outlet: "B", date: "", text: "" },
  ],
});

function renderPage(qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return render(
    <QueryClientProvider client={qc}>
      <LangProvider>
        <MemoryRouter initialEntries={["/pauta/5"]}>
          <Routes>
            <Route path="/pauta/:id" element={<PautaPage />} />
            <Route path="/pauta/:id/export" element={<div>export screen</div>} />
          </Routes>
        </MemoryRouter>
      </LangProvider>
    </QueryClientProvider>,
  );
}

function mockTitleServer(initial = state(), catalogEntries = initial.entries) {
  const server = initial;
  const catalog = catalogEntries.map((e) => ({ ...e }));
  const title = () =>
    server.entries[0]?.title ||
    server.opening.replace(/\r/g, "").split("\n", 2)[0].trim() ||
    server.title;
  vi.mocked(api.pauta).mockImplementation(async () => ({
    ...server,
    entries: server.entries.map((e) => ({ ...e })),
    title: title(),
  }));
  vi.mocked(api.pautaSave).mockImplementation(async (_id, body) => {
    server.opening = body.opening;
    server.entries = body.entries.flatMap((be) => {
      const cur = catalog.find((e) => e.story_id === be.story_id);
      return cur ? [{ ...cur, text: be.text }] : [];
    });
    return { saved: true };
  });
  vi.mocked(api.issue).mockImplementation(async () => ({
    id: 5, publication_id: 7, title: title(),
  } as never));
  return server;
}

describe("Escrever (KLOX-168)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.setItem("kloxus.lang", "en");
    vi.mocked(api.pauta).mockResolvedValue(state());
    vi.mocked(api.pautaSave).mockResolvedValue({ saved: true });
    vi.mocked(api.pautaFinish).mockResolvedValue({ ready: true, missing_text: 1 });
    vi.mocked(api.issue).mockResolvedValue({ id: 5, publication_id: 7 } as never);
  });

  it("links titles, saves free text, reorders and goes to export", async () => {
    renderPage();
    const link = await screen.findByRole("link", { name: "Primeira" });
    expect(link).toHaveAttribute("href", "https://a.test/1");
    expect(link).toHaveAttribute("target", "_blank");

    // KLOX-162: the comment space stays closed until "Write"
    expect(screen.queryByLabelText("Your text: Primeira")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Write: Primeira" }));
    fireEvent.change(screen.getByLabelText("Your text: Primeira"), { target: { value: "Minha nota" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Move story down" })[0]);
    await waitFor(() => expect(api.pautaSave).toHaveBeenCalled(), { timeout: 3000 });
    const body = vi.mocked(api.pautaSave).mock.calls.at(-1)![1];
    expect(body.entries).toEqual([
      { story_id: 2, text: "" },
      { story_id: 1, text: "Minha nota" },
    ]);

    fireEvent.click(screen.getByRole("button", { name: "Go to export" }));
    expect(await screen.findByText("export screen")).toBeVisible();
    expect(api.pautaFinish).toHaveBeenCalledWith(5);
  });

  it("focuses the comment box when Write opens it", async () => {
    renderPage();
    await screen.findByRole("link", { name: "Primeira" });
    fireEvent.click(screen.getByRole("button", { name: "Write: Primeira" }));
    expect(screen.getByLabelText("Your text: Primeira")).toHaveFocus();
  });

  // KLOX-196: leaving mid-save used to drop the last keystrokes
  it("saves on blur without waiting for the debounce", async () => {
    renderPage();
    await screen.findByRole("link", { name: "Primeira" });
    fireEvent.click(screen.getByRole("button", { name: "Write: Primeira" }));
    const ta = screen.getByLabelText("Your text: Primeira");
    fireEvent.change(ta, { target: { value: "Minha nota" } });
    fireEvent.blur(ta);
    await waitFor(() => expect(api.pautaSave).toHaveBeenCalled(), { timeout: 1000 });
    expect(vi.mocked(api.pautaSave).mock.calls.at(-1)![1].entries[0])
      .toEqual({ story_id: 1, text: "Minha nota" });
  });

  it("blocks export until the autosave finishes", async () => {
    let resolveSave: (value: { saved: boolean }) => void = () => {};
    vi.mocked(api.pautaSave).mockImplementationOnce(() => new Promise((resolve) => { resolveSave = resolve; }));
    renderPage();
    await screen.findByRole("link", { name: "Primeira" });
    fireEvent.change(screen.getByLabelText(/Opening/), { target: { value: "oi" } });
    await waitFor(() => expect(api.pautaSave).toHaveBeenCalled(), { timeout: 3000 });

    const exportButton = screen.getByRole("button", { name: "Go to export" });
    expect(await screen.findByText("Saving…")).toBeTruthy();
    expect(exportButton).toBeDisabled();
    fireEvent.click(exportButton);
    expect(api.pautaFinish).not.toHaveBeenCalled();

    resolveSave({ saved: true });
    expect(await screen.findByText("Saved")).toBeTruthy();
    expect(exportButton).not.toBeDisabled();
    fireEvent.click(exportButton);
    await waitFor(() => expect(api.pautaFinish).toHaveBeenCalledWith(5));
  });

  it("warns before unloading while a save is pending", async () => {
    renderPage();
    await screen.findByRole("link", { name: "Primeira" });
    fireEvent.change(screen.getByLabelText(/Opening/), { target: { value: "oi" } });
    const ev = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
  });

  it("keeps the text and shows an error when a save fails", async () => {
    vi.mocked(api.pautaSave).mockRejectedValueOnce(new Error("boom"));
    renderPage();
    await screen.findByRole("link", { name: "Primeira" });
    const ta = screen.getByLabelText(/Opening/);
    fireEvent.change(ta, { target: { value: "abertura" } });
    expect(await screen.findByText("Could not save — will retry on your next edit")).toBeTruthy();
    fireEvent.change(ta, { target: { value: "abertura 2" } });
    await waitFor(() => expect(api.pautaSave).toHaveBeenCalledTimes(2), { timeout: 3000 });
    expect(vi.mocked(api.pautaSave).mock.calls.at(-1)![1].opening).toBe("abertura 2");
  });

  it("removes a story from the pauta", async () => {
    renderPage();
    await screen.findByRole("link", { name: "Primeira" });
    fireEvent.click(screen.getByRole("button", { name: "Remove story: Primeira" }));
    expect(screen.queryByRole("link", { name: "Primeira" })).toBeNull();
    expect(screen.getByText("Story removed.")).toBeTruthy();
    await waitFor(() => expect(api.pautaSave).toHaveBeenCalled(), { timeout: 3000 });
  });

  it("revalidates the automatic name after removing and undoing the first story", async () => {
    mockTitleServer();
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderPage(qc);
    await screen.findByRole("button", { name: "Primeira" });
    fireEvent.click(screen.getByRole("button", { name: "Remove story: Primeira" }));

    await waitFor(() => expect(api.pautaSave).toHaveBeenCalled(), { timeout: 3000 });
    expect(await screen.findByRole("button", { name: "Segunda" })).toBeTruthy();
    await waitFor(() => expect(qc.getQueryData<{ title?: string }>(["issue", 5])?.title).toBe("Segunda"));

    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    expect(await screen.findByRole("button", { name: "Primeira" })).toBeTruthy();
    await waitFor(() => expect(qc.getQueryData<{ title?: string }>(["issue", 5])?.title).toBe("Primeira"));
  });

  it("revalidates the automatic name when the first story joins an empty pauta", async () => {
    const empty = { ...state(), opening: "Linha de abertura", title: "Linha de abertura", entries: [] };
    const added = { story_id: 3, title: "Terceira", url: "https://c.test/3", outlet: "C", date: "", text: "" };
    mockTitleServer(empty, [added]);
    vi.mocked(api.today).mockResolvedValue({
      cards: [{ story_id: 3, title: "Terceira", outlet: "C", outlets: 1, summary: "", topics: [], url: "https://c.test/3", age_hours: null }],
      pieces: [], open_piece: 5, window_hours: 48,
    });
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderPage(qc);
    await screen.findByRole("button", { name: "Linha de abertura" });

    fireEvent.click(screen.getByRole("button", { name: "+ Add story" }));
    fireEvent.click(await screen.findByRole("button", { name: "Add: Terceira" }));

    await waitFor(() => expect(api.pautaSave).toHaveBeenCalled(), { timeout: 3000 });
    expect(await screen.findByRole("button", { name: "Terceira" })).toBeTruthy();
    await waitFor(() => expect(qc.getQueryData<{ title?: string }>(["issue", 5])?.title).toBe("Terceira"));
  });

  it("restores a removed story with its text from the undo toast", async () => {
    renderPage();
    await screen.findByRole("link", { name: "Primeira" });
    fireEvent.click(screen.getByRole("button", { name: "Write: Primeira" }));
    const ta = screen.getByLabelText("Your text: Primeira");
    fireEvent.change(ta, { target: { value: "Minha nota" } });
    fireEvent.blur(ta);
    await waitFor(() => expect(api.pautaSave).toHaveBeenCalled(), { timeout: 1000 });

    fireEvent.click(screen.getByRole("button", { name: "Remove story: Primeira" }));
    expect(screen.queryByRole("link", { name: "Primeira" })).toBeNull();
    expect(screen.getByText("Story removed.")).toBeTruthy();
    await waitFor(() => {
      expect(vi.mocked(api.pautaSave).mock.calls.at(-1)![1].entries).toEqual([{ story_id: 2, text: "" }]);
    }, { timeout: 3000 });

    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    expect(await screen.findByRole("link", { name: "Primeira" })).toBeTruthy();
    expect(screen.getByLabelText("Your text: Primeira")).toHaveValue("Minha nota");
    await waitFor(() => {
      expect(vi.mocked(api.pautaSave).mock.calls.at(-1)![1].entries).toEqual([
        { story_id: 1, text: "Minha nota" },
        { story_id: 2, text: "" },
      ]);
    }, { timeout: 3000 });
  });

  // KLOX-199: the pauta is named by its first story everywhere —
  // clicking the name in the editor renames it; "" restores the
  // automatic name
  it("shows the pauta name and renames it on click", async () => {
    renderPage();
    // the editor header is the same name the list shows
    expect(await screen.findByRole("button", { name: "Primeira" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Primeira" }));
    const input = screen.getByLabelText("Brief name");
    fireEvent.change(input, { target: { value: "Meu resumo da semana" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(api.pautaSave).toHaveBeenCalled(), { timeout: 3000 });
    expect(vi.mocked(api.pautaSave).mock.calls.at(-1)![1].title).toBe("Meu resumo da semana");
    expect(screen.getByRole("button", { name: "Meu resumo da semana" })).toBeTruthy();
  });

  it("does not send a title when the name was never edited", async () => {
    renderPage();
    await screen.findByRole("link", { name: "Primeira" });
    fireEvent.change(screen.getByLabelText(/Opening/), { target: { value: "oi" } });
    await waitFor(() => expect(api.pautaSave).toHaveBeenCalled(), { timeout: 3000 });
    expect(vi.mocked(api.pautaSave).mock.calls.at(-1)![1].title).toBeUndefined();
  });

  // KLOX-233: editor → export → Briefs → reopen remounts the editor on a
  // stale cache — a written story came back empty and the next whole-
  // pauta save would have wiped its text on the server
  it("still shows saved text after navigating away and back", async () => {
    const saved = state();
    vi.mocked(api.pauta).mockImplementation(async () => JSON.parse(JSON.stringify(saved)) as PautaState);
    vi.mocked(api.pautaSave).mockImplementation(async (_id, body) => {
      saved.opening = body.opening;
      for (const e of body.entries) {
        const it = saved.entries.find((x) => x.story_id === e.story_id);
        if (it) it.text = e.text;
      }
      return { saved: true };
    });

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const first = renderPage(qc);
    await screen.findByRole("link", { name: "Primeira" });
    fireEvent.click(screen.getByRole("button", { name: "Write: Primeira" }));
    fireEvent.change(screen.getByLabelText("Your text: Primeira"), { target: { value: "Minha nota" } });
    await waitFor(() => expect(api.pautaSave).toHaveBeenCalled(), { timeout: 3000 });

    first.unmount();
    renderPage(qc);
    expect(await screen.findByLabelText("Your text: Primeira")).toHaveValue("Minha nota");
  });

  // KLOX-233: a reload (or a dismissed leave-warning) must not drop the
  // last keystrokes — pagehide flushes them with a keepalive save
  it("flushes pending text with keepalive on pagehide", async () => {
    renderPage();
    await screen.findByRole("link", { name: "Primeira" });
    fireEvent.change(screen.getByLabelText(/Opening/), { target: { value: "oi" } });
    window.dispatchEvent(new Event("pagehide"));
    await waitFor(() => expect(api.pautaSave).toHaveBeenCalled(), { timeout: 3000 });
    const last = vi.mocked(api.pautaSave).mock.calls.at(-1)!;
    expect(last[1].opening).toBe("oi");
    expect(last[2]).toBe(true);
  });
});

// KLOX-213: pautaFinish is a POST — as a queryFn StrictMode fired it
// twice and a failure left the step silently empty. It is a mutation
// fired once on mount now, with an error branch and a retry.
describe("Exportar finish mutation (KLOX-213)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.setItem("kloxus.lang", "en");
    vi.mocked(api.pauta).mockResolvedValue(state());
    vi.mocked(api.pautaFinish).mockResolvedValue({ ready: true, missing_text: 0 });
    vi.mocked(api.renditions).mockResolvedValue({ renditions: [], stories: 2 });
  });

  function renderExport(entry = "/pauta/5/export") {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <LangProvider>
          <MemoryRouter initialEntries={[entry]}>
            <Routes>
              <Route path="/pauta/:id/export" element={<PautaExport />} />
            </Routes>
          </MemoryRouter>
        </LangProvider>
      </QueryClientProvider>,
    );
  }

  it("fires finish once and opens the destinations", async () => {
    renderExport();
    await waitFor(() => expect(api.pautaFinish).toHaveBeenCalledTimes(1));
    expect(api.pautaFinish).toHaveBeenCalledWith(5);
    expect(await screen.findByText("Where is it going?")).toBeTruthy();
  });

  it("keeps the missing-text warning when a story really has no text", async () => {
    vi.mocked(api.pautaFinish).mockResolvedValue({ ready: true, missing_text: 1 });
    renderExport("/pauta/5/export?sem_texto=1");
    expect(await screen.findByText("1 story has no text. You can export anyway.")).toBeTruthy();
  });

  it("shows the finish error with a retry instead of a silent empty step", async () => {
    vi.mocked(api.pautaFinish).mockRejectedValueOnce(new Error("boom"));
    renderExport();
    expect(await screen.findByRole("alert")).toHaveTextContent("Something went wrong");
    expect(screen.queryByText("Where is it going?")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(api.pautaFinish).toHaveBeenCalledTimes(2));
    expect(await screen.findByText("Where is it going?")).toBeTruthy();
  });
});
