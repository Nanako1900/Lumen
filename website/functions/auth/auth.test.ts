import { describe, it, expect, afterEach } from "vitest";
import { env } from "cloudflare:test";
import { onRequestGet as authLogin } from "./login";
import { onRequestGet as authCallback } from "./callback";
import { onRequestPost as authLogout } from "./logout";
import { onRequestGet as apiMe } from "../api/me";
import {
  makeContext,
  stubFetch,
  idpTokenRoute,
  idpUserinfoRoute,
  fakeJwt,
} from "../_lib/testutil";
import {
  AUTH_FLOW_COOKIE,
  SESSION_COOKIE,
  sealAuthFlow,
  buildSessionCookie,
  sealSession,
  defaultSessionExp,
  defaultAuthFlowExp,
} from "../_lib/session";
import type { Env } from "../_lib/env";

const testEnv = env as unknown as Env;

let restoreFetch: (() => void) | null = null;
afterEach(() => {
  restoreFetch?.();
  restoreFetch = null;
});

/** Extract a cookie value from one or more Set-Cookie header lines. */
function getSetCookie(res: Response, name: string): string | null {
  const all = res.headers.getSetCookie?.() ?? [res.headers.get("set-cookie") ?? ""];
  for (const line of all) {
    const m = line.match(new RegExp(`(?:^|; )${name}=([^;]*)`));
    if (m && m[1]) return m[1];
  }
  return null;
}

describe("GET /auth/login", () => {
  it("302s to IdP with scope 'openid profile email' (no offline_access, no audience) and sets flow cookie", async () => {
    const res = await authLogin(makeContext(new Request("https://test.example/auth/login"), testEnv));
    expect(res.status).toBe(302);
    const loc = new URL(res.headers.get("location")!);
    expect(loc.origin + loc.pathname).toBe(testEnv.OIDC_AUTHORIZE_URL);
    expect(loc.searchParams.get("scope")).toBe("openid profile email");
    expect(loc.searchParams.get("audience")).toBeNull();
    expect(loc.searchParams.get("redirect_uri")).toBe(testEnv.OIDC_WEB_REDIRECT_URI);
    expect(loc.searchParams.get("code_challenge_method")).toBe("S256");
    const flow = getSetCookie(res, AUTH_FLOW_COOKIE);
    expect(flow).toBeTruthy();
  });
});

describe("GET /auth/callback", () => {
  it("exchanges code, sets httpOnly session cookie, and 302s to /account", async () => {
    const state = "web-state-1";
    const flowCookie = await sealAuthFlow(testEnv, {
      verifier: "web-verifier-1",
      state,
      exp: defaultAuthFlowExp(),
    });
    const stub = stubFetch([
      idpTokenRoute(testEnv.OIDC_TOKEN_URL, {
        access_token: fakeJwt({ sub: "web-user" }),
        id_token: fakeJwt({ sub: "web-user", name: "Carol", picture: "https://img/c.png" }),
        expires_in: 3600,
      }),
    ]);
    restoreFetch = stub.restore;

    const url = new URL("https://test.example/auth/callback");
    url.searchParams.set("code", "web-code");
    url.searchParams.set("state", state);
    const req = new Request(url.toString(), { headers: { cookie: `${AUTH_FLOW_COOKIE}=${flowCookie}` } });
    const res = await authCallback(makeContext(req, testEnv));

    expect(res.status).toBe(302);
    expect(new URL(res.headers.get("location")!).pathname).toBe("/account");
    const sessionValue = getSetCookie(res, SESSION_COOKIE);
    expect(sessionValue).toBeTruthy();
    // full Set-Cookie for session must include HttpOnly
    const lines = res.headers.getSetCookie?.() ?? [];
    const sessionLine = lines.find((l) => l.startsWith(`${SESSION_COOKIE}=`));
    expect(sessionLine).toContain("HttpOnly");
    expect(sessionLine).toContain("Secure");
    expect(sessionLine).toContain("SameSite=Lax");
  });

  it("does not connect to Lumen API (only IdP token + userinfo are fetched)", async () => {
    const state = "web-state-2";
    const flowCookie = await sealAuthFlow(testEnv, { verifier: "v", state, exp: defaultAuthFlowExp() });
    const stub = stubFetch([
      idpTokenRoute(testEnv.OIDC_TOKEN_URL, { access_token: fakeJwt({ sub: "u" }), id_token: fakeJwt({ sub: "u" }) }),
      idpUserinfoRoute(testEnv.OIDC_USERINFO_URL, { sub: "u", name: "Dan" }),
    ]);
    restoreFetch = stub.restore;

    const url = new URL("https://test.example/auth/callback");
    url.searchParams.set("code", "c");
    url.searchParams.set("state", state);
    await authCallback(
      makeContext(new Request(url.toString(), { headers: { cookie: `${AUTH_FLOW_COOKIE}=${flowCookie}` } }), testEnv),
    );
    // every fetched host must be the IdP, never chat.example (Lumen API)
    for (const call of stub.calls) {
      expect(call.url).not.toContain("chat.");
      expect(new URL(call.url).origin).toBe(new URL(testEnv.OIDC_ISSUER).origin);
    }
  });

  it("302s to /account?error on state mismatch", async () => {
    const flowCookie = await sealAuthFlow(testEnv, {
      verifier: "v",
      state: "real-state",
      exp: defaultAuthFlowExp(),
    });
    const url = new URL("https://test.example/auth/callback");
    url.searchParams.set("code", "c");
    url.searchParams.set("state", "attacker-state");
    const res = await authCallback(
      makeContext(new Request(url.toString(), { headers: { cookie: `${AUTH_FLOW_COOKIE}=${flowCookie}` } }), testEnv),
    );
    expect(res.status).toBe(302);
    expect(new URL(res.headers.get("location")!).searchParams.get("error")).toBe("state_mismatch");
  });

  it("302s to /account?error when no flow cookie present", async () => {
    const url = new URL("https://test.example/auth/callback");
    url.searchParams.set("code", "c");
    url.searchParams.set("state", "s");
    const res = await authCallback(makeContext(new Request(url.toString()), testEnv));
    expect(res.status).toBe(302);
    expect(new URL(res.headers.get("location")!).searchParams.get("error")).toBe("invalid_flow");
  });
});

describe("GET /api/me", () => {
  it("returns 401 when not logged in", async () => {
    const res = await apiMe(makeContext(new Request("https://test.example/api/me"), testEnv));
    expect(res.status).toBe(401);
    const body = await res.json<{ error: { code: string } }>();
    expect(body.error.code).toBe("UNAUTHENTICATED");
  });

  it("returns {display_name, avatar_url} for a valid session cookie", async () => {
    const sessionValue = await sealSession(testEnv, {
      sub: "u",
      display_name: "Eve",
      avatar_url: "https://img/e.png",
      exp: defaultSessionExp(),
    });
    const req = new Request("https://test.example/api/me", {
      headers: { cookie: buildSessionCookie(sessionValue).split(";")[0] },
    });
    const res = await apiMe(makeContext(req, testEnv));
    expect(res.status).toBe(200);
    const body = await res.json<{ display_name: string; avatar_url: string }>();
    expect(body.display_name).toBe("Eve");
    expect(body.avatar_url).toBe("https://img/e.png");
  });
});

describe("POST /auth/logout", () => {
  it("clears the session cookie and returns 204", async () => {
    const res = await authLogout(makeContext(new Request("https://test.example/auth/logout", { method: "POST" }), testEnv));
    expect(res.status).toBe(204);
    expect(res.headers.get("set-cookie")).toContain("Max-Age=0");
  });
});
