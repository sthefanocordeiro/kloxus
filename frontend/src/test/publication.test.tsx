// KLOX-179: the issues list is the "Pautas" index — every row opens the
// pauta editor, sent pautas land on the export step, and the empty
// state carries real copy.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { api, type IssueSummary, type Publication } from "../api";
import { LangProvider } from "../i18n";
import { PublicationPage } from "../pages/PublicationPage";

afterEach(cleanup);

vi.mock("../api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../api")>();
  return {
    ...mod,
    api: {
      ...mod.api,
      publication: vi.fn(),
      issues: vi.fn(),
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

const issue = (id: number, status: IssueSummary["status"]): IssueSummary => ({
  id,
  publication_id: 42,
  number: id,
  status,
  subject: `Issue ${id}`,
  created_at: "",
  items: {},
  flags: 0,
  missing_required_commentary: 0,
});

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <LangProvider>
        <MemoryRouter initialEntries={["/publications/42"]}>
          <Routes>
            <Route path="/publications/:id" element={<PublicationPage />} />
          </Routes>
        </MemoryRouter>
      </LangProvider>
    </QueryClientProvider>,
  );
}

describe("PublicationPage pauta list (KLOX-179)", () => {
  beforeEach(() => {
    vi.mocked(api.publication).mockResolvedValue({ publication: pub });
  });

  it("links each issue to /pauta/:id — /pauta/:id/export once sent", async () => {
    vi.mocked(api.issues).mockResolvedValue([issue(7, "drafted"), issue(8, "approved"), issue(9, "sent")]);
    renderPage();

    const links = await screen.findAllByRole("link");
    const hrefs = links.map((a) => a.getAttribute("href"));
    expect(hrefs).toContain("/pauta/7");
    expect(hrefs).toContain("/pauta/8");
    expect(hrefs).toContain("/pauta/9/export");
    expect(hrefs.some((h) => h?.startsWith("/issues/"))).toBe(false);
  });

  // KLOX-184: the row names the pauta by its content, shows the date
  // and the formats that left, and an exported one opens /export.
  it("names the pauta and shows date plus exported formats", async () => {
    vi.mocked(api.issues).mockResolvedValue([{
      ...issue(9, "approved"),
      title: "Fim da escala 6×1 no Brasil",
      created_at: "2026-11-24 10:00:00",
      exported: true,
      exported_formats: ["linkedin", "markdown"],
    }]);
    renderPage();

    expect(await screen.findByText("Fim da escala 6×1 no Brasil")).toBeInTheDocument();
    expect(screen.getByText("LinkedIn")).toBeInTheDocument();
    expect(screen.getByText("Markdown")).toBeInTheDocument();
    expect(screen.getByText("Exported")).toBeInTheDocument();
    expect(screen.getByRole("link").getAttribute("href")).toBe("/pauta/9/export");
  });

  it("shows the empty copy when there are no pautas", async () => {
    vi.mocked(api.issues).mockResolvedValue([]);
    renderPage();

    expect(await screen.findByText("No briefs yet — start with today's stories.")).toBeInTheDocument();
  });
});
