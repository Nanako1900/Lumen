/**
 * KV 读写封装：HANDOFF（一次性短期）与 SESSIONS（桌面长期会话），
 * web-design.md §5.4 / §8.3。
 */
import type { Env } from "./env";

// --- 登录上下文（/desktop/login 暂存，/desktop/callback 取回）---
export interface LoginContext {
  state: string; // 桌面侧不透明 state（回环校验用）
  challenge: string; // S256(handoff_verifier)，桌面 PKCE challenge
  redirectUri: string; // 已校验的 127.0.0.1 回环地址
  oidcVerifier: string; // 官网自建 OIDC PKCE verifier
}

// --- HANDOFF 值（一次性消费）---
export interface HandoffRecord {
  access_token: string;
  expires_in: number;
  refresh_token: string;
  sub: string;
  bound_challenge: string; // = 桌面 challenge
  profile: DesktopProfile;
}

export interface DesktopProfile {
  display_name: string;
  avatar_url: string;
}

// --- SESSIONS 值（桌面长期会话）---
export interface SessionRecord {
  refresh_token: string; // 不出 Cloudflare
  sub: string;
  created_at: string;
}

const LOGIN_CTX_TTL = 600; // 暂存上下文 ≈600s（web-design.md §8.3）
const HANDOFF_TTL = 120; // handoff_code ≈120s（一次性消费）

const ctxKey = (oidcState: string) => `ctx:${oidcState}`;

/** 暂存登录上下文（键为 oidc_state），短 TTL。 */
export async function putLoginContext(
  env: Env,
  oidcState: string,
  ctx: LoginContext,
): Promise<void> {
  await env.HANDOFF.put(ctxKey(oidcState), JSON.stringify(ctx), {
    expirationTtl: LOGIN_CTX_TTL,
  });
}

/** 取回并删除登录上下文（一次性）；缺失/损坏返回 null。 */
export async function takeLoginContext(
  env: Env,
  oidcState: string,
): Promise<LoginContext | null> {
  const key = ctxKey(oidcState);
  const raw = await env.HANDOFF.get(key);
  if (!raw) return null;
  await env.HANDOFF.delete(key); // 一次性取回
  return safeParse<LoginContext>(raw);
}

/** 写 HANDOFF：handoff_code → token set（绑 bound_challenge），TTL≈120s。 */
export async function putHandoff(
  env: Env,
  handoffCode: string,
  record: HandoffRecord,
): Promise<void> {
  await env.HANDOFF.put(handoffCode, JSON.stringify(record), {
    expirationTtl: HANDOFF_TTL,
  });
}

/**
 * 一次性消费 HANDOFF：读到即删除（无论后续成败），防重放。
 * 不存在/过期返回 null。
 */
export async function consumeHandoff(
  env: Env,
  handoffCode: string,
): Promise<HandoffRecord | null> {
  const raw = await env.HANDOFF.get(handoffCode);
  if (!raw) return null;
  await env.HANDOFF.delete(handoffCode); // 一次性消费
  return safeParse<HandoffRecord>(raw);
}

/** 写 SESSIONS：desktop_session_id → {refresh_token, sub, created_at}，无 TTL。 */
export async function putSession(
  env: Env,
  sessionId: string,
  record: SessionRecord,
): Promise<void> {
  await env.SESSIONS.put(sessionId, JSON.stringify(record));
}

/** 读 SESSIONS；不存在/损坏返回 null。 */
export async function getSession(
  env: Env,
  sessionId: string,
): Promise<SessionRecord | null> {
  const raw = await env.SESSIONS.get(sessionId);
  if (!raw) return null;
  return safeParse<SessionRecord>(raw);
}

/** 删 SESSIONS（logout / refresh 失败清理）。 */
export async function deleteSession(env: Env, sessionId: string): Promise<void> {
  await env.SESSIONS.delete(sessionId);
}

function safeParse<T>(raw: string): T | null {
  try {
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}
