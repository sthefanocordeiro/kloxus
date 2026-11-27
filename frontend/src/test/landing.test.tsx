import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api, ApiRequestError, type Publication } from "../api";
import { LangProvider } from "../i18n";
import { Landing } from "../pages/Landing";
import { PrivacyPage, TermsPage } from "../pages/Legal";

vi.mock("../api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../api")>();
  return {
    ...mod,
    api: {
      ...mod.api,
      workspace: vi.fn(),
      publications: vi.fn(),
    },
  };
});

const pub: Publication = {
  id: 1,
  slug: "futuro-proximo",
  name: "Futuro Próximo",
  language: "pt",
  topic: "t",
  audience: "a",
  editorial_line: "e",
  tone: "t",
  frequency: "weekly",
  ai_disclosure: "",
  sections: [],
  paywall: { enabled: false, paid_sections: [], price: "" },
};

function renderApp() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <LangProvider>
        <MemoryRouter initialEntries={["/"]}>
          <Routes>
            <Route path="/" element={<Landing />} />
            <Route path="/start" element={<div>onboarding screen</div>} />
            <Route path="/publications" element={<div>publications screen</div>} />
            <Route path="/entrar" element={<div>sign-in screen</div>} />
            <Route path="/termos" element={<TermsPage />} />
            <Route path="/privacidade" element={<PrivacyPage />} />
          </Routes>
        </MemoryRouter>
      </LangProvider>
    </QueryClientProvider>,
  );
}

describe("Landing", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    vi.mocked(api.workspace).mockResolvedValue({ id: "w1", label: "visitor w1…", mode: "demo", hosted: true, llm_mode: "auto" });
    vi.mocked(api.publications).mockResolvedValue([]);
  });

  it("creates the anonymous workspace and lands on onboarding", async () => {
    renderApp();
    const cta = await screen.findAllByRole("button", { name: /create your Radar/i });
    fireEvent.click(cta[0]);
    await waitFor(() => expect(api.workspace).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("onboarding screen")).toBeInTheDocument();
  });

  it("switches the page between EN and pt-BR", async () => {
    renderApp();
    expect(
      await screen.findByRole("heading", { name: /the research is ready/i }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "PT" }));
    expect(
      await screen.findByRole("heading", { name: /a pauta pronta/i }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /criar meu Radar/i }).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("button", { name: "EN" }));
    expect(
      await screen.findByRole("heading", { name: /the research is ready/i }),
    ).toBeInTheDocument();
  });

  it("offers the dashboard to a returning visitor and sign-in to a new one", async () => {
    renderApp();
    expect((await screen.findAllByRole("button", { name: /sign in/i })).length).toBeGreaterThan(0);
    expect(screen.queryByRole("link", { name: /dashboard/i })).not.toBeInTheDocument();

    // KLOX-194: the "Ir para o painel" detection only probes browsers
    // that already hold a session (the marker survives reloads)
    localStorage.setItem("kloxus.session", "1");
    vi.mocked(api.publications).mockResolvedValue([pub]);
    renderApp();
    expect(await screen.findByRole("link", { name: /dashboard/i })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("link", { name: /dashboard/i }));
    expect(await screen.findByText("publications screen")).toBeInTheDocument();
  });

  // KLOX-272: "Already have an account" is account recovery, not the
  // workspace-create path that used to strand returning users in an
  // empty workspace.
  it("opens the sign-in recovery page without creating a workspace", async () => {
    renderApp();
    fireEvent.click(await screen.findByRole("button", { name: /already have an account\? sign in/i }));
    expect(await screen.findByText("sign-in screen")).toBeInTheDocument();
    expect(api.workspace).not.toHaveBeenCalled();
  });

  // KLOX-194: a fresh visit fires no session-touching call — the
  // landing stays open without minting a workspace. The publications
  // probe resumes once the browser holds a session.
  it("fires no API call on a fresh visit and probes only with a session", async () => {
    renderApp();
    await screen.findAllByRole("button", { name: /create your Radar/i });
    await waitFor(() => expect(api.publications).not.toHaveBeenCalled());

    localStorage.setItem("kloxus.session", "1");
    renderApp();
    await waitFor(() => expect(api.publications).toHaveBeenCalledTimes(1));
  });

  // KLOX-194: a 429 on "Criar meu Radar" shows the friendly card with
  // "Tentar de novo" — and the retry re-fires the same entry.
  it("shows the friendly state on 429 and retries on click", async () => {
    renderApp();
    const error = new ApiRequestError(429, { code: "rate_limited", message: "slow down" });
    vi.mocked(api.workspace)
      .mockRejectedValueOnce(error)
      .mockResolvedValue({ id: "w1", label: "visitor w1…", mode: "demo", hosted: true, llm_mode: "auto" });

    fireEvent.click((await screen.findAllByRole("button", { name: /create your Radar/i }))[0]);
    expect(await screen.findByText(/a lot of people are signing in at once/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /try again/i }));
    await waitFor(() => expect(api.workspace).toHaveBeenCalledTimes(2));
    expect(await screen.findByText("onboarding screen")).toBeInTheDocument();
  });

  // KLOX-161: the landing no longer captures an email — the hero CTA
  // goes straight to onboarding, where the email is confirmed by magic link.
  it("has no email form on the landing and explains where the email goes", async () => {
    renderApp();
    await screen.findAllByRole("button", { name: /create your Radar/i });
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(screen.getByText(/confirm your email by magic link/i)).toBeInTheDocument();
  });

  // KLOX-69: the hamburger covers the section links, entry and CTA at
  // 390 px; the PT/EN switch lives in the header on every width.
  it("folds nav, entry and CTA into the header menu on mobile", async () => {
    renderApp();
    const menuBtn = await screen.findByRole("button", { name: /menu/i });
    expect(menuBtn).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(menuBtn);
    expect(menuBtn).toHaveAttribute("aria-expanded", "true");
    const menu = document.getElementById("site-menu");
    expect(menu).not.toBeNull();
    expect(within(menu!).getByRole("link", { name: /how it works/i })).toBeInTheDocument();
    expect(within(menu!).getByRole("link", { name: /questions/i })).toBeInTheDocument();
    expect(within(menu!).getByRole("button", { name: /sign in/i })).toBeInTheDocument();
    expect(
      within(menu!).getByRole("button", { name: /create your Radar/i }),
    ).toBeInTheDocument();
  });

  // KLOX-159 / P0-3: the menu CTA closes the menu (unmounting itself) —
  // the navigation to onboarding must still happen.
  it("opens onboarding from the mobile menu's Create your Radar", async () => {
    renderApp();
    fireEvent.click(await screen.findByRole("button", { name: /menu/i }));
    const menu = document.getElementById("site-menu")!;
    fireEvent.click(within(menu).getByRole("button", { name: /create your Radar/i }));
    await waitFor(() => expect(api.workspace).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("onboarding screen")).toBeInTheDocument();
  });

  // KLOX-70: footer legal links resolve to real pages, in the design
  // system, with no dead ends (the contact mailto is the only link out).
  it("links Terms and Privacy to real pages and renders them", async () => {
    renderApp();
    const terms = await screen.findByRole("link", { name: "Terms" });
    const privacy = screen.getByRole("link", { name: "Privacy" });
    expect(terms).toHaveAttribute("href", "/termos");
    expect(privacy).toHaveAttribute("href", "/privacidade");

    fireEvent.click(terms);
    expect(
      await screen.findByRole("heading", { name: /terms of use/i }),
    ).toBeInTheDocument();
    // header/footer chrome carries over — lang switch still works
    fireEvent.click(screen.getByRole("button", { name: "PT" }));
    expect(
      await screen.findByRole("heading", { name: /termos de uso/i }),
    ).toBeInTheDocument();
  });
});
