/**
 * 前端可配置项（构建期经 Vite 环境变量注入，均为**非机密**）。
 * 域名 example.com 为占位，保持可配置——绝不硬编码真实域名。
 * 前端配置绝不含任何 secret（client_secret 仅在 Worker）。
 */

/** 下载清单直连地址（前端优先直连，跨域回退 Worker 代理）。 */
export const UPDATES_LATEST_URL: string =
  import.meta.env.VITE_UPDATES_LATEST_URL ?? "https://chat.example.com/updates/latest.json";

/** 官网基址（展示用）。 */
export const WEB_BASE_URL: string =
  import.meta.env.VITE_WEB_BASE_URL ?? "https://example.com";
