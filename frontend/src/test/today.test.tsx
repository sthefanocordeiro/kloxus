// KLOX-123 slice 2: Hoje lists the day's pauta with Sim/Não and
// Escrever, and the pieces in progress with their status.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { api, type PautaCard, type TodayData } from "../api";
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

  it("lists pieces in progress with their status", async () => {
    vi.mocked(api.today).mockResolvedValue(data({
      pieces: [{ id: 3, number: 2, status: "reviewed" }, { id: 2, number: 1, status: "draft" }],
    }));
    renderToday();
    expect(await screen.findByText("Pauta 2")).toBeVisible();
    expect(screen.getByText("Ready")).toBeVisible();
    expect(screen.getByText("Draft")).toBeVisible();
  });

  it("explains an empty pauta", async () => {
    vi.mocked(api.today).mockResolvedValue(data({ cards: [] }));
    renderToday();
    expect(await screen.findByText(/No new stories/)).toBeVisible();
    expect(screen.getByText(/No pautas yet/)).toBeVisible();
  });
});
