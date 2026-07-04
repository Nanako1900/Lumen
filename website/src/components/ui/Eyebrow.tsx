/**
 * 小标题眉标（uppercase + 宽字距），设计稿用于分区上方。
 * tone: aurora（极光青）/ brand（靛蓝深）。
 */
interface EyebrowProps {
  children: React.ReactNode;
  tone?: "aurora" | "brand";
  className?: string;
}

export default function Eyebrow({
  children,
  tone = "aurora",
  className = "",
}: EyebrowProps) {
  const color = tone === "aurora" ? "text-aurora-deep" : "text-brand-deep";
  return (
    <div
      className={`text-xs font-bold uppercase tracking-[0.15em] ${color} ${className}`}
    >
      {children}
    </div>
  );
}
