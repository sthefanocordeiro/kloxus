// KLOX-74 regression: the shell's pub-scoped nav targets derive from the
// URL pubId (or the issue's publication_id on /pauta/:id), not the
// fetched publication entity. Deriving them from `pub` left every link
// pointing at /publications/<word> — a route that re-matches
// /publications/:id, 404s and never recovers — for as long as pubQ was
// in flight, which on hosted latency is long enough to click.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
              <Route path="/start" element={<div>start screen</div>} />
              <Route path="/publications" element={<div>pubs screen</div>} />
              <Route path="/publications/:id" element={<div>pub screen</div>} />
              <Route path="/publications/:id/:tail" element={<div>pub screen</div>} />
              <Route path="/pauta/:id" element={<div>issue screen</div>} />
              <Route path="/pauta/:id/export" element={<div>export screen</div>} />
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
    await screen.findAllByRole("link", { name: "Radar" }); // workspace flag settled

    // pubQ is unresolved — the nav must already point at /publications/42
    expectNoDeadLinks();
    const hrefs = screen.getAllByRole("link").map((a) => a.getAttribute("href"));
    expect(hrefs).toContain("/publications/42/sources");

    // resolving pubQ only fills in data — the nav targets stay identical
    d.resolve({ publication: pub, latest_issue: issue });
    await waitFor(() => {
      for (const a of screen.getAllByRole("link", { name: "Current brief" })) {
        expect(a).toHaveAttribute("href", "/pauta/7");
      }
    });
    for (const a of screen.getAllByRole("link", { name: "Radar" })) {
      expect(a).toHaveAttribute("href", "/publications/42/sources");
    }
  });

  it("resolves pub links from the issue's publication_id ahead of pubQ", async () => {
    const d = deferred<{ publication: Publication }>();
    vi.mocked(api.publication).mockReturnValue(d.promise);
    renderAt("/pauta/7");
    await screen.findByText("issue screen");

    // the issue fetch supplies the publication id one round-trip before
    // pubQ could — links go pub-scoped as soon as it lands
    await waitFor(() => {
      const radars = screen.getAllByRole("link", { name: "Radar" });
      expect(radars.some((a) => a.getAttribute("href") === "/publications/42/sources")).toBe(true);
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
    const radar = screen.getAllByRole("link", { name: "Radar" })[0];
    expect(radar.className).toContain("bg-teal-soft");
  });

  it("glows nothing on /publications — the title is Radar-level (KLOX-185)", async () => {
    renderAt("/publications");
    await screen.findByText("pubs screen");
    await screen.findAllByRole("link", { name: "Current brief" });

    expectInactive("Current brief");
    expectInactive("Briefs");
    // the publications list is Radar-level chrome — the merged Radar
    // item is what glows (KLOX-185, KLOX-207)
    const radar = screen.getAllByRole("link", { name: "Radar" })[0];
    expect(radar.className).toContain("bg-teal-soft");
  });
});

// KLOX-122: with publishing off (the default) the sidebar is exactly
// Today · Briefs · Radar — Configurações merged into Radar (KLOX-207);
// no readers, outbox or send.
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

  it("shows only Today, Briefs and Radar", async () => {
    renderAt("/publications/42/sources");
    await screen.findByText("pub screen");
    const side = screen.getAllByRole("navigation", { name: "Radar" })[0];
    expect(Array.from(side.querySelectorAll("a")).map((a) => a.textContent)).toEqual([
      "Today",
      "Briefs",
      "Radar",
    ]);
    for (const a of screen.getAllByRole("link")) {
      expect(a.getAttribute("href") ?? "").not.toMatch(/readers|outbox/);
    }
  });
});

// KLOX-187: the pauta screens used to show a bare "Kloxus" header.
// The trail is Radar › Pauta, and on export Pauta › Exportar so
// "← Voltar" lands back on the editor, like every other screen.
describe("AppShell header trail on pauta screens (KLOX-187)", () => {
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
    vi.mocked(api.publication).mockResolvedValue({ publication: pub, latest_issue: issue });
  });

  const crumbLinks = () => {
    const trail = screen.getByRole("navigation", { name: "breadcrumb" });
    return within(trail)
      .getAllByRole("link")
      .map((a) => [a.textContent, a.getAttribute("href")]);
  };

  it("shows Radar › Brief 3 on the pauta editor", async () => {
    renderAt("/pauta/7");
    await screen.findByText("issue screen");

    // the crumb's target and the title fill in once the issue fetch
    // lands (publication_id and number come from it, not pubQ)
    await waitFor(() => {
      expect(crumbLinks()).toEqual([["Radar", "/publications/42"]]);
    });
    expect(screen.getByRole("heading", { name: "Brief 3" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Back to Radar" })).toHaveAttribute(
      "href",
      "/publications/42",
    );
  });

  it("shows Radar › Brief 3 on the export screen and Voltar returns to the editor", async () => {
    renderAt("/pauta/7/export");
    await screen.findByText("export screen");

    await waitFor(() => {
      expect(crumbLinks()).toEqual([
        ["Radar", "/publications/42"],
        ["Brief 3", "/pauta/7"],
      ]);
    });
    expect(screen.getByRole("heading", { name: "Export" })).toBeInTheDocument();
    // "← Voltar" is the last crumb — back to the editor, not to Radar
    expect(screen.getByRole("link", { name: "Back to Brief 3" })).toHaveAttribute(
      "href",
      "/pauta/7",
    );
  });

  // KLOX-199: the header names the pauta like the lists do — the
  // content title, "Brief N" only while the issue fetch is pending
  it("shows the pauta title once the issue lands", async () => {
    const d = deferred<IssueSummary>();
    vi.mocked(api.issue).mockReturnValue(d.promise);
    renderAt("/pauta/7");
    await screen.findByText("issue screen");

    // loading — the numbered label fills in only until the title arrives
    expect(screen.getByRole("heading", { name: "Brief" })).toBeInTheDocument();

    d.resolve({ ...issue, title: "A semana do Pix" });
    expect(await screen.findByRole("heading", { name: "A semana do Pix" })).toBeInTheDocument();
    await waitFor(() => {
      expect(crumbLinks()).toEqual([["Radar", "/publications/42"]]);
    });
  });

  it("names the export crumb by the pauta title", async () => {
    vi.mocked(api.issue).mockResolvedValue({ ...issue, title: "A semana do Pix" });
    renderAt("/pauta/7/export");
    await screen.findByText("export screen");

    await waitFor(() => {
      expect(crumbLinks()).toEqual([
        ["Radar", "/publications/42"],
        ["A semana do Pix", "/pauta/7"],
      ]);
    });
  });
});

// KLOX-187: before the first Radar exists the nav's destinations all
// 404 — hide the items (sidebar and bottom tabs) until there is one.
// pubs.data === undefined means still loading: keep the items rather
// than flash an empty shell on every page.
describe("AppShell nav before the first Radar (KLOX-187)", () => {
  beforeEach(() => {
    vi.mocked(api.workspace).mockResolvedValue({
      id: "w1",
      label: "w",
      mode: "demo",
      hosted: false,
      llm_mode: "off",
      publishing: true,
    });
    vi.mocked(api.publication).mockResolvedValue({ publication: pub });
  });

  it("keeps the items while publications loads, hides them when it returns empty", async () => {
    const d = deferred<Publication[]>();
    vi.mocked(api.publications).mockReturnValue(d.promise);
    renderAt("/publications");
    await screen.findByText("pubs screen");

    // still loading — the shell must not flash an empty nav
    await screen.findAllByRole("link", { name: "Radar" });

    d.resolve([]);
    await waitFor(() => {
      expect(screen.queryByRole("link", { name: "Radar" })).toBeNull();
    });
    for (const name of ["Current brief", "Briefs"]) {
      expect(screen.queryByRole("link", { name })).toBeNull();
    }
    // the only way forward is creating the first Radar
    expect(screen.getByRole("link", { name: "New Radar" })).toHaveAttribute("href", "/start");
  });
});

// KLOX-206: "New Radar" leaked where it cannot work — the API allows
// one Radar per account (409 one_newsletter). On /start the sidebar
// offered a link to the page the user is already on, and the claimed
// account menu silently signed the user out to /start. The only entry
// points left are the empty-state CTA and the sidebar block off /start.
describe("AppShell 'New Radar' entry points (KLOX-206)", () => {
  beforeEach(() => {
    vi.mocked(api.workspace).mockResolvedValue({
      id: "w1",
      label: "w",
      mode: "demo",
      hosted: false,
      llm_mode: "off",
      publishing: true,
      claimed: true,
      owner_email: "ana@x.dev",
    });
    vi.mocked(api.publication).mockResolvedValue({ publication: pub });
  });

  it("does not offer New Radar on the sidebar during onboarding (/start)", async () => {
    vi.mocked(api.publications).mockResolvedValue([]);
    renderAt("/start");
    await screen.findByText("start screen");

    // pubs resolved empty — the nav items hid; the sidebar create link
    // must not point back at the page the user is already on
    await waitFor(() => {
      expect(screen.queryByRole("link", { name: "Radar" })).toBeNull();
    });
    expect(screen.queryByRole("link", { name: "New Radar" })).toBeNull();
  });

  it("shows only Sign out in a claimed account's menu", async () => {
    vi.mocked(api.publications).mockResolvedValue([pub]);
    renderAt("/publications/42");
    await screen.findByText("pub screen");
    await screen.findAllByRole("link", { name: "Radar" }); // queries settled

    fireEvent.click(screen.getByRole("button", { name: "Account" }));
    expect(await screen.findByRole("button", { name: "Sign out" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "New Radar" })).toBeNull();
  });
});

// KLOX-213: a menu link navigates without an outside click — the open
// menu stayed floating over the next screen. Drop closes on the
// pathname change now.
describe("AppShell menus close on navigation (KLOX-213)", () => {
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
    vi.mocked(api.publication).mockResolvedValue({ publication: pub });
  });

  it("closes the More menu after a destination is picked", async () => {
    renderAt("/publications/42");
    await screen.findByText("pub screen");
    await screen.findAllByRole("link", { name: "Radar" }); // queries settled

    fireEvent.click(screen.getByRole("button", { name: "More" }));
    const menu = await screen.findByRole("navigation", { name: "More" });
    // both menu links are named "Radar" (t.publications === t.nav.radar)
    // — [0] is the /sources one
    fireEvent.click(within(menu).getAllByRole("link", { name: "Radar" })[0]);

    await waitFor(() => {
      expect(screen.queryByRole("navigation", { name: "More" })).toBeNull();
    });
  });
});
