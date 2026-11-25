import type { ReactNode } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { cn } from "./lib/cn";
import { useT } from "./i18n";

// Radix-backed primitives live apart from ui.tsx: only lazy-loaded
// routes use them, so this module stays out of the landing bundle.
export function KDialog({
  open,
  onOpenChange,
  title,
  children,
  wide,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: string;
  children: ReactNode;
  wide?: boolean;
}) {
  const { t } = useT();
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-ink/40 z-40" />
        <Dialog.Content
          className={cn(
            "fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-50 rounded-[var(--radius-dialog)]",
            "bg-paper p-6 shadow-xl border border-line max-h-[85vh] overflow-y-auto",
            wide ? "w-[min(92vw,880px)]" : "w-[min(92vw,520px)]",
          )}
        >
          <Dialog.Title className="text-lg font-semibold mb-4">{title}</Dialog.Title>
          {children}
          <Dialog.Close className="absolute right-4 top-4 text-muted hover:text-ink" aria-label={t.close}>
            ✕
          </Dialog.Close>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

