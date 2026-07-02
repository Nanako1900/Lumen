/**
 * EdgeOne Pages Functions 运行时垫片（本地最小类型，不依赖任何 vendor 类型包）。
 *
 * 目的：把「运行时/平台」相关的类型隔离在此单文件，业务与端点只 import 本文件，
 * 从而 KV / 上下文注入的具体形态若与假设不同，仅需改本文件与各 handler 的导出包装。
 *
 * // ✅ 已核实（EdgeOne 官方文档，2026-07）：EdgeOne Pages Functions 采用 functions/ 文件路由
 * //（扁平文件亦可，如 auth/login.ts → /auth/login），支持命名导出 onRequestGet/onRequestPost/…
 * //（及 catch-all onRequest）；KV 与环境变量经 context.env 注入。与本项目现有写法一致。
 */

/**
 * KV 命名空间的最小接口（HANDOFF / SESSIONS 用到的子集）。
 * 只声明本项目实际调用的方法，避免绑定到某个 vendor 的完整 KV 类型。
 *
 * // ✅ 已核实（EdgeOne 官方文档，2026-07）：KV 提供 get/put/delete；put(key,value) 无原生 TTL，
 * // 过期由 kv.ts「值内嵌 exp + 读取时惰性删除」保证（expirationTtl 仅为占位，EdgeOne 会忽略）。
 */
export interface EdgeKV {
  get(key: string): Promise<string | null>;
  put(key: string, value: string, opts?: { expirationTtl?: number }): Promise<void>;
  delete(key: string): Promise<void>;
}

/**
 * Pages Function 处理器类型（保留 onRequestGet / onRequestPost 写法）。
 * 上下文字段取业务实际用到的最小集合（request / env / params / waitUntil / next / data / functionPath）。
 *
 * // ✅ 已核实（EdgeOne 官方文档，2026-07）：context 提供 request / env / params / waitUntil；
 * // 本项目端点仅用 request 与 env（KV 经 env 注入），next / data / functionPath 为兼容占位。
 */
export type PagesFunction<E> = (context: {
  request: Request;
  env: E;
  params: Record<string, string>;
  waitUntil(p: Promise<unknown>): void;
  next(): Promise<Response>;
  data: Record<string, unknown>;
  functionPath: string;
}) => Response | Promise<Response>;
