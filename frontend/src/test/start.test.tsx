// KLOX-123 (pivot Block 2): onboarding from zero — one sentence creates
// the radar, the calibration deck takes Sim/Não by button or ← →, two
// "Não" on a shared topic ask once to hide it, and sources can only be
// unchecked.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { api, type PautaCard, type Publication, type SourceRow, type Workspace } from "../api";
import { LangProvider } from "../i18n";
import { Start } from "../pages/Start";

afterEach(cleanup);

vi.mock("../api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../api")>();
  return {
    ...mod,
    api: {
      ...mod.api,
      publications: vi.fn(),
      workspace: vi.fn(),
      claim: vi.fn(),
      createRadar: vi.fn(),
      calibration: vi.fn(),
      calibrationAnswer: vi.fn(),
      skipTopic: vi.fn(),
      sourceDelete: vi.fn(),
    },
  };
});

const ws = (claimed: boolean) =>
  ({ id: "w1", label: "w1", mode: "demo", hosted: true, llm_mode: "off", claimed } as Workspace);
const pub = { id: 7, name: "Radar: fintech", topic: "fintech" } as Publication;
const src = (id: number, name: string) => ({ id, name } as SourceRow);
const card = (id: number, topics: string[]): PautaCard => ({
  story_id: id, title: `Story ${id}`, outlet: "Exame", outlets: 1,
  summary: `Resumo ${id}`, topics, url: "https://x.test/" + id, age_hours: 3,
});

function renderStart() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <LangProvider>
        <MemoryRouter initialEntries={["/start"]}>
          <Routes>
            <Route path="/start" element={<Start />} />
            <Route path="/publications/:id/today" element={<div>today screen</div>} />
          </Routes>
        </MemoryRouter>
      </LangProvider>
    </QueryClientProvider>,
  );
}

describe("onboarding from zero (KLOX-123)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.publications).mockResolvedValue([]);
    vi.mocked(api.workspace).mockResolvedValue(ws(true));
    vi.mocked(api.createRadar).mockResolvedValue({ publication: pub, sources: [src(1, "Exame"), src(2, "Tecnoblog")] });
    vi.mocked(api.calibration).mockResolvedValue([
      card(1, ["games"]), card(2, ["games"]), card(3, ["fintech"]),
    ]);
    vi.mocked(api.calibrationAnswer).mockResolvedValue({ ok: true });
    vi.mocked(api.skipTopic).mockResolvedValue({ publication: pub });
    vi.mocked(api.sourceDelete).mockResolvedValue({ ok: true });
  });

  it("redirects an account that already has a radar", async () => {
    vi.mocked(api.publications).mockResolvedValue([pub]);
    renderStart();
    expect(await screen.findByText("today screen")).toBeVisible();
  });

  // KLOX-161: the email, confirmed by magic link, comes before the radar
  it("asks for the email first and shows the topic form once the claim lands", async () => {
    vi.mocked(api.workspace).mockResolvedValue(ws(false));
    vi.mocked(api.claim).mockResolvedValue({ sent: true, email: "ana@exemplo.com", claim_url: "/claim?t=x" });
    renderStart();
    expect(await screen.findByTestId("email-gate")).toBeVisible();
    expect(screen.queryByLabelText("Your topic")).toBeNull();
    fireEvent.change(screen.getByLabelText("you@example.com"), { target: { value: "ana@exemplo.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Save with your email" }));
    await waitFor(() => expect(api.claim).toHaveBeenCalledWith("ana@exemplo.com", "en"));

    // the link is opened (here: elsewhere) — the next poll sees the claim
    vi.mocked(api.workspace).mockResolvedValue(ws(true));
    expect(await screen.findByLabelText("Your topic", {}, { timeout: 6000 })).toBeVisible();
    expect(screen.queryByTestId("email-gate")).toBeNull();
  }, 15000);

  it("needs a topic or a niche before continuing", async () => {
    renderStart();
    const next = await screen.findByRole("button", { name: "Continue" });
    expect(next).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Fintech" }));
    expect(next).toBeEnabled();
  });

  it("creates the radar, calibrates with ←/→, offers to hide a twice-refused topic, then unchecks a source", async () => {
    renderStart();
    fireEvent.change(await screen.findByLabelText("Your topic"), { target: { value: "fintech e Pix" } });
    fireEvent.click(screen.getByRole("button", { name: "Fintech" }));
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    await waitFor(() => expect(api.createRadar).toHaveBeenCalledWith("fintech e Pix", ["fintech"]));

    // card 1 → "No" by key
    expect(await screen.findByText("Story 1")).toBeVisible();
    fireEvent.keyDown(window, { key: "ArrowLeft" });
    await waitFor(() => expect(api.calibrationAnswer).toHaveBeenCalledWith(7, 1, "no"));
    expect(screen.queryByRole("alertdialog")).toBeNull();

    // card 2 → second "No" on "games" asks to hide it
    expect(await screen.findByText("Story 2")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /No/ }));
    const ask = await screen.findByRole("alertdialog");
    expect(ask).toHaveTextContent("Hide games?");
    fireEvent.click(screen.getByRole("button", { name: "Hide" }));
    await waitFor(() => expect(api.skipTopic).toHaveBeenCalledWith(7, "games"));

    // card 3 → "Yes" by key ends the deck and opens sources
    expect(await screen.findByText("Story 3")).toBeVisible();
    fireEvent.keyDown(window, { key: "ArrowRight" });
    await waitFor(() => expect(api.calibrationAnswer).toHaveBeenCalledWith(7, 3, "yes"));
    expect(await screen.findByRole("heading", { name: "Sources" })).toBeVisible();

    const exame = screen.getByLabelText("Exame");
    expect(exame).toBeChecked();
    fireEvent.click(exame);
    await waitFor(() => expect(api.sourceDelete).toHaveBeenCalledWith(7, 1));
    await waitFor(() => expect(screen.getByLabelText("Exame")).toBeDisabled());

    fireEvent.click(screen.getByRole("button", { name: "See today's pauta" }));
    expect(await screen.findByText("today screen")).toBeVisible();
  });

  it("lets the visitor skip calibration", async () => {
    renderStart();
    fireEvent.click(await screen.findByRole("button", { name: "Startups" }));
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    await screen.findByText("Story 1");
    fireEvent.click(screen.getByRole("button", { name: "Skip" }));
    expect(await screen.findByRole("heading", { name: "Sources" })).toBeVisible();
    expect(api.calibrationAnswer).not.toHaveBeenCalled();
  });
});
