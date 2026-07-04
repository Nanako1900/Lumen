/**
 * 统一按钮样式（返回 className 字符串，可用于 <Link> / <a> / <button>）。
 * 变体与设计稿 1a–1f 的 CTA 一致：primary 靛蓝实心、secondary 玻璃描边、
 * white 用于靛蓝 CTA band 上、danger 危险操作。
 */
export type ButtonVariant = "primary" | "secondary" | "white" | "danger" | "ghost";
export type ButtonSize = "sm" | "md" | "lg";

const base =
  "inline-flex items-center justify-center gap-2 font-semibold transition " +
  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/60 " +
  "focus-visible:ring-offset-2 focus-visible:ring-offset-canvas disabled:cursor-not-allowed";

const sizes: Record<ButtonSize, string> = {
  sm: "h-9 rounded-xl px-4 text-[13px]",
  md: "h-[38px] rounded-xl px-4 text-sm",
  lg: "h-[50px] rounded-[14px] px-6 text-[15px]",
};

const variants: Record<ButtonVariant, string> = {
  primary: "bg-brand text-white shadow-cta hover:brightness-[1.07] active:brightness-95",
  secondary:
    "bg-white/70 text-brand-deep shadow-[inset_0_0_0_1px_rgba(91,110,245,.25)] hover:bg-white",
  white:
    "bg-white text-brand-deep shadow-[0_14px_30px_-8px_rgba(20,24,80,.4)] hover:brightness-[0.98]",
  danger:
    "bg-danger/10 text-danger-deep shadow-[inset_0_0_0_1px_rgba(236,76,86,.25)] hover:bg-danger/15",
  ghost: "text-ink-muted hover:bg-black/5",
};

export function buttonClass(
  variant: ButtonVariant = "primary",
  size: ButtonSize = "md",
  extra = "",
): string {
  return [base, sizes[size], variants[variant], extra].filter(Boolean).join(" ");
}
