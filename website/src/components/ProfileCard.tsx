import type { MeResponse } from "../lib/api";
import GlassCard from "./ui/GlassCard";

/** 账户中心资料卡：头像 + 昵称（web-design.md §6.2）。数据仅来自 OIDC，不调 Lumen API。 */
export default function ProfileCard({ profile }: { profile: MeResponse }) {
  const initial = profile.display_name?.trim().charAt(0).toUpperCase() || "?";
  return (
    <GlassCard className="flex items-center gap-5 p-6 shadow-card">
      {profile.avatar_url ? (
        <img
          src={profile.avatar_url}
          alt=""
          className="h-16 w-16 flex-none rounded-full object-cover shadow-[0_0_0_3px_rgba(91,110,245,.25)]"
          referrerPolicy="no-referrer"
        />
      ) : (
        <div className="flex h-16 w-16 flex-none items-center justify-center rounded-full bg-orb text-2xl font-bold text-white shadow-[0_0_0_3px_rgba(91,110,245,.25)]">
          {initial}
        </div>
      )}
      <div className="min-w-0">
        <p className="text-[11.5px] font-semibold uppercase tracking-wide text-aurora-deep">
          已登录
        </p>
        <p className="truncate text-xl font-bold text-ink">
          {profile.display_name || "Lumen 用户"}
        </p>
        <p className="mt-0.5 text-[12.5px] text-ink-faint">Lumen 账户</p>
      </div>
    </GlassCard>
  );
}
