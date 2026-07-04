import type { ComponentType } from "react";
import GlassCard from "../ui/GlassCard";
import Eyebrow from "../ui/Eyebrow";
import {
  ActivityIcon,
  HashIcon,
  type IconProps,
  KeyIcon,
  LockIcon,
  NoiseIcon,
  ServerIcon,
} from "../icons";

interface Feature {
  icon: ComponentType<IconProps>;
  tone: "brand" | "aurora" | "deep";
  title: string;
  desc: string;
  badge?: string;
}

const features: Feature[] = [
  {
    icon: ActivityIcon,
    tone: "brand",
    title: "低延迟语音",
    desc: "WebRTC + Opus，服务端 SFU 选择性转发，为开黑压到最低延迟，只转发不转码。",
  },
  {
    icon: NoiseIcon,
    tone: "aurora",
    title: "AI 降噪",
    desc: "内置 RNNoise，机械键盘、风扇、环境噪声一并压掉，队友只听见你说话。",
  },
  {
    icon: HashIcon,
    tone: "brand",
    title: "类 Discord 频道",
    desc: "一个服务器，多个文字 / 语音频道。点一下就进房，喊人开黑，切频道无缝。",
  },
  {
    icon: ServerIcon,
    tone: "aurora",
    title: "自托管 · 你做主",
    desc: "服务端是单个 Go 二进制 + 一个 SQLite 文件，跑在你自己的 VPS，数据永远在你手里。",
  },
  {
    icon: KeyIcon,
    tone: "brand",
    title: "OAuth 登录 · 无密码托管",
    desc: "用你自己的 OAuth2 服务器登录（PKCE），业务服务器从不保存密码，只认签名的 token。",
  },
  {
    icon: LockIcon,
    tone: "deep",
    title: "端到端加密",
    badge: "路线图",
    desc: "传输层 DTLS-SRTP 起步；v2 引入 SFrame 应用层加密，连自托管服务器也只转发密文。",
  },
];

const tileTone: Record<Feature["tone"], string> = {
  brand: "bg-brand/10 text-brand",
  aurora: "bg-aurora/[0.14] text-aurora-deep",
  deep: "bg-brand-deep/10 text-brand-deep",
};

/** 为什么选 Lumen：六张玻璃功能卡。 */
export default function FeatureGrid() {
  return (
    <section className="mx-auto max-w-content px-5 pt-20 md:px-10">
      <div className="mx-auto mb-10 max-w-xl text-center">
        <Eyebrow tone="aurora">为什么选 Lumen</Eyebrow>
        <h2 className="mt-3 text-[2.1rem] font-extrabold tracking-[-0.01em]">
          开黑要的，它都轻量地给你
        </h2>
        <p className="mt-3.5 text-[15px] leading-relaxed text-ink-muted">
          不堆功能，只把语音、频道、隐私三件事做到顺手。
        </p>
      </div>

      <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
        {features.map(({ icon: Icon, tone, title, desc, badge }) => (
          <GlassCard key={title} className="p-6">
            <span
              className={`flex h-11 w-11 items-center justify-center rounded-[13px] ${tileTone[tone]}`}
            >
              <Icon size={21} />
            </span>
            <div className="mt-4 flex items-center gap-2">
              <h3 className="text-base font-bold">{title}</h3>
              {badge && (
                <span className="rounded-full bg-warn/15 px-2 py-0.5 text-[10px] font-semibold text-warn-deep">
                  {badge}
                </span>
              )}
            </div>
            <p className="mt-2 text-[13.5px] leading-relaxed text-ink-muted">{desc}</p>
          </GlassCard>
        ))}
      </div>
    </section>
  );
}
