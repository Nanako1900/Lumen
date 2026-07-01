/**
 * 前端 fetch（同源 /auth/*、/api/*）。所有请求 same-origin、credentials:include
 * 以携带 httpOnly 会话 cookie。前端绝不接触 access_token / client_secret。
 */

export interface MeResponse {
  display_name: string;
  avatar_url: string;
}

export interface UpdateManifest {
  version: string;
  notes?: string;
  pub_date?: string;
  platforms: Record<string, { url: string; signature?: string }>;
}

const WINDOWS_PLATFORM = "windows/amd64";

/** 读取账户中心会话资料；未登录返回 null（401）。 */
export async function fetchMe(signal?: AbortSignal): Promise<MeResponse | null> {
  const res = await fetch("/api/me", {
    credentials: "include",
    headers: { accept: "application/json" },
    signal,
  });
  if (res.status === 401) return null;
  if (!res.ok) throw new Error(`/api/me failed: ${res.status}`);
  return (await res.json()) as MeResponse;
}

/** 触发账户中心登录（整页跳转到 Worker 端点）。 */
export function goToLogin(): void {
  window.location.assign("/auth/login");
}

/** 退出登录（POST /auth/logout）。 */
export async function logout(): Promise<void> {
  await fetch("/auth/logout", { method: "POST", credentials: "include" });
}

/**
 * 读取下载清单：先直连 chat.example.com/updates/latest.json（公开 GET），
 * 跨域失败则回退官网 Worker 代理 /api/download/latest。
 * updatesUrl 为可配置的直连地址（由构建期注入或默认占位）。
 */
export async function fetchLatestManifest(
  updatesUrl: string,
  signal?: AbortSignal,
): Promise<UpdateManifest> {
  // 优先直连（若 Go 服务端放行 CORS）
  try {
    const direct = await fetch(updatesUrl, { headers: { accept: "application/json" }, signal });
    if (direct.ok) return (await direct.json()) as UpdateManifest;
  } catch {
    // 跨域/网络失败 → 回退代理
  }
  const proxied = await fetch("/api/download/latest", {
    headers: { accept: "application/json" },
    signal,
  });
  if (!proxied.ok) throw new Error(`download manifest unavailable: ${proxied.status}`);
  return (await proxied.json()) as UpdateManifest;
}

/** 从清单取 Windows 安装包 URL。 */
export function windowsDownloadUrl(manifest: UpdateManifest): string | null {
  return manifest.platforms?.[WINDOWS_PLATFORM]?.url ?? null;
}
