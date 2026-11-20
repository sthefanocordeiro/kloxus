// KLOX-79 regression: one newsletter per account means /new never
// renders the create flow for an account that already has one — it
// redirects to the editions. KLOX-99: the paste-a-link option is live —
// a pasted URL goes to the scraper and the extracted proposal fills the
// editable form for review. The shell's Voltar on /new points at a real
// parent (/publications), not at the page itself.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { api, ApiRequestError, type Proposal, type Publication } from "../api";
import { LangProvider } from "../i18n";
import { AppShell } from "../AppShell";
import { CreateNewsletter } from "../pages/Create";

afterEach(cleanup);

vi.mock("../api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../api")>();
  return {
    ...mod,
    api: {
      ...mod.api,
      workspace: vi.fn(),
      proposal: vi.fn(),
      publication: vi.fn(),
      publications: vi.fn(),
      propose: vi.fn(),
      createPublication: vi.fn(),
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

const proposal: Proposal = {
  name: "Futuro Próximo",
  topic: "t",
  audience: "a",
  editorial_line: "e",
  tone: "t",
  language: "pt-BR",
  frequency: "weekly",
  ai_disclosure: "short",
  sections: [{ id: "s1", title: "News", format: "news", items: 3, commentary: "optional", access: "all" }],
  sources: [],
  paywall: { enabled: false, paid_sections: [], price: "" },
};

function renderAt(path: string, withShell = false) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const flow = <Route path="/new" element={<CreateNewsletter />} />;
  render(
    <QueryClientProvider client={qc}>
      <LangProvider>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            {withShell ? (
              <Route element={<AppShell />}>
                {flow}
                <Route path="/publications" element={<div>publications list</div>} />
                <Route path="/publications/:id" element={<div>editions screen</div>} />
              </Route>
            ) : (
              <>
                {flow}
                <Route path="/publications" element={<div>publications list</div>} />
                <Route path="/publications/:id" element={<div>editions screen</div>} />
              </>
            )}
          </Routes>
        </MemoryRouter>
      </LangProvider>
    </QueryClientProvider>,
  );
}

describe("create-newsletter flow (KLOX-79)", () => {
  beforeEach(() => {
    vi.mocked(api.workspace).mockResolvedValue({
      id: "w1",
      label: "w",
      mode: "demo",
      hosted: false,
      llm_mode: "off",
    });
    vi.mocked(api.proposal).mockResolvedValue(proposal);
  });

  it("redirects an account that already has a newsletter to its editions", async () => {
    vi.mocked(api.publications).mockResolvedValue([pub]);
    renderAt("/new");
    expect(await screen.findByText("editions screen")).toBeVisible();
    // the create flow never renders — no heading, no paste card
    expect(screen.queryByLabelText("Paste a link")).toBeNull();
    expect(screen.queryByRole("heading", { name: "Create your newsletter" })).toBeNull();
  });

  it("keeps the create flow hidden while the publications query is in flight", async () => {
    vi.mocked(api.publications).mockImplementation(() => new Promise<Publication[]>(() => {}));
    renderAt("/new");
    // the query never resolves — the flow must never appear in the meantime
    await waitFor(() => expect(document.querySelector("[aria-busy]")).not.toBeNull());
    expect(screen.queryByLabelText("Paste a link")).toBeNull();
    expect(screen.queryByRole("heading", { name: "Create your newsletter" })).toBeNull();
  });

  it("paste-a-link is live — the scraped proposal fills the form for review", async () => {
    vi.mocked(api.publications).mockResolvedValue([]);
    vi.mocked(api.propose).mockResolvedValue({
      proposal: {
        ...proposal,
        name: "Radar Fintech",
        sections: [
          { id: "s1", title: "O pix fora do ar", format: "featured", items: 1, commentary: "required", access: "all" },
          { id: "s2", title: "Bancos sob pressão", format: "news", items: 3, commentary: "optional", access: "all" },
          { id: "s3", title: "Links da semana", format: "links", items: 5, commentary: "none", access: "all" },
        ],
      },
      scrape: {
        url: "https://radar.example.com",
        title: "Radar Fintech",
        provider: "firecrawl (simulated)",
        simulated: true,
        words: 220,
        warnings: ["thin_content"],
      },
    });
    renderAt("/new");
    const paste = await screen.findByLabelText("Paste a link");
    expect(paste).toBeEnabled();
    fireEvent.change(paste, { target: { value: "https://radar.example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Create from this" }));
    // the "what we extracted" preview shows before anything is created
    const preview = await screen.findByTestId("scrape-preview");
    expect(preview).toHaveTextContent("Radar Fintech");
    expect(preview).toHaveTextContent("Simulated — no page was fetched");
    expect(preview).toHaveTextContent("little readable text");
    // the extracted proposal replaced the sample in the editable form
    expect(screen.getByLabelText("Name")).toHaveValue("Radar Fintech");
    expect(vi.mocked(api.propose).mock.calls[0][0]).toBe("https://radar.example.com");
    // nothing is created until the steps are completed
    expect(api.createPublication).not.toHaveBeenCalled();
  });

  it("shows a localized scrape error and keeps the sample form", async () => {
    vi.mocked(api.publications).mockResolvedValue([]);
    vi.mocked(api.propose).mockRejectedValue(
      new ApiRequestError(502, { code: "scrape_failed", message: "upstream" }),
    );
    renderAt("/new");
    const paste = await screen.findByLabelText("Paste a link");
    fireEvent.change(paste, { target: { value: "https://x.example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Create from this" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("couldn't be read");
    // no preview, and the form still carries the sample proposal
    expect(screen.queryByTestId("scrape-preview")).toBeNull();
    expect(screen.getByLabelText("Name")).toHaveValue("Futuro Próximo");
  });

  it("points the /new Voltar at /publications, not at the page itself", async () => {
    vi.mocked(api.publications).mockResolvedValue([]);
    renderAt("/new", true);
    const back = await screen.findByRole("link", { name: "Back to Publications" });
    expect(back).toHaveAttribute("href", "/publications");
  });

  it("uses one button pattern — Next alone on step 1, Back + Next on sections", async () => {
    vi.mocked(api.publications).mockResolvedValue([]);
    renderAt("/new");
    const next = await screen.findByRole("button", { name: "Next" });
    // the first step carries no in-flow Back
    expect(screen.queryByRole("button", { name: "Back" })).toBeNull();
    fireEvent.click(next);
    expect(await screen.findByRole("heading", { name: "Your sections" })).toBeVisible();
    const back = screen.getByRole("button", { name: "Back" });
    const fwd = screen.getByRole("button", { name: "Next" });
    expect(fwd).toBeVisible();
    // reading order is back ← forward (Voltar left, Avançar right)
    expect(back.compareDocumentPosition(fwd) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.click(back);
    expect(await screen.findByRole("heading", { name: "Create your newsletter" })).toBeVisible();
  });

  it("a raced one_newsletter 409 leaves the flow — no in-flow notice", async () => {
    // entry guard says "no publications", the POST still races into
    // the server's one-newsletter 409 — the account's own editions
    // are the only landing; the message never renders inside the flow
    vi.mocked(api.publications)
      .mockResolvedValueOnce([])
      .mockResolvedValue([pub]);
    vi.mocked(api.createPublication).mockRejectedValue(
      new ApiRequestError(409, { code: "one_newsletter", message: "already one" }),
    );
    renderAt("/new");
    fireEvent.click(await screen.findByRole("button", { name: "Next" }));
    await screen.findByRole("heading", { name: "Your sections" });
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(await screen.findByText("editions screen")).toBeVisible();
    expect(screen.queryByText(/already has a newsletter/i)).toBeNull();
    expect(screen.queryByText(/já tem uma newsletter/i)).toBeNull();
  });
});
