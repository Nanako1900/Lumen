/**
 * 回环 redirect_uri 校验（web-design.md §5.1 端点1 / §8.1 安全红线）。
 *
 * 仅允许 http://127.0.0.1:<port>/...：
 *   - scheme 必须 http（回环明文可接受，端口任意）
 *   - host 必须字面 127.0.0.1（拒绝 localhost 主机名，防 DNS 重绑定）
 *   - 拒绝相对/畸形 URL
 */
export function isLoopbackRedirectUri(value: string): boolean {
  const url = safeUrl(value);
  if (!url) return false;
  if (url.protocol !== "http:") return false;
  if (url.hostname !== "127.0.0.1") return false;
  return true;
}

/** 解析 URL；畸形返回 null（不抛错）。 */
export function safeUrl(value: string): URL | null {
  if (typeof value !== "string" || value.length === 0) return null;
  try {
    return new URL(value);
  } catch {
    return null;
  }
}
