/**
 * 统一 JSON 响应、错误信封与输入解析（web-design.md §8.2）。
 *
 * 错误信封形状（全端点一致）：
 *   { "error": { "code": "...", "message": "..." } }
 * 不回显 token / secret / 堆栈。
 */

const JSON_HEADERS: Record<string, string> = {
  "content-type": "application/json; charset=utf-8",
  // 端点不缓存（含敏感响应体）
  "cache-control": "no-store",
};

/** 成功 JSON 响应。 */
export function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: JSON_HEADERS });
}

/** 错误信封响应。 */
export function jsonError(status: number, code: string, message: string): Response {
  return json(status, { error: { code, message } });
}

export function badRequest(message: string, code = "BAD_REQUEST"): Response {
  return jsonError(400, code, message);
}

export function notFound(code = "NOT_FOUND", message = "resource not found"): Response {
  return jsonError(404, code, message);
}

export function methodNotAllowed(): Response {
  return jsonError(405, "METHOD_NOT_ALLOWED", "method not allowed");
}

/** 安全解析 JSON body；非法/超大返回 null（由调用方转 400）。 */
export async function readJson<T = Record<string, unknown>>(
  request: Request,
  maxBytes = 8 * 1024,
): Promise<T | null> {
  const contentType = request.headers.get("content-type") ?? "";
  if (!contentType.toLowerCase().includes("application/json")) return null;
  const raw = await request.text();
  if (raw.length === 0 || raw.length > maxBytes) return null;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

/**
 * expires_in 归一化（web-design.md §5 端点3/4 约定）：
 * 必须为正整数秒；IdP 未返回/≤0/非法 → 回退保守默认。
 */
export function normalizeExpiresIn(value: unknown, fallbackSeconds = 300): number {
  const n = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(n)) return fallbackSeconds;
  const floored = Math.floor(n);
  return floored > 0 ? floored : fallbackSeconds;
}

/** 从对象读必填字符串字段；缺失/非字符串/超长返回 null。 */
export function readStringField(
  body: Record<string, unknown> | null,
  key: string,
  maxLen = 4096,
): string | null {
  if (!body) return null;
  const v = body[key];
  if (typeof v !== "string" || v.length === 0 || v.length > maxLen) return null;
  return v;
}
