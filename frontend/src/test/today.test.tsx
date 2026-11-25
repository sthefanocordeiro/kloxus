// KLOX-123 slice 2: Hoje lists the day's pauta with Sim/Não and
// Escrever, and the pieces in progress with their status.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { api, ApiRequestError, type PautaCard, type TodayData } from "../api";
import { LangProvider } from "../i18n";
import { Today } from "../pages/Today";

afterEach(cleanup);

vi.mock("../api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../api")>();
  return {
    ...mod,
    api: {
      ...mod.api,
      today: vi.fn(),
      pautaCreate: vi.fn(),
      calibrationAnswer: vi.fn(),
      document: vi.fn(),
      blockInsert: vi.fn(),
    },
  };
});

const card = (id: number): PautaCard => ({
  story_id: id, title: `Story ${id}`, outlet: "Exame", outlets: 2,
  summary: `Resumo ${id}`, topics: [], url: "https://x.test/" + id, age_hours: 5,
});
const data = (over: Partial<TodayData> = {}): TodayData => ({
  cards: [card(1), card(2)], pieces: [], open_piece: 0, window_hours: 48, ...over,
});

function renderToday() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <LangProvider>
        <MemoryRouter initialEntries={["/publications/7/today"]}>
          <Routes>
            <Route path="/publications/:id/today" element={<Today />} />
            <Route path="/pauta/:id" element={<div>pauta screen</div>} />
          </Routes>
        </MemoryRouter>
      </LangProvider>
    </QueryClientProvider>,
  );
}

describe("Hoje (KLOX-123)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.today).mockResolvedValue(data());
    vi.mocked(api.calibrationAnswer).mockResolvedValue({ ok: true });
  });

  it("ranks cards, trains on Não (card leaves) and Sim (card stays marked)", async () => {
    renderToday();
    expect(await screen.findByText("Story 1")).toBeVisible();
    const first = screen.getAllByTestId("today-card")[0];
    fireEvent.click(first.querySelectorAll("button")[1]); // Não
    await waitFor(() => expect(api.calibrationAnswer).toHaveBeenCalledWith("7", 1, "no"));
    await waitFor(() => expect(screen.queryByText("Story 1")).toBeNull());
    fireEvent.click(screen.getByRole("button", { name: "Yes" }));
    await waitFor(() => expect(api.calibrationAnswer).toHaveBeenCalledWith("7", 2, "yes"));
    expect(await screen.findByRole("button", { name: /✓ Yes/ })).toBeDisabled();
  });

  it("ticks stories in order, shows the numbering and builds the pauta", async () => {
    vi.mocked(api.pautaCreate).mockResolvedValue({ issue_id: 55 });
    renderToday();
    await screen.findByText("Story 1");
    expect(screen.queryByRole("region", { name: "Build pauta →" })).toBeNull();
    fireEvent.click(screen.getByRole("checkbox", { name: /Story 2/ }));
    fireEvent.click(screen.getByRole("checkbox", { name: /Story 1/ }));
    expect(screen.getByText("2 stories selected")).toBeVisible();
    expect(screen.getAllByTestId("pick-order").map((n) => n.textContent)).toEqual(["2", "1"]);
    fireEvent.click(screen.getByRole("button", { name: "Build pauta →" }));
    expect(await screen.findByText("pauta screen")).toBeVisible();
    expect(api.pautaCreate).toHaveBeenCalledWith("7", [2, 1]);
  });

  // KLOX-162: "Write" on a card marks it in this choose step
  it("marks the card from its Write button and builds the pauta", async () => {
    vi.mocked(api.pautaCreate).mockResolvedValue({ issue_id: 55 });
    renderToday();
    await screen.findByText("Story 1");
    fireEvent.click(screen.getByRole("button", { name: "Write: Story 1" }));
    expect(screen.getByText("1 story selected")).toBeVisible();
    expect(screen.getByRole("button", { name: "Write: Story 1" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Build pauta →" }));
    expect(await screen.findByText("pauta screen")).toBeVisible();
    expect(api.pautaCreate).toHaveBeenCalledWith("7", [1]);
  });

  // KLOX-202: pautaCreate takes ~9 s — the button narrates short
  // steps instead of a bare "Building…".
  it("narrates the build in short steps while it runs", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      vi.mocked(api.pautaCreate).mockImplementation(() => new Promise(() => {}));
      renderToday();
      await screen.findByText("Story 1");
      fireEvent.click(screen.getByRole("checkbox", { name: /Story 1/ }));
      fireEvent.click(screen.getByRole("checkbox", { name: /Story 2/ }));
      fireEvent.click(screen.getByRole("button", { name: "Build pauta →" }));
      expect(await screen.findByText("Putting the 2 stories together…")).toBeVisible();
      await act(async () => { vi.advanceTimersByTime(2600); });
      expect(await screen.findByText("Opening the editor…")).toBeVisible();
    } finally {
      vi.useRealTimers();
    }
  });

  it("lists pieces in progress with their status", async () => {
    vi.mocked(api.today).mockResolvedValue(data({
      pieces: [{ id: 3, number: 2, status: "reviewed" }, { id: 2, number: 1, status: "draft" }],
    }));
    renderToday();
    expect(await screen.findByText("Pauta 2")).toBeVisible();
    expect(screen.getByText("Ready")).toBeVisible();
    expect(screen.getByText("Draft")).toBeVisible();
  });

  // KLOX-184: an exported piece reads "Exportada" with its content
  // title, the day and the formats that left.
  it("lists pieces with title, date and exported formats", async () => {
    vi.mocked(api.today).mockResolvedValue(data({
      pieces: [{
        id: 4, number: 3, status: "exported",
        title: "Fim da escala 6×1", created_at: "2026-11-24 10:00:00",
        exported_formats: ["x"],
      }],
    }));
    renderToday();
    expect(await screen.findByText("Fim da escala 6×1")).toBeVisible();
    expect(screen.getByText("Exported")).toBeVisible();
    expect(screen.getByText("X")).toBeVisible();
  });

  // KLOX-197: a rebuilt deck makes pautaCreate 404 — picks the new
  // deck no longer has must be pruned and the user told, not looped
  it("prunes gone picks and explains when the deck changed (404)", async () => {
    vi.mocked(api.today)
      .mockResolvedValueOnce(data({ cards: [card(1), card(2)] }))
      .mockResolvedValue(data({ cards: [card(1), card(3)] }));
    vi.mocked(api.pautaCreate)
      .mockRejectedValueOnce(new ApiRequestError(404, { code: "pauta", message: "story not found" }))
      .mockResolvedValue({ issue_id: 55 });
    renderToday();
    await screen.findByText("Story 1");
    fireEvent.click(screen.getByRole("checkbox", { name: /Story 1/ }));
    fireEvent.click(screen.getByRole("checkbox", { name: /Story 2/ }));
    fireEvent.click(screen.getByRole("button", { name: "Build pauta →" }));
    expect(await screen.findByText(/story list changed/)).toBeVisible();
    // story 2 left the deck: only story 1 stays picked
    await waitFor(() => expect(screen.getByText("1 story selected")).toBeVisible());
    expect(await screen.findByText("Story 3")).toBeVisible();
    // the retry sends only live ids and goes through
    fireEvent.click(screen.getByRole("button", { name: "Build pauta →" }));
    expect(await screen.findByText("pauta screen")).toBeVisible();
    expect(api.pautaCreate).toHaveBeenLastCalledWith("7", [1]);
  });

  it("explains an empty pauta", async () => {
    vi.mocked(api.today).mockResolvedValue(data({ cards: [] }));
    renderToday();
    expect(await screen.findByText(/No new stories/)).toBeVisible();
    expect(screen.getByText(/No pautas yet/)).toBeVisible();
  });
});
