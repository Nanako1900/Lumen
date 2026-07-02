/**
 * POST /api/desktop/refresh （web-design.md §5.1 端点4）
 *
 * 用 desktop_session_id 刷新 access_token。查 SESSIONS → 用 client_secret 刷新 →
 * IdP 轮换 refresh_token 则更新 KV → 返回 {access_token, expires_in}。
 * session 不存在/IdP 拒绝 → 401 SESSION_INVALID（并删除失效会话）。
 */
import type { Env } from "../../_lib/env";
import type { PagesFunction } from "../../_lib/runtime";
import { badRequest, json, jsonError, normalizeExpiresIn, readJson, readStringField } from "../../_lib/http";
import { isBase64Url } from "../../_lib/pkce";
import { deleteSession, getSession, putSession } from "../../_lib/kv";
import { refreshWithIdp } from "../../_lib/oidc";

export const onRequestPost: PagesFunction<Env> = async ({ request, env }) => {
  const body = await readJson(request);
  const sessionId = readStringField(body, "desktop_session_id");
  if (!sessionId) {
    return badRequest("missing desktop_session_id");
  }
  if (!isBase64Url(sessionId)) {
    return badRequest("desktop_session_id must be base64url");
  }

  const session = await getSession(env, sessionId);
  if (!session) {
    return jsonError(401, "SESSION_INVALID", "session expired or revoked");
  }

  const token = await refreshWithIdp(env, session.refresh_token);
  if (!token) {
    // IdP 拒绝 refresh → 删除失效会话并回 SESSION_INVALID（桌面转重新登录）
    await deleteSession(env, sessionId);
    return jsonError(401, "SESSION_INVALID", "refresh rejected by identity provider");
  }

  // IdP 轮换 refresh_token 则更新 KV（refresh_token 不出服务端）
  if (token.refresh_token && token.refresh_token !== session.refresh_token) {
    await putSession(env, sessionId, { ...session, refresh_token: token.refresh_token });
  }

  return json(200, {
    access_token: token.access_token,
    expires_in: normalizeExpiresIn(token.expires_in),
  });
};
