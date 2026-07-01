import type { ReactNode } from "react";

/** 静态内容页的标准标题 + 容器。 */
export default function PageSection({
  title,
  subtitle,
  children,
}: {
  title: string;
  subtitle?: string;
  children: ReactNode;
}) {
  return (
    <section className="space-y-6">
      <header className="space-y-2">
        <h1 className="text-3xl font-bold tracking-tight text-zinc-100">{title}</h1>
        {subtitle ? <p className="text-zinc-400">{subtitle}</p> : null}
      </header>
      <div className="space-y-4 leading-relaxed text-zinc-300">{children}</div>
    </section>
  );
}
