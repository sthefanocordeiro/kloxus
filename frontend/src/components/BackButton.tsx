import { ArrowLeft } from "lucide-react";
import { Link } from "react-router-dom";
import { useT } from "../i18n";
import { cn } from "../lib/cn";

// The real back button (§3.2): 40 px tall on desktop, 44×44 on mobile,
// bordered, rounded 10 px, arrow + "Voltar" and the destination's label
// on hover/focus. Replaces every bare "←" in the app.
export function BackButton({ to, dest, className }: { to: string; dest: string; className?: string }) {
  const { t } = useT();
  return (
    <Link
      to={to}
      title={t.nav.backTo(dest)}
      aria-label={t.nav.backTo(dest)}
      className={cn(
        "inline-flex h-11 items-center gap-1.5 rounded-[10px] border border-line bg-paper-raised px-3 text-sm font-medium text-ink transition-colors duration-150 hover:bg-surface md:h-10",
        className,
      )}
    >
      <ArrowLeft size={15} aria-hidden />
      {t.nav.back}
    </Link>
  );
}
