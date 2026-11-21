// KLOX-65 section builder: the same component edits the section list
// in onboarding step 2 and on the Seções screen. These tests pin the
// contract every caller relies on — stable ids, onChange-shaped
// updates, reorder/remove/add semantics, templates and the 20-story
// edition ceiling.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { NewsletterSection } from "../api";
import { SectionBuilder, newSection, totalStories } from "../components/SectionBuilder";

afterEach(cleanup);

const make = (over: Partial<NewsletterSection> = {}): NewsletterSection => ({
  id: over.id ?? `sec-${Math.random().toString(36).slice(2)}`,
  title: "Section",
  format: "news",
  items: 3,
  commentary: "optional",
  access: "all",
  ...over,
});

function setup(initial: NewsletterSection[]) {
  const onChange = vi.fn();
  // access options only show with publishing on (KLOX-122)
  const qc = new QueryClient();
  qc.setQueryData(["workspace"], { id: "w", label: "w", mode: "demo", hosted: false, llm_mode: "auto", publishing: true });
  render(
    <QueryClientProvider client={qc}>
      <SectionBuilder value={initial} onChange={onChange} />
    </QueryClientProvider>,
  );
  return onChange;
}

describe("SectionBuilder", () => {
  it("shows the live story total and skeleton preview", () => {
    setup([make({ items: 3 }), make({ items: 5 })]);
    expect(screen.getByText("Your piece will have 8 stories")).toBeVisible();
    expect(screen.getByText("Preview")).toBeVisible();
  });

  it("renames a section without changing its id", () => {
    const sec = make({ id: "keep-me", title: "Old" });
    const onChange = setup([sec]);
    fireEvent.change(screen.getByDisplayValue("Old"), { target: { value: "New name" } });
    expect(onChange).toHaveBeenCalledWith([{ ...sec, title: "New name" }]);
  });

  it("moves a section down with the button reorder", () => {
    const a = make({ title: "A" });
    const b = make({ title: "B" });
    const onChange = setup([a, b]);
    fireEvent.click(screen.getAllByRole("button", { name: "Move down" })[0]);
    expect(onChange).toHaveBeenCalledWith([b, a]);
  });

  it("removes a section", () => {
    const a = make({ title: "A" });
    const b = make({ title: "B" });
    const onChange = setup([a, b]);
    fireEvent.click(screen.getAllByRole("button", { name: "Remove section" })[0]);
    expect(onChange).toHaveBeenCalledWith([b]);
  });

  it("adds a numbered section", () => {
    const a = make({ title: "A" });
    const onChange = setup([a]);
    fireEvent.click(screen.getByRole("button", { name: "Add section" }));
    const next = onChange.mock.calls[0][0] as NewsletterSection[];
    expect(next).toHaveLength(2);
    expect(next[0]).toEqual(a);
    expect(next[1].title).toBe("Section 2");
    expect(next[1].id).not.toBe(a.id);
  });

  it("applies a template as a fresh section list", () => {
    const onChange = setup([make()]);
    fireEvent.click(screen.getByRole("button", { name: "Short brief" }));
    const next = onChange.mock.calls[0][0] as NewsletterSection[];
    expect(next).toHaveLength(1);
    expect(next[0].title).toBe("News");
    expect(next[0].format).toBe("news");
    expect(next[0].id).toBeTruthy();
  });

  it("steps the story count within 1–10", () => {
    const sec = make({ items: 3 });
    const onChange = setup([sec]);
    fireEvent.click(screen.getByRole("button", { name: "More stories" }));
    expect(onChange).toHaveBeenCalledWith([{ ...sec, items: 4 }]);
  });

  it("flags the edition when the total passes 20 stories", () => {
    setup([make({ items: 10 }), make({ items: 10 }), make({ items: 3 })]);
    expect(screen.getByText(/the limit is 20 stories/)).toBeVisible();
  });

  it("explains what a section is and what each format means (KLOX-81)", () => {
    setup([make()]);
    expect(screen.getByText(/Each block is a section of the piece/)).toBeVisible();
    // the legend names all three formats; the picked format's help
    // also sits under the Format select
    expect(screen.getAllByText(/the one that opens the piece/).length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText(/cited facts plus your take/).length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText(/nothing to write/).length).toBeGreaterThanOrEqual(1);
  });

  it("switches a section's format, commentary and access", () => {
    const sec = make({ id: "s1" });
    const onChange = setup([sec]);
    const selects = screen.getAllByRole("combobox");
    fireEvent.change(selects[0], { target: { value: "links" } });
    expect(onChange).toHaveBeenLastCalledWith([{ ...sec, format: "links" }]);
    fireEvent.change(selects[1], { target: { value: "required" } });
    expect(onChange).toHaveBeenLastCalledWith([{ ...sec, commentary: "required" }]);
    fireEvent.change(selects[2], { target: { value: "subscribers" } });
    expect(onChange).toHaveBeenLastCalledWith([{ ...sec, access: "subscribers" }]);
  });
});

describe("newSection/totalStories", () => {
  it("generates unique ids", () => {
    const ids = new Set([newSection(), newSection(), newSection()].map((s) => s.id));
    expect(ids.size).toBe(3);
  });
  it("sums items", () => {
    expect(totalStories([make({ items: 2 }), make({ items: 7 })])).toBe(9);
  });
});
