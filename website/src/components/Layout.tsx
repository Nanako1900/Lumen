import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import Nav from "./Nav";

const footerLinks = [
  { to: "/privacy", label: "隐私政策" },
  { to: "/terms", label: "服务条款" },
  { to: "/about", label: "关于" },
  { to: "/help", label: "帮助" },
];

export default function Layout({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-screen flex-col bg-zinc-950 text-zinc-100">
      <Nav />
      <main className="mx-auto w-full max-w-5xl flex-1 px-4 py-10">{children}</main>
      <footer className="border-t border-zinc-800">
        <div className="mx-auto flex max-w-5xl flex-col gap-3 px-4 py-6 text-sm text-zinc-400 md:flex-row md:items-center md:justify-between">
          <p>© {new Date().getFullYear()} Lumen</p>
          <ul className="flex flex-wrap gap-4">
            {footerLinks.map((link) => (
              <li key={link.to}>
                <Link
                  to={link.to}
                  className="hover:text-zinc-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400 rounded"
                >
                  {link.label}
                </Link>
              </li>
            ))}
          </ul>
        </div>
      </footer>
    </div>
  );
}
