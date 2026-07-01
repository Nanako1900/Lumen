import Hero from "../components/Hero";

const features = [
  {
    title: "低延迟语音",
    desc: "为开黑而生的实时语音，进房即聊，减少沟通延迟。",
  },
  {
    title: "轻量专注",
    desc: "没有臃肿的多余功能，界面清爽，把资源留给流畅体验。",
  },
  {
    title: "安全登录",
    desc: "经官网中介完成 OIDC 登录，凭据不落桌面，账号更安心。",
  },
];

export default function Home() {
  return (
    <div className="space-y-16">
      <Hero />
      <section className="grid gap-6 md:grid-cols-3">
        {features.map((f) => (
          <div
            key={f.title}
            className="rounded-2xl border border-zinc-800 bg-zinc-900 p-6 shadow-lg"
          >
            <h2 className="text-lg font-semibold text-zinc-100">{f.title}</h2>
            <p className="mt-2 text-sm leading-relaxed text-zinc-400">{f.desc}</p>
          </div>
        ))}
      </section>
    </div>
  );
}
