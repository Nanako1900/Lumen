import Eyebrow from "../ui/Eyebrow";
import { CheckIcon } from "../icons";

const points = [
  "单二进制 + SQLite，无需 CGO",
  "JWKS 本地验签，零往返鉴权",
  "WSS + 一段 UDP 端口即可上线",
];

function Terminal() {
  return (
    <div className="min-w-0 rounded-[16px] bg-night p-4 shadow-[0_24px_54px_-18px_rgba(30,34,80,.55)]">
      <div className="mb-3 flex items-center gap-1.5" aria-hidden="true">
        <span className="h-2.5 w-2.5 rounded-full bg-danger/80" />
        <span className="h-2.5 w-2.5 rounded-full bg-warn/80" />
        <span className="h-2.5 w-2.5 rounded-full bg-aurora-deep/90" />
      </div>
      <pre className="overflow-x-auto font-mono text-[12.5px] leading-[1.85] text-[#c7cbe6]">
        <div>
          <span className="text-brand">$</span> ./lumen-server --config lumen.toml
        </div>
        <div className="text-[#8b90b5]">
          <span className="text-aurora">✔</span> OAuth JWKS 已加载 (auth.example.com)
        </div>
        <div className="text-[#8b90b5]">
          <span className="text-aurora">✔</span> SQLite 就绪 (lumen.db)
        </div>
        <div className="text-[#8b90b5]">
          <span className="text-aurora">✔</span> SFU 监听 UDP 50000-50100
        </div>
        <div>
          <span className="text-aurora">➜</span> 信令 WSS 监听{" "}
          <span className="text-warn">:443</span>{" "}
          <span className="animate-caret opacity-60">▋</span>
        </div>
      </pre>
    </div>
  );
}

/** 自托管分区：左文案 + 右终端演示，包在极光渐变面板里。 */
export default function SelfHostSection() {
  return (
    <section className="mx-auto max-w-content px-5 pt-24 md:px-10">
      <div
        className="grid items-center gap-9 rounded-[24px] p-8 shadow-[inset_0_0_0_1px_rgba(255,255,255,.5)] md:grid-cols-2 md:p-10"
        style={{
          background:
            "linear-gradient(135deg,rgba(91,110,245,.1),rgba(41,212,180,.09))",
        }}
      >
        <div>
          <Eyebrow tone="brand">自托管</Eyebrow>
          <h2 className="mt-3 text-[1.9rem] font-extrabold tracking-[-0.01em]">
            你的服务器，你做主
          </h2>
          <p className="mb-5 mt-3.5 text-[14.5px] leading-relaxed text-ink-muted">
            一条命令跑起来。单个 Go 二进制内含信令、SFU、频道与鉴权，配上一个 SQLite
            文件即可开黑。公网 IP 直连，通常连 STUN/TURN 都不用。
          </p>
          <ul className="flex flex-col gap-2.5">
            {points.map((p) => (
              <li key={p} className="flex items-center gap-2.5 text-[13.5px] text-ink">
                <CheckIcon size={17} className="text-aurora-deep" strokeWidth={2.4} />
                {p}
              </li>
            ))}
          </ul>
        </div>
        <Terminal />
      </div>
    </section>
  );
}
