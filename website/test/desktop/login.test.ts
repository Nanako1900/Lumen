import { describe, it, expect, beforeEach } from "vitest";
import { onRequestGet } from "../../functions/desktop/login";
import { makeContext, makeEnv } from "../_lib/testutil";
import { s256 } from "../../functions/_lib/pkce";
import { takeLoginContext } from "../../functions/_lib/kv";
import type { Env } from "../../functions/_lib/env";

let testEnv: Env;
beforeEach(() => {
  testEnv = makeEnv(); // 每个用例新建内存 KV，保证隔离
});

function loginRequest(params: Record<string, string>): Request {
  const url = new URL("https://test.example/desktop/login");
  for (const [k, v] of Object.entries(params)) url.searchParams.set(k, v);
  return new Request(url.toString());
}

async function validChallenge(): Promise<string> {
  return s256("desktop-handoff-verifier-value");
}

describe("GET /desktop/login", () => {
  it("302s to the IdP authorize endpoint with PKCE + audience + offline_access", async () => {
    const challenge = await validChallenge();
    const req = loginRequest({
      redirect_uri: "http://127.0.0.1:8931/cb",
      state: "desktop-state-123",
      challenge,
    });
    const res = await onRequestGet(makeContext(req, testEnv));

    expect(res.status).toBe(302);
    const loc = res.headers.get("location")!;
    const authUrl = new URL(loc);
    expect(authUrl.origin + authUrl.pathname).toBe(testEnv.OIDC_AUTHORIZE_URL);
    expect(authUrl.searchParams.get("response_type")).toBe("code");
    expect(authUrl.searchParams.get("client_id")).toBe(testEnv.OIDC_CLIENT_ID);
    expect(authUrl.searchParams.get("redirect_uri")).toBe(testEnv.OIDC_DESKTOP_REDIRECT_URI);
    expect(authUrl.searchParams.get("code_challenge_method")).toBe("S256");
    expect(authUrl.searchParams.get("code_challenge")).toBeTruthy();
    expect(authUrl.searchParams.get("scope")).toBe("openid profile email offline_access");
    // aud=lumen-api carried via audience/resource (依 IdP 约定)
    expect(authUrl.searchParams.get("audience")).toBe("lumen-api");
    expect(authUrl.searchParams.get("state")).toBeTruthy();
  });

  it("stashes login context in KV keyed by oidc_state", async () => {
    const challenge = await validChallenge();
    const req = loginRequest({
      redirect_uri: "http://127.0.0.1:9000/cb",
      state: "desktop-state-abc",
      challenge,
    });
    const res = await onRequestGet(makeContext(req, testEnv));
    const oidcState = new URL(res.headers.get("location")!).searchParams.get("state")!;

    // 经真实读路径取回暂存上下文（KV 值内嵌过期信封由 takeLoginContext 解封）
    const ctx = await takeLoginContext(testEnv, oidcState);
    expect(ctx).not.toBeNull();
    expect(ctx!.state).toBe("desktop-state-abc");
    expect(ctx!.challenge).toBe(challenge);
    expect(ctx!.redirectUri).toBe("http://127.0.0.1:9000/cb");
    expect(typeof ctx!.oidcVerifier).toBe("string");
    expect(ctx!.oidcVerifier.length).toBeGreaterThan(20);
  });

  it("rejects non-loopback redirect_uri with 400", async () => {
    const challenge = await validChallenge();
    const req = loginRequest({
      redirect_uri: "http://localhost:8931/cb",
      state: "s",
      challenge,
    });
    const res = await onRequestGet(makeContext(req, testEnv));
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error: { code: string } };
    expect(body.error.code).toBe("BAD_REQUEST");
  });

  it("rejects missing state", async () => {
    const challenge = await validChallenge();
    const req = loginRequest({ redirect_uri: "http://127.0.0.1:8931/cb", challenge });
    const res = await onRequestGet(makeContext(req, testEnv));
    expect(res.status).toBe(400);
  });

  it("rejects non-base64url challenge", async () => {
    const req = loginRequest({
      redirect_uri: "http://127.0.0.1:8931/cb",
      state: "s",
      challenge: "has=padding/and+chars",
    });
    const res = await onRequestGet(makeContext(req, testEnv));
    expect(res.status).toBe(400);
  });
});
