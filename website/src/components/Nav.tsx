import { Link, NavLink } from "react-router-dom";
import Logo from "./Logo";
import { buttonClass } from "./ui/button";
import { CodeIcon, DownloadIcon } from "./icons";
import { GITHUB_URL } from "../lib/config";

const navItems = [
  { to: "/download", label: "下载" },
  { to: "/help", label: "帮助" },
  { to: "/about", label: "关于" },
];

const linkClass = (isActive: boolean) =>
  [
    "rounded-lg px-1 py-1 text-sm font-medium transition-colors",
    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/50",
    isActive ? "text-brand-deep" : "text-ink-muted hover:text-ink",
  ].join(" ");

/** 顶栏：玻璃质感、粘性置顶。左标识 / 中导航 / 右登录 + 下载 CTA。 */
export default function Nav() {
  return (
    <header className="sticky top-0 z-30 border-b border-white/40 bg-white/60 backdrop-blur-xl">
      <nav
        aria-label="主导航"
        className="mx-auto flex h-16 max-w-content items-center justify-between gap-4 px-5 md:px-10"
      >
        <Logo />

        <ul className="hidden items-center gap-7 md:flex">
          {navItems.map((item) => (
            <li key={item.to}>
              <NavLink to={item.to} className={({ isActive }) => linkClass(isActive)}>
                {item.label}
              </NavLink>
            </li>
          ))}
          <li>
            <a
              href={GITHUB_URL}
              target="_blank"
              rel="noreferrer noopener"
              className="flex items-center gap-1.5 rounded-lg text-sm font-medium text-ink-muted transition-colors hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/50"
            >
              <CodeIcon size={15} />
              GitHub
            </a>
          </li>
        </ul>

        <div className="flex items-center gap-3">
          <Link
            to="/account"
            className="hidden rounded-lg text-sm font-semibold text-ink-muted transition-colors hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/50 sm:inline"
          >
            登录
          </Link>
          <Link to="/download" className={buttonClass("primary", "md")}>
            <DownloadIcon size={15} />
            下载客户端
          </Link>
        </div>
      </nav>
    </header>
  );
}
