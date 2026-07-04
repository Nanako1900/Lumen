const stats = [
  { value: "24ms", label: "端到端延迟" },
  { value: "2–6 人", label: "语音房规模" },
  { value: "~48kbps", label: "每路带宽" },
  { value: "1", label: "个二进制部署" },
];

/** 四项开黑数据条，带上下细分隔线。 */
export default function StatsBar() {
  return (
    <section className="mx-auto max-w-content px-5 md:px-10">
      <dl className="grid grid-cols-2 gap-y-6 border-y border-black/[0.07] py-6 md:grid-cols-4 md:divide-x md:divide-black/[0.09]">
        {stats.map((s) => (
          <div key={s.label} className="text-center">
            <dd className="text-[26px] font-extrabold text-brand-deep">{s.value}</dd>
            <dt className="mt-1 text-[12px] text-ink-faint">{s.label}</dt>
          </div>
        ))}
      </dl>
    </section>
  );
}
