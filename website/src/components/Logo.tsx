import { Link } from "react-router-dom";
import Orb from "./ui/Orb";

/** 品牌标识：光球 + Lumen 字标，默认链接回首页。 */
interface LogoProps {
  orbSize?: number;
  textClassName?: string;
  to?: string | null;
}

export default function Logo({
  orbSize = 26,
  textClassName = "text-lg font-extrabold tracking-[0.02em] text-ink",
  to = "/",
}: LogoProps) {
  const inner = (
    <>
      <Orb size={orbSize} />
      <span className={textClassName}>Lumen</span>
    </>
  );

  if (to === null) {
    return <span className="flex items-center gap-2.5">{inner}</span>;
  }

  return (
    <Link
      to={to}
      className="flex items-center gap-2.5 rounded-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/50"
      aria-label="Lumen 首页"
    >
      {inner}
    </Link>
  );
}
