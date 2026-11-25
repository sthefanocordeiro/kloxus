import { useEffect, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";

// Pages portal their single primary action here — the shell renders it
// at the top bar's right edge, next to the language switch and the
// account menu (§3.1). Lives apart from AppShell so eager routes don't
// pull the whole shell into the landing bundle (KLOX-16).
export function ShellPrimary({ children }: { children: ReactNode }) {
  const [slot, setSlot] = useState<Element | null>(null);
  useEffect(() => {
    setSlot(document.getElementById("shell-primary-slot"));
  }, []);
  return slot ? createPortal(children, slot) : null;
}
