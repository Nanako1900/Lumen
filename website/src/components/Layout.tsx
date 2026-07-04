import type { ReactNode } from "react";
import Nav from "./Nav";
import Footer from "./Footer";

/** 全站骨架：固定 Aurora 渐变底图层 + 玻璃顶栏 + 内容 + 页脚。 */
export default function Layout({ children }: { children: ReactNode }) {
  return (
    <div className="relative flex min-h-screen flex-col overflow-x-clip text-ink">
      {/* 固定的 Aurora Indigo 渐变背景（移动端更稳，避免 attachment:fixed 抖动）。 */}
      <div aria-hidden="true" className="fixed inset-0 -z-10 transform-gpu bg-aurora" />
      <Nav />
      <main className="flex-1">{children}</main>
      <Footer />
    </div>
  );
}
