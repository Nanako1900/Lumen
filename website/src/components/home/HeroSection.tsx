import { Link } from "react-router-dom";
import ProductShot from "./ProductShot";
import { buttonClass } from "../ui/button";
import { CheckIcon, CodeIcon, DownloadIcon } from "../icons";
import { GITHUB_URL } from "../../lib/config";

const checks = ["Windows 10 / 11", "免费开源", "自托管"];

/** 首页 Hero：定位徽章 + 主标题 + 双 CTA + 卖点勾选 + 悬浮产品截图。 */
export default function HeroSection() {
  return (
    <section className="mx-auto grid max-w-content items-center gap-10 px-5 pb-12 pt-14 md:px-10 md:pb-16 lg:grid-cols-[1fr_420px] lg:gap-12">
      <div>
        <div className="mb-5 inline-flex items-center gap-2 rounded-full bg-brand/10 py-1.5 pl-2.5 pr-3.5 shadow-[inset_0_0_0_1px_rgba(91,110,245,.2)]">
          <span className="h-[7px] w-[7px] rounded-full bg-aurora shadow-[0_0_8px_rgba(41,212,180,.7)]" />
          <span className="text-[12.5px] font-semibold text-brand-deep">
            v0.4 · 为开黑而生的轻量语音
          </span>
        </div>

        <h1 className="text-[2.75rem] font-extrabold leading-[1.07] tracking-[-0.02em] sm:text-[3.3rem]">
          和朋友开黑，
          <br />
          一句话就上线
        </h1>

        <p className="mt-5 max-w-[28rem] text-[1.05rem] leading-relaxed text-ink-muted">
          低延迟语音专为开黑而生。AI 降噪、类 Discord 频道、完全自托管——只服务你和你信任的朋友。
        </p>

        <div className="mt-8 flex flex-wrap gap-3">
          <Link to="/download" className={buttonClass("primary", "lg")}>
            <DownloadIcon size={17} />
            下载 Windows 客户端
          </Link>
          <a
            href={GITHUB_URL}
            target="_blank"
            rel="noreferrer noopener"
            className={buttonClass("secondary", "lg")}
          >
            <CodeIcon size={17} />
            在 GitHub 查看
          </a>
        </div>

        <ul className="mt-5 flex flex-wrap gap-x-5 gap-y-2 text-[12.5px] font-medium text-ink-faint">
          {checks.map((c) => (
            <li key={c} className="flex items-center gap-1.5">
              <CheckIcon size={14} className="text-aurora-deep" strokeWidth={2.6} />
              {c}
            </li>
          ))}
        </ul>
      </div>

      <div className="mx-auto w-full min-w-0 max-w-[420px] lg:animate-float">
        <ProductShot />
      </div>
    </section>
  );
}
