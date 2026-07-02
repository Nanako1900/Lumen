/**
 * POST /api/desktop/logout （web-design.md §5.1 端点5）
 *
 * 删 SESSIONS，返回 204（幂等：session 不存在也回 204）。
 * 可选：向 IdP token revocation 端点撤销 refresh_token（best-effort）。
 */
import type { Env } from "../../_lib/env";
import type { PagesFunction } from "../../_lib/runtime";
import { badRequest, readJson, readStringField } from "../../_lib/http";
import { isBase64Url } from "../../_lib/pkce";
import { deleteSession, getSession } from "../../_lib/kv";
import { revokeRefreshToken } from "../../_lib/oidc";

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
  if (session) {
    await deleteSession(env, sessionId);
    // best-effort 撤销（仅当配置了 revocation 端点）
    await revokeRefreshToken(env, session.refresh_token);
  }

  // 幂等：无论会话是否存在都回 204
  return new Response(null, { status: 204 });
};
