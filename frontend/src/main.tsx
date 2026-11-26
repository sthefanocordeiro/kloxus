import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, Navigate, RouterProvider, useParams } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiRequestError } from "./api";
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
  defaultOptions: {
    queries: {
      // KLOX-194: never auto-retry a 429 — an immediate second hit just
      // feeds the limiter; Retry-After lives on the error for the UI
      retry: (n, err) => n < 1 && !(err instanceof ApiRequestError && err.status === 429),
      refetchOnWindowFocus: false,
    },
  },
});

// Configurações merged into the Radar screen (KLOX-207) — old
// /settings links land on it instead of the 404.
function SettingsRedirect() {
  const { id } = useParams();
  return <Navigate to={`/publications/${id}/sources`} replace />;
}

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
    path: "/entrar",
    lazy: () => import("./pages/SignIn").then((m) => ({ Component: m.SignIn })),
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
      // the legacy /new wizard is gone (KLOX-177); keep old external
      // links landing on onboarding instead of the 404 — replace so
      // back doesn't return to the dead route
      { path: "/new", element: <Navigate to="/start" replace /> },
      { path: "/publications", lazy: () => import("./pages/Publications").then((m) => ({ Component: m.Publications })) },
      { path: "/publications/:id", lazy: () => import("./pages/PublicationPage").then((m) => ({ Component: m.PublicationPage })) },
      { path: "/publications/:id/today", lazy: () => import("./pages/Today").then((m) => ({ Component: m.Today })) },
      // deep link target of the daily briefing CTA (KLOX-267)
      { path: "/compor", lazy: () => import("./pages/ComposeRedirect").then((m) => ({ Component: m.ComposeRedirect })) },
      { path: "/publications/:id/sources", lazy: () => import("./pages/Sources").then((m) => ({ Component: m.SourcesPage })) },
      { path: "/publications/:id/settings", element: <SettingsRedirect /> },
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
