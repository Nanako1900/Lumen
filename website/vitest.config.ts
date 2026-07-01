import { defineWorkersConfig } from "@cloudflare/vitest-pool-workers/config";

// Worker endpoint tests run inside the real workerd runtime via
// @cloudflare/vitest-pool-workers, which provides genuine KV namespaces
// (HANDOFF / SESSIONS) through Miniflare. The IdP token endpoint is mocked
// per-test with fetch stubs (see functions/**/__tests__).
export default defineWorkersConfig({
  test: {
    include: ["functions/**/*.{test,spec}.ts", "test/**/*.{test,spec}.ts"],
    poolOptions: {
      workers: {
        miniflare: {
          compatibilityDate: "2024-11-27",
          compatibilityFlags: ["nodejs_compat"],
          kvNamespaces: ["HANDOFF", "SESSIONS"],
          bindings: {
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
          },
        },
      },
    },
  },
});
