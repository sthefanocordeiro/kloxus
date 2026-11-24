// KLOX-74 regression: the shell's pub-scoped nav targets derive from the
// URL pubId (or the issue's publication_id on /pauta/:id), not the
// fetched publication entity. Deriving them from `pub` left every link
// pointing at /publications/<word> — a route that re-matches
// /publications/:id, 404s and never recovers — for as long as pubQ was
// in flight, which on hosted latency is long enough to click.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { api, type IssueSummary, type Publication } from "../api";
import { LangProvider } from "../i18n";
import { AppShell } from "../AppShell";

afterEach(cleanup);

vi.mock("../api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../api")>();
  return {
    ...mod,
    api: {
      ...mod.api,
      workspace: vi.fn(),
      issue: vi.fn(),
      publication: vi.fn(),
      publications: vi.fn(),
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

const issue: IssueSummary = {
  id: 7,
  publication_id: 42,
  number: 3,
  status: "drafted",
  subject: "Issue 3",
  created_at: "",
  items: {},
  flags: 0,
  missing_required_commentary: 0,
};

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

function renderAt(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <LangProvider>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route element={<AppShell />}>
              <Route path="/publications" element={<div>pubs screen</div>} />
              <Route path="/publications/:id" element={<div>pub screen</div>} />
              <Route path="/publications/:id/:tail" element={<div>pub screen</div>} />
              <Route path="/pauta/:id" element={<div>issue screen</div>} />
            </Route>
          </Routes>
        </MemoryRouter>
      </LangProvider>
    </QueryClientProvider>,
  );
}

// Any href shaped /publications/<word> is a dead route — publication
// ids are numeric, so /publications/sections re-matches :id and 404s.
const deadLink = /^\/publications\/[a-z]/i;
const expectNoDeadLinks = () => {
  for (const a of screen.getAllByRole("link")) {
    expect(a.getAttribute("href") ?? "").not.toMatch(deadLink);
  }
};

describe("AppShell pub-scoped nav (KLOX-74)", () => {
  beforeEach(() => {
    vi.mocked(api.workspace).mockResolvedValue({
      id: "w1",
      label: "w",
      mode: "demo",
      hosted: false,
      llm_mode: "off",
      publishing: true,
    });
    vi.mocked(api.publications).mockResolvedValue([pub]);
    vi.mocked(api.issue).mockResolvedValue(issue);
  });

  it("targets the URL pubId while the publication fetch is in flight", async () => {
    const d = deferred<{ publication: Publication; latest_issue?: IssueSummary }>();
    vi.mocked(api.publication).mockReturnValue(d.promise);
    renderAt("/publications/42/sources");
    await screen.findByText("pub screen");
    await screen.findAllByRole("link", { name: "Settings" }); // workspace flag settled

    // pubQ is unresolved — the nav must already point at /publications/42
    expectNoDeadLinks();
    const hrefs = screen.getAllByRole("link").map((a) => a.getAttribute("href"));
    for (const tail of ["sources", "settings"]) {
      expect(hrefs).toContain(`/publications/42/${tail}`);
    }
    expect(screen.getByRole("link", { name: "Settings" })).toHaveAttribute(
      "href",
      "/publications/42/settings",
    );

    // resolving pubQ only fills in data — the nav targets stay identical
    d.resolve({ publication: pub, latest_issue: issue });
    await waitFor(() => {
      for (const a of screen.getAllByRole("link", { name: "Current brief" })) {
        expect(a).toHaveAttribute("href", "/pauta/7");
      }
    });
    expect(screen.getByRole("link", { name: "Settings" })).toHaveAttribute(
      "href",
      "/publications/42/settings",
    );
  });

  it("resolves pub links from the issue's publication_id ahead of pubQ", async () => {
    const d = deferred<{ publication: Publication }>();
    vi.mocked(api.publication).mockReturnValue(d.promise);
    renderAt("/pauta/7");
    await screen.findByText("issue screen");

    // the issue fetch supplies the publication id one round-trip before
    // pubQ could — links go pub-scoped as soon as it lands
    await waitFor(() => {
      expect(screen.getByRole("link", { name: "Settings" })).toHaveAttribute(
        "href",
        "/publications/42/settings",
      );
    });
    expect(api.publication).toHaveBeenCalledWith("42");
    expectNoDeadLinks();
  });
});

// KLOX-90 regression: with no edition yet the "Current brief" link
// points at pubBase, and NavLink's prefix match lit it on every
// publication sub-page (plus /publications, where it collided with
// "Issues"). Highlight now follows chrome.section only.
describe("AppShell nav highlight without an edition (KLOX-90)", () => {
  beforeEach(() => {
    vi.mocked(api.workspace).mockResolvedValue({
      id: "w1",
      label: "w",
      mode: "demo",
      hosted: false,
      llm_mode: "off",
      publishing: true,
    });
    vi.mocked(api.publications).mockResolvedValue([pub]);
    vi.mocked(api.publication).mockResolvedValue({ publication: pub }); // no latest_issue
  });

  const expectInactive = (name: string) => {
    for (const a of screen.getAllByRole("link", { name })) {
      expect(a.className).not.toContain("bg-teal-soft");
      expect(a.className).not.toContain("text-teal");
    }
  };

  it("does not glow 'Current brief' on a publication sub-page", async () => {
    renderAt("/publications/42/sources");
    await screen.findByText("pub screen");
    await waitFor(() => expect(api.publication).toHaveBeenCalled());
    await screen.findAllByRole("link", { name: "Current brief" });

    expectInactive("Current brief");
    expectInactive("Briefs");
    const sources = screen.getAllByRole("link", { name: "Sources" })[0];
    expect(sources.className).toContain("bg-teal-soft");
  });

  it("glows nothing on /publications — the title is Radar-level (KLOX-185)", async () => {
    renderAt("/publications");
    await screen.findByText("pubs screen");
    await screen.findAllByRole("link", { name: "Current brief" });

    expectInactive("Current brief");
    expectInactive("Briefs");
    expectInactive("Sources");
    expectInactive("Settings");
  });
});

// KLOX-122: with publishing off (the default) the sidebar is exactly
// Today · Briefs · Radar · Settings — no readers, outbox or send.
describe("AppShell nav with publishing off (KLOX-122)", () => {
  beforeEach(() => {
    vi.mocked(api.workspace).mockResolvedValue({
      id: "w1",
      label: "w",
      mode: "demo",
      hosted: false,
      llm_mode: "off",
      publishing: false,
    });
    vi.mocked(api.publications).mockResolvedValue([pub]);
    vi.mocked(api.publication).mockResolvedValue({ publication: pub });
  });

  it("shows only Today, Briefs, Radar and Settings", async () => {
    renderAt("/publications/42/sources");
    await screen.findByText("pub screen");
    const side = screen.getAllByRole("navigation", { name: "Radar" })[0];
    expect(Array.from(side.querySelectorAll("a")).map((a) => a.textContent)).toEqual([
      "Today",
      "Briefs",
      "Radar",
      "Settings",
    ]);
    for (const a of screen.getAllByRole("link")) {
      expect(a.getAttribute("href") ?? "").not.toMatch(/readers|outbox/);
    }
  });
});
