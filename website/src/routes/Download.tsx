import { useEffect, useState } from "react";
import DownloadButton from "../components/DownloadButton";
import GlassCard from "../components/ui/GlassCard";
import Eyebrow from "../components/ui/Eyebrow";
import {
  AppleIcon,
  ArrowRightIcon,
  InfoIcon,
  LinuxIcon,
  ServerIcon,
  WindowsIcon,
} from "../components/icons";
import {
  fetchLatestManifest,
  windowsDownloadUrl,
  type UpdateManifest,
} from "../lib/api";
import { GITHUB_URL, UPDATES_LATEST_URL } from "../lib/config";
import { formatDate, formatVersion } from "../lib/format";

type LoadState =
  | { status: "loading" }
  | { status: "ready"; manifest: UpdateManifest }
  | { status: "error" };

const requirements: [string, string][] = [
  ["操作系统", "Windows 10 20H2+ / 11"],
  ["运行时", "WebView2（自动安装）"],
  ["内存", "4 GB 及以上"],
  ["网络", "WSS + UDP 到服务端"],
];

export default function Download() {
  const [state, setState] = useState<LoadState>({ status: "loading" });

  useEffect(() => {
    const controller = new AbortController();
    // 每次进入下载页读取最新清单（与 latest.json 的 no-cache 语义一致）
    fetchLatestManifest(UPDATES_LATEST_URL, controller.signal)
      .then((manifest) => setState({ status: "ready", manifest }))
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        console.error("failed to load update manifest", err);
        setState({ status: "error" });
      });
    return () => controller.abort();
  }, []);

  return (
    <div className="mx-auto max-w-content px-5 pb-10 pt-14 md:px-10">
      <header className="mx-auto max-w-xl text-center">
        <Eyebrow tone="aurora">下载</Eyebrow>
        <h1 className="mt-3 text-[2.4rem] font-extrabold tracking-[-0.02em]">
          下载 Lumen
        </h1>
        <p className="mt-3.5 text-[15.5px] text-ink-muted">
          选择你的平台，拉朋友开黑只差一步。
        </p>
      </header>

      <div className="mx-auto mt-8 max-w-2xl">
        {state.status === "loading" && <WindowsCardSkeleton />}
        {state.status === "error" && <WindowsCardError />}
        {state.status === "ready" && <WindowsCard manifest={state.manifest} />}
      </div>

      <OtherPlatforms />
      {state.status === "ready" && <RequirementsAndNotes manifest={state.manifest} />}
      <SelfHostNote />
    </div>
  );
}

function WindowsCardShell({ children }: { children: React.ReactNode }) {
  return (
    <GlassCard className="p-7 shadow-card md:p-8">
      <div className="flex flex-col gap-5 sm:flex-row sm:items-center">
        <span className="flex h-16 w-16 flex-none items-center justify-center rounded-[18px] bg-brand/10 text-brand shadow-[inset_0_0_0_1px_rgba(91,110,245,.16)]">
          <WindowsIcon size={32} />
        </span>
        {children}
      </div>
    </GlassCard>
  );
}

function WindowsCard({ manifest }: { manifest: UpdateManifest }) {
  const url = windowsDownloadUrl(manifest);
  const version = formatVersion(manifest.version);
  const date = formatDate(manifest.pub_date);
  const signature = manifest.platforms?.["windows/amd64"]?.signature;

  return (
    <div className="space-y-0">
      <WindowsCardShell>
        <div className="flex-1">
          <div className="flex flex-wrap items-center gap-2.5">
            <span className="text-xl font-bold">Windows 版</span>
            {version && (
              <span className="rounded-full bg-brand/10 px-2.5 py-0.5 text-[11px] font-semibold text-brand-deep">
                {version}
              </span>
            )}
          </div>
          <div className="mt-1 text-[13px] text-ink-faint">
            适用于 Windows 10 / 11（64 位） · 含自动更新
            {date ? ` · 更新于 ${date}` : ""}
          </div>
        </div>
        <DownloadButton href={url} label="下载 .exe" />
      </WindowsCardShell>

      {url === null && (
        <p className="mt-4 text-sm text-warn-deep">
          当前清单未提供 Windows 安装包链接，请稍后重试。
        </p>
      )}

      {signature && (
        <GlassCard className="mt-4 flex items-center gap-3 bg-black/[0.03] p-3.5 shadow-[inset_0_0_0_1px_rgba(20,24,55,.05)]">
          <span className="font-mono text-[11.5px] uppercase tracking-wide text-ink-faint">
            签名
          </span>
          <span className="flex-1 truncate font-mono text-[11.5px] text-ink-muted">
            {signature}
          </span>
        </GlassCard>
      )}

      <div className="mt-3.5 flex items-start gap-2 text-[12.5px] text-ink-faint">
        <InfoIcon size={15} className="mt-px flex-none text-brand-light" />
        Windows 11 已内置 WebView2；Windows 10 首次运行会自动安装运行时。
      </div>
    </div>
  );
}

function WindowsCardSkeleton() {
  return (
    <WindowsCardShell>
      <div className="flex-1" role="status" aria-live="polite">
        <div className="h-5 w-32 animate-pulse rounded bg-brand/10" />
        <div className="mt-2 h-3.5 w-56 animate-pulse rounded bg-black/[0.06]" />
        <span className="sr-only">正在获取最新版本…</span>
      </div>
      <DownloadButton href={null} label="获取中…" disabled />
    </WindowsCardShell>
  );
}

function WindowsCardError() {
  return (
    <WindowsCardShell>
      <div className="flex-1">
        <div className="text-lg font-bold text-ink">暂时无法获取版本</div>
        <div className="mt-1 text-[13px] text-ink-faint">
          请稍后重试，或前往帮助页反馈。
        </div>
      </div>
      <DownloadButton href={null} label="下载 .exe" disabled />
    </WindowsCardShell>
  );
}

function OtherPlatforms() {
  return (
    <div className="mx-auto mt-5 grid max-w-2xl gap-4 sm:grid-cols-2">
      <GlassCard className="flex items-center justify-between bg-white/40 p-4 opacity-75">
        <div className="flex items-center gap-3">
          <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-black/[0.05] text-ink-faint">
            <AppleIcon size={20} />
          </span>
          <div>
            <div className="text-sm font-semibold text-ink-muted">macOS</div>
            <div className="text-[11px] text-ink-ghost">暂未支持</div>
          </div>
        </div>
        <span className="rounded-full bg-black/[0.05] px-2.5 py-1 text-[10.5px] font-semibold text-ink-faint">
          计划外
        </span>
      </GlassCard>
      <GlassCard className="flex items-center justify-between bg-white/40 p-4 opacity-75">
        <div className="flex items-center gap-3">
          <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-black/[0.05] text-ink-faint">
            <LinuxIcon size={20} />
          </span>
          <div>
            <div className="text-sm font-semibold text-ink-muted">Linux</div>
            <div className="text-[11px] text-ink-ghost">客户端暂未支持</div>
          </div>
        </div>
        <span className="rounded-full bg-aurora/[0.12] px-2.5 py-1 text-[10.5px] font-semibold text-aurora-deeper">
          服务端可跑
        </span>
      </GlassCard>
    </div>
  );
}

function RequirementsAndNotes({ manifest }: { manifest: UpdateManifest }) {
  const version = formatVersion(manifest.version);
  const date = formatDate(manifest.pub_date);

  return (
    <div className="mx-auto mt-9 grid max-w-3xl items-start gap-5 md:grid-cols-2">
      <GlassCard className="p-6">
        <div className="mb-4 text-[15px] font-bold text-ink">系统要求</div>
        <dl className="flex flex-col gap-3">
          {requirements.map(([k, v], i) => (
            <div key={k}>
              {i > 0 && <div className="mb-3 h-px bg-black/[0.06]" />}
              <div className="flex items-center justify-between text-[13px]">
                <dt className="text-ink-faint">{k}</dt>
                <dd className="font-semibold text-ink">{v}</dd>
              </div>
            </div>
          ))}
        </dl>
      </GlassCard>

      <GlassCard className="p-6">
        <div className="mb-4 text-[15px] font-bold text-ink">更新说明</div>
        <div className="flex gap-3.5">
          <span className="mt-1.5 h-2.5 w-2.5 flex-none rounded-full bg-brand shadow-[0_0_0_4px_rgba(91,110,245,.14)]" />
          <div>
            <div className="flex items-center gap-2.5">
              <span className="text-sm font-bold text-ink">{version || "最新版本"}</span>
              {date && <span className="text-[11px] text-ink-faint">{date}</span>}
            </div>
            <p className="mt-1 whitespace-pre-line text-[13px] leading-relaxed text-ink-muted">
              {manifest.notes || "本次更新包含若干稳定性与体验改进。"}
            </p>
          </div>
        </div>
      </GlassCard>
    </div>
  );
}

function SelfHostNote() {
  return (
    <div className="mx-auto mt-6 max-w-3xl">
      <div
        className="flex flex-col items-start justify-between gap-4 rounded-[16px] p-5 shadow-[inset_0_0_0_1px_rgba(255,255,255,.5)] sm:flex-row sm:items-center"
        style={{
          background:
            "linear-gradient(135deg,rgba(91,110,245,.1),rgba(41,212,180,.08))",
        }}
      >
        <div className="flex items-center gap-3.5">
          <ServerIcon size={22} className="flex-none text-brand-deep" />
          <div>
            <div className="text-sm font-bold text-ink">要部署自己的 Lumen 服务器？</div>
            <div className="mt-0.5 text-[12.5px] text-ink-muted">
              单二进制 + SQLite，5 分钟跑在你的 VPS 上。
            </div>
          </div>
        </div>
        <a
          href={GITHUB_URL}
          target="_blank"
          rel="noreferrer noopener"
          className="inline-flex h-10 flex-none items-center gap-2 rounded-xl bg-white/70 px-4 text-[13.5px] font-semibold text-brand-deep shadow-[inset_0_0_0_1px_rgba(91,110,245,.25)] transition hover:bg-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/50"
        >
          阅读服务端文档
          <ArrowRightIcon size={15} />
        </a>
      </div>
    </div>
  );
}
