/**
 * Pages Function 环境变量与 KV 绑定类型（web-design.md §9）。
 *
 * Secret（OIDC_CLIENT_SECRET / SESSION_ENC_KEY）与非机密 env 同型注入运行时，
 * 但仅服务端可读；前端产物绝不包含任何 secret。
 */
import type { EdgeKV } from "./runtime";

export interface Env {
  // KV 命名空间绑定（EdgeOne KV，经 env 注入）
  HANDOFF: EdgeKV;
  SESSIONS: EdgeKV;

  // IdP（OAuth2/OIDC）端点
  OIDC_ISSUER: string;
  OIDC_AUTHORIZE_URL: string;
  OIDC_TOKEN_URL: string;
  OIDC_USERINFO_URL: string;

  // 官网 OIDC client（confidential）
  OIDC_CLIENT_ID: string;
  OIDC_CLIENT_SECRET: string; // Secret：仅服务端（Pages Function）
  OIDC_AUDIENCE: string; // 令 access_token 的 aud 含此值

  // redirect_uri（IdP 登记）
  OIDC_DESKTOP_REDIRECT_URI: string;
  OIDC_WEB_REDIRECT_URI: string;

  // 官网基址 / 下载清单
  WEB_BASE_URL: string;
  UPDATES_LATEST_URL: string;

  // 网页会话 cookie 加密/签名密钥（账户中心）
  SESSION_ENC_KEY: string; // Secret：仅服务端（Pages Function）
}

/** 缺失/空环境变量时抛错，避免用空串静默调用 IdP。 */
export function requireEnv<K extends keyof Env>(env: Env, key: K): string {
  const value = env[key];
  if (typeof value !== "string" || value.length === 0) {
    throw new Error(`missing required environment variable: ${String(key)}`);
  }
  return value;
}
