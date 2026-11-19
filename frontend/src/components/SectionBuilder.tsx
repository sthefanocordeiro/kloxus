import { useState } from "react";
import { ArrowDown, ArrowUp, GripVertical, Link2, Minus, Newspaper, Plus, Star, Trash2 } from "lucide-react";
import type { NewsletterSection } from "../api";
import { useT, type Lang } from "../i18n";
import { cn } from "../lib/cn";
import { Button } from "../ui";

// SectionBuilder (KLOX-65): the creator-defined section list — add,
// rename, reorder (drag or buttons) and tune each section's format,
// story count, commentary rule and access. The same component builds
// the sections on onboarding step 2 and edits them on the Seções
// screen. Live total and the skeleton preview travel with it.

export const SECTION_LIMITS = { nameMax: 40, itemsMin: 1, itemsMax: 10, totalMax: 20 } as const;

let seq = 0;
export function newSection(partial?: Partial<NewsletterSection>): NewsletterSection {
  seq += 1;
  return {
    id: `sec-${Date.now().toString(36)}-${seq}`,
    title: "",
    format: "news",
    items: 3,
    commentary: "optional",
    access: "all",
    ...partial,
  };
}

type TemplateKey = "classic" | "short" | "essay" | "blank";

// Templates give the creator a running start. Titles are localized —
// they land in the issue body verbatim — while ids are generated
// fresh so a template never collides with an existing section.
function templateSections(key: TemplateKey, lang: Lang): Omit<NewsletterSection, "id">[] {
  const pt = lang === "pt";
  switch (key) {
    case "classic":
      return [
        { title: pt ? "O destaque" : "The big one", format: "featured", items: 1, commentary: "required", access: "all" },
        { title: pt ? "Vale saber" : "Worth knowing", format: "news", items: 4, commentary: "optional", access: "all" },
        { title: pt ? "Links rápidos" : "Quick links", format: "links", items: 5, commentary: "none", access: "all" },
      ];
    case "short":
      return [{ title: pt ? "Notícias" : "News", format: "news", items: 5, commentary: "optional", access: "all" }];
    case "essay":
      return [
        { title: pt ? "Destaque" : "Featured", format: "featured", items: 1, commentary: "optional", access: "all" },
        { title: "Links", format: "links", items: 8, commentary: "none", access: "all" },
      ];
    case "blank":
      return [{ title: pt ? "Seção 1" : "Section 1", format: "news", items: 3, commentary: "optional", access: "all" }];
  }
}

export function totalStories(sections: NewsletterSection[]): number {
  return sections.reduce((sum, s) => sum + s.items, 0);
}

export function SectionBuilder({
  value,
  onChange,
  showTemplates = true,
  showPreview = true,
}: {
  value: NewsletterSection[];
  onChange: (sections: NewsletterSection[]) => void;
  showTemplates?: boolean;
  showPreview?: boolean;
}) {
  const { t, lang } = useT();
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [overIndex, setOverIndex] = useState<number | null>(null);
  const total = totalStories(value);
  const overLimit = total > SECTION_LIMITS.totalMax;

  const update = (i: number, patch: Partial<NewsletterSection>) =>
    onChange(value.map((s, j) => (j === i ? { ...s, ...patch } : s)));
  const move = (from: number, to: number) => {
    if (to < 0 || to >= value.length || from === to) return;
    const next = [...value];
    const [s] = next.splice(from, 1);
    next.splice(to, 0, s);
    onChange(next);
  };
  const remove = (i: number) => onChange(value.filter((_, j) => j !== i));
  const add = () =>
    onChange([...value, newSection({ title: t.builder.sectionN(value.length + 1) })]);
  const applyTemplate = (key: TemplateKey) =>
    onChange(templateSections(key, lang).map((s) => newSection(s)));

  const list = (
    <div>
      {showTemplates && (
        <div className="mb-3">
          <p className="text-xs font-semibold uppercase tracking-wide text-muted">{t.builder.templates}</p>
          <div className="mt-1.5 flex flex-wrap gap-1.5">
            {(["classic", "short", "essay", "blank"] as const).map((key) => (
              <button
                key={key}
                type="button"
                onClick={() => applyTemplate(key)}
                className="min-h-11 rounded-full border border-line bg-paper-raised px-3 py-1.5 text-xs font-medium text-ink hover:border-teal hover:text-teal md:min-h-10"
              >
                {t.builder.templateNames[key]}
              </button>
            ))}
          </div>
        </div>
      )}

      <ul className="grid gap-3">
        {value.map((s, i) => (
          <li
            key={s.id}
            onDragOver={(e) => {
              e.preventDefault();
              setOverIndex(i);
            }}
            onDrop={(e) => {
              e.preventDefault();
              if (dragIndex !== null) move(dragIndex, i);
              setDragIndex(null);
              setOverIndex(null);
            }}
            className={cn(
              "rounded-[var(--radius-card)] border border-line bg-paper-raised p-3",
              overIndex === i && dragIndex !== null && dragIndex !== i && "ring-2 ring-teal",
            )}
          >
            <div className="flex items-center gap-2">
              <span
                draggable
                onDragStart={() => setDragIndex(i)}
                onDragEnd={() => {
                  setDragIndex(null);
                  setOverIndex(null);
                }}
                aria-label={t.builder.drag}
                className="flex h-11 w-6 cursor-grab items-center justify-center text-muted active:cursor-grabbing md:h-10"
              >
                <GripVertical size={16} aria-hidden />
              </span>
              <input
                value={s.title}
                maxLength={SECTION_LIMITS.nameMax}
                onChange={(e) => update(i, { title: e.target.value })}
                aria-label={t.builder.name}
                placeholder={t.builder.namePlaceholder}
                className="min-w-0 flex-1 rounded border border-line bg-paper px-3 py-2 text-sm font-medium outline-none focus:border-teal"
              />
              <div className="flex shrink-0">
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => move(i, i - 1)}
                  disabled={i === 0}
                  aria-label={t.builder.moveUp}
                >
                  <ArrowUp size={16} />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => move(i, i + 1)}
                  disabled={i === value.length - 1}
                  aria-label={t.builder.moveDown}
                >
                  <ArrowDown size={16} />
                </Button>
                <Button
                  variant="dangerGhost"
                  size="icon"
                  onClick={() => remove(i)}
                  aria-label={t.builder.remove}
                >
                  <Trash2 size={16} />
                </Button>
              </div>
            </div>

            <div className="mt-3 grid gap-3 sm:grid-cols-2">
              <label className="block">
                <span className="text-xs font-semibold uppercase tracking-wide text-muted">{t.builder.format}</span>
                <select
                  value={s.format}
                  onChange={(e) => update(i, { format: e.target.value as NewsletterSection["format"] })}
                  className="mt-1 w-full rounded border border-line bg-paper px-2 py-2 text-sm outline-none focus:border-teal"
                >
                  <option value="featured">{t.builder.formats.featured}</option>
                  <option value="news">{t.builder.formats.news}</option>
                  <option value="links">{t.builder.formats.links}</option>
                </select>
                {/* one line on the picked format — first-time creators
                    can't tell Destaque/Notícias/Links apart (KLOX-81) */}
                <p className="mt-1 text-xs text-muted">{t.builder.formatHelp[s.format]}</p>
              </label>

              <div>
                <span className="text-xs font-semibold uppercase tracking-wide text-muted">{t.builder.stories}</span>
                <div className="mt-1 flex items-center gap-1">
                  <Button
                    variant="outline"
                    size="icon"
                    onClick={() => update(i, { items: Math.max(SECTION_LIMITS.itemsMin, s.items - 1) })}
                    disabled={s.items <= SECTION_LIMITS.itemsMin}
                    aria-label={t.builder.fewer}
                  >
                    <Minus size={14} />
                  </Button>
                  <span className="w-8 text-center text-sm font-semibold" aria-live="polite">{s.items}</span>
                  <Button
                    variant="outline"
                    size="icon"
                    onClick={() => update(i, { items: Math.min(SECTION_LIMITS.itemsMax, s.items + 1) })}
                    disabled={s.items >= SECTION_LIMITS.itemsMax}
                    aria-label={t.builder.more}
                  >
                    <Plus size={14} />
                  </Button>
                </div>
              </div>

              <label className="block">
                <span className="text-xs font-semibold uppercase tracking-wide text-muted">{t.builder.commentaryLabel}</span>
                <select
                  value={s.commentary}
                  onChange={(e) => update(i, { commentary: e.target.value })}
                  className="mt-1 w-full rounded border border-line bg-paper px-2 py-2 text-sm outline-none focus:border-teal"
                >
                  <option value="required">{t.builder.commentary.required}</option>
                  <option value="optional">{t.builder.commentary.optional}</option>
                  <option value="none">{t.builder.commentary.none}</option>
                </select>
              </label>

              <label className="block">
                <span className="text-xs font-semibold uppercase tracking-wide text-muted">{t.builder.access}</span>
                <select
                  value={s.access}
                  onChange={(e) => update(i, { access: e.target.value as NewsletterSection["access"] })}
                  className="mt-1 w-full rounded border border-line bg-paper px-2 py-2 text-sm outline-none focus:border-teal"
                >
                  <option value="all">{t.builder.accesses.all}</option>
                  <option value="subscribers">{t.builder.accesses.subscribers}</option>
                </select>
              </label>
            </div>
          </li>
        ))}
      </ul>

      <Button variant="outline" className="mt-3 w-full" onClick={add}>
        <Plus size={16} aria-hidden />
        {t.builder.add}
      </Button>

      <p className={cn("mt-3 text-sm font-medium", overLimit ? "text-danger" : "text-muted")} aria-live="polite">
        {t.builder.total(total)}
        {overLimit && ` — ${t.builder.overLimit}`}
      </p>
      {value.length === 0 && <p className="mt-1 text-xs text-danger">{t.builder.emptyError}</p>}
    </div>
  );

  if (!showPreview) return list;

  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_300px]">
      {list}
      <div>
        <p className="text-xs font-semibold uppercase tracking-wide text-muted">{t.builder.preview}</p>
        <div className="mt-1.5 rounded-[var(--radius-card)] border border-line bg-surface p-3" aria-hidden>
          {value.map((s) => (
            <div key={s.id} className="mb-3 last:mb-0">
              <div className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted">
                {s.format === "featured" && <Star size={11} />}
                {s.format === "links" && <Link2 size={11} />}
                {s.title || t.builder.namePlaceholder}
                {s.access === "subscribers" && <span className="text-teal">· {t.builder.accesses.subscribers}</span>}
              </div>
              {s.format === "links" ? (
                <ul className="mt-1 space-y-1">
                  {Array.from({ length: s.items }).map((_, k) => (
                    <li key={k} className="h-1.5 w-3/4 rounded bg-line" />
                  ))}
                </ul>
              ) : (
                <div className="mt-1 space-y-1.5">
                  {Array.from({ length: s.items }).map((_, k) => (
                    <div key={k} className={cn("rounded bg-line", s.format === "featured" ? "h-3 w-full" : "h-2 w-5/6")} />
                  ))}
                </div>
              )}
            </div>
          ))}
          {value.length === 0 && <div className="h-20 rounded border border-dashed border-line" />}
        </div>
        <div className="mt-2 text-xs text-muted">
          <p>{t.builder.previewIntro}</p>
          <ul className="mt-1.5 space-y-1">
            <li className="flex items-center gap-1.5">
              <Star size={11} className="shrink-0" aria-hidden />
              <span>
                <span className="font-medium text-ink">{t.builder.formats.featured}</span>
                {" — "}{t.builder.formatHelp.featured}
              </span>
            </li>
            <li className="flex items-center gap-1.5">
              <Newspaper size={11} className="shrink-0" aria-hidden />
              <span>
                <span className="font-medium text-ink">{t.builder.formats.news}</span>
                {" — "}{t.builder.formatHelp.news}
              </span>
            </li>
            <li className="flex items-center gap-1.5">
              <Link2 size={11} className="shrink-0" aria-hidden />
              <span>
                <span className="font-medium text-ink">{t.builder.formats.links}</span>
                {" — "}{t.builder.formatHelp.links}
              </span>
            </li>
          </ul>
        </div>
      </div>
    </div>
  );
}
