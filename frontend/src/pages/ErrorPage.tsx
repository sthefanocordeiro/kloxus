import { Link, useRouteError } from "react-router-dom";
import { useT } from "../i18n";
import { Button, Card } from "../ui";

// Route error boundary — a render failure shows this instead of
// crashing the app (React Router's default is a bare error dump).
export function ErrorPage() {
  const { t } = useT();
  const err = useRouteError();
  const detail = err instanceof Error ? err.message : String(err ?? "");
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
