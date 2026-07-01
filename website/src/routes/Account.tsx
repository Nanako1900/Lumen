import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import ProfileCard from "../components/ProfileCard";
import PageSection from "../components/PageSection";
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
    <PageSection title="账户中心" subtitle="查看资料、下载客户端与退出登录。">
      {loginError && (
        <div className="rounded-xl border border-amber-800/50 bg-amber-950/30 px-4 py-3 text-sm text-amber-300">
          登录未完成（{loginError}），请重试。
        </div>
      )}

      {state.status === "loading" && (
        <p className="text-zinc-400" role="status">
          正在加载账户信息…
        </p>
      )}

      {state.status === "error" && (
        <div className="rounded-2xl border border-zinc-800 bg-zinc-900 p-6">
          <p className="text-zinc-300">加载账户信息失败，请稍后重试。</p>
        </div>
      )}

      {state.status === "anon" && (
        <div className="space-y-4 rounded-2xl border border-zinc-800 bg-zinc-900 p-8 text-center shadow-lg">
          <p className="text-zinc-300">登录后可查看你的资料并下载客户端。</p>
          <button
            type="button"
            onClick={goToLogin}
            className="inline-flex items-center justify-center rounded-xl bg-indigo-600 px-6 py-3 text-base font-semibold text-white transition-colors hover:bg-indigo-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400"
          >
            登录
          </button>
        </div>
      )}

      {state.status === "authed" && (
        <div className="space-y-6">
          <ProfileCard profile={state.profile} />
          <div className="flex flex-col gap-3 sm:flex-row">
            <Link
              to="/download"
              className="inline-flex items-center justify-center rounded-xl bg-indigo-600 px-5 py-2.5 font-semibold text-white transition-colors hover:bg-indigo-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400"
            >
              下载客户端
            </Link>
            <button
              type="button"
              onClick={handleLogout}
              className="inline-flex items-center justify-center rounded-xl border border-zinc-700 px-5 py-2.5 font-semibold text-zinc-200 transition-colors hover:border-zinc-500 hover:bg-zinc-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400"
            >
              退出登录
            </button>
          </div>
        </div>
      )}
    </PageSection>
  );
}
