/**
 * GET /desktop/callback （web-design.md §5.1 端点2）
 *
 * IdP 回调：用 client_secret 换 token，写 KV HANDOFF（绑 bound_challenge），
 * 302 回 redirect_uri?handoff_code&state。失败 302 回 redirect_uri?error=&state。
 * access_token 绝不进 URL。
 */
import type { Env } from "../_lib/env";
import type { PagesFunction } from "../_lib/runtime";
import { badRequest } from "../_lib/http";
import { normalizeExpiresIn } from "../_lib/http";
import { exchangeAuthCode, fetchProfile, subjectFrom } from "../_lib/oidc";
import { randomToken } from "../_lib/pkce";
import { putHandoff, takeLoginContext } from "../_lib/kv";
import { safeUrl } from "../_lib/loopback";

export const onRequestGet: PagesFunction<Env> = async ({ request, env }) => {
  const url = new URL(request.url);
  const code = url.searchParams.get("code");
  const oidcState = url.searchParams.get("state") ?? "";
  const idpError = url.searchParams.get("error");

  // 1) 取回并删除暂存上下文（一次性）；校验 state 一致性
  const ctx = oidcState ? await takeLoginContext(env, oidcState) : null;
  if (!ctx) {
    // 无有效上下文可回退到桌面回环 → 只能返回 400（不知道回环地址）
    return badRequest("invalid or expired state");
  }

  const back = safeUrl(ctx.redirectUri);
  if (!back) {
    return badRequest("stored redirect_uri is invalid");
  }

  // IdP 直接返回 error（用户取消/授权失败）→ 302 回回环带 error
  if (idpError) {
    return redirectError(back, idpError, ctx.state);
  }
  if (!code) {
    return redirectError(back, "missing_code", ctx.state);
  }

  // 2) 用 client_secret + oidc_verifier 换 token
  const token = await exchangeAuthCode(
    env,
    code,
    ctx.oidcVerifier,
    env.OIDC_DESKTOP_REDIRECT_URI,
  );
  if (!token) {
    // IdP token 交换失败 → 302 回回环带 error（便于桌面回环页展示）
    return redirectError(back, "token_exchange_failed", ctx.state);
  }

  // 3) 解析 sub + 资料
  const sub = subjectFrom(token.id_token, token.access_token);
  const profile = await fetchProfile(env, token.access_token, token.id_token);

  // 4) 生成一次性 handoff_code，写 HANDOFF（绑 bound_challenge=桌面 challenge）
  const handoffCode = randomToken();
  await putHandoff(env, handoffCode, {
    access_token: token.access_token,
    expires_in: normalizeExpiresIn(token.expires_in),
    refresh_token: token.refresh_token ?? "",
    sub,
    bound_challenge: ctx.challenge,
    profile,
  });

  // 5) 302 回回环：仅带 handoff_code + 原桌面 state（access_token 绝不进 URL）
  back.searchParams.set("handoff_code", handoffCode);
  back.searchParams.set("state", ctx.state);
  return Response.redirect(back.toString(), 302);
};

function redirectError(back: URL, errorCode: string, state: string): Response {
  const target = new URL(back.toString());
  target.searchParams.set("error", errorCode);
  target.searchParams.set("state", state);
  return Response.redirect(target.toString(), 302);
}
