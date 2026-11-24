import { Check } from "lucide-react";
import { useT } from "../i18n";
import { cn } from "../lib/cn";

// "Passo N de M" + progress bar (§3.3) for the two short flows —
// onboarding (Sua newsletter → Seções → Fontes) and the edition
// (Pautas → Escrever → Revisar e enviar). The counter shows
// completion, finished steps are revisitable via onStep, and the
// screen's primary button always names the next step.
export function StepCounter({
  steps,
  current,
  onStep,
  navAhead = false,
  className,
}: {
  steps: readonly string[];
  current: number; // 1-based index of the active step
  onStep?: (i: number) => void; // revisit a completed step (0-based)
  // navAhead lets flows whose later steps are already reachable (the
  // edition's Revisar e enviar) navigate forward too — without it only
  // completed steps are buttons (KLOX-100).
  navAhead?: boolean;
  className?: string;
}) {
  const { t } = useT();
  const total = steps.length;
  const pct = Math.round(((current - 1) / Math.max(total - 1, 1)) * 100);
  return (
    <div className={cn("min-w-0", className)}>
      <div className="flex items-center gap-2">
        <ol className="flex min-w-0 items-center gap-1.5 overflow-hidden whitespace-nowrap text-xs" aria-label={t.steps.of(current, total)}>
          {steps.map((label, i) => {
            const done = i + 1 < current;
            const active = i + 1 === current;
            const navigable = !active && !!onStep && (done || navAhead);
            const chip = (
              <span
                className={cn(
                  "inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold",
                  done ? "bg-teal-soft text-teal" : active ? "bg-teal text-white" : "bg-line text-muted",
                )}
                aria-hidden
              >
                {done ? <Check size={11} /> : i + 1}
              </span>
            );
            return (
              <li key={label} className="flex shrink-0 items-center gap-1.5">
                {i > 0 && <span className="text-muted/40" aria-hidden>·</span>}
                {navigable ? (
                  // the whole step (chip + label) is the hit area — a
                  // bare text button was 16 px tall, under the 40 px
                  // desktop / 44 px mobile minimum (§4.3, KLOX-68)
                  <button
                    type="button"
                    onClick={() => onStep(i)}
                    aria-label={label}
                    className={cn(
                      "inline-flex items-center gap-1.5 rounded-md px-3 py-3 -mx-3 -my-3 font-medium hover:underline",
                      done ? "text-teal" : "text-muted hover:text-ink",
                    )}
                  >
                    {chip}
                    <span className={cn("max-w-[5.5rem] truncate sm:max-w-none", !done && "hidden sm:inline")}>{label}</span>
                  </button>
                ) : (
                  <>
                    {chip}
                    <span className={cn(
                      active ? "font-semibold text-ink" : done ? "max-w-[5.5rem] truncate text-teal sm:max-w-none" : "hidden text-muted sm:inline",
                    )}>
                      {label}
                    </span>
                  </>
                )}
              </li>
            );
          })}
        </ol>
        <span className="ml-auto shrink-0 text-xs font-medium text-muted">{t.steps.of(current, total)}</span>
      </div>
      <div
        className="mt-1.5 h-1 overflow-hidden rounded-full bg-line"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct}
        aria-label={t.steps.of(current, total)}
      >
        <div className="h-full rounded-full bg-teal transition-[width] duration-200" style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}
