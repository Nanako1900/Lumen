import PageSection from "../components/PageSection";

const H2 = "text-lg font-bold text-ink";

export default function Terms() {
  return (
    <PageSection title="服务条款" subtitle="使用 Lumen 前请阅读以下条款。" eyebrow="法律">
      <h2 className={H2}>接受条款</h2>
      <p>下载、安装或使用 Lumen 客户端与官网服务，即表示你同意本服务条款。</p>

      <h2 className={H2}>可接受使用</h2>
      <ul className="list-disc space-y-1.5 pl-6 marker:text-brand">
        <li>不得利用本服务从事违法活动或侵犯他人权益。</li>
        <li>不得干扰、破坏服务的正常运行或规避安全机制。</li>
        <li>你对使用本人账号进行的活动负责，请妥善保管登录凭据。</li>
      </ul>

      <h2 className={H2}>服务变更</h2>
      <p>我们可能不时更新客户端与服务功能；重大变更会通过官网或客户端内通知。</p>

      <h2 className={H2}>免责声明</h2>
      <p>本服务按“现状”提供，在适用法律允许的范围内不对特定用途适用性作出保证。</p>

      <p className="text-sm text-ink-faint">本页为示意性条款，具体以正式发布版本为准。</p>
    </PageSection>
  );
}
