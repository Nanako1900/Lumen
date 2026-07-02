import { describe, it, expect, afterEach } from "vitest";
import {
  buildAuthorizeUrl,
  subjectFrom,
  profileFromClaims,
  profileFromJwt,
  exchangeAuthCode,
  refreshWithIdp,
} from "../../functions/_lib/oidc";
import { fakeJwt, stubFetch, idpTokenRoute, makeEnv } from "./testutil";

const testEnv = makeEnv();

let restoreFetch: (() => void) | null = null;
afterEach(() => {
  restoreFetch?.();
  restoreFetch = null;
});

describe("buildAuthorizeUrl", () => {
  it("includes all required OAuth2 + PKCE params", () => {
    const url = new URL(
      buildAuthorizeUrl(testEnv, {
        codeChallenge: "challenge-value",
        state: "state-value",
        redirectUri: "https://test.example/desktop/callback",
        scope: "openid profile email offline_access",
        audience: "lumen-api",
      }),
    );
    expect(url.searchParams.get("response_type")).toBe("code");
    expect(url.searchParams.get("client_id")).toBe(testEnv.OIDC_CLIENT_ID);
    expect(url.searchParams.get("redirect_uri")).toBe("https://test.example/desktop/callback");
    expect(url.searchParams.get("scope")).toBe("openid profile email offline_access");
    expect(url.searchParams.get("state")).toBe("state-value");
    expect(url.searchParams.get("code_challenge")).toBe("challenge-value");
    expect(url.searchParams.get("code_challenge_method")).toBe("S256");
    expect(url.searchParams.get("audience")).toBe("lumen-api");
    expect(url.searchParams.get("resource")).toBe("lumen-api");
  });

  it("omits audience/resource when not provided (account center)", () => {
    const url = new URL(
      buildAuthorizeUrl(testEnv, {
        codeChallenge: "c",
        state: "s",
        redirectUri: testEnv.OIDC_WEB_REDIRECT_URI,
        scope: "openid profile email",
      }),
    );
    expect(url.searchParams.get("audience")).toBeNull();
    expect(url.searchParams.get("resource")).toBeNull();
  });
});

describe("subjectFrom", () => {
  it("prefers id_token sub, falls back to access_token", () => {
    expect(subjectFrom(fakeJwt({ sub: "from-id" }), fakeJwt({ sub: "from-access" }))).toBe("from-id");
    expect(subjectFrom(undefined, fakeJwt({ sub: "from-access" }))).toBe("from-access");
  });
  it("returns empty string when no sub present", () => {
    expect(subjectFrom(undefined, undefined)).toBe("");
    expect(subjectFrom("not-a-jwt")).toBe("");
  });
});

describe("profileFromClaims / profileFromJwt", () => {
  it("maps name → display_name and picture → avatar_url", () => {
    expect(profileFromClaims({ name: "Alice", picture: "https://img/a" })).toEqual({
      display_name: "Alice",
      avatar_url: "https://img/a",
    });
  });
  it("falls back to preferred_username then nickname", () => {
    expect(profileFromClaims({ preferred_username: "bob" }).display_name).toBe("bob");
    expect(profileFromClaims({ nickname: "bobby" }).display_name).toBe("bobby");
  });
  it("reads profile from a JWT payload", () => {
    const p = profileFromJwt(fakeJwt({ name: "Carol", picture: "https://img/c" }));
    expect(p?.display_name).toBe("Carol");
  });
});

describe("exchangeAuthCode / refreshWithIdp", () => {
  it("returns token on 200 and null on IdP error", async () => {
    const stub = stubFetch([
      idpTokenRoute(testEnv.OIDC_TOKEN_URL, { access_token: "at", expires_in: 3600 }),
    ]);
    restoreFetch = stub.restore;
    const tok = await exchangeAuthCode(testEnv, "code", "verifier", testEnv.OIDC_DESKTOP_REDIRECT_URI);
    expect(tok?.access_token).toBe("at");
    // client_secret must be sent to the IdP token endpoint (form body)
    const body = stub.calls[0].init?.body as URLSearchParams;
    expect(body.get("client_secret")).toBe(testEnv.OIDC_CLIENT_SECRET);
    expect(body.get("grant_type")).toBe("authorization_code");
  });

  it("refreshWithIdp returns null when IdP rejects", async () => {
    const stub = stubFetch([idpTokenRoute(testEnv.OIDC_TOKEN_URL, { error: "invalid_grant" }, 400)]);
    restoreFetch = stub.restore;
    expect(await refreshWithIdp(testEnv, "refresh")).toBeNull();
  });

  it("returns null when token response lacks access_token", async () => {
    const stub = stubFetch([idpTokenRoute(testEnv.OIDC_TOKEN_URL, { not_a_token: true })]);
    restoreFetch = stub.restore;
    expect(await refreshWithIdp(testEnv, "refresh")).toBeNull();
  });
});
