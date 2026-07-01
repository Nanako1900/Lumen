/**
 * PKCE / 高熵随机 / base64url（Web Crypto），web-design.md §5.5。
 *
 * base64url = base64 去填充、`+`→`-`、`/`→`_`（RFC 7636）。
 */

/** 字节数组 → base64url（无填充）。 */
export function base64urlEncode(bytes: Uint8Array): string {
  let binary = "";
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i]!);
  }
  // btoa 在 Workers 运行时可用
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/**
 * 将任意 Uint8Array 复制为 ArrayBuffer 背衬的视图，供 Web Crypto API 使用。
 * 规避 workers-types 中 ArrayBufferLike（含 SharedArrayBuffer）与 BufferSource
 * 的类型不兼容；同时避免把可能共享的底层缓冲区直接传入 crypto。
 */
export function toBytes(view: Uint8Array): Uint8Array<ArrayBuffer> {
  const copy = new Uint8Array(view.length);
  copy.set(view);
  return copy;
}

/** base64url（无填充）→ 字节数组；非法输入抛错。 */
export function base64urlDecode(input: string): Uint8Array<ArrayBuffer> {
  if (!isBase64Url(input)) {
    throw new Error("invalid base64url input");
  }
  const padded = input.replace(/-/g, "+").replace(/_/g, "/");
  const pad = padded.length % 4 === 0 ? "" : "=".repeat(4 - (padded.length % 4));
  const binary = atob(padded + pad);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

/** 是否为合法 base64url 串（无填充、URL-safe 字母表、非空）。 */
export function isBase64Url(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && /^[A-Za-z0-9_-]+$/.test(value);
}

/** S256(verifier) → base64url（无填充），PKCE code_challenge / handoff challenge。 */
export async function s256(verifier: string): Promise<string> {
  const data = toBytes(new TextEncoder().encode(verifier));
  const digest = await crypto.subtle.digest("SHA-256", data);
  return base64urlEncode(new Uint8Array(digest));
}

/** 高熵随机 token（默认 32 字节 → base64url）。用于 handoff_code / session_id / verifier。 */
export function randomToken(bytes = 32): string {
  const b = crypto.getRandomValues(new Uint8Array(bytes));
  return base64urlEncode(b);
}

/**
 * 常量时间字符串比较（防时序侧信道）。
 * 长度不同直接 false；相同长度逐字符异或累加。
 */
export function timingSafeEqual(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let mismatch = 0;
  for (let i = 0; i < a.length; i++) {
    mismatch |= a.charCodeAt(i) ^ b.charCodeAt(i);
  }
  return mismatch === 0;
}
