import type { ReactNode } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import * as Tabs from "@radix-ui/react-tabs";
import * as Popover from "@radix-ui/react-popover";
import { cn } from "./lib/cn";

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
          <Dialog.Close className="absolute right-4 top-4 text-muted hover:text-ink" aria-label="Close">
            ✕
          </Dialog.Close>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

export const KTabs = Tabs;

export function KPopover({ trigger, children, align = "end" }: { trigger: ReactNode; children: ReactNode; align?: "start" | "center" | "end" }) {
  return (
    <Popover.Root>
      <Popover.Trigger asChild>{trigger}</Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          align={align}
          sideOffset={8}
          className="z-50 w-80 rounded-[var(--radius-card)] border border-line bg-paper-raised p-4 shadow-lg"
        >
          {children}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}
