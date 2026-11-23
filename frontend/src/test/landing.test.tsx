import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiRequestError, api, type Publication } from "../api";
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
      waitlist: vi.fn(),
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

    vi.mocked(api.publications).mockResolvedValue([pub]);
    renderApp();
    expect(await screen.findByRole("link", { name: /dashboard/i })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("link", { name: /dashboard/i }));
    expect(await screen.findByText("publications screen")).toBeInTheDocument();
  });

  // KLOX-127: the hero captures a waitlist email; the app stays reachable.
  it("joins the waitlist from the hero and shows the success state", async () => {
    vi.mocked(api.waitlist).mockResolvedValue({ ok: true });
    renderApp();
    fireEvent.click(await screen.findByRole("button", { name: "PT" }));
    const input = await screen.findByLabelText("Seu melhor e-mail");
    fireEvent.change(input, { target: { value: "nao-e-email" } });
    fireEvent.click(screen.getByRole("button", { name: "Avise-me quando lançar" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/não parece certo/i);
    expect(api.waitlist).not.toHaveBeenCalled();

    fireEvent.change(input, { target: { value: " ana@exemplo.com " } });
    fireEvent.click(screen.getByRole("button", { name: "Avise-me quando lançar" }));
    expect(await screen.findByText("Você está na lista.")).toBeInTheDocument();
    expect(api.waitlist).toHaveBeenCalledWith("ana@exemplo.com", "landing");
    expect(screen.getByRole("button", { name: /já sou criador/i })).toBeInTheDocument();
  });

  it("shows an error when the waitlist call fails", async () => {
    vi.mocked(api.waitlist).mockRejectedValue(new Error("boom"));
    renderApp();
    fireEvent.change(await screen.findByLabelText("Your best email"), { target: { value: "a@b.co" } });
    fireEvent.click(screen.getByRole("button", { name: "Notify me when it launches" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/could not save/i);
  });

  it("shows the dedicated message when the waitlist is rate limited", async () => {
    vi.mocked(api.waitlist).mockRejectedValue(
      new ApiRequestError(429, { code: "rate_limited", message: "too many requests" }),
    );
    renderApp();
    fireEvent.change(await screen.findByLabelText("Your best email"), { target: { value: "a@b.co" } });
    fireEvent.click(screen.getByRole("button", { name: "Notify me when it launches" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/too many tries/i);
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
