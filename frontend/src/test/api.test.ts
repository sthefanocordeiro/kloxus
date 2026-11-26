import { afterEach, describe, expect, it, vi } from "vitest";
import { api, ApiRequestError, parseRetryAfter, request } from "../api";

// The Go API rejects any non-GET/HEAD call under /api/v1 without
// Content-Type: application/json (415 unsupported_media_type). Body-less
// mutations like sourceDelete must still send the header — see KLOX-17.

function mockFetch() {
  const spy = vi.fn().mockResolvedValue(
    new Response(JSON.stringify({ ok: true }), { status: 200 }),
  );
  vi.stubGlobal("fetch", spy);
  return spy;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("request", () => {
  it("sends Content-Type: application/json on body-less mutating calls", async () => {
    const spy = mockFetch();
    for (const method of ["POST", "PUT", "PATCH", "DELETE"]) {
      spy.mockClear();
      await request(method, "/api/v1/x");
      const init = spy.mock.calls[0][1] as RequestInit;
      expect(new Headers(init.headers).get("Content-Type")).toBe("application/json");
    }
  });

  it("does not force a Content-Type on GET", async () => {
    const spy = mockFetch();
    await request("GET", "/api/v1/x");
    const init = spy.mock.calls[0][1] as RequestInit;
    expect(new Headers(init.headers).get("Content-Type")).toBeNull();
  });

  it("sourceDelete issues DELETE with the JSON content type", async () => {
    const spy = mockFetch();
    await api.sourceDelete(3, 42);
    const [path, init] = spy.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/v1/publications/3/sources/42");
    expect(init.method).toBe("DELETE");
    expect(new Headers(init.headers).get("Content-Type")).toBe("application/json");
  });

  // KLOX-194: a 429 surfaces as ApiRequestError carrying the Retry-After
  // seconds — even when the body isn't the JSON envelope (an HTML
  // error page must not break the parse).
  it("carries Retry-After on a 429, JSON or not", async () => {
    const spy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: { code: "rate_limited", message: "slow down" } }), {
        status: 429,
        headers: { "Retry-After": "120" },
      }),
    );
    vi.stubGlobal("fetch", spy);
    await expect(request("GET", "/api/v1/x")).rejects.toMatchObject({
      status: 429,
      retryAfter: 120,
      body: { code: "rate_limited" },
    });

    spy.mockResolvedValue(new Response("<html>rate limited</html>", {
      status: 429,
      headers: { "Retry-After": "30", "Content-Type": "text/html" },
    }));
    await expect(request("GET", "/api/v1/x")).rejects.toMatchObject({ status: 429, retryAfter: 30 });

    spy.mockResolvedValue(new Response(JSON.stringify({ error: { code: "rate_limited", message: "x" } }), { status: 429 }));
    await expect(request("GET", "/api/v1/x")).rejects.toMatchObject({ status: 429, retryAfter: undefined });
  });
});

describe("parseRetryAfter", () => {
  it("reads delta-seconds and rejects junk", () => {
    expect(parseRetryAfter("45")).toBe(45);
    expect(parseRetryAfter(" 7 ")).toBe(7);
    expect(parseRetryAfter(null)).toBeUndefined();
    expect(parseRetryAfter("")).toBeUndefined();
    expect(parseRetryAfter("garbage")).toBeUndefined();
    expect(parseRetryAfter("-3")).toBeUndefined();
  });

  it("reads an HTTP-date as seconds-from-now", () => {
    const at = new Date(Date.now() + 60_000).toUTCString();
    const s = parseRetryAfter(at);
    expect(s).toBeGreaterThan(50);
    expect(s).toBeLessThanOrEqual(60);
  });

  it("caps absurd waits", () => {
    expect(parseRetryAfter("999999")).toBe(3600);
  });
});

describe("ApiRequestError", () => {
  it("isRateLimited-compatible: status lives on the error", () => {
    expect(new ApiRequestError(429, { code: "rate_limited", message: "x" }).status).toBe(429);
  });
});
