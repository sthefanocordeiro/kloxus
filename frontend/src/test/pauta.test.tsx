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

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
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
    await waitFor(() => expect(api.pautaSave).toHaveBeenCalled(), { timeout: 3000 });
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

  function renderExport() {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <LangProvider>
          <MemoryRouter initialEntries={["/pauta/5/export"]}>
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
