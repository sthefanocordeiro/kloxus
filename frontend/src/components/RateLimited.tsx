import { useEffect, useState, type ReactNode } from "react";
import { ApiRequestError } from "../api";
import { useT } from "../i18n";
import { cn } from "../lib/cn";
import { Button, Card } from "../ui";

// The friendly 429 state (KLOX-194): instead of a raw error line or a
// dead click, "Muita gente entrando ao mesmo tempo daqui" with a retry
// button. A Retry-After on the response holds the button in a countdown
// so the retry only fires once the server says it should.
export function RateLimited({
  error,
  onRetry,
  busy = false,
  className,
  children,
}: {
  error?: unknown;
  onRetry: () => void;
  // the retry request is in flight — the button stays down
  busy?: boolean;
  className?: string;
  children?: ReactNode;
}) {
  const { t } = useT();
  const retryAfter = error instanceof ApiRequestError ? error.retryAfter : undefined;
  const [left, setLeft] = useState(() => Math.floor(retryAfter ?? 0));
  useEffect(() => {
    setLeft(Math.floor(retryAfter ?? 0));
  }, [retryAfter]);
  useEffect(() => {
    if (left <= 0) return;
    const id = setInterval(() => setLeft((s) => Math.max(0, s - 1)), 1000);
    return () => clearInterval(id);
  }, [left]);

  const waiting = left > 0;
  return (
    <Card className={cn("w-full max-w-md text-center", className)} role="alert">
      <p className="font-doc text-xl font-semibold">{t.error.rateLimitedTitle}</p>
      <p className="mt-1 text-sm text-muted">{t.error.rateLimitedBody}</p>
      {children}
      <div className="mt-4 flex justify-center">
        <Button size="sm" disabled={waiting || busy} onClick={onRetry}>
          {waiting ? t.error.rateLimitedWait(left) : t.error.rateLimitedRetry}
        </Button>
      </div>
    </Card>
  );
}
