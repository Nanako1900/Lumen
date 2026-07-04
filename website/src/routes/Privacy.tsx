import PageSection from "../components/PageSection";

const H2 = "text-lg font-bold text-ink";

export default function Privacy() {
  return (
    <PageSection title="隐私政策" subtitle="我们如何处理你的账号与数据。" eyebrow="法律">
      <h2 className={H2}>身份与登录</h2>
      <p>
        Lumen 通过外部身份提供方（OAuth2 / OIDC）完成登录。服务端 broker 作为登录中介，
        仅保存必要的会话映射，用于代表桌面客户端完成授权与令牌刷新。
      </p>

      <h2 className={H2}>令牌与凭据留存</h2>
      <ul className="list-disc space-y-1.5 pl-6 marker:text-brand">
        <li>
          刷新令牌（refresh_token）仅<strong className="text-ink">加密</strong>留存于服务端数据库（PostgreSQL），
          <strong className="text-ink">不下发到桌面</strong>，也不出服务端。
        </li>
        <li>
          桌面客户端只持有一个不透明的会话标识（desktop_session_id，存于系统凭据库），
          访问令牌仅存在于客户端内存。
        </li>
        <li>client_secret 仅存在于服务端加密环境变量中，绝不下发前端或桌面。</li>
      </ul>

      <h2 className={H2}>账户中心</h2>
      <p>
        账户中心仅展示来自身份提供方的资料（头像与昵称），并提供下载与退出，
        <strong className="text-ink">不调用 Lumen 聊天服务</strong>，也不访问你的频道或消息数据。
      </p>

      <h2 className={H2}>Cookie</h2>
      <p>
        账户中心使用 HttpOnly、Secure、SameSite 的会话 Cookie 维持登录状态；该 Cookie 不可被前端脚本读取。
      </p>

      <p className="text-sm text-ink-faint">本页为示意性隐私说明，具体条款以正式发布版本为准。</p>
    </PageSection>
  );
}
