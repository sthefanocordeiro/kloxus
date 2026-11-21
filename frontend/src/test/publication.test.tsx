// KLOX-90 regression: on a fresh publication — zero confirmed sources,
// zero issues — "Prepare next piece" used to launch a run that died on
// "no items collected", and the Edições list was a bare "—". Prepare is
// now gated on a confirmed source and the empty state carries real copy.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { api, type Publication, type SourceRow } from "../api";
import { LangProvider } from "../i18n";
import { PublicationPage } from "../pages/PublicationPage";

afterEach(() => {
  cleanup();
  document.getElementById("shell-primary-slot")?.remove();
});

vi.mock("../api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../api")>();
  return {
    ...mod,
    api: {
      ...mod.api,
      publication: vi.fn(),
      issues: vi.fn(),
      sources: vi.fn(),
      startRun: vi.fn(),
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

const source: SourceRow = {
  id: 1,
  name: "Feed",
  type: "rss",
  url: "https://example.com/feed.xml",
  canonical_url: "https://example.com/feed.xml",
  items_per_week: 0,
  monitor: {},
};

function renderPage() {
  const slot = document.createElement("div");
  slot.id = "shell-primary-slot";
  document.body.appendChild(slot);
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

describe("PublicationPage first-run state (KLOX-90)", () => {
  beforeEach(() => {
    vi.mocked(api.publication).mockResolvedValue({ publication: pub });
    vi.mocked(api.issues).mockResolvedValue([]);
  });

  it("gates Prepare on a confirmed source and shows the empty copy", async () => {
    vi.mocked(api.sources).mockResolvedValue([]);
    renderPage();

    const btn = await screen.findByRole("button", { name: "Prepare next piece" });
    await waitFor(() => expect(btn).toBeDisabled());
    expect(screen.getByRole("link", { name: "Open sources" })).toHaveAttribute(
      "href",
      "/publications/42/sources",
    );
    expect(await screen.findByText("No pieces yet — prepare the first one.")).toBeInTheDocument();
  });

  it("enables Prepare once a source is confirmed", async () => {
    vi.mocked(api.sources).mockResolvedValue([source]);
    renderPage();

    const btn = await screen.findByRole("button", { name: "Prepare next piece" });
    await waitFor(() => expect(btn).toBeEnabled());
    expect(screen.queryByRole("link", { name: "Open sources" })).toBeNull();
  });
});
