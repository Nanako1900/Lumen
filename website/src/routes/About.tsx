import PageSection from "../components/PageSection";
import GlassCard from "../components/ui/GlassCard";

export default function About() {
  return (
    <PageSection title="关于 Lumen" subtitle="为开黑而生的轻量语音。" eyebrow="关于">
      <p>
        Lumen 是一款专注开黑场景的轻量语音应用。我们希望把“进房即聊、低延迟、界面清爽”做到位，
        让你和朋友把注意力留在游戏与交流本身。
      </p>
      <p>
        所有聊天与语音能力均在 Windows 桌面客户端内提供；官网仅承担下载、账户中心与登录中介。
        服务端是单个 Go 二进制，完全自托管——数据永远在你手里。
      </p>

      <GlassCard className="p-6">
        <h2 className="text-sm font-bold text-ink">许可与版本</h2>
        <p className="mt-2 text-[13.5px] leading-relaxed text-ink-muted">
          免费、开源、自托管。客户端版本号可在下载页查看（读取自服务端自动更新清单）。
        </p>
      </GlassCard>
    </PageSection>
  );
}
