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
  className,
}: {
  steps: readonly string[];
  current: number; // 1-based index of the active step
  onStep?: (i: number) => void; // revisit a completed step (0-based)
  className?: string;
}) {
  const { t } = useT();
  const total = steps.length;
  const pct = Math.round(((current - 1) / Math.max(total - 1, 1)) * 100);
  return (
    <div className={cn("min-w-0", className)}>
      <div className="flex items-center gap-2">
        <ol className="flex min-w-0 items-center gap-1.5 text-xs" aria-label={t.steps.of(current, total)}>
          {steps.map((label, i) => {
            const done = i + 1 < current;
            const active = i + 1 === current;
            const revisitable = done && !!onStep;
            return (
              <li key={label} className="flex items-center gap-1.5">
                {i > 0 && <span className="text-muted/40" aria-hidden>·</span>}
                <span
                  className={cn(
                    "inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold",
                    done ? "bg-teal-soft text-teal" : active ? "bg-teal text-white" : "bg-line text-muted",
                  )}
                  aria-hidden
                >
                  {done ? <Check size={11} /> : i + 1}
                </span>
                {revisitable ? (
                  <button
                    type="button"
                    onClick={() => onStep(i)}
                    className="font-medium text-teal hover:underline"
                  >
                    {label}
                  </button>
                ) : (
                  <span className={active ? "font-semibold text-ink" : done ? "text-teal" : "text-muted"}>
                    {label}
                  </span>
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
