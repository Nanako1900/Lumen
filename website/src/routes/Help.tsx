import PageSection from "../components/PageSection";

const faqs = [
  {
    q: "如何登录？",
    a: "打开桌面客户端点击登录，系统浏览器会跳转到官网完成 OIDC 授权，随后自动返回客户端。你的凭据不会保存在本机明文文件中。",
  },
  {
    q: "连接不上语音怎么办？",
    a: "请先确认网络通畅、防火墙未拦截客户端；尝试重新登录或重启客户端。若仍无法连接，可在关于页查看版本并反馈。",
  },
  {
    q: "PTT（按键说话）如何使用？",
    a: "在客户端设置中绑定 PTT 快捷键，按住即说话、松开即静音，适合嘈杂环境下的开黑沟通。",
  },
  {
    q: "支持哪些平台？",
    a: "当前提供 Windows 桌面客户端。所有聊天与语音能力均在桌面客户端内，官网不提供网页聊天/语音。",
  },
];

export default function Help() {
  return (
    <PageSection title="帮助" subtitle="常见问题与排障指引。">
      <dl className="space-y-4">
        {faqs.map((item) => (
          <div key={item.q} className="rounded-2xl border border-zinc-800 bg-zinc-900 p-6">
            <dt className="font-semibold text-zinc-100">{item.q}</dt>
            <dd className="mt-2 text-sm leading-relaxed text-zinc-400">{item.a}</dd>
          </div>
        ))}
      </dl>
    </PageSection>
  );
}
