import { describe, it, expect, afterEach, beforeEach } from "vitest";
import { onRequestGet } from "../../functions/desktop/callback";
import {
  makeContext,
  stubFetch,
  idpTokenRoute,
  idpUserinfoRoute,
  fakeJwt,
  makeEnv,
} from "../_lib/testutil";
import { putLoginContext, consumeHandoff } from "../../functions/_lib/kv";
import { s256 } from "../../functions/_lib/pkce";
import type { Env } from "../../functions/_lib/env";

let testEnv: Env;
beforeEach(() => {
  testEnv = makeEnv(); // 每个用例新建内存 KV，保证隔离
});

let restoreFetch: (() => void) | null = null;
afterEach(() => {
  restoreFetch?.();
  restoreFetch = null;
});

async function seedContext(oidcState: string, opts?: { challenge?: string; redirectUri?: string }) {
  const challenge = opts?.challenge ?? (await s256("desktop-verifier"));
  await putLoginContext(testEnv, oidcState, {
    state: "desktop-state-xyz",
    challenge,
    redirectUri: opts?.redirectUri ?? "http://127.0.0.1:8931/cb",
    oidcVerifier: "oidc-verifier-value",
  });
  return challenge;
}

function callbackRequest(params: Record<string, string>): Request {
  const url = new URL("https://test.example/desktop/callback");
  for (const [k, v] of Object.entries(params)) url.searchParams.set(k, v);
  return new Request(url.toString());
}

describe("GET /desktop/callback", () => {
  it("exchanges code, writes HANDOFF bound to challenge, and 302s handoff_code+state (no access_token in URL)", async () => {
    const oidcState = "oidc-state-happy";
    const challenge = await seedContext(oidcState);

    const stub = stubFetch([
      idpTokenRoute(testEnv.OIDC_TOKEN_URL, {
        access_token: fakeJwt({ sub: "user-1", aud: "lumen-api" }),
        refresh_token: "refresh-abc",
        id_token: fakeJwt({ sub: "user-1", name: "Alice", picture: "https://img/a.png" }),
        expires_in: 3600,
      }),
    ]);
    restoreFetch = stub.restore;

    const res = await onRequestGet(
      makeContext(callbackRequest({ code: "auth-code-1", state: oidcState }), testEnv),
    );

    expect(res.status).toBe(302);
    const loc = new URL(res.headers.get("location")!);
    expect(loc.origin).toBe("http://127.0.0.1:8931");
    const handoffCode = loc.searchParams.get("handoff_code")!;
    expect(handoffCode).toBeTruthy();
    expect(loc.searchParams.get("state")).toBe("desktop-state-xyz");
    // access_token must NOT appear anywhere in the URL
    expect(loc.toString()).not.toContain("access_token");
    expect(loc.searchParams.has("access_token")).toBe(false);

    // HANDOFF written, bound to the desktop challenge（经真实读路径解封内嵌信封）
    const rec = await consumeHandoff(testEnv, handoffCode);
    expect(rec).not.toBeNull();
    expect(rec!.bound_challenge).toBe(challenge);
    expect(rec!.refresh_token).toBe("refresh-abc");
    expect(rec!.sub).toBe("user-1");
    expect(rec!.expires_in).toBe(3600);
    expect(rec!.profile.display_name).toBe("Alice");
    expect(rec!.profile.avatar_url).toBe("https://img/a.png");

    // login context consumed (one-time)
    expect(await testEnv.HANDOFF.get(`ctx:${oidcState}`)).toBeNull();
  });

  it("falls back to userinfo when JWT lacks profile claims", async () => {
    const oidcState = "oidc-state-userinfo";
    await seedContext(oidcState);
    const stub = stubFetch([
      idpTokenRoute(testEnv.OIDC_TOKEN_URL, {
        access_token: fakeJwt({ sub: "user-2" }),
        refresh_token: "refresh-2",
        id_token: fakeJwt({ sub: "user-2" }),
        expires_in: 1200,
      }),
      idpUserinfoRoute(testEnv.OIDC_USERINFO_URL, {
        sub: "user-2",
        preferred_username: "bob",
        picture: "https://img/b.png",
      }),
    ]);
    restoreFetch = stub.restore;

    const res = await onRequestGet(
      makeContext(callbackRequest({ code: "code-2", state: oidcState }), testEnv),
    );
    const handoffCode = new URL(res.headers.get("location")!).searchParams.get("handoff_code")!;
    const rec = await consumeHandoff(testEnv, handoffCode);
    expect(rec!.profile.display_name).toBe("bob");
    expect(rec!.profile.avatar_url).toBe("https://img/b.png");
  });

  it("302s back to loopback with error when IdP token exchange fails", async () => {
    const oidcState = "oidc-state-tokenfail";
    await seedContext(oidcState);
    const stub = stubFetch([idpTokenRoute(testEnv.OIDC_TOKEN_URL, { error: "invalid_grant" }, 400)]);
    restoreFetch = stub.restore;

    const res = await onRequestGet(
      makeContext(callbackRequest({ code: "bad-code", state: oidcState }), testEnv),
    );
    expect(res.status).toBe(302);
    const loc = new URL(res.headers.get("location")!);
    expect(loc.searchParams.get("error")).toBe("token_exchange_failed");
    expect(loc.searchParams.get("state")).toBe("desktop-state-xyz");
    expect(loc.searchParams.has("handoff_code")).toBe(false);
  });

  it("302s back to loopback with error when IdP returns error param", async () => {
    const oidcState = "oidc-state-idperror";
    await seedContext(oidcState);
    const res = await onRequestGet(
      makeContext(callbackRequest({ error: "access_denied", state: oidcState }), testEnv),
    );
    expect(res.status).toBe(302);
    const loc = new URL(res.headers.get("location")!);
    expect(loc.searchParams.get("error")).toBe("access_denied");
  });

  it("returns 400 when state has no stored context", async () => {
    const res = await onRequestGet(
      makeContext(callbackRequest({ code: "c", state: "unknown-state" }), testEnv),
    );
    expect(res.status).toBe(400);
  });
});
