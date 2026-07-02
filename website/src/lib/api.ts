/**
 * 前端 fetch：账户中心/认证跨域直连 Go 中介（chat.example.com）。
 * XHR 均 credentials:'include' 以携带 host-only 会话 cookie；
 * 登录/回调为顶层导航（见 goToLogin）。前端绝不接触 access_token / client_secret。
 */
import { API_BASE_URL } from "./config";

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
  const res = await fetch(`${API_BASE_URL}/api/me`, {
    credentials: "include",
    headers: { accept: "application/json" },
    signal,
  });
  if (res.status === 401) return null;
  if (!res.ok) throw new Error(`/api/me failed: ${res.status}`);
  return (await res.json()) as MeResponse;
}

/** 触发账户中心登录（顶层跳转到 Go 中介的 /auth/login）。 */
export function goToLogin(): void {
  window.location.assign(`${API_BASE_URL}/auth/login`);
}

/** 退出登录（POST /auth/logout，携带会话 cookie）。 */
export async function logout(): Promise<void> {
  await fetch(`${API_BASE_URL}/auth/logout`, {
    method: "POST",
    credentials: "include",
  });
}

/**
 * 读取下载清单：直连 chat.example.com/updates/latest.json（公开 GET）。
 * updatesUrl 为可配置的直连地址（由构建期注入或默认占位）。
 */
export async function fetchLatestManifest(
  updatesUrl: string,
  signal?: AbortSignal,
): Promise<UpdateManifest> {
  const res = await fetch(updatesUrl, { headers: { accept: "application/json" }, signal });
  if (!res.ok) throw new Error(`download manifest unavailable: ${res.status}`);
  return (await res.json()) as UpdateManifest;
}

/** 从清单取 Windows 安装包 URL。 */
export function windowsDownloadUrl(manifest: UpdateManifest): string | null {
  return manifest.platforms?.[WINDOWS_PLATFORM]?.url ?? null;
}
