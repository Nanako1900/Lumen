import type { ReactNode } from "react";

/**
 * 玻璃拟态卡片（Aurora Indigo）：半透明白底 + 背景模糊 + 内描边高光 + 柔和靛蓝阴影。
 * 设计稿功能卡 / 资料卡 / 下载卡的统一底座。
 */
interface GlassCardProps {
  children: ReactNode;
  className?: string;
  as?: "div" | "section" | "article" | "li";
}

export default function GlassCard({
  children,
  className = "",
  as: Tag = "div",
}: GlassCardProps) {
  return (
    <Tag
      className={
        "rounded-[18px] bg-white/60 shadow-glass shadow-ringcard backdrop-blur-md " +
        className
      }
    >
      {children}
    </Tag>
  );
}
