// KLOX-235: Sources lands back here after "Apagar Radar" with the
// deleted name in location state — the banner confirms what went away
// where the empty state could otherwise read as a fresh account.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { api } from "../api";
import { LangProvider } from "../i18n";
import { Publications } from "../pages/Publications";

afterEach(cleanup);

vi.mock("../api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../api")>();
  return {
    ...mod,
    api: {
      ...mod.api,
      publications: vi.fn(),
      workspace: vi.fn(),
    },
  };
});

function renderAt(entry: string | { pathname: string; state: unknown }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <LangProvider>
        <MemoryRouter initialEntries={[entry]}>
          <Routes>
            <Route path="/publications" element={<Publications />} />
          </Routes>
        </MemoryRouter>
      </LangProvider>
    </QueryClientProvider>,
  );
}

describe("Publications delete banner (KLOX-235)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.setItem("kloxus.lang", "en");
    vi.mocked(api.publications).mockResolvedValue([]);
    vi.mocked(api.workspace).mockResolvedValue({
      id: "w1",
      label: "w",
      mode: "hosted",
      hosted: true,
      llm_mode: "auto",
      publishing: true,
    });
  });

  it("names the deleted Radar in the banner from location state", async () => {
    renderAt({ pathname: "/publications", state: { deleted: "Futuro Próximo" } });

    const banner = await screen.findByRole("status");
    expect(banner).toHaveTextContent("Futuro Próximo deleted");
  });

  it("shows no banner on a plain visit", async () => {
    renderAt("/publications");

    // settle on the empty state, then confirm no banner is up
    await screen.findByRole("link", { name: "Create your Radar" });
    expect(screen.queryByRole("status")).toBeNull();
  });
});
