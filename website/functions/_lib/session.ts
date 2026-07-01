/**
 * 账户中心网页会话：AES-GCM 加密 cookie（stateless），web-design.md §6.2。
 *
 * cookie：httpOnly + Secure + SameSite=Lax，值为加密 payload（含 sub/资料/exp）。
 * 网页会话**不持久化 refresh_token**（账户中心无离线刷新需求，§6.1）。
 * 用加密 cookie 而非新 KV 命名空间：契约 §9.2 仅定义 HANDOFF/SESSIONS 两个命名空间。
 */
import type { Env } from "./env";
import { base64urlDecode, base64urlEncode, toBytes } from "./pkce";

export const SESSION_COOKIE = "lumen_session";
const DEFAULT_MAX_AGE = 60 * 60 * 8; // 8 小时

export interface WebSession {
  sub: string;
  display_name: string;
  avatar_url: string;
  exp: number; // epoch 秒（服务端权威过期时间）
}

/** 从 SESSION_ENC_KEY（base64）导入 AES-GCM 密钥（要求 32 字节）。 */
async function importKey(env: Env): Promise<CryptoKey> {
  const rawKey = decodeKey(env.SESSION_ENC_KEY);
  if (rawKey.length !== 32) {
    throw new Error("SESSION_ENC_KEY must decode to 32 bytes (AES-256-GCM)");
  }
  return crypto.subtle.importKey("raw", rawKey, { name: "AES-GCM" }, false, [
    "encrypt",
    "decrypt",
  ]);
}

/** 支持 base64 / base64url 的密钥解码。 */
function decodeKey(value: string): Uint8Array<ArrayBuffer> {
  const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
  const pad = normalized.length % 4 === 0 ? "" : "=".repeat(4 - (normalized.length % 4));
  const binary = atob(normalized + pad);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

/** 加密会话为 cookie 值（iv.ciphertext，均 base64url）。 */
export async function sealSession(env: Env, session: WebSession): Promise<string> {
  const key = await importKey(env);
  const iv = toBytes(crypto.getRandomValues(new Uint8Array(12)));
  const plaintext = toBytes(new TextEncoder().encode(JSON.stringify(session)));
  const ciphertext = await crypto.subtle.encrypt({ name: "AES-GCM", iv }, key, plaintext);
  return `${base64urlEncode(iv)}.${base64urlEncode(new Uint8Array(ciphertext))}`;
}

/** 解密 cookie 值为会话；非法/篡改/过期返回 null。 */
export async function openSession(env: Env, cookieValue: string): Promise<WebSession | null> {
  const dot = cookieValue.indexOf(".");
  if (dot <= 0) return null;
  const ivPart = cookieValue.slice(0, dot);
  const ctPart = cookieValue.slice(dot + 1);
  let iv: Uint8Array<ArrayBuffer>;
  let ciphertext: Uint8Array<ArrayBuffer>;
  try {
    iv = base64urlDecode(ivPart);
    ciphertext = base64urlDecode(ctPart);
  } catch {
    return null;
  }
  if (iv.length !== 12) return null;
  try {
    const key = await importKey(env);
    const plaintext = await crypto.subtle.decrypt({ name: "AES-GCM", iv }, key, ciphertext);
    const session = JSON.parse(new TextDecoder().decode(plaintext)) as WebSession;
    if (typeof session.exp !== "number" || session.exp <= nowSeconds()) return null;
    return session;
  } catch {
    return null; // GCM 认证失败（篡改）或 JSON 损坏
  }
}

/** 读取请求 cookie 中的会话值。 */
export function readSessionCookie(request: Request): string | null {
  const header = request.headers.get("cookie");
  if (!header) return null;
  for (const part of header.split(";")) {
    const [name, ...rest] = part.trim().split("=");
    if (name === SESSION_COOKIE) return rest.join("=");
  }
  return null;
}

/** 构造 Set-Cookie（httpOnly + Secure + SameSite=Lax + Path=/）。 */
export function buildSessionCookie(value: string, maxAgeSeconds = DEFAULT_MAX_AGE): string {
  return [
    `${SESSION_COOKIE}=${value}`,
    "Path=/",
    "HttpOnly",
    "Secure",
    "SameSite=Lax",
    `Max-Age=${maxAgeSeconds}`,
  ].join("; ");
}

/** 构造清除会话的 Set-Cookie（Max-Age=0）。 */
export function clearSessionCookie(): string {
  return [
    `${SESSION_COOKIE}=`,
    "Path=/",
    "HttpOnly",
    "Secure",
    "SameSite=Lax",
    "Max-Age=0",
  ].join("; ");
}

export function defaultSessionExp(maxAgeSeconds = DEFAULT_MAX_AGE): number {
  return nowSeconds() + maxAgeSeconds;
}

// --- 账户中心 OIDC 登录流程上下文（/auth/login → /auth/callback）---
// 用短 TTL 加密 cookie 暂存 verifier/state，避免为网页登录再开 KV 命名空间。

export const AUTH_FLOW_COOKIE = "lumen_auth_flow";
const AUTH_FLOW_MAX_AGE = 600; // 10 分钟

export interface AuthFlowContext {
  verifier: string; // OIDC PKCE verifier
  state: string; // OIDC state（回调校验）
  exp: number;
}

export async function sealAuthFlow(env: Env, ctx: AuthFlowContext): Promise<string> {
  return sealSession(env, ctx as unknown as WebSession);
}

export async function openAuthFlow(
  env: Env,
  cookieValue: string,
): Promise<AuthFlowContext | null> {
  const opened = (await openSession(env, cookieValue)) as unknown as AuthFlowContext | null;
  if (!opened || typeof opened.verifier !== "string" || typeof opened.state !== "string") {
    return null;
  }
  return opened;
}

export function readAuthFlowCookie(request: Request): string | null {
  const header = request.headers.get("cookie");
  if (!header) return null;
  for (const part of header.split(";")) {
    const [name, ...rest] = part.trim().split("=");
    if (name === AUTH_FLOW_COOKIE) return rest.join("=");
  }
  return null;
}

export function buildAuthFlowCookie(value: string): string {
  return [
    `${AUTH_FLOW_COOKIE}=${value}`,
    "Path=/",
    "HttpOnly",
    "Secure",
    "SameSite=Lax",
    `Max-Age=${AUTH_FLOW_MAX_AGE}`,
  ].join("; ");
}

export function clearAuthFlowCookie(): string {
  return [`${AUTH_FLOW_COOKIE}=`, "Path=/", "HttpOnly", "Secure", "SameSite=Lax", "Max-Age=0"].join(
    "; ",
  );
}

export function defaultAuthFlowExp(): number {
  return nowSeconds() + AUTH_FLOW_MAX_AGE;
}

function nowSeconds(): number {
  return Math.floor(Date.now() / 1000);
}
