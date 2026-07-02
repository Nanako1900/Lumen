/**
 * GET /auth/callback （web-design.md §6.1）
 *
 * 账户中心 OIDC 回调：校验 state；用 client_secret + verifier 换 token；
 * 解析 {sub, display_name, avatar_url}；建立最小网页会话（httpOnly+Secure+SameSite cookie，
 * 不持久化 refresh_token）；302 回 /account。失败 302 回 /account?error=。
 */
import type { Env } from "../_lib/env";
import type { PagesFunction } from "../_lib/runtime";
import { exchangeAuthCode, fetchProfile, subjectFrom } from "../_lib/oidc";
import { timingSafeEqual } from "../_lib/pkce";
import {
  buildSessionCookie,
  clearAuthFlowCookie,
  defaultSessionExp,
  openAuthFlow,
  readAuthFlowCookie,
  sealSession,
} from "../_lib/session";

export const onRequestGet: PagesFunction<Env> = async ({ request, env }) => {
  const url = new URL(request.url);
  const code = url.searchParams.get("code");
  const state = url.searchParams.get("state") ?? "";
  const idpError = url.searchParams.get("error");

  const flowValue = readAuthFlowCookie(request);
  const flow = flowValue ? await openAuthFlow(env, flowValue) : null;

  // 无有效流程上下文 → 回 /account?error（并清流程 cookie）
  if (!flow) {
    return redirectAccount(env, "invalid_flow");
  }
  if (idpError) {
    return redirectAccount(env, idpError);
  }
  if (!code || !timingSafeEqual(state, flow.state)) {
    return redirectAccount(env, "state_mismatch");
  }

  const token = await exchangeAuthCode(env, code, flow.verifier, env.OIDC_WEB_REDIRECT_URI);
  if (!token) {
    return redirectAccount(env, "token_exchange_failed");
  }

  const sub = subjectFrom(token.id_token, token.access_token);
  const profile = await fetchProfile(env, token.access_token, token.id_token);

  // 建立最小网页会话（不含 refresh_token / access_token）
  const sessionCookie = await sealSession(env, {
    sub,
    display_name: profile.display_name,
    avatar_url: profile.avatar_url,
    exp: defaultSessionExp(),
  });

  const headers = new Headers();
  headers.append("location", new URL("/account", env.WEB_BASE_URL).toString());
  headers.append("set-cookie", buildSessionCookie(sessionCookie));
  headers.append("set-cookie", clearAuthFlowCookie()); // 用完清流程 cookie
  return new Response(null, { status: 302, headers });
};

function redirectAccount(env: Env, errorCode: string): Response {
  const target = new URL("/account", env.WEB_BASE_URL);
  target.searchParams.set("error", errorCode);
  const headers = new Headers();
  headers.append("location", target.toString());
  headers.append("set-cookie", clearAuthFlowCookie());
  return new Response(null, { status: 302, headers });
}
