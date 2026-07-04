/**
 * Lumen 品牌光球（Aurora Indigo）：靛蓝径向渐变 + 静态发光。
 * `glow` 时叠加一层模糊光晕，仅动画 opacity/scale（合成器友好，不触发逐帧重绘）。
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
      className={`relative inline-flex flex-none ${className}`}
      style={{ width: size, height: size }}
    >
      {glow && (
        <span className="absolute inset-[-38%] -z-10 rounded-full bg-orb blur-lg animate-glow" />
      )}
      <span className="h-full w-full rounded-full bg-orb shadow-orb" />
    </span>
  );
}
