/**
 * GET /api/download/latest （web-design.md §4.2）
 *
 * 可选 CORS 代理：Worker 服务端侧拉取 chat.example.com/updates/latest.json，
 * 以同源 JSON 返回前端（规避浏览器跨域）。纯只读、无鉴权，不改变 Go 服务端契约。
 */
import type { Env } from "../../_lib/env";
import { json, jsonError } from "../../_lib/http";

export const onRequestGet: PagesFunction<Env> = async ({ env }) => {
  const upstream = env.UPDATES_LATEST_URL;
  if (!upstream) {
    return jsonError(500, "NOT_CONFIGURED", "updates manifest URL not configured");
  }

  let resp: Response;
  try {
    resp = await fetch(upstream, {
      headers: { accept: "application/json" },
      // 与 latest.json 的 no-cache 语义一致：每次读最新
      cf: { cacheTtl: 0, cacheEverything: false },
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
