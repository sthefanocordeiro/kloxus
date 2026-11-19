// KLOX-79 regression: one newsletter per account means /new never
// renders the create flow for an account that already has one — it
// redirects to the editions. The paste-to-create option is not
// launched, so its control stays disabled behind an in-development
// notice, and the shell's Voltar on /new points at a real parent
// (/publications), not at the page itself.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { api, type Proposal, type Publication } from "../api";
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
    expect(screen.queryByLabelText("Paste a link or an issue")).toBeNull();
    expect(screen.queryByRole("heading", { name: "Create your newsletter" })).toBeNull();
  });

  it("keeps the create flow hidden while the publications query is in flight", async () => {
    vi.mocked(api.publications).mockImplementation(() => new Promise<Publication[]>(() => {}));
    renderAt("/new");
    // the query never resolves — the flow must never appear in the meantime
    await waitFor(() => expect(document.querySelector("[aria-busy]")).not.toBeNull());
    expect(screen.queryByLabelText("Paste a link or an issue")).toBeNull();
    expect(screen.queryByRole("heading", { name: "Create your newsletter" })).toBeNull();
  });

  it("shows the paste option as in-development with a disabled control", async () => {
    vi.mocked(api.publications).mockResolvedValue([]);
    renderAt("/new");
    const paste = await screen.findByLabelText("Paste a link or an issue");
    expect(paste).toBeDisabled();
    expect(screen.getByRole("button", { name: "Create from this" })).toBeDisabled();
    expect(
      screen.getByText("This feature hasn't launched yet — it's in development."),
    ).toBeVisible();
  });

  it("points the /new Voltar at /publications, not at the page itself", async () => {
    vi.mocked(api.publications).mockResolvedValue([]);
    renderAt("/new", true);
    const back = await screen.findByRole("link", { name: "Back to Publications" });
    expect(back).toHaveAttribute("href", "/publications");
  });
});
