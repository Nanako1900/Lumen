/**
 * POST /auth/logout （web-design.md §6.1）
 *
 * 清网页会话（失效 cookie），返回 204。
 * 会话为 stateless 加密 cookie，故清 cookie 即失效（无 KV 记录需删）。
 */
import type { Env } from "../_lib/env";
import type { PagesFunction } from "../_lib/runtime";
import { clearSessionCookie } from "../_lib/session";

export const onRequestPost: PagesFunction<Env> = async () => {
  return new Response(null, {
    status: 204,
    headers: { "set-cookie": clearSessionCookie() },
  });
};
