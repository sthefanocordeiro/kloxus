import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api, type Sample } from "../api";
import { LangProvider } from "../i18n";
import { AppShell } from "../AppShell";
import { Landing } from "../pages/Landing";

vi.mock("../api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../api")>();
  return {
    ...mod,
    api: {
      ...mod.api,
      workspace: vi.fn(),
      publications: vi.fn(),
      sample: vi.fn(),
    },
  };
});

const sample: Sample = {
  publication: "Futuro Próximo",
  issue_number: 7,
  date: "2026-03-02",
  dossiers: [],
  how_it_works: [],
};

function renderApp() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <LangProvider>
        <MemoryRouter initialEntries={["/"]}>
          <Routes>
            <Route element={<AppShell />}>
              <Route path="/" element={<Landing />} />
              <Route path="/new" element={<div>onboarding screen</div>} />
            </Route>
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
    vi.mocked(api.sample).mockResolvedValue(sample);
  });

  it("creates the anonymous workspace and lands on onboarding", async () => {
    renderApp();
    const cta = await screen.findAllByRole("button", { name: /get started without login/i });
    fireEvent.click(cta[0]);
    await waitFor(() => expect(api.workspace).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("onboarding screen")).toBeInTheDocument();
  });

  it("switches the page between EN and pt-BR", async () => {
    renderApp();
    expect(
      await screen.findByRole("heading", { name: /sourced line by line/i }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "PT" }));
    expect(
      await screen.findByRole("heading", { name: /com fonte linha a linha/i }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /começar sem login/i }).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("button", { name: "EN" }));
    expect(
      await screen.findByRole("heading", { name: /sourced line by line/i }),
    ).toBeInTheDocument();
  });
});
