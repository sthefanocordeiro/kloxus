import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { LangProvider } from "../i18n";
import { ComposeRedirect } from "../pages/ComposeRedirect";

vi.mock("../api", async (orig) => {
  const real = await orig<typeof import("../api")>();
  return {
    ...real,
    api: {
      ...real.api,
      pautaCreate: vi.fn(),
    },
  };
});

function renderAt(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <LangProvider>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path="/compor" element={<ComposeRedirect />} />
            <Route path="/pauta/:id" element={<div>composer screen</div>} />
          </Routes>
        </MemoryRouter>
      </LangProvider>
    </QueryClientProvider>,
  );
}

describe("/compor deep link (KLOX-267)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.setItem("kloxus.lang", "en");
    vi.mocked(api.pautaCreate).mockResolvedValue({ issue_id: 77 });
  });

  it("creates a pauta with the mailed story pre-linked and opens its editor", async () => {
    renderAt("/compor?pub=42&noticia=123&lang=en");
    expect(await screen.findByText("composer screen")).toBeTruthy();
    expect(api.pautaCreate).toHaveBeenCalledTimes(1);
    expect(api.pautaCreate).toHaveBeenCalledWith(42, [123]);
  });

  it("rejects an incomplete link instead of creating an empty pauta", async () => {
    renderAt("/compor?pub=42");
    expect(await screen.findByText("Incomplete link")).toBeTruthy();
    expect(api.pautaCreate).not.toHaveBeenCalled();
  });

  it("shows the way back to today's stories when creation fails", async () => {
    vi.mocked(api.pautaCreate).mockRejectedValue(new Error("boom"));
    renderAt("/compor?pub=42&noticia=123");
    expect(await screen.findByText("The post could not be opened")).toBeTruthy();
    const back = screen.getByRole("link", { name: "See today's stories" });
    expect(back).toHaveAttribute("href", "/publications/42/today");
    await waitFor(() => expect(api.pautaCreate).toHaveBeenCalledTimes(1));
  });
});
