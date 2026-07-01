import PageSection from "../components/PageSection";

export default function About() {
  return (
    <PageSection title="关于 Lumen" subtitle="类 Discord 的轻量开黑语音。">
      <p>
        Lumen 是一款专注开黑场景的轻量语音应用。我们希望把“进房即聊、低延迟、界面清爽”做到位，
        让你和朋友把注意力留在游戏与交流本身。
      </p>
      <p>所有聊天与语音能力均在 Windows 桌面客户端内提供；官网仅承担下载、账户中心与登录中介。</p>

      <div className="rounded-2xl border border-zinc-800 bg-zinc-900 p-6">
        <h2 className="text-sm font-semibold text-zinc-300">许可与版本</h2>
        <p className="mt-2 text-sm text-zinc-400">
          客户端版本号可在下载页查看（读取自服务端自动更新清单）。
        </p>
      </div>
    </PageSection>
  );
}
