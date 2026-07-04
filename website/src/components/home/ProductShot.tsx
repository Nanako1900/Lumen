import { HeadphonesIcon, LogOutIcon, MicIcon, MicOffIcon, VolumeIcon } from "../icons";

const AVATAR: React.CSSProperties = {
  background:
    "repeating-linear-gradient(45deg,#c8cbe0,#c8cbe0 4px,#d9dcec 4px,#d9dcec 8px)",
};

function EqBars() {
  return (
    <span className="ml-auto flex h-3 items-end gap-[2.5px]" aria-hidden="true">
      {[-0.1, -0.4, -0.6].map((d) => (
        <i
          key={d}
          className="block w-[3px] origin-bottom animate-eq rounded-sm bg-brand"
          style={{ height: "100%", animationDelay: `${d}s` }}
        />
      ))}
    </span>
  );
}

/**
 * 悬浮的产品截图仿件（正在说话态）——纯 CSS/组件复刻设计稿 1a 的客户端小窗，
 * 无外部图片，随主题一致。装饰性，aria-hidden。
 */
export default function ProductShot() {
  return (
    <div className="relative" aria-hidden="true">
      {/* 光晕 */}
      <div
        className="absolute -inset-x-6 -inset-y-8 blur-[10px]"
        style={{
          background:
            "radial-gradient(60% 50% at 60% 30%,rgba(91,110,245,.3),transparent 70%),radial-gradient(50% 40% at 20% 90%,rgba(41,212,180,.22),transparent 70%)",
        }}
      />
      <div
        className="relative overflow-hidden rounded-[20px] shadow-float"
        style={{
          background: "linear-gradient(180deg,#f5f6fd,#e9ebf6)",
          boxShadow:
            "0 30px 60px -20px rgba(60,70,180,.5), inset 0 0 0 1px rgba(255,255,255,.7)",
        }}
      >
        {/* 窗口头 */}
        <div className="flex h-[38px] items-center justify-between border-b border-white/50 bg-white/50 px-3.5 backdrop-blur-md">
          <div className="flex items-center gap-2">
            <span
              className="h-3.5 w-3.5 rounded-full"
              style={{ background: "radial-gradient(circle at 34% 30%,#a9b4ff,#5b6ef5)" }}
            />
            <span className="text-[12.5px] font-bold text-ink">Lumen</span>
          </div>
          <span className="text-[12px] text-ink-faint">✕</span>
        </div>

        {/* 房间标题 */}
        <div className="px-3.5 pb-1 pt-3.5">
          <div className="text-sm font-bold">夜猫开黑房</div>
          <div className="mt-0.5 flex items-center gap-1.5 text-[11px] text-ink-muted">
            <span className="h-[7px] w-[7px] rounded-full bg-aurora-deep shadow-[0_0_7px_rgba(23,180,126,.6)]" />
            已连接 · 24ms · 信号良好
          </div>
        </div>

        {/* 频道面板 */}
        <div className="px-3 pb-3.5 pt-2">
          <div
            className="rounded-[15px] bg-white/60 p-1.5 backdrop-blur-md"
            style={{
              boxShadow:
                "0 6px 20px -8px rgba(91,110,245,.28), inset 0 0 0 1px rgba(91,110,245,.14)",
            }}
          >
            <div className="flex items-center gap-2 px-2.5 pb-1.5 pt-2 text-[12.5px] font-bold text-brand-deep">
              <VolumeIcon size={15} className="text-brand" />
              开黑1
              <span className="ml-auto rounded-full bg-brand/10 px-2 py-0.5 text-[10.5px] font-semibold text-brand">
                3 人
              </span>
            </div>

            {/* Neo 正在说话 */}
            <div
              className="flex items-center gap-2.5 rounded-[11px] px-2.5 py-2"
              style={{
                background:
                  "linear-gradient(90deg,rgba(91,110,245,.18),rgba(91,110,245,.05))",
                boxShadow:
                  "inset 0 0 0 1px rgba(91,110,245,.3), 0 5px 16px -8px rgba(91,110,245,.55)",
              }}
            >
              <span
                className="h-[26px] w-[26px] rounded-full"
                style={{
                  ...AVATAR,
                  boxShadow: "0 0 0 2px #5b6ef5, 0 0 9px rgba(91,110,245,.4)",
                }}
              />
              <span className="text-[12.5px] font-semibold">Neo</span>
              <span className="text-[10px] font-semibold text-brand">正在说话</span>
              <EqBars />
            </div>

            {/* 阿珂 静音 */}
            <div className="flex items-center gap-2.5 rounded-[11px] px-2.5 py-1.5">
              <span className="h-[26px] w-[26px] rounded-full" style={AVATAR} />
              <span className="text-[12.5px] text-ink-muted">阿珂</span>
              <MicOffIcon size={15} className="ml-auto text-danger" />
            </div>

            {/* 你 */}
            <div className="flex items-center gap-2.5 rounded-[11px] px-2.5 py-1.5">
              <span
                className="h-[26px] w-[26px] rounded-full"
                style={{ ...AVATAR, boxShadow: "0 0 0 1.5px rgba(91,110,245,.4)" }}
              />
              <span className="text-[12.5px] font-semibold">你</span>
              <MicIcon size={15} className="ml-auto text-brand" />
            </div>
          </div>

          {/* 自我控制条 */}
          <div className="mt-2.5 flex items-center justify-between rounded-[14px] bg-white/60 px-2.5 py-2.5 shadow-[inset_0_0_0_1px_rgba(255,255,255,.6)] backdrop-blur-md">
            <div className="flex items-center gap-2">
              <span className="h-7 w-7 rounded-full" style={AVATAR} />
              <div className="leading-tight">
                <div className="text-[12px] font-semibold">你</div>
                <div className="text-[10px] text-aurora-deeper">开黑1 中</div>
              </div>
            </div>
            <div className="flex items-center gap-1.5">
              <span className="flex h-[30px] w-[30px] items-center justify-center rounded-[10px] bg-brand text-white shadow-[0_5px_12px_-3px_rgba(91,110,245,.55)]">
                <MicIcon size={15} />
              </span>
              <span className="flex h-[30px] w-[30px] items-center justify-center rounded-[10px] bg-white/70 text-ink-muted shadow-[inset_0_0_0_1px_rgba(20,24,55,.07)]">
                <HeadphonesIcon size={15} />
              </span>
              <span className="flex h-[30px] w-[30px] items-center justify-center rounded-[10px] bg-danger/10 text-danger">
                <LogOutIcon size={15} />
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
