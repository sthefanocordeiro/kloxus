import { afterEach, describe, expect, it, vi } from "vitest";
import { api, request } from "../api";

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
});
