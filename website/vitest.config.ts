import { defineConfig } from "vitest/config";

// 运行时无关的纯 vitest（node 环境）+ 内存 KV。
// 端点/单测直接以标准 Web API（Request/Response/crypto.subtle/fetch/TextEncoder/
// btoa/atob，均由 Node 20 原生全局提供）运行；KV 用 functions/_lib/testutil.ts 的
// 内存 EdgeKV（基于 Map）注入 makeEnv()；IdP token/userinfo 端点用 fetch stub mock。
// 不依赖任何平台运行时（无 workerd / miniflare / @cloudflare/*）。
export default defineConfig({
  test: {
    environment: "node",
    include: ["functions/**/*.{test,spec}.ts", "test/**/*.{test,spec}.ts"],
    globals: true,
  },
});
