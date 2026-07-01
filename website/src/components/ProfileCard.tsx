import type { MeResponse } from "../lib/api";

/** 账户中心资料卡：头像 + 昵称（web-design.md §6.2）。数据仅来自 OIDC，不调 Lumen API。 */
export default function ProfileCard({ profile }: { profile: MeResponse }) {
  const initial = profile.display_name?.trim().charAt(0).toUpperCase() || "?";
  return (
    <div className="flex items-center gap-4 rounded-2xl border border-zinc-800 bg-zinc-900 p-6 shadow-lg">
      {profile.avatar_url ? (
        <img
          src={profile.avatar_url}
          alt={profile.display_name}
          className="h-16 w-16 rounded-full object-cover ring-1 ring-zinc-700"
          referrerPolicy="no-referrer"
        />
      ) : (
        <div className="flex h-16 w-16 items-center justify-center rounded-full bg-indigo-600 text-2xl font-semibold text-white">
          {initial}
        </div>
      )}
      <div className="min-w-0">
        <p className="text-xs uppercase tracking-wide text-zinc-500">已登录</p>
        <p className="truncate text-xl font-semibold text-zinc-100">
          {profile.display_name || "Lumen 用户"}
        </p>
      </div>
    </div>
  );
}
