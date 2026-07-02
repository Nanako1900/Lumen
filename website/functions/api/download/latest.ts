/**
 * GET /api/download/latest （web-design.md §4.2）
 *
 * 可选 CORS 代理：Worker 服务端侧拉取 chat.example.com/updates/latest.json，
 * 以同源 JSON 返回前端（规避浏览器跨域）。纯只读、无鉴权，不改变 Go 服务端契约。
 */
import type { Env } from "../../_lib/env";
import type { PagesFunction } from "../../_lib/runtime";
import { json, jsonError } from "../../_lib/http";

export const onRequestGet: PagesFunction<Env> = async ({ env }) => {
  const upstream = env.UPDATES_LATEST_URL;
  if (!upstream) {
    return jsonError(500, "NOT_CONFIGURED", "updates manifest URL not configured");
  }

  let resp: Response;
  try {
    // 与 latest.json 的 no-cache 语义一致：每次读最新。
    // cache: "no-store" 为标准 fetch 选项（跨运行时通用）。
    // EDGEONE-VERIFY: 若 EdgeOne 提供平台特有的边缘缓存控制（如原 Cloudflare 的 cf 选项），
    // 可在此按需附加；标准 no-store 已表达「不缓存、每次读最新」的意图。
    resp = await fetch(upstream, {
      headers: { accept: "application/json" },
      cache: "no-store",
    });
  } catch {
    return jsonError(502, "UPSTREAM_UNREACHABLE", "failed to fetch updates manifest");
  }

  if (!resp.ok) {
    return jsonError(502, "UPSTREAM_ERROR", `updates manifest returned ${resp.status}`);
  }

  let manifest: unknown;
  try {
    manifest = await resp.json();
  } catch {
    return jsonError(502, "UPSTREAM_INVALID", "updates manifest is not valid JSON");
  }

  // 同源 JSON 返回；前端每次进入下载页读取最新清单
  return json(200, manifest);
};
