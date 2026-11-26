import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
      sourceConfirm: vi.fn(),
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

// KLOX-235: the post-issue button counts and adds only the ticked
// suggestions — "Add N suggested sources" never re-counts the sources
// already added.
describe("SourcesPage suggested count (KLOX-235)", () => {
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
    vi.mocked(api.sources).mockResolvedValue([
      {
        id: 7,
        name: "Added",
        url: "https://added.example/feed",
        type: "rss",
        canonical_url: "https://added.example",
        items_per_week: 5,
        monitor: {},
      },
    ]);
    vi.mocked(api.sourceCatalog).mockResolvedValue({
      offered: [
        { type: "rss", url: "https://a.example/feed", name: "Feed A" },
        { type: "rss", url: "https://b.example/feed", name: "Feed B" },
        { type: "rss", url: "https://pay.example/feed", name: "Paywalled", paywall: true },
      ],
    });
    vi.mocked(api.sourceConfirm).mockResolvedValue([]);
  });

  function renderPage() {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <LangProvider>
          <MemoryRouter initialEntries={["/publications/42/sources"]}>
            <Routes>
              <Route path="/publications/:id/sources" element={<SourcesPage />} />
              <Route path="/publications/:id/today" element={<div>today</div>} />
            </Routes>
          </MemoryRouter>
        </LangProvider>
      </QueryClientProvider>,
    );
  }

  it("counts only the ticked suggestions and confirms only those", async () => {
    renderPage();

    // all three suggestions start ticked
    await screen.findByRole("button", { name: "Add 3 suggested sources" });
    fireEvent.click(screen.getByRole("checkbox", { name: "Feed B" }));
    expect(await screen.findByRole("button", { name: "Add 2 suggested sources" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add 3 suggested sources" })).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Add 2 suggested sources" }));
    await waitFor(() =>
      expect(api.sourceConfirm).toHaveBeenCalledWith("42", [
        "https://a.example/feed",
        "https://pay.example/feed",
      ]),
    );
  });

  it("disables the button when nothing is ticked", async () => {
    renderPage();
    await screen.findByRole("button", { name: "Add 3 suggested sources" });
    for (const name of ["Feed A", "Feed B", "Paywalled"]) {
      fireEvent.click(screen.getByRole("checkbox", { name }));
    }
    expect(screen.getByRole("button", { name: "Add 0 suggested sources" })).toBeDisabled();
  });
});

// KLOX-235: the editorias chips derive from the theme text and edit it
// back — unticking a chip strips its cadastro label, ticking appends it.
describe("SourcesPage editoria chips (KLOX-235)", () => {
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
      publication: { ...pub, topic: "fintech, economia" },
      latest_issue: { id: 1 } as IssueSummary,
    });
    vi.mocked(api.sources).mockResolvedValue([]);
    vi.mocked(api.sourceCatalog).mockResolvedValue({ offered: [] });
  });

  it("derives chips from the theme and rewrites it on toggle", async () => {
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

    const topic = await screen.findByLabelText("Topics");
    expect(topic).toHaveValue("fintech, economia");
    // "economia" in the theme turns the Economy chip on
    const economy = screen.getByRole("button", { name: "Economy" });
    const politics = screen.getByRole("button", { name: "Politics" });
    expect(economy).toHaveAttribute("aria-pressed", "true");
    expect(politics).toHaveAttribute("aria-pressed", "false");

    // unticking strips the label and rejoins the remaining topics
    fireEvent.click(economy);
    expect(topic).toHaveValue("fintech");
    expect(economy).toHaveAttribute("aria-pressed", "false");

    // ticking appends the cadastro label (pt), and the chip re-derives on
    fireEvent.click(politics);
    expect(topic).toHaveValue("fintech, política");
    expect(politics).toHaveAttribute("aria-pressed", "true");
  });
});
