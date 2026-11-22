// KLOX-102: the Leitores screen carries the copyable subscribe link —
// the stable public newsletter page — and the /p/{slug} landing shows
// the newsletter's own brand with its editions archive. Counts stay
// honest: the demo reader is never counted as confirmed.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { api, type PublicNewsletter } from "../api";
import { LangProvider } from "../i18n";
import { ReadersPage } from "../pages/ReadersPage";
import { PublicNewsletterPage } from "../pages/ReaderPage";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

vi.mock("../api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../api")>();
  return {
    ...mod,
    api: {
      ...mod.api,
      reader: vi.fn(),
      publicNewsletter: vi.fn(),
      subscribe: vi.fn(),
    },
  };
});

const newsletter: PublicNewsletter = {
  publication: { name: "Futuro Próximo", slug: "futuro-proximo", language: "pt" },
  editions: [
    { number: 2, subject: "Edição dois", created_at: "2026-11-20T12:00:00Z" },
    { number: 1, subject: "Edição um", created_at: "2026-11-13T12:00:00Z" },
  ],
  paywall: { enabled: true, price: "R$ 12/mês" },
};

function renderReader() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <LangProvider>
        <MemoryRouter initialEntries={["/publications/42/readers"]}>
          <Routes>
            <Route path="/publications/:id/readers" element={<ReadersPage />} />
          </Routes>
        </MemoryRouter>
      </LangProvider>
    </QueryClientProvider>,
  );
}

function renderLanding() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <LangProvider>
        <MemoryRouter initialEntries={["/p/futuro-proximo"]}>
          <Routes>
            <Route path="/p/:slug" element={<PublicNewsletterPage />} />
            <Route path="/p/:slug/:num" element={<div>issue page</div>} />
          </Routes>
        </MemoryRouter>
      </LangProvider>
    </QueryClientProvider>,
  );
}

describe("ReadersPage subscribe link (KLOX-102)", () => {
  beforeEach(() => {
    vi.mocked(api.reader).mockResolvedValue({
      reader: { email: "demo@reader.test", name: "Demo Reader" },
      subscribed: false,
      subscribers: 0,
      readers: 0,
      confirmed: 0,
      public_url: "https://kloxus.fly.dev/p/futuro-proximo?tok=sealed",
    });
  });

  it("shows the copyable subscribe link with its copy affordance", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    renderReader();

    const input = await screen.findByLabelText("Subscribe link");
    expect(input).toHaveValue("https://kloxus.fly.dev/p/futuro-proximo?tok=sealed");
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("https://kloxus.fly.dev/p/futuro-proximo?tok=sealed"));
    expect(await screen.findByRole("button", { name: "Copied!" })).toBeInTheDocument();
  });

  it("keeps the counts honest — zero confirmed readers stays zero", async () => {
    renderReader();
    await screen.findByLabelText("Subscribe link");
    expect(screen.getByText("0 confirmed readers")).toBeInTheDocument();
    expect(screen.getByText("0 paying subscribers")).toBeInTheDocument();
  });
});

describe("PublicNewsletterPage /p/{slug} (KLOX-102)", () => {
  beforeEach(() => {
    vi.mocked(api.publicNewsletter).mockResolvedValue({ ...newsletter });
    vi.mocked(api.subscribe).mockResolvedValue({ sent: true, delivery: "outbox", confirm_url: "/confirm-sub?pub=futuro-proximo&r=1&t=mac" });
  });

  it("shows the newsletter's brand, paywall line and editions archive", async () => {
    renderLanding();

    expect(await screen.findByRole("heading", { level: 1, name: "Futuro Próximo" })).toBeInTheDocument();
    expect(screen.getByText("R$ 12/mês · paid sections are for subscribers.")).toBeInTheDocument();
    expect(screen.getByText("Editions")).toBeInTheDocument();
    // the newest edition is first and badged; both link to the reader page
    const links = screen.getAllByRole("link", { name: /Edição/ });
    expect(links).toHaveLength(2);
    expect(links[0]).toHaveAttribute("href", "/p/futuro-proximo/2");
    expect(screen.getByText("Latest")).toBeInTheDocument();
  });

  it("captures the subscribe email through the existing double opt-in", async () => {
    renderLanding();

    await screen.findByRole("heading", { name: "Futuro Próximo" });
    fireEvent.change(screen.getByPlaceholderText("you@example.com"), { target: { value: "reader@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Send sign-in link" }));
    await waitFor(() =>
      expect(api.subscribe).toHaveBeenCalledWith("futuro-proximo", "reader@example.com", ""),
    );
    expect(await screen.findByText(/Check your inbox/)).toBeInTheDocument();
  });

  it("shows the empty archive copy when nothing is out yet", async () => {
    vi.mocked(api.publicNewsletter).mockResolvedValue({
      ...newsletter,
      editions: [],
      paywall: { enabled: false, price: "" },
    });
    renderLanding();

    expect(await screen.findByText("No editions out yet — subscribe to get the next one.")).toBeInTheDocument();
    expect(screen.queryByText(/paid sections/)).toBeNull();
  });
});
