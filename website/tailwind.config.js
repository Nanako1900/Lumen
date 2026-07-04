/** @type {import('tailwindcss').Config} */
export default {
  // Aurora Indigo 是单一浅色主题（设计稿 1a–1f），不提供暗色切换。
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        // 文本层级（深 → 浅）。faint/ghost 已加深以满足 WCAG AA
        // （在白与 #eef0f8 画布上均 ≥4.5:1；ghost 仅用于停用平台的占位标签）。
        ink: {
          DEFAULT: "#1c1f2e",
          muted: "#565c74",
          faint: "#656b82",
          ghost: "#767c92",
        },
        // 品牌靛蓝（Aurora Indigo 主色）
        brand: {
          soft: "#c3ccff",
          glow: "#9da8ff",
          light: "#8391ff",
          DEFAULT: "#5b6ef5",
          deep: "#3e44c4",
        },
        // 极光青（辅助强调 / 语义成功）
        aurora: {
          light: "#5fe6c9",
          DEFAULT: "#29d4b4",
          deep: "#17b47e",
          deeper: "#128a5f",
        },
        danger: { DEFAULT: "#ec4c56", deep: "#c43a44" },
        warn: { DEFAULT: "#e3a015", deep: "#9a6b06" },
        // 画布（页面底色渐变端点）
        canvas: { DEFAULT: "#f6f7fd", deep: "#eef0f8" },
        night: "#141726", // 终端演示暗底
      },
      fontFamily: {
        sans: [
          "-apple-system",
          "BlinkMacSystemFont",
          '"PingFang SC"',
          '"Microsoft YaHei"',
          '"Segoe UI"',
          "Roboto",
          "system-ui",
          "sans-serif",
        ],
        mono: [
          "ui-monospace",
          "Menlo",
          "Monaco",
          '"Cascadia Code"',
          "monospace",
        ],
      },
      maxWidth: {
        content: "72rem", // 1152px 主容器
      },
      boxShadow: {
        cta: "0 12px 26px -6px rgba(91,110,245,.55)",
        brand: "0 8px 18px -5px rgba(91,110,245,.55)",
        glass: "0 8px 24px -10px rgba(91,110,245,.18)",
        card: "0 18px 50px -18px rgba(91,110,245,.32)",
        float: "0 30px 60px -20px rgba(60,70,180,.5)",
        orb: "0 0 16px 2px rgba(91,110,245,.45), inset 0 -3px 7px rgba(40,44,120,.35)",
        ringcard: "inset 0 0 0 1px rgba(255,255,255,.65)",
      },
      backgroundImage: {
        aurora:
          "radial-gradient(120% 70% at 6% -8%, rgba(91,110,245,.26), transparent 52%), radial-gradient(90% 60% at 112% 0%, rgba(41,212,180,.2), transparent 50%), linear-gradient(180deg, #f6f7fd, #eef0f8)",
        "aurora-center":
          "radial-gradient(120% 60% at 50% -6%, rgba(91,110,245,.3), transparent 56%), radial-gradient(80% 60% at 100% 108%, rgba(41,212,180,.18), transparent 55%), linear-gradient(180deg, #f6f7fd, #eaecf6)",
        orb: "radial-gradient(circle at 34% 28%, #c3ccff, #5b6ef5 72%)",
        "brand-cta": "linear-gradient(135deg, #5b6ef5, #3e44c4)",
      },
      keyframes: {
        eq: {
          "0%,100%": { transform: "scaleY(.3)" },
          "50%": { transform: "scaleY(1)" },
        },
        // 光球呼吸：仅动画 opacity/transform（合成器友好），不再动画 box-shadow。
        glow: {
          "0%,100%": { opacity: ".5", transform: "scale(1)" },
          "50%": { opacity: ".9", transform: "scale(1.14)" },
        },
        caret: { "0%,100%": { opacity: ".4" }, "50%": { opacity: "1" } },
        spin: { to: { transform: "rotate(360deg)" } },
        float: {
          "0%,100%": { transform: "translateY(0)" },
          "50%": { transform: "translateY(-10px)" },
        },
      },
      animation: {
        eq: "eq .7s ease-in-out infinite",
        glow: "glow 3.4s ease-in-out infinite",
        caret: "caret 1.1s ease-in-out infinite",
        spin: "spin .9s linear infinite",
        float: "float 6.5s ease-in-out infinite",
      },
    },
  },
  plugins: [],
};
