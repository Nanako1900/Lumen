import { Link, NavLink } from "react-router-dom";

const navItems = [
  { to: "/download", label: "下载" },
  { to: "/account", label: "账户中心" },
  { to: "/help", label: "帮助" },
];

export default function Nav() {
  return (
    <header className="border-b border-zinc-800 bg-zinc-950/80 backdrop-blur sticky top-0 z-10">
      <nav className="mx-auto flex max-w-5xl items-center justify-between px-4 py-4">
        <Link
          to="/"
          className="text-lg font-semibold tracking-tight text-zinc-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400 rounded"
        >
          Lumen
        </Link>
        <ul className="flex items-center gap-1 text-sm">
          {navItems.map((item) => (
            <li key={item.to}>
              <NavLink
                to={item.to}
                className={({ isActive }) =>
                  [
                    "rounded-lg px-3 py-2 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400",
                    isActive
                      ? "text-indigo-400"
                      : "text-zinc-400 hover:text-zinc-100 hover:bg-zinc-900",
                  ].join(" ")
                }
              >
                {item.label}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>
    </header>
  );
}
