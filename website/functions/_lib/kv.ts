/**
 * KV 读写封装：HANDOFF（一次性短期）与 SESSIONS（桌面长期会话），
 * web-design.md §5.4 / §8.3。
 *
 * 关键设计（EdgeOne 移植）：值内嵌过期时间，使「过期」正确性不依赖平台原生 TTL。
 * 存储时把记录包一层 `{ v: <record>, exp: <number|null> }`（exp = nowSec + ttl；无 ttl 则 null）；
 * 读取时若 `exp && nowSec > exp` 则立即 delete 并返回 null。
 * 仍把 `expirationTtl` 传给 put 作为原生 TTL 兜底——
 * // EDGEONE-VERIFY: EdgeOne KV 若不支持 put 的 expirationTtl（原生 TTL），该选项会被忽略，
 * // 但值内嵌 exp 已保证过期语义正确；若 EdgeOne 支持原生 TTL 则两者叠加、以先到者为准。
 */
import type { Env } from "./env";
import type { EdgeKV } from "./runtime";

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
  refresh_token: string; // 不出服务端
  sub: string;
  created_at: string;
}

const LOGIN_CTX_TTL = 600; // 暂存上下文 ≈600s（web-design.md §8.3）
const HANDOFF_TTL = 120; // handoff_code ≈120s（一次性消费）

const ctxKey = (oidcState: string) => `ctx:${oidcState}`;

/** 当前秒（服务端权威时间）。 */
function nowSeconds(): number {
  return Math.floor(Date.now() / 1000);
}

/** 内嵌过期的 KV 记录信封：v 为业务值，exp 为绝对过期秒（null 表示不过期）。 */
interface Envelope<T> {
  v: T;
  exp: number | null;
}

/**
 * 写入 KV：把记录包一层 { v, exp }，exp = nowSec + ttl（无 ttl 则 null）；
 * 仍把 expirationTtl 传给底层 put 作为原生 TTL 兜底。
 */
async function putEnvelope<T>(
  kv: EdgeKV,
  key: string,
  value: T,
  ttlSeconds?: number,
): Promise<void> {
  const exp = ttlSeconds && ttlSeconds > 0 ? nowSeconds() + ttlSeconds : null;
  const envelope: Envelope<T> = { v: value, exp };
  const opts = ttlSeconds && ttlSeconds > 0 ? { expirationTtl: ttlSeconds } : undefined;
  await kv.put(key, JSON.stringify(envelope), opts);
}

/**
 * 读取 KV：解封信封；若已过期（exp 且 nowSec > exp）则删除并返回 null。
 * 兼容旧值/损坏值：非信封结构或解析失败一律返回 null。
 */
async function getEnvelope<T>(kv: EdgeKV, key: string): Promise<T | null> {
  const raw = await kv.get(key);
  if (!raw) return null;
  const envelope = safeParse<Envelope<T>>(raw);
  if (!envelope || typeof envelope !== "object" || !("v" in envelope)) return null;
  if (envelope.exp !== null && envelope.exp !== undefined && nowSeconds() > envelope.exp) {
    await kv.delete(key); // 内嵌过期：读到过期即清除
    return null;
  }
  return envelope.v;
}

/** 暂存登录上下文（键为 oidc_state），短 TTL。 */
export async function putLoginContext(
  env: Env,
  oidcState: string,
  ctx: LoginContext,
): Promise<void> {
  await putEnvelope(env.HANDOFF, ctxKey(oidcState), ctx, LOGIN_CTX_TTL);
}

/** 取回并删除登录上下文（一次性）；缺失/损坏/过期返回 null。 */
export async function takeLoginContext(
  env: Env,
  oidcState: string,
): Promise<LoginContext | null> {
  const key = ctxKey(oidcState);
  const ctx = await getEnvelope<LoginContext>(env.HANDOFF, key);
  if (!ctx) return null;
  await env.HANDOFF.delete(key); // 一次性取回
  return ctx;
}

/** 写 HANDOFF：handoff_code → token set（绑 bound_challenge），TTL≈120s。 */
export async function putHandoff(
  env: Env,
  handoffCode: string,
  record: HandoffRecord,
): Promise<void> {
  await putEnvelope(env.HANDOFF, handoffCode, record, HANDOFF_TTL);
}

/**
 * 一次性消费 HANDOFF：读到即删除（无论后续成败），防重放。
 * 不存在/过期返回 null。
 */
export async function consumeHandoff(
  env: Env,
  handoffCode: string,
): Promise<HandoffRecord | null> {
  const record = await getEnvelope<HandoffRecord>(env.HANDOFF, handoffCode);
  if (!record) return null;
  await env.HANDOFF.delete(handoffCode); // 一次性消费
  return record;
}

/** 写 SESSIONS：desktop_session_id → {refresh_token, sub, created_at}，无 TTL。 */
export async function putSession(
  env: Env,
  sessionId: string,
  record: SessionRecord,
): Promise<void> {
  await putEnvelope(env.SESSIONS, sessionId, record); // 无 TTL → exp=null
}

/** 读 SESSIONS；不存在/损坏返回 null。 */
export async function getSession(
  env: Env,
  sessionId: string,
): Promise<SessionRecord | null> {
  return getEnvelope<SessionRecord>(env.SESSIONS, sessionId);
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
