// KLOX-123 slice 2: Hoje lists the day's pauta — clicking a card ticks
// it (KLOX-200), thumbs train the radar — and the pieces in progress.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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

  it("ranks cards, trains on 👎 (card leaves) and 👍 (card stays marked)", async () => {
    renderToday();
    expect(await screen.findByText("Story 1")).toBeVisible();
    const first = screen.getAllByTestId("today-card")[0];
    fireEvent.click(within(first).getByRole("button", { name: /Less of this/ }));
    await waitFor(() => expect(api.calibrationAnswer).toHaveBeenCalledWith("7", card(1), "no"));
    await waitFor(() => expect(screen.queryByText("Story 1")).toBeNull());
    // a thumb only trains the radar — it must not tick the card
    fireEvent.click(screen.getByRole("button", { name: /More of this: Story 2/ }));
    await waitFor(() => expect(api.calibrationAnswer).toHaveBeenCalledWith("7", card(2), "yes"));
    expect(screen.queryByText(/selected/)).toBeNull();
    const liked = screen.getByRole("button", { name: /More of this: Story 2/ });
    expect(liked).toBeDisabled();
    expect(liked).toHaveAttribute("aria-pressed", "true");
  });

  // KLOX-276: before choosing, the card carries an evident way to read
  // the source story; opening it must not accidentally tick the card.
  it("shows the source link without ticking the card", async () => {
    renderToday();
    await screen.findByText("Story 1");
    const first = screen.getAllByTestId("today-card")[0];
    const link = within(first).getByRole("link", { name: "Read story" });
    expect(link).toHaveAttribute("href", "https://x.test/1");
    expect(link).toHaveAttribute("target", "_blank");
    fireEvent.click(link);
    expect(screen.queryByText(/selected/)).toBeNull();
    expect(screen.getByRole("checkbox", { name: /Story 1/ })).not.toBeChecked();
  });

  it("ticks stories in order, shows the numbering and builds the pauta", async () => {
    vi.mocked(api.pautaCreate).mockResolvedValue({ issue_id: 55 });
    renderToday();
    await screen.findByText("Story 1");
    expect(screen.queryByRole("region", { name: "Build brief →" })).toBeNull();
    fireEvent.click(screen.getByRole("checkbox", { name: /Story 2/ }));
    fireEvent.click(screen.getByRole("checkbox", { name: /Story 1/ }));
    expect(screen.getByText("2 stories selected")).toBeVisible();
    expect(screen.getAllByTestId("pick-order").map((n) => n.textContent)).toEqual(["2", "1"]);
    fireEvent.click(screen.getByRole("button", { name: "Build brief →" }));
    expect(await screen.findByText("pauta screen")).toBeVisible();
    expect(api.pautaCreate).toHaveBeenCalledWith("7", [2, 1]);
  });

  // KLOX-200: clicking anywhere on the card ticks/unticks it
  it("ticks and unticks a card by clicking it, then builds the pauta", async () => {
    vi.mocked(api.pautaCreate).mockResolvedValue({ issue_id: 55 });
    renderToday();
    await screen.findByText("Story 1");
    const first = screen.getAllByTestId("today-card")[0];
    fireEvent.click(first);
    expect(screen.getByText("1 story selected")).toBeVisible();
    expect(screen.getByRole("checkbox", { name: /Story 1/ })).toBeChecked();
    // clicking again unticks and hides the action bar
    fireEvent.click(first);
    expect(screen.queryByRole("region", { name: "Build brief →" })).toBeNull();
    expect(screen.getByRole("checkbox", { name: /Story 1/ })).not.toBeChecked();
    // its own checkbox still works without double-toggling
    fireEvent.click(screen.getByRole("checkbox", { name: /Story 1/ }));
    expect(screen.getByText("1 story selected")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Build brief →" }));
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
      fireEvent.click(screen.getByRole("button", { name: "Build brief →" }));
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
    expect(await screen.findByText("Brief 2")).toBeVisible();
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
    fireEvent.click(screen.getByRole("button", { name: "Build brief →" }));
    expect(await screen.findByText(/story list changed/)).toBeVisible();
    // story 2 left the deck: only story 1 stays picked
    await waitFor(() => expect(screen.getByText("1 story selected")).toBeVisible());
    expect(await screen.findByText("Story 3")).toBeVisible();
    // the retry sends only live ids and goes through
    fireEvent.click(screen.getByRole("button", { name: "Build brief →" }));
    expect(await screen.findByText("pauta screen")).toBeVisible();
    expect(api.pautaCreate).toHaveBeenLastCalledWith("7", [1]);
  });

  it("explains an empty pauta", async () => {
    vi.mocked(api.today).mockResolvedValue(data({ cards: [] }));
    renderToday();
    expect(await screen.findByText(/No new stories/)).toBeVisible();
    expect(screen.getByText(/No briefs yet/)).toBeVisible();
  });
});
