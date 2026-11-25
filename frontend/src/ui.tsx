import { forwardRef, type ButtonHTMLAttributes, type HTMLAttributes, type InputHTMLAttributes, type ReactNode } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "./lib/cn";

// Buttons (§4.3): one primary per screen — filled deep green, white
// text. Accent is the lime highlight reserved for the hero CTA.
// Secondary = outline, destructive = red text. Touch targets never go
// under 44 px on mobile / 40 px on desktop (the 36 px "sm" spec row is
// overridden by the min-target rule — recorded in decisions.md).
const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 rounded-[var(--radius-btn)] font-medium transition-colors duration-150 disabled:opacity-50 disabled:pointer-events-none cursor-pointer",
  {
    variants: {
      variant: {
        primary: "bg-teal text-white hover:bg-ink-soft",
        accent: "bg-accent text-teal hover:brightness-95",
        outline: "border border-line bg-paper-raised hover:bg-surface text-ink",
        ghost: "hover:bg-surface text-ink",
        danger: "bg-danger-soft text-danger hover:bg-danger hover:text-white",
        dangerGhost: "text-danger hover:bg-danger-soft",
      },
      size: {
        sm: "h-11 px-3 text-sm md:h-10",
        md: "h-11 px-4 text-[15px]",
        lg: "h-13 px-6 text-base",
        icon: "h-11 w-11 md:h-10 md:w-10",
      },
    },
    defaultVariants: { variant: "primary", size: "md" },
  },
);

export interface ButtonProps
  extends ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, ...props }, ref) => (
    <button ref={ref} className={cn(buttonVariants({ variant, size }), className)} {...props} />
  ),
);
Button.displayName = "Button";

// The one styled checkbox (KLOX-214): the news list and the export
// destinations share it so a tick never falls back to the native blue.
export function Tick({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      type="checkbox"
      className={cn("mt-1.5 h-5 w-5 shrink-0 cursor-pointer accent-teal", className)}
      {...props}
    />
  );
}

export function Badge({
  className,
  tone = "muted",
  children,
}: {
  className?: string;
  tone?: "muted" | "teal" | "danger" | "warn" | "ink" | "chip";
  children: ReactNode;
}) {
  const tones = {
    muted: "bg-line/60 text-muted",
    teal: "bg-teal-soft text-teal",
    chip: "bg-teal-soft text-chip",
    danger: "bg-danger-soft text-danger",
    warn: "bg-warn-soft text-warn",
    ink: "bg-ink text-paper",
  };
  return (
    <span className={cn("inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium", tones[tone], className)}>
      {children}
    </span>
  );
}

export function Card({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("rounded-[var(--radius-card)] border border-line bg-paper-raised p-4", className)}
      {...props}
    />
  );
}

// Loading placeholder for content surfaces — a quiet skeleton line
// instead of a bare "…" (§4.4: every state looks finished).
export function SkeletonLine({ className }: { className?: string }) {
  return <div className={cn("h-4 w-40 animate-pulse rounded bg-line/60", className)} aria-hidden />;
}
