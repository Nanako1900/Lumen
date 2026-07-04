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
        // 卡片位于平滑的 Aurora 渐变之上，backdrop-blur 视觉上等同于无（渐变无高频细节），
        // 却会造成持续合成/滚动闪烁；改用略高不透明度的半透明白，观感一致且不再重绘。
        "rounded-[18px] bg-white/70 shadow-glass shadow-ringcard " + className
      }
    >
      {children}
    </Tag>
  );
}
