import { describe, it, expect, afterEach } from "vitest";
import { env } from "cloudflare:test";
import { onRequestGet } from "./latest";
import { makeContext, stubFetch } from "../../_lib/testutil";
import type { Env } from "../../_lib/env";

const testEnv = env as unknown as Env;
const URL_LATEST = "https://test.example/api/download/latest";

let restoreFetch: (() => void) | null = null;
afterEach(() => {
  restoreFetch?.();
  restoreFetch = null;
});

describe("GET /api/download/latest (CORS proxy)", () => {
  it("proxies the upstream latest.json as same-origin JSON", async () => {
    const manifest = {
      version: "1.2.3",
      platforms: { "windows/amd64": { url: "https://chat.test.example/updates/Lumen-Setup-1.2.3.exe" } },
    };
    const stub = stubFetch([
      {
        match: (url) => url === testEnv.UPDATES_LATEST_URL,
        respond: () =>
          new Response(JSON.stringify(manifest), {
            status: 200,
            headers: { "content-type": "application/json" },
          }),
      },
    ]);
    restoreFetch = stub.restore;

    const res = await onRequestGet(makeContext(new Request(URL_LATEST), testEnv));
    expect(res.status).toBe(200);
    const body = await res.json<typeof manifest>();
    expect(body.version).toBe("1.2.3");
    expect(body.platforms["windows/amd64"].url).toContain("Lumen-Setup-1.2.3.exe");
    expect(stub.calls[0].url).toBe(testEnv.UPDATES_LATEST_URL);
  });

  it("returns 502 when upstream is unreachable", async () => {
    const stub = stubFetch([
      {
        match: (url) => url === testEnv.UPDATES_LATEST_URL,
        respond: () => {
          throw new Error("network down");
        },
      },
    ]);
    restoreFetch = stub.restore;
    const res = await onRequestGet(makeContext(new Request(URL_LATEST), testEnv));
    expect(res.status).toBe(502);
    const body = await res.json<{ error: { code: string } }>();
    expect(body.error.code).toBe("UPSTREAM_UNREACHABLE");
  });

  it("returns 502 when upstream returns an error status", async () => {
    const stub = stubFetch([
      {
        match: (url) => url === testEnv.UPDATES_LATEST_URL,
        respond: () => new Response("nope", { status: 404 }),
      },
    ]);
    restoreFetch = stub.restore;
    const res = await onRequestGet(makeContext(new Request(URL_LATEST), testEnv));
    expect(res.status).toBe(502);
  });
});
