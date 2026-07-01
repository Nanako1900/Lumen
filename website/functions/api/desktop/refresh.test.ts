import { describe, it, expect, afterEach } from "vitest";
import { env } from "cloudflare:test";
import { onRequestPost } from "./refresh";
import { makeContext, jsonPost, stubFetch, idpTokenRoute, fakeJwt } from "../../_lib/testutil";
import { putSession, getSession } from "../../_lib/kv";
import { randomToken } from "../../_lib/pkce";
import type { Env } from "../../_lib/env";

const testEnv = env as unknown as Env;
const URL_REFRESH = "https://test.example/api/desktop/refresh";

let restoreFetch: (() => void) | null = null;
afterEach(() => {
  restoreFetch?.();
  restoreFetch = null;
});

async function seedSession(refreshToken = "refresh-1"): Promise<string> {
  const id = randomToken(48);
  await putSession(testEnv, id, {
    refresh_token: refreshToken,
    sub: "u-1",
    created_at: new Date().toISOString(),
  });
  return id;
}

describe("POST /api/desktop/refresh", () => {
  it("refreshes and returns new access_token + expires_in", async () => {
    const id = await seedSession();
    const stub = stubFetch([
      idpTokenRoute(testEnv.OIDC_TOKEN_URL, {
        access_token: fakeJwt({ sub: "u-1", aud: "lumen-api" }),
        expires_in: 1800,
      }),
    ]);
    restoreFetch = stub.restore;

    const res = await onRequestPost(
      makeContext(jsonPost(URL_REFRESH, { desktop_session_id: id }), testEnv),
    );
    expect(res.status).toBe(200);
    const body = await res.json<{ access_token: string; expires_in: number }>();
    expect(body.access_token).toBeTruthy();
    expect(body.expires_in).toBe(1800);
    // response must not leak refresh_token or session id
    expect(JSON.stringify(body)).not.toContain("refresh");
    expect(JSON.stringify(body)).not.toContain(id);
  });

  it("updates KV when IdP rotates the refresh_token", async () => {
    const id = await seedSession("old-refresh");
    const stub = stubFetch([
      idpTokenRoute(testEnv.OIDC_TOKEN_URL, {
        access_token: fakeJwt({ sub: "u-1" }),
        refresh_token: "new-rotated-refresh",
        expires_in: 900,
      }),
    ]);
    restoreFetch = stub.restore;

    await onRequestPost(makeContext(jsonPost(URL_REFRESH, { desktop_session_id: id }), testEnv));
    const session = await getSession(testEnv, id);
    expect(session!.refresh_token).toBe("new-rotated-refresh");
    expect(session!.sub).toBe("u-1");
  });

  it("does not change KV when IdP omits refresh_token (non-rolling)", async () => {
    const id = await seedSession("keep-refresh");
    const stub = stubFetch([
      idpTokenRoute(testEnv.OIDC_TOKEN_URL, {
        access_token: fakeJwt({ sub: "u-1" }),
        expires_in: 900,
      }),
    ]);
    restoreFetch = stub.restore;

    await onRequestPost(makeContext(jsonPost(URL_REFRESH, { desktop_session_id: id }), testEnv));
    const session = await getSession(testEnv, id);
    expect(session!.refresh_token).toBe("keep-refresh");
  });

  it("returns 401 SESSION_INVALID for unknown session", async () => {
    const res = await onRequestPost(
      makeContext(jsonPost(URL_REFRESH, { desktop_session_id: randomToken(48) }), testEnv),
    );
    expect(res.status).toBe(401);
    const body = await res.json<{ error: { code: string } }>();
    expect(body.error.code).toBe("SESSION_INVALID");
  });

  it("returns 401 SESSION_INVALID and deletes session when IdP rejects refresh", async () => {
    const id = await seedSession("revoked-refresh");
    const stub = stubFetch([idpTokenRoute(testEnv.OIDC_TOKEN_URL, { error: "invalid_grant" }, 400)]);
    restoreFetch = stub.restore;

    const res = await onRequestPost(
      makeContext(jsonPost(URL_REFRESH, { desktop_session_id: id }), testEnv),
    );
    expect(res.status).toBe(401);
    const body = await res.json<{ error: { code: string } }>();
    expect(body.error.code).toBe("SESSION_INVALID");
    // failed session should be purged
    expect(await getSession(testEnv, id)).toBeNull();
  });

  it("falls back to expires_in=300 when IdP omits it", async () => {
    const id = await seedSession();
    const stub = stubFetch([
      idpTokenRoute(testEnv.OIDC_TOKEN_URL, { access_token: fakeJwt({ sub: "u-1" }) }),
    ]);
    restoreFetch = stub.restore;
    const res = await onRequestPost(
      makeContext(jsonPost(URL_REFRESH, { desktop_session_id: id }), testEnv),
    );
    const body = await res.json<{ expires_in: number }>();
    expect(body.expires_in).toBe(300);
  });

  it("returns 400 for missing desktop_session_id", async () => {
    const res = await onRequestPost(makeContext(jsonPost(URL_REFRESH, {}), testEnv));
    expect(res.status).toBe(400);
  });
});
