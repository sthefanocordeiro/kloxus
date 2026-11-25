import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, type IssueSummary, type Publication } from "../api";
import { LangProvider } from "../i18n";
import { SourcesPage } from "../pages/Sources";

vi.mock("../api", async (orig) => {
  const real = await orig<typeof import("../api")>();
  return {
    ...real,
    api: {
      ...real.api,
      workspace: vi.fn(),
      publication: vi.fn(),
      sources: vi.fn(),
      sourceCatalog: vi.fn(),
      deletePublication: vi.fn(),
    },
  };
});

const pub: Publication = {
  id: 42,
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

// KLOX-213: a failed Apagar Radar rendered its error on the card
// *behind* the open confirm dialog — a dead-looking button. The error
// lives inside the dialog now.
describe("SourcesPage delete dialog (KLOX-213)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.setItem("kloxus.lang", "en");
    vi.mocked(api.workspace).mockResolvedValue({
      id: "w1",
      label: "w",
      mode: "demo",
      hosted: false,
      llm_mode: "off",
      publishing: true,
    });
    vi.mocked(api.publication).mockResolvedValue({
      publication: pub,
      latest_issue: { id: 1 } as IssueSummary,
    });
    vi.mocked(api.sources).mockResolvedValue([]);
    vi.mocked(api.sourceCatalog).mockResolvedValue({ offered: [] });
    vi.mocked(api.deletePublication).mockRejectedValue(new Error("boom"));
  });

  it("shows the delete error inside the confirm dialog", async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <LangProvider>
          <MemoryRouter initialEntries={["/publications/42/sources"]}>
            <Routes>
              <Route path="/publications/:id/sources" element={<SourcesPage />} />
            </Routes>
          </MemoryRouter>
        </LangProvider>
      </QueryClientProvider>,
    );

    fireEvent.click(await screen.findByRole("button", { name: "Delete Radar" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Yes, delete it" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Something went wrong");
    expect(api.deletePublication).toHaveBeenCalledWith("42");
    // the dialog stays open — the same button retries
    expect(screen.getByRole("dialog")).toBeTruthy();
  });
});
