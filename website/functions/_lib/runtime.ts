/**
 * EdgeOne Pages Functions 运行时垫片（本地最小类型，不依赖任何 vendor 类型包）。
 *
 * 目的：把「运行时/平台」相关的类型隔离在此单文件，业务与端点只 import 本文件，
 * 从而 KV / 上下文注入的具体形态若与假设不同，仅需改本文件与各 handler 的导出包装。
 *
 * // EDGEONE-VERIFY: EdgeOne Pages Functions 是否为 onRequest* 导出 + functions 文件路由，
 * // 且 context.env 注入 KV/vars；若不同，仅需改本文件与各 handler 的导出包装。
 */

/**
 * KV 命名空间的最小接口（HANDOFF / SESSIONS 用到的子集）。
 * 只声明本项目实际调用的方法，避免绑定到某个 vendor 的完整 KV 类型。
 *
 * // EDGEONE-VERIFY: EdgeOne KV 的方法名/签名（get/put/delete、put 的 TTL 选项）
 * // 以 EdgeOne 官方 KV SDK 为准；如有差异，仅改此接口与 kv.ts 适配层。
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
 * // EDGEONE-VERIFY: context 的字段与注入方式以 EdgeOne 运行时为准；本项目端点仅用到
 * // request 与 env（KV 经 env 注入），其余字段为兼容占位。
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
