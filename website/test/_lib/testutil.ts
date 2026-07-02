/**
 * 测试辅助：构造内存 KV 的 Env、PagesFunction 上下文、mock IdP fetch。
 * 运行时无关（纯 Node + 内存 KV，不依赖任何平台运行时）。
 * 仅测试使用（不参与生产构建路由——文件名非端点，且在 _lib/ 下）。
 */
import { vi } from "vitest";
import type { Env } from "../../functions/_lib/env";
import type { EdgeKV, PagesFunction } from "../../functions/_lib/runtime";

/**
 * 内存 EdgeKV（基于 Map），实现 get/put/delete。
 * put 记录但不主动执行 expirationTtl 过期——kv.ts 已内嵌 exp 保证过期语义，
 * 故测试用内存 KV 无需实现原生 TTL。
 */
export function makeMemoryKV(): EdgeKV {
  const store = new Map<string, string>();
  return {
    async get(key: string): Promise<string | null> {
      return store.has(key) ? store.get(key)! : null;
    },
    async put(key: string, value: string, _opts?: { expirationTtl?: number }): Promise<void> {
      store.set(key, value);
    },
    async delete(key: string): Promise<void> {
      store.delete(key);
    },
  };
}

/**
 * 测试用 OIDC 常量（与生产同名字段，值为测试占位）。
 * SESSION_ENC_KEY 为 base64（解码恰为 32 字节，AES-256-GCM）——仅测试用密钥。
 */
const TEST_ENV_BASE: Omit<Env, "HANDOFF" | "SESSIONS"> = {
  OIDC_ISSUER: "https://auth.test.example/realms/lumen",
  OIDC_AUTHORIZE_URL: "https://auth.test.example/realms/lumen/protocol/openid-connect/auth",
  OIDC_TOKEN_URL: "https://auth.test.example/realms/lumen/protocol/openid-connect/token",
  OIDC_USERINFO_URL: "https://auth.test.example/realms/lumen/protocol/openid-connect/userinfo",
  OIDC_CLIENT_ID: "lumen-website",
  OIDC_CLIENT_SECRET: "test-client-secret",
  OIDC_AUDIENCE: "lumen-api",
  OIDC_DESKTOP_REDIRECT_URI: "https://test.example/desktop/callback",
  OIDC_WEB_REDIRECT_URI: "https://test.example/auth/callback",
  WEB_BASE_URL: "https://test.example",
  UPDATES_LATEST_URL: "https://chat.test.example/updates/latest.json",
  // base64 that decodes to exactly 32 bytes (AES-256-GCM) — test key only
  SESSION_ENC_KEY: "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=",
};

/**
 * 构造测试 Env：内置测试 OIDC 常量 + 全新内存 KV（HANDOFF/SESSIONS）。
 * 每次调用新建独立 KV，保证用例间隔离。可用 overrides 覆盖任意字段。
 */
export function makeEnv(overrides?: Partial<Env>): Env {
  return {
    ...TEST_ENV_BASE,
    HANDOFF: makeMemoryKV(),
    SESSIONS: makeMemoryKV(),
    ...overrides,
  };
}

/** 最小 PagesFunctionContext（仅端点用到的字段）。 */
export function makeContext(
  request: Request,
  env: Env,
): Parameters<PagesFunction<Env>>[0] {
  const waitUntilPromises: Promise<unknown>[] = [];
  return {
    request,
    env,
    params: {},
    data: {},
    functionPath: new URL(request.url).pathname,
    waitUntil: (p: Promise<unknown>) => {
      waitUntilPromises.push(p);
    },
    next: async () => new Response(null, { status: 404 }),
  };
}

export interface FetchStubRoute {
  match: (url: string, init?: RequestInit) => boolean;
  respond: (url: string, init?: RequestInit) => Response | Promise<Response>;
}

/**
 * 安装 globalThis.fetch 的 mock，按路由匹配返回。未匹配抛错（暴露漏配）。
 * 返回记录的调用与还原函数。
 */
export function stubFetch(routes: FetchStubRoute[]): {
  calls: { url: string; init?: RequestInit }[];
  restore: () => void;
} {
  const calls: { url: string; init?: RequestInit }[] = [];
  const original = globalThis.fetch;
  const spy = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.toString();
    calls.push({ url, init });
    for (const route of routes) {
      if (route.match(url, init)) return route.respond(url, init);
    }
    throw new Error(`unexpected fetch to ${url}`);
  });
  globalThis.fetch = spy as unknown as typeof fetch;
  return {
    calls,
    restore: () => {
      globalThis.fetch = original;
    },
  };
}

/** 便捷：IdP /token 返回给定 token 响应。 */
export function idpTokenRoute(
  tokenUrl: string,
  body: Record<string, unknown>,
  status = 200,
): FetchStubRoute {
  return {
    match: (url) => url === tokenUrl,
    respond: () =>
      new Response(JSON.stringify(body), {
        status,
        headers: { "content-type": "application/json" },
      }),
  };
}

/** 便捷：IdP /userinfo 返回给定 claims。 */
export function idpUserinfoRoute(
  userinfoUrl: string,
  claims: Record<string, unknown>,
  status = 200,
): FetchStubRoute {
  return {
    match: (url) => url === userinfoUrl,
    respond: () =>
      new Response(JSON.stringify(claims), {
        status,
        headers: { "content-type": "application/json" },
      }),
  };
}

/** 构造一个未签名但结构合法的 JWT（header.payload.sig），payload 为给定 claims。 */
export function fakeJwt(claims: Record<string, unknown>): string {
  const enc = (obj: unknown) =>
    btoa(JSON.stringify(obj)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  return `${enc({ alg: "RS256", typ: "JWT" })}.${enc(claims)}.${enc("sig")}`;
}

/** JSON POST 请求构造。 */
export function jsonPost(url: string, body: unknown): Request {
  return new Request(url, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
}
