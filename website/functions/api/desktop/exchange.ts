/**
 * POST /api/desktop/exchange （web-design.md §5.1 端点3）
 *
 * 用 handoff_code + handoff_verifier 换 access_token 与 desktop_session_id。
 * 校验 S256(handoff_verifier)==bound_challenge；一次性消费 HANDOFF；写 SESSIONS。
 * 返回 {access_token, expires_in, desktop_session_id, profile}。
 */
import type { Env } from "../../_lib/env";
import type { PagesFunction } from "../../_lib/runtime";
import { badRequest, json, normalizeExpiresIn, notFound, readJson, readStringField } from "../../_lib/http";
import { isBase64Url, randomToken, s256, timingSafeEqual } from "../../_lib/pkce";
import { consumeHandoff, putSession } from "../../_lib/kv";

export const onRequestPost: PagesFunction<Env> = async ({ request, env }) => {
  const body = await readJson(request);
  const handoffCode = readStringField(body, "handoff_code");
  const handoffVerifier = readStringField(body, "handoff_verifier");

  if (!handoffCode || !handoffVerifier) {
    return badRequest("missing handoff_code or handoff_verifier");
  }
  if (!isBase64Url(handoffCode) || !isBase64Url(handoffVerifier)) {
    return badRequest("handoff_code and handoff_verifier must be base64url");
  }

  // 一次性消费：读到即删除（无论后续成败）
  const record = await consumeHandoff(env, handoffCode);
  if (!record) {
    return notFound("HANDOFF_NOT_FOUND", "handoff_code not found, already used, or expired");
  }

  // 校验 S256(handoff_verifier) == bound_challenge（常量时间比较）
  const computed = await s256(handoffVerifier);
  if (!timingSafeEqual(computed, record.bound_challenge)) {
    return badRequest("handoff_verifier does not match bound_challenge", "VERIFIER_MISMATCH");
  }

  // 生成高熵 desktop_session_id（48 字节），写 SESSIONS
  const desktopSessionId = randomToken(48);
  await putSession(env, desktopSessionId, {
    refresh_token: record.refresh_token,
    sub: record.sub,
    created_at: new Date().toISOString(),
  });

  return json(200, {
    access_token: record.access_token,
    expires_in: normalizeExpiresIn(record.expires_in),
    desktop_session_id: desktopSessionId,
    profile: record.profile,
  });
};
