/**
 * Lumen 品牌光球（Aurora Indigo）：靛蓝径向渐变 + 发光。
 * 设计稿贯穿使用；`glow` 开启呼吸发光动效（尊重 prefers-reduced-motion）。
 */
interface OrbProps {
  size?: number;
  glow?: boolean;
  className?: string;
}

export default function Orb({ size = 26, glow = false, className = "" }: OrbProps) {
  return (
    <span
      aria-hidden="true"
      className={`inline-block flex-none rounded-full bg-orb shadow-orb ${
        glow ? "animate-orbpulse" : ""
      } ${className}`}
      style={{ width: size, height: size }}
    />
  );
}
