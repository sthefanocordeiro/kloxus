import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { LangProvider } from "./i18n";
import { AppShell } from "./AppShell";
import { ErrorPage } from "./pages/ErrorPage";
import { Publications } from "./pages/Publications";
import { Landing } from "./pages/Landing";
import { PublicIssue, ReaderShell, UnsubPage } from "./pages/ReaderPage";
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
  {
    element: <AppShell />,
    errorElement: <ErrorPage />,
    hydrateFallbackElement: (
      <div className="grid flex-1 place-items-center text-muted" aria-busy="true">
        …
      </div>
    ),
    children: [
      { path: "/", element: <Landing /> },
      { path: "/new", lazy: () => import("./pages/Create").then((m) => ({ Component: m.CreateNewsletter })) },
      { path: "/publications", element: <Publications /> },
      { path: "/publications/:id", lazy: () => import("./pages/PublicationPage").then((m) => ({ Component: m.PublicationPage })) },
      { path: "/publications/:id/sources", lazy: () => import("./pages/Sources").then((m) => ({ Component: m.SourcesPage })) },
      { path: "/publications/:id/outbox", lazy: () => import("./pages/OutboxPage").then((m) => ({ Component: m.OutboxPage })) },
      { path: "/issues/:id", lazy: () => import("./pages/IssuePage").then((m) => ({ Component: m.IssuePage })) },
      { path: "/runs/:runID", lazy: () => import("./pages/Board").then((m) => ({ Component: m.RunLanding })) },
    ],
  },
  // reader-facing pages live outside the editor shell — no app nav
  {
    element: <ReaderShell />,
    errorElement: <ErrorPage />,
    children: [
      { path: "/p/:slug/:num", element: <PublicIssue /> },
      { path: "/unsub", element: <UnsubPage /> },
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
