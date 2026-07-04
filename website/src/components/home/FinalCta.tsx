import { Link } from "react-router-dom";
import { buttonClass } from "../ui/button";
import { DownloadIcon } from "../icons";

/** 收尾 CTA：靛蓝渐变横幅 + 白色下载按钮。 */
export default function FinalCta() {
  return (
    <section className="mx-auto max-w-content px-5 pb-4 pt-24 md:px-10">
      <div className="relative overflow-hidden rounded-[24px] bg-brand-cta px-8 py-14 text-center shadow-[0_26px_60px_-22px_rgba(62,68,196,.6)] md:px-10">
        <div
          aria-hidden="true"
          className="absolute -left-10 -top-16 h-56 w-56 rounded-full"
          style={{
            background: "radial-gradient(circle,rgba(41,212,180,.4),transparent 68%)",
          }}
        />
        <div
          aria-hidden="true"
          className="absolute -bottom-20 -right-8 h-64 w-64 rounded-full"
          style={{
            background: "radial-gradient(circle,rgba(157,168,255,.5),transparent 68%)",
          }}
        />
        <div className="relative">
          <h2 className="text-[2rem] font-extrabold tracking-[-0.01em] text-white sm:text-[2.15rem]">
            现在就拉朋友进来开黑
          </h2>
          <p className="mx-auto mt-3.5 max-w-md text-[15px] text-white/80">
            下载客户端，登录，进房——三步之内就能开口。
          </p>
          <Link
            to="/download"
            className={buttonClass("white", "lg", "mx-auto mt-6 w-max")}
          >
            <DownloadIcon size={18} />
            下载 Windows 客户端
          </Link>
          <div className="mt-4 text-[12.5px] text-white/70">
            免费 · 开源 · 自托管 · Windows 10 / 11
          </div>
        </div>
      </div>
    </section>
  );
}
