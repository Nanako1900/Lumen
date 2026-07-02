/**
 * GET /api/me （web-design.md §3.2 / §6.2）
 *
 * 账户中心读当前网页会话资料 {display_name, avatar_url}；未登录 → 401。
 * 不调用 Lumen API，仅回显来自 OIDC 的会话内资料。
 */
import type { Env } from "../_lib/env";
import type { PagesFunction } from "../_lib/runtime";
import { json, jsonError } from "../_lib/http";
import { openSession, readSessionCookie } from "../_lib/session";

export const onRequestGet: PagesFunction<Env> = async ({ request, env }) => {
  const cookieValue = readSessionCookie(request);
  const session = cookieValue ? await openSession(env, cookieValue) : null;
  if (!session) {
    return jsonError(401, "UNAUTHENTICATED", "not logged in");
  }
  return json(200, {
    display_name: session.display_name,
    avatar_url: session.avatar_url,
  });
};
