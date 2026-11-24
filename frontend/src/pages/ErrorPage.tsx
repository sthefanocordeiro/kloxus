import { isRouteErrorResponse, Link, useRouteError } from "react-router-dom";
import { useT } from "../i18n";
import { isRateLimited } from "../lib/errors";
import { RateLimited } from "../components/RateLimited";
import { Button, Card } from "../ui";

// Shared "not found" card — unknown routes (the catch-all), missing
// issues and missing publications all land here instead of spinning.
export function NotFound() {
  const { t } = useT();
  return (
    <div className="flex flex-1 items-center justify-center p-4">
      <Card className="w-full max-w-md text-center">
        <p className="font-doc text-xl font-semibold">{t.error.notFoundTitle}</p>
        <p className="mt-1 text-sm text-muted">{t.error.notFoundDetail}</p>
        <div className="mt-4 flex justify-center">
          <Link to="/">
            <Button size="sm" variant="outline">
              {t.error.home}
            </Button>
          </Link>
        </div>
      </Card>
    </div>
  );
}

// Route error boundary — a render failure shows this instead of
// crashing the app (React Router's default is a bare error dump).
export function ErrorPage() {
  const { t } = useT();
  const err = useRouteError();
  if (isRouteErrorResponse(err) && err.status === 404) {
    return (
      <div className="flex min-h-screen flex-col bg-paper">
        <NotFound />
      </div>
    );
  }
  // KLOX-194: a 429 that reaches the boundary still gets the friendly
  // page — never the raw status dump
  if (isRateLimited(err) || (isRouteErrorResponse(err) && err.status === 429)) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-paper p-4">
        <RateLimited error={err} onRetry={() => window.location.reload()} />
      </div>
    );
  }
  const detail = isRouteErrorResponse(err)
    ? `${err.status} ${err.statusText}`
    : err instanceof Error
      ? err.message
      : typeof err === "string"
        ? err
        : JSON.stringify(err);
  return (
    <div className="flex min-h-screen items-center justify-center bg-paper p-4">
      <Card className="w-full max-w-md text-center">
        <p className="font-doc text-xl font-semibold">{t.error.title}</p>
        <p className="mt-1 text-sm text-muted">{t.error.detail}</p>
        {detail && (
          <pre className="mt-3 max-h-32 overflow-auto rounded bg-line/40 p-2 text-left text-[11px] text-muted">
            {detail}
          </pre>
        )}
        <div className="mt-4 flex justify-center gap-2">
          <Button size="sm" onClick={() => window.location.reload()}>
            {t.error.retry}
          </Button>
          <Link to="/">
            <Button size="sm" variant="outline">
              {t.error.home}
            </Button>
          </Link>
        </div>
      </Card>
    </div>
  );
}
