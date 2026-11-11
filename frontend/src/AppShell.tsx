import { Link, Outlet } from "react-router-dom";
import { useT, type Lang } from "./i18n";

// The chrome around every screen: brand, language switch.
export function AppShell() {
  const { t, lang, setLang } = useT();
  return (
    <div className="min-h-screen flex flex-col">
      <header className="border-b border-line bg-paper-raised">
        <div className="mx-auto flex h-12 max-w-[1400px] items-center gap-4 px-4">
          <Link to="/" className="font-doc text-lg font-semibold tracking-tight">
            {t.appName}
          </Link>
          <span className="text-xs text-muted">demo</span>
          <div className="ml-auto flex items-center gap-1 text-xs">
            {(["en", "pt"] as Lang[]).map((l) => (
              <button
                key={l}
                onClick={() => setLang(l)}
                className={
                  "rounded px-2 py-1 " +
                  (lang === l ? "bg-ink text-paper font-medium" : "text-muted hover:text-ink")
                }
                aria-pressed={lang === l}
              >
                {l === "en" ? "EN" : "PT"}
              </button>
            ))}
          </div>
        </div>
      </header>
      <main className="flex-1 min-h-0 flex flex-col">
        <Outlet />
      </main>
    </div>
  );
}
