import { Link } from "react-router-dom";
import Orb from "../components/ui/Orb";
import { buttonClass } from "../components/ui/button";

export default function NotFound() {
  return (
    <section className="mx-auto flex max-w-xl flex-col items-center px-5 py-24 text-center md:py-32">
      <Orb size={56} glow />
      <div className="mt-6 text-[3.5rem] font-extrabold leading-none tracking-tight text-brand-deep">
        404
      </div>
      <h1 className="mt-3 text-2xl font-bold text-ink">页面未找到</h1>
      <p className="mt-2 text-[15px] text-ink-muted">你访问的页面不存在或已移动。</p>
      <Link to="/" className={buttonClass("primary", "lg", "mt-7")}>
        返回首页
      </Link>
    </section>
  );
}
