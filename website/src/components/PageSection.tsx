import type { ReactNode } from "react";
import Eyebrow from "./ui/Eyebrow";

/** 内容页标准头 + 居中容器（Aurora 浅色）。 */
interface PageSectionProps {
  title: string;
  subtitle?: string;
  eyebrow?: string;
  children: ReactNode;
  wide?: boolean;
}

export default function PageSection({
  title,
  subtitle,
  eyebrow,
  children,
  wide = false,
}: PageSectionProps) {
  return (
    <section
      className={`mx-auto px-5 py-14 md:px-10 md:py-16 ${wide ? "max-w-content" : "max-w-3xl"}`}
    >
      <header className="mb-8">
        {eyebrow && <Eyebrow tone="aurora" className="mb-3">{eyebrow}</Eyebrow>}
        <h1 className="text-[2.1rem] font-extrabold tracking-[-0.01em] text-ink">
          {title}
        </h1>
        {subtitle && <p className="mt-3 text-[15px] text-ink-muted">{subtitle}</p>}
      </header>
      <div className="space-y-4 leading-relaxed text-ink-muted">{children}</div>
    </section>
  );
}
