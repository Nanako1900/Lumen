import { useEffect, useState } from "react";
import DownloadButton from "../components/DownloadButton";
import PageSection from "../components/PageSection";
import {
  fetchLatestManifest,
  windowsDownloadUrl,
  type UpdateManifest,
} from "../lib/api";
import { UPDATES_LATEST_URL } from "../lib/config";
import { formatDate, formatVersion } from "../lib/format";

type LoadState =
  | { status: "loading" }
  | { status: "ready"; manifest: UpdateManifest }
  | { status: "error" };

export default function Download() {
  const [state, setState] = useState<LoadState>({ status: "loading" });

  useEffect(() => {
    const controller = new AbortController();
    // 每次进入下载页读取最新清单（与 latest.json 的 no-cache 语义一致）
    fetchLatestManifest(UPDATES_LATEST_URL, controller.signal)
      .then((manifest) => setState({ status: "ready", manifest }))
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        // 记录到控制台便于排障；UI 显示友好错误
        console.error("failed to load update manifest", err);
        setState({ status: "error" });
      });
    return () => controller.abort();
  }, []);

  return (
    <PageSection title="下载 Lumen" subtitle="Windows 桌面客户端。安装后即可登录开黑。">
      {state.status === "loading" && (
        <p className="text-zinc-400" role="status">
          正在获取最新版本…
        </p>
      )}

      {state.status === "error" && (
        <div className="rounded-2xl border border-zinc-800 bg-zinc-900 p-6">
          <p className="text-zinc-300">暂时无法获取最新版本信息。</p>
          <p className="mt-1 text-sm text-zinc-500">请稍后重试，或前往帮助页反馈。</p>
        </div>
      )}

      {state.status === "ready" && <ReadyCard manifest={state.manifest} />}
    </PageSection>
  );
}

function ReadyCard({ manifest }: { manifest: UpdateManifest }) {
  const url = windowsDownloadUrl(manifest);
  const version = formatVersion(manifest.version);
  const date = formatDate(manifest.pub_date);

  return (
    <div className="space-y-6 rounded-2xl border border-zinc-800 bg-zinc-900 p-8 shadow-lg">
      <div className="space-y-1">
        <div className="flex flex-wrap items-baseline gap-3">
          <h2 className="text-xl font-semibold text-zinc-100">Lumen for Windows</h2>
          {version && (
            <span className="rounded-full bg-zinc-800 px-3 py-1 text-sm text-indigo-400">
              {version}
            </span>
          )}
        </div>
        {date && <p className="text-sm text-zinc-500">更新于 {date}</p>}
      </div>

      <DownloadButton href={url} label="下载 Setup.exe" />

      {!url && (
        <p className="text-sm text-amber-400">
          当前清单未提供 Windows 安装包链接，请稍后重试。
        </p>
      )}

      {manifest.notes && (
        <div className="border-t border-zinc-800 pt-4">
          <h3 className="text-sm font-semibold text-zinc-300">更新说明</h3>
          <p className="mt-1 whitespace-pre-line text-sm text-zinc-400">{manifest.notes}</p>
        </div>
      )}
    </div>
  );
}
