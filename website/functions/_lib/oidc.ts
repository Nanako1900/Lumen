/**
 * OIDC 交互：authorize URL 构造、authorization_code / refresh_token 交换、
 * JWT subject 解析、userinfo 资料兜底（web-design.md §5 / §6 / §7.2）。
 *
 * client_secret 仅在此模块经 env 使用，绝不进任何响应/URL。
 */
import type { Env } from "./env";
import type { DesktopProfile } from "./kv";

export interface TokenResponse {
  access_token: string;
  refresh_token?: string;
  id_token?: string;
  expires_in?: number;
  token_type?: string;
}

/** authorize URL 构造参数。 */
export interface AuthorizeParams {
  codeChallenge: string; // S256(oidc_verifier)
  state: string; // oidc_state
  redirectUri: string; // 桌面中介 / 账户中心回调
  scope: string; // "openid profile email offline_access" | "openid profile email"
  audience?: string; // 令 access_token 的 aud 含 lumen-api（依 IdP 约定）
}

/**
 * 构造 IdP /authorize 302 目标 URL（Auth Code + PKCE，S256）。
 *
 * aud=lumen-api 依 IdP 约定：多数 IdP（Auth0/Logto）用 `audience`/`resource` 参数；
 * Keycloak 经 client scope/audience mapper（此时 audience 参数可忽略但无害）。
 * 因此这里附加 `audience` 参数而非写死到某个字面，保持可配置。
 */
export function buildAuthorizeUrl(env: Env, params: AuthorizeParams): string {
  const url = new URL(env.OIDC_AUTHORIZE_URL);
  url.searchParams.set("response_type", "code");
  url.searchParams.set("client_id", env.OIDC_CLIENT_ID);
  url.searchParams.set("redirect_uri", params.redirectUri);
  url.searchParams.set("scope", params.scope);
  url.searchParams.set("state", params.state);
  url.searchParams.set("code_challenge", params.codeChallenge);
  url.searchParams.set("code_challenge_method", "S256");
  if (params.audience) {
    // Auth0/Logto 风格；Keycloak 忽略此参数（用 audience mapper 达成 aud）
    url.searchParams.set("audience", params.audience);
    url.searchParams.set("resource", params.audience);
  }
  return url.toString();
}

/** 用 client_secret + verifier 换 token（grant_type=authorization_code）。 */
export async function exchangeAuthCode(
  env: Env,
  code: string,
  codeVerifier: string,
  redirectUri: string,
): Promise<TokenResponse | null> {
  const body = new URLSearchParams({
    grant_type: "authorization_code",
    code,
    redirect_uri: redirectUri,
    client_id: env.OIDC_CLIENT_ID,
    client_secret: env.OIDC_CLIENT_SECRET,
    code_verifier: codeVerifier,
  });
  return postToken(env, body);
}

/** 用 client_secret + refresh_token 刷新（grant_type=refresh_token）。 */
export async function refreshWithIdp(
  env: Env,
  refreshToken: string,
): Promise<TokenResponse | null> {
  const body = new URLSearchParams({
    grant_type: "refresh_token",
    refresh_token: refreshToken,
    client_id: env.OIDC_CLIENT_ID,
    client_secret: env.OIDC_CLIENT_SECRET,
  });
  return postToken(env, body);
}

/** 可选：向 IdP token revocation 端点撤销 refresh_token（best-effort，不阻塞 logout）。 */
export async function revokeRefreshToken(env: Env, refreshToken: string): Promise<void> {
  // revocation 端点非必配；仅当明确提供时才尝试，失败静默（logout 已删 KV）。
  const revocationUrl = (env as unknown as { OIDC_REVOCATION_URL?: string }).OIDC_REVOCATION_URL;
  if (!revocationUrl) return;
  try {
    await fetch(revocationUrl, {
      method: "POST",
      headers: { "content-type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        token: refreshToken,
        token_type_hint: "refresh_token",
        client_id: env.OIDC_CLIENT_ID,
        client_secret: env.OIDC_CLIENT_SECRET,
      }),
    });
  } catch {
    // best-effort：忽略撤销失败
  }
}

async function postToken(env: Env, body: URLSearchParams): Promise<TokenResponse | null> {
  let resp: Response;
  try {
    resp = await fetch(env.OIDC_TOKEN_URL, {
      method: "POST",
      headers: {
        "content-type": "application/x-www-form-urlencoded",
        accept: "application/json",
      },
      body,
    });
  } catch {
    return null; // 网络错误
  }
  if (!resp.ok) return null; // IdP 拒绝（4xx/5xx）
  let data: unknown;
  try {
    data = await resp.json();
  } catch {
    return null;
  }
  if (!isTokenResponse(data)) return null;
  return data;
}

function isTokenResponse(v: unknown): v is TokenResponse {
  return (
    typeof v === "object" &&
    v !== null &&
    typeof (v as Record<string, unknown>).access_token === "string" &&
    (v as Record<string, unknown>).access_token !== ""
  );
}

/**
 * 从 id_token（优先）或 access_token 的 JWT payload 解析 sub。
 * 仅解析 payload 取 sub（不验签——签名验证是 Go 服务端的职责，见 §7.1）；
 * 解析失败返回空串，由调用方决定兜底。
 */
export function subjectFrom(...jwts: (string | undefined)[]): string {
  for (const jwt of jwts) {
    const sub = subFromJwt(jwt);
    if (sub) return sub;
  }
  return "";
}

function subFromJwt(jwt: string | undefined): string {
  if (!jwt) return "";
  const parts = jwt.split(".");
  if (parts.length < 2) return "";
  try {
    const payloadJson = decodeJwtSegment(parts[1]!);
    const payload = JSON.parse(payloadJson) as Record<string, unknown>;
    return typeof payload.sub === "string" ? payload.sub : "";
  } catch {
    return "";
  }
}

/** 从 id_token / access_token 的 claims 提取资料（无需网络的首选）。 */
export function profileFromJwt(...jwts: (string | undefined)[]): DesktopProfile | null {
  for (const jwt of jwts) {
    const claims = claimsFromJwt(jwt);
    if (!claims) continue;
    const profile = profileFromClaims(claims);
    if (profile.display_name || profile.avatar_url) return profile;
  }
  return null;
}

function claimsFromJwt(jwt: string | undefined): Record<string, unknown> | null {
  if (!jwt) return null;
  const parts = jwt.split(".");
  if (parts.length < 2) return null;
  try {
    return JSON.parse(decodeJwtSegment(parts[1]!)) as Record<string, unknown>;
  } catch {
    return null;
  }
}

function decodeJwtSegment(segment: string): string {
  const padded = segment.replace(/-/g, "+").replace(/_/g, "/");
  const pad = padded.length % 4 === 0 ? "" : "=".repeat(4 - (padded.length % 4));
  return atob(padded + pad);
}

/**
 * 取资料：优先 id_token/access_token claims，缺失则 /userinfo 兜底。
 * 任一失败均回退为空字段（不阻断登录）。
 */
export async function fetchProfile(
  env: Env,
  accessToken: string,
  idToken?: string,
): Promise<DesktopProfile> {
  const fromJwt = profileFromJwt(idToken, accessToken);
  if (fromJwt && fromJwt.display_name && fromJwt.avatar_url) return fromJwt;

  const fromUserinfo = await fetchUserinfo(env, accessToken);
  return {
    display_name: fromJwt?.display_name || fromUserinfo?.display_name || "",
    avatar_url: fromJwt?.avatar_url || fromUserinfo?.avatar_url || "",
  };
}

async function fetchUserinfo(env: Env, accessToken: string): Promise<DesktopProfile | null> {
  if (!env.OIDC_USERINFO_URL) return null;
  try {
    const resp = await fetch(env.OIDC_USERINFO_URL, {
      headers: { authorization: `Bearer ${accessToken}`, accept: "application/json" },
    });
    if (!resp.ok) return null;
    const claims = (await resp.json()) as Record<string, unknown>;
    return profileFromClaims(claims);
  } catch {
    return null;
  }
}

/**
 * 从 OIDC 标准 claims 归一化为 {display_name, avatar_url}。
 * display_name 优先 name → preferred_username → nickname；avatar_url 取 picture。
 */
export function profileFromClaims(claims: Record<string, unknown>): DesktopProfile {
  const str = (k: string): string => (typeof claims[k] === "string" ? (claims[k] as string) : "");
  return {
    display_name: str("name") || str("preferred_username") || str("nickname"),
    avatar_url: str("picture"),
  };
}
