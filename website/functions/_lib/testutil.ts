/**
 * 测试辅助：构造 PagesFunction 上下文、mock IdP fetch。
 * 仅测试使用（不参与生产构建路由——文件名非端点，且在 _lib/ 下）。
 */
import { vi } from "vitest";
import type { Env } from "./env";

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
    passThroughOnException: () => {},
    next: async () => new Response(null, { status: 404 }),
  } as unknown as Parameters<PagesFunction<Env>>[0];
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
