export const FAKE_PORT: number;
export const FAKE_ORIGIN: string;
export const APP_PORT: number;
export const APP_ORIGIN: string;
export type E2EMode = "local" | "fake-live" | "hosted";
export function liveSafe(env?: Record<string, string | undefined>): boolean;
export function resolveMode(env?: Record<string, string | undefined>): E2EMode;
export function appEnv(mode: E2EMode): Record<string, string>;
export function assertTransport(
  ws: { mail?: string; mail_transport?: string; mode?: string } | null | undefined,
  mode: E2EMode,
): void;
export function assertLiveSafe(
  ws:
    | {
        hosted?: boolean;
        mode?: string;
        llm_mode?: string;
        mail?: string;
        mail_transport?: string;
        claimed?: boolean;
        mail_webhook?: string;
      }
    | null
    | undefined,
): void;
