import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api, type Publication } from "../api";
import { LangProvider } from "../i18n";
import { Landing } from "../pages/Landing";

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
            <Route path="/new" element={<div>onboarding screen</div>} />
            <Route path="/publications" element={<div>publications screen</div>} />
          </Routes>
        </MemoryRouter>
      </LangProvider>
    </QueryClientProvider>,
  );
}

describe("Landing", () => {
  beforeEach(() => {
    vi.mocked(api.workspace).mockResolvedValue({ id: "w1", label: "demo session w1…", mode: "demo", hosted: true, llm_mode: "auto" });
    vi.mocked(api.publications).mockResolvedValue([]);
  });

  it("creates the anonymous workspace and lands on onboarding", async () => {
    renderApp();
    const cta = await screen.findAllByRole("button", { name: /create your newsletter/i });
    fireEvent.click(cta[0]);
    await waitFor(() => expect(api.workspace).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("onboarding screen")).toBeInTheDocument();
  });

  it("switches the page between EN and pt-BR", async () => {
    renderApp();
    expect(
      await screen.findByRole("heading", { name: /the research is done/i }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "PT" }));
    expect(
      await screen.findByRole("heading", { name: /a pauta pronta/i }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /criar minha newsletter/i }).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("button", { name: "EN" }));
    expect(
      await screen.findByRole("heading", { name: /the research is done/i }),
    ).toBeInTheDocument();
  });

  it("offers the dashboard to a returning visitor and sign-in to a new one", async () => {
    renderApp();
    expect(await screen.findByRole("button", { name: /sign in/i })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /dashboard/i })).not.toBeInTheDocument();

    vi.mocked(api.publications).mockResolvedValue([pub]);
    renderApp();
    expect(await screen.findByRole("link", { name: /dashboard/i })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("link", { name: /dashboard/i }));
    expect(await screen.findByText("publications screen")).toBeInTheDocument();
  });
});
