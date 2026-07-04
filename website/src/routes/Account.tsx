import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import ProfileCard from "../components/ProfileCard";
import GlassCard from "../components/ui/GlassCard";
import Orb from "../components/ui/Orb";
import { buttonClass } from "../components/ui/button";
import { AlertTriangleIcon, DownloadIcon, LogOutIcon } from "../components/icons";
import { fetchMe, goToLogin, logout, type MeResponse } from "../lib/api";

type LoadState =
  | { status: "loading" }
  | { status: "authed"; profile: MeResponse }
  | { status: "anon" }
  | { status: "error" };

export default function Account() {
  const [state, setState] = useState<LoadState>({ status: "loading" });
  const [searchParams] = useSearchParams();
  const loginError = searchParams.get("error");

  useEffect(() => {
    const controller = new AbortController();
    fetchMe(controller.signal)
      .then((profile) => {
        if (controller.signal.aborted) return;
        setState(profile ? { status: "authed", profile } : { status: "anon" });
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        console.error("failed to load /api/me", err);
        setState({ status: "error" });
      });
    return () => controller.abort();
  }, []);

  async function handleLogout() {
    try {
      await logout();
    } finally {
      setState({ status: "anon" });
    }
  }

  return (
    <div className="mx-auto max-w-2xl px-5 py-14 md:px-10">
      <header className="mb-8">
        <h1 className="text-[2.1rem] font-extrabold tracking-[-0.01em] text-ink">账户</h1>
        <p className="mt-3 text-[15px] text-ink-muted">
          查看资料、下载客户端与退出登录。
        </p>
      </header>

      {loginError && (
        <div className="mb-6 flex items-center gap-2.5 rounded-2xl bg-warn/10 px-4 py-3 text-sm text-warn-deep shadow-[inset_0_0_0_1px_rgba(227,160,21,.25)]">
          <AlertTriangleIcon size={16} className="flex-none" />
          登录未完成（{loginError}），请重试。
        </div>
      )}

      {state.status === "loading" && <LoadingState />}
      {state.status === "error" && <ErrorState />}
      {state.status === "anon" && <AnonState />}
      {state.status === "authed" && (
        <AuthedState profile={state.profile} onLogout={handleLogout} />
      )}
    </div>
  );
}

function LoadingState() {
  return (
    <GlassCard className="p-6" >
      <div role="status" aria-live="polite">
        <div className="flex items-center gap-5">
          <div className="h-16 w-16 flex-none animate-pulse rounded-full bg-brand/10" />
          <div className="flex-1 space-y-2">
            <div className="h-3 w-16 animate-pulse rounded bg-black/[0.06]" />
            <div className="h-5 w-32 animate-pulse rounded bg-brand/10" />
          </div>
        </div>
        <span className="sr-only">正在加载账户信息…</span>
      </div>
    </GlassCard>
  );
}

function ErrorState() {
  return (
    <GlassCard className="p-6 text-center">
      <p className="text-ink">加载账户信息失败，请稍后重试。</p>
    </GlassCard>
  );
}

/** 未登录：品牌光球 + 浏览器登录（顶层导航到 broker /auth/login）。 */
function AnonState() {
  return (
    <GlassCard className="p-8 text-center shadow-card">
      <Orb size={56} glow className="mx-auto" />
      <h2 className="mt-4 text-xl font-bold text-ink">登录 Lumen 账户中心</h2>
      <p className="mx-auto mt-2 max-w-xs text-[13.5px] leading-relaxed text-ink-muted">
        登录后可查看你的资料并下载客户端。
      </p>
      <button
        type="button"
        onClick={goToLogin}
        className={buttonClass("primary", "lg", "mx-auto mt-6 w-full max-w-xs")}
      >
        使用浏览器登录
      </button>
      <p className="mt-3 text-[12px] text-ink-faint">
        将在系统浏览器完成登录后自动返回
      </p>
    </GlassCard>
  );
}

function AuthedState({
  profile,
  onLogout,
}: {
  profile: MeResponse;
  onLogout: () => void;
}) {
  return (
    <div className="space-y-5">
      <ProfileCard profile={profile} />

      <GlassCard className="flex flex-col items-start justify-between gap-4 p-6 sm:flex-row sm:items-center">
        <div>
          <div className="text-[14px] font-bold text-ink">下载桌面客户端</div>
          <div className="mt-0.5 text-[12.5px] text-ink-faint">
            所有聊天与语音都在 Windows 客户端内。
          </div>
        </div>
        <Link to="/download" className={buttonClass("primary", "md")}>
          <DownloadIcon size={15} />
          下载客户端
        </Link>
      </GlassCard>

      <GlassCard className="flex flex-col items-start justify-between gap-4 p-6 sm:flex-row sm:items-center">
        <div>
          <div className="text-[14px] font-bold text-ink">退出登录</div>
          <div className="mt-0.5 text-[12.5px] text-ink-faint">
            清除本浏览器的账户中心会话。
          </div>
        </div>
        <button type="button" onClick={onLogout} className={buttonClass("danger", "md")}>
          <LogOutIcon size={15} />
          退出登录
        </button>
      </GlassCard>
    </div>
  );
}
