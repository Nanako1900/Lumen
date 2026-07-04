import { Link } from "react-router-dom";
import Logo from "./Logo";
import { GITHUB_URL } from "../lib/config";

interface FooterCol {
  title: string;
  links: { label: string; to?: string; href?: string }[];
}

const columns: FooterCol[] = [
  {
    title: "产品",
    links: [
      { label: "下载", to: "/download" },
      { label: "账户中心", to: "/account" },
      { label: "帮助", to: "/help" },
    ],
  },
  {
    title: "资源",
    links: [
      { label: "GitHub", href: GITHUB_URL },
      { label: "关于 Lumen", to: "/about" },
    ],
  },
  {
    title: "法律",
    links: [
      { label: "隐私政策", to: "/privacy" },
      { label: "服务条款", to: "/terms" },
    ],
  },
];

const linkCls =
  "text-[13px] text-ink-muted transition-colors hover:text-ink " +
  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/40 rounded";

/** 页脚：品牌简介 + 三列链接 + 底部条。 */
export default function Footer() {
  return (
    <footer className="mt-24 border-t border-black/[0.06] bg-white/40">
      <div className="mx-auto max-w-content px-5 py-14 md:px-10">
        <div className="grid gap-10 md:grid-cols-[1.6fr_1fr_1fr_1fr]">
          <div>
            <Logo orbSize={24} textClassName="text-base font-extrabold text-ink" />
            <p className="mt-3 max-w-[15rem] text-[13px] leading-relaxed text-ink-faint">
              轻量语音，为开黑而生。完全自托管，只服务你和你信任的朋友。
            </p>
          </div>

          {columns.map((col) => (
            <nav key={col.title} aria-label={col.title}>
              <div className="mb-3 text-[13px] font-bold text-ink">{col.title}</div>
              <ul className="flex flex-col gap-2.5">
                {col.links.map((link) => (
                  <li key={link.label}>
                    {link.to ? (
                      <Link to={link.to} className={linkCls}>
                        {link.label}
                      </Link>
                    ) : (
                      <a
                        href={link.href}
                        target="_blank"
                        rel="noreferrer noopener"
                        className={linkCls}
                      >
                        {link.label}
                      </a>
                    )}
                  </li>
                ))}
              </ul>
            </nav>
          ))}
        </div>

        <div className="mt-10 flex flex-col gap-3 border-t border-black/[0.06] pt-6 text-[12px] text-ink-faint sm:flex-row sm:items-center sm:justify-between">
          <span>© {new Date().getFullYear()} Lumen · 免费 · 开源 · 自托管</span>
          <span>Windows 10 / 11</span>
        </div>
      </div>
    </footer>
  );
}
