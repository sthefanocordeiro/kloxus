import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { LangProvider } from "./i18n";
import { AppShell } from "./AppShell";
import { ErrorPage } from "./pages/ErrorPage";
import { Publications } from "./pages/Publications";
import { Landing } from "./pages/Landing";
import { CreateNewsletter } from "./pages/Create";
import { SourcesPage } from "./pages/Sources";
import { PublicationPage } from "./pages/PublicationPage";
import { IssuePage } from "./pages/IssuePage";
import { RunLanding } from "./pages/Board";
import { OutboxPage } from "./pages/OutboxPage";
import { PublicIssue, ReaderShell, UnsubPage } from "./pages/ReaderPage";
import "./styles.css";

const qc = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false } },
});

const router = createBrowserRouter(
  [
    {
      element: <AppShell />,
      errorElement: <ErrorPage />,
      children: [
        { path: "/", element: <Landing /> },
        { path: "/new", element: <CreateNewsletter /> },
        { path: "/publications", element: <Publications /> },
        { path: "/publications/:id", element: <PublicationPage /> },
        { path: "/publications/:id/sources", element: <SourcesPage /> },
        { path: "/publications/:id/outbox", element: <OutboxPage /> },
        { path: "/issues/:id", element: <IssuePage /> },
        { path: "/runs/:runID", element: <RunLanding /> },
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
  ],
);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={qc}>
      <LangProvider>
        <RouterProvider router={router} />
      </LangProvider>
    </QueryClientProvider>
  </StrictMode>,
);
