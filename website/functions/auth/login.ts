/**
 * GET /auth/login （web-design.md §6.1）
 *
 * 账户中心登录：生成 OIDC PKCE + state，暂存（加密 cookie，短 TTL），
 * 302 到 IdP /authorize。scope=openid profile email（无 offline_access、无 aud=lumen-api，
 * 账户中心不调 Lumen API）。
 */
import type { Env } from "../_lib/env";
import { buildAuthorizeUrl } from "../_lib/oidc";
import { randomToken, s256 } from "../_lib/pkce";
import {
  buildAuthFlowCookie,
  defaultAuthFlowExp,
  sealAuthFlow,
} from "../_lib/session";

const WEB_SCOPE = "openid profile email";

export const onRequestGet: PagesFunction<Env> = async ({ env }) => {
  const verifier = randomToken();
  const challenge = await s256(verifier);
  const state = randomToken();

  const flowCookie = await sealAuthFlow(env, {
    verifier,
    state,
    exp: defaultAuthFlowExp(),
  });

  const authorizeUrl = buildAuthorizeUrl(env, {
    codeChallenge: challenge,
    state,
    redirectUri: env.OIDC_WEB_REDIRECT_URI,
    scope: WEB_SCOPE,
    // 账户中心不请求 aud=lumen-api（不调 Lumen API）
  });

  return new Response(null, {
    status: 302,
    headers: {
      location: authorizeUrl,
      "set-cookie": buildAuthFlowCookie(flowCookie),
    },
  });
};
