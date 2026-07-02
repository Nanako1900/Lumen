/**
 * GET /desktop/login （web-design.md §5.1 端点1）
 *
 * 校验回环 redirect_uri（仅 127.0.0.1），暂存登录上下文，302 到 IdP /authorize。
 * scope=openid profile email offline_access；令 access_token 的 aud 含 lumen-api。
 */
import type { Env } from "../_lib/env";
import type { PagesFunction } from "../_lib/runtime";
import { badRequest } from "../_lib/http";
import { isLoopbackRedirectUri } from "../_lib/loopback";
import { buildAuthorizeUrl } from "../_lib/oidc";
import { isBase64Url, randomToken, s256 } from "../_lib/pkce";
import { putLoginContext } from "../_lib/kv";

const DESKTOP_SCOPE = "openid profile email offline_access";

export const onRequestGet: PagesFunction<Env> = async ({ request, env }) => {
  const url = new URL(request.url);
  const redirectUri = url.searchParams.get("redirect_uri") ?? "";
  const state = url.searchParams.get("state") ?? "";
  const challenge = url.searchParams.get("challenge") ?? "";

  // 1) 回环校验：仅允许 http://127.0.0.1:<port>/...（拒 localhost）
  if (!isLoopbackRedirectUri(redirectUri)) {
    return badRequest("redirect_uri must be an http://127.0.0.1:<port>/... loopback URI");
  }
  // 校验 state 与 challenge（challenge = S256(handoff_verifier)，base64url）
  if (!state || state.length > 512) {
    return badRequest("missing or invalid state");
  }
  if (!isBase64Url(challenge)) {
    return badRequest("challenge must be a base64url S256 value");
  }

  // 2) 官网自建 OIDC PKCE + state'
  const oidcVerifier = randomToken();
  const oidcChallenge = await s256(oidcVerifier);
  const oidcState = randomToken();

  // 3) 暂存上下文（KV，短 TTL），键为 oidc_state
  await putLoginContext(env, oidcState, {
    state,
    challenge,
    redirectUri,
    oidcVerifier,
  });

  // 4) 302 到 IdP /authorize（带 aud=lumen-api、offline_access）
  const authorizeUrl = buildAuthorizeUrl(env, {
    codeChallenge: oidcChallenge,
    state: oidcState,
    redirectUri: env.OIDC_DESKTOP_REDIRECT_URI,
    scope: DESKTOP_SCOPE,
    audience: env.OIDC_AUDIENCE,
  });

  return Response.redirect(authorizeUrl, 302);
};
