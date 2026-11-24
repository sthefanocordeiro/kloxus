import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { LangProvider } from "./i18n";
import { ErrorPage, NotFound } from "./pages/ErrorPage";
import { Landing } from "./pages/Landing";
import "./styles.css";

// Legacy /app/* URLs are served the SPA as-is (a 301 back to "/" would
// loop against browsers that cached the old "/" → "/app/" 301), so the
// router sees the stripped path.
if (location.pathname === "/app" || location.pathname.startsWith("/app/")) {
  const stripped = location.pathname.slice(4) || "/";
  history.replaceState(null, "", stripped + location.search + location.hash);
}

const qc = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false } },
});

// Heavy screens (the Tiptap editor above all) load on demand so the
// landing bundle stays small — see scripts/check-bundle-size.mjs.
const router = createBrowserRouter([
  // the landing stands alone — the app shell (sidebar, tabs, menus) is
  // lazy so none of it lands in the "/" bundle (KLOX-16 budget)
  { path: "/", element: <Landing />, errorElement: <ErrorPage /> },
  // legal pages share the landing chrome and stay out of the app
  // shell — lazy so the landing payload budget is untouched (KLOX-70)
  {
    path: "/termos",
    lazy: () => import("./pages/Legal").then((m) => ({ Component: m.TermsPage })),
    errorElement: <ErrorPage />,
  },
  {
    path: "/privacidade",
    lazy: () => import("./pages/Legal").then((m) => ({ Component: m.PrivacyPage })),
    errorElement: <ErrorPage />,
  },
  {
    lazy: () => import("./AppShell").then((m) => ({ Component: m.AppShell })),
    errorElement: <ErrorPage />,
    hydrateFallbackElement: (
      <div className="grid flex-1 place-items-center text-muted" aria-busy="true">
        …
      </div>
    ),
    children: [
      { path: "/start", lazy: () => import("./pages/Start").then((x) => ({ Component: x.Start })) },
      { path: "/publications", lazy: () => import("./pages/Publications").then((m) => ({ Component: m.Publications })) },
      { path: "/publications/:id", lazy: () => import("./pages/PublicationPage").then((m) => ({ Component: m.PublicationPage })) },
      { path: "/publications/:id/today", lazy: () => import("./pages/Today").then((m) => ({ Component: m.Today })) },
      { path: "/publications/:id/sources", lazy: () => import("./pages/Sources").then((m) => ({ Component: m.SourcesPage })) },
      { path: "/publications/:id/settings", lazy: () => import("./pages/SettingsPage").then((m) => ({ Component: m.SettingsPage })) },
      { path: "/pauta/:id", lazy: () => import("./pages/PautaPage").then((m) => ({ Component: m.PautaPage })) },
      { path: "/pauta/:id/export", lazy: () => import("./pages/PautaPage").then((m) => ({ Component: m.PautaExport })) },
      { path: "/issues/:id", lazy: () => import("./pages/PautaPage").then((m) => ({ Component: m.IssueRedirect })) },
      { path: "*", element: <NotFound /> },
    ],
  },
  // reader-facing pages live outside the editor shell — no app nav
  {
    // lazy like the app shell — the reader pages pull in the shared
    // markdown renderer, which now carries the composer's image/embed
    // blocks (KLOX-66) and must not inflate the landing payload
    // (KLOX-16 budget)
    lazy: () => import("./pages/ReaderPage").then((m) => ({ Component: m.ReaderShell })),
    errorElement: <ErrorPage />,
    children: [
      {
        // the newsletter's own landing page: brand, subscribe, archive
        // (KLOX-102) — distinct from the per-issue page below
        path: "/p/:slug",
        lazy: () => import("./pages/ReaderPage").then((m) => ({ Component: m.PublicNewsletterPage })),
      },
      {
        path: "/p/:slug/:num",
        lazy: () => import("./pages/ReaderPage").then((m) => ({ Component: m.PublicIssue })),
      },
      {
        path: "/unsub",
        lazy: () => import("./pages/ReaderPage").then((m) => ({ Component: m.UnsubPage })),
      },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={qc}>
      <LangProvider>
        <RouterProvider router={router} />
      </LangProvider>
    </QueryClientProvider>
  </StrictMode>,
);
