import { Link } from "react-router-dom";

/** 首页 Hero：一句话定位 + 主/次 CTA（web-design.md §4.1）。 */
export default function Hero() {
  return (
    <section className="flex flex-col items-center gap-8 py-16 text-center md:py-24">
      <div className="space-y-5">
        <h1 className="text-4xl font-bold tracking-tight text-zinc-100 md:text-6xl">
          类 Discord 的
          <span className="text-indigo-400">轻量开黑语音</span>
        </h1>
        <p className="mx-auto max-w-2xl text-lg leading-relaxed text-zinc-400">
          Lumen 让你和朋友一键进房、低延迟畅聊。清爽的 Windows 桌面客户端，专注开黑，别无冗余。
        </p>
      </div>
      <div className="flex flex-col gap-4 sm:flex-row">
        <Link
          to="/download"
          className="inline-flex items-center justify-center rounded-xl bg-indigo-600 px-6 py-3 text-base font-semibold text-white transition-colors hover:bg-indigo-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400"
        >
          下载 Windows 客户端
        </Link>
        <Link
          to="/account"
          className="inline-flex items-center justify-center rounded-xl border border-zinc-700 px-6 py-3 text-base font-semibold text-zinc-200 transition-colors hover:border-zinc-500 hover:bg-zinc-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400"
        >
          登录账户中心
        </Link>
      </div>
    </section>
  );
}
