import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { LangProvider } from "./i18n";
import { AppShell } from "./AppShell";
import { ErrorPage } from "./pages/ErrorPage";
import { Publications } from "./pages/Publications";
import { PublicationPage } from "./pages/PublicationPage";
import { IssuePage } from "./pages/IssuePage";
import { RunLanding } from "./pages/Board";
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
        { path: "/", element: <Publications /> },
        { path: "/pub/:id", element: <PublicationPage /> },
        { path: "/issues/:id", element: <IssuePage /> },
        { path: "/runs/:runID", element: <RunLanding /> },
      ],
    },
  ],
  { basename: "/app" },
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
