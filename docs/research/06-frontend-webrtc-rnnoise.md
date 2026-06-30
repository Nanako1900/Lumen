# 前端 WebRTC + RNNoise + 语音控制流水线调研

> 适用场景: Svelte / webview 前端。WebRTC 多人语音，RNNoise WASM 降噪，PTT + VAD 语音门控，远端多路逐人音量，设备枚举/切换，SFU 多 track 重协商，WS 断线重连后重建 PC。
>
> 调研日期: 2026-06-29。版本与行为以本文末尾检索到的官方/仓库为准（知识截止后请复核）。

---

## 0. 关键库与版本（检索确认）

| 库 | 最新版本 | 发布时间 | API 风格 | 是否自带 AudioWorkletNode |
|----|---------|---------|---------|--------------------------|
| `@jitsi/rnnoise-wasm` | **0.2.1** | 约 2024（"a year ago"） | Emscripten 工厂函数 `createRNNWasmModule` / `createRNNWasmModuleSync`，提供 `processAudioFrame` | 否，但 Jitsi Meet 仓库有现成 worklet 可直接抄 |
| `@shiguredo/rnnoise-wasm` | **2025.1.5** | 约 2024-08 | 高层 `Rnnoise.load()` → `createDenoiseState()` → `processFrame()` → `destroy()` | 否（仅帧级 API，需自行包 worklet） |
| `@timephy/rnnoise-wasm` | fork of jitsi 0.2 | — | 自带可直接用的 `NoiseSuppressorWorklet`，已加 `atob`/`self.location.href` polyfill | **是**（drop-in） |

选型建议:
- 想最省事直接拿到一个 `AudioWorkletNode`: 用 `@timephy/rnnoise-wasm`（fork 自 Jitsi，自带 worklet + polyfill）。
- 想用官方维护、自己控制 worklet 细节: 用 `@jitsi/rnnoise-wasm` + 抄 Jitsi Meet 的 `NoiseSuppressorWorklet.ts`。
- 想要更"干净"的帧级 API 自己搭管线: 用 `@shiguredo/rnnoise-wasm`（注意它没有 worklet，且 issue/PR 走 Discord 日语）。

**RNNoise 关键约束**: RNNoise 帧固定 **480 samples**（10ms @ 48kHz），而 AudioWorklet `process()` 每次给 **128 samples**。必须用环形缓冲做帧对齐。AudioWorklet 最小缓冲 128 frames（2.667ms），叠加 RNNoise 的 480，实际端到端延迟约 **640 frames ≈ 13.3ms**。

**WASM 同步加载（worklet 必须）**: AudioWorklet 的 `addModule()` 初始化不等待 Promise，所以 worklet 内必须同步加载 WASM。Jitsi 用 emscripten 的 `-s SINGLE_FILE=1`（把 wasm 以 base64 内联）+ `-s WASM_ASYNC_COMPILATION=0`，产出 `rnnoise-sync.js`。worklet 里调用的工厂是 `createRNNWasmModuleSync`。

**Safari 注意**: Safari/iOS 对 AudioContext 有额外限制，AudioWorklet 兼容性不如 Chromium。需准备 ScriptProcessor 回退或 polyfill。webview（WKWebView）等同 Safari 行为。

---

## 1. getUserMedia 约束 + RNNoise worklet + 输出 track 加入 PC

### 1.1 采集约束

关闭浏览器内置降噪（`noiseSuppression:false`，交给 RNNoise），保留回声消除与自动增益:

```js
const RAW_AUDIO_CONSTRAINTS = {
  audio: {
    echoCancellation: { ideal: true },
    autoGainControl:  { ideal: true },
    noiseSuppression: { exact: false }, // 关键: 关掉浏览器降噪，避免与 RNNoise 叠加
    channelCount:     { ideal: 1 },
    sampleRate:       { ideal: 48000 }, // RNNoise 设计采样率
  },
  video: false,
};

const rawStream = await navigator.mediaDevices.getUserMedia(RAW_AUDIO_CONSTRAINTS);
// 校验实际生效值（约束 != 设置）
const settings = rawStream.getAudioTracks()[0].getSettings();
console.log("actual:", settings.noiseSuppression, settings.echoCancellation, settings.autoGainControl);
```

注意:
- `exact:false` 是强制约束，若设备不支持关闭会 reject；可改 `ideal:false` 容错。
- Chrome 历史上 `autoGainControl` 与 `echoCancellation` 耦合，务必用 `getSettings()` 核对。
- 运行时可用 `track.applyConstraints({...})` 改约束；用 `navigator.mediaDevices.getSupportedConstraints()` 先探测支持项。

### 1.2 接入 RNNoise worklet（用 @timephy/rnnoise-wasm，drop-in）

```js
// Vite 下用 ?worker&url 拿到 worklet 脚本 URL
import { NoiseSuppressorWorklet_Name } from "@timephy/rnnoise-wasm";
import NoiseSuppressorWorklet from "@timephy/rnnoise-wasm/NoiseSuppressorWorklet?worker&url";

export async function buildDenoisedTrack(rawStream) {
  const ctx = new AudioContext({ sampleRate: 48000 });
  await ctx.audioWorklet.addModule(NoiseSuppressorWorklet);

  const source  = ctx.createMediaStreamSource(rawStream);
  const denoise = new AudioWorkletNode(ctx, NoiseSuppressorWorklet_Name);
  const dest    = ctx.createMediaStreamDestination(); // 拿到处理后的 MediaStream

  source.connect(denoise).connect(dest);

  const processedTrack = dest.stream.getAudioTracks()[0];
  return { ctx, source, denoise, dest, processedTrack };
}
```

把处理后的 track 加入 PC:

```js
const { ctx, processedTrack } = await buildDenoisedTrack(rawStream);
// 用 addTransceiver 显式声明方向，便于 SFU 场景
const sender = pc.addTrack(processedTrack, new MediaStream([processedTrack]));
// 或: const tx = pc.addTransceiver(processedTrack, { direction: "sendrecv" });
```

### 1.3 自定义 worklet（基于 @jitsi/rnnoise-wasm，含 480/128 环形缓冲）

Jitsi Meet 的 `NoiseSuppressorWorklet.ts` 核心结构（从源码提取）:

```js
// processor 文件（注册到 AudioWorkletGlobalScope）
import { createRNNWasmModuleSync } from "@jitsi/rnnoise-wasm";

const RNNOISE_SAMPLE_LENGTH = 480;       // RNNoise 帧大小
const PROCESS_BLOCK = 128;               // AudioWorklet 块大小
// 环形缓冲取 128 与 480 的最小公倍数(=1920)，避免 rollover 残料拆分
const CIRCULAR_BUFFER_LENGTH = 1920;

class NoiseSuppressorWorklet extends AudioWorkletProcessor {
  constructor() {
    super();
    this._wasm = createRNNWasmModuleSync();       // 同步加载内联 wasm
    this._denoiseState = /* 由 wasm 模块创建 denoise state */;
    this._circularBuffer = new Float32Array(CIRCULAR_BUFFER_LENGTH);
    this._inputBufferLength = 0;
    this._denoisedBufferLength = 0;
    // ...输出索引等
  }

  process(inputs, outputs) {
    const inData  = inputs[0][0];
    const outData = outputs[0][0];
    if (!inData) return true;

    // 1) 把本次 128 样本塞进环形缓冲
    this._circularBuffer.set(inData, this._inputBufferLength);
    this._inputBufferLength += inData.length;

    // 2) 凑够 480 就处理一帧（可能一次 process 凑不够，也可能凑够多帧）
    for (; this._denoisedBufferLength + RNNOISE_SAMPLE_LENGTH <= this._inputBufferLength;
           this._denoisedBufferLength += RNNOISE_SAMPLE_LENGTH) {
      const frame = this._circularBuffer.subarray(
        this._denoisedBufferLength,
        this._denoisedBufferLength + RNNOISE_SAMPLE_LENGTH,
      );
      // 原地降噪；第二参 true = 复制回 frame
      this._denoiseProcessor.processAudioFrame(frame, true);
    }

    // 3) 从已降噪区取 128 样本写到 outData；处理读写指针 + rollover
    // ...（管理 _denoisedBufferLength / 输出游标，超过 LCM 时绕回头部）
    return true; // 持续运行
  }
}

registerProcessor("NoiseSuppressorWorklet", NoiseSuppressorWorklet);
```

要点: 因为 `process()` 给的是 128，RNNoise 要 480，所以两种情况——(a) 数据不够先缓冲；(b) 够了但处理后有残料继续缓冲。用 LCM(128,480)=1920 长度的环形缓冲，绕回时整除对齐，避免拆分残料。

---

## 2. PTT 门控 + VAD 切换

两种门控手段，按需选:

```js
// 方式 A: 直接禁用 track（最省 CPU，但是硬切，会瞬断）
function setPTT(track, pressed) { track.enabled = pressed; }
// track.enabled=false 时仍占 RTP 但发静音帧（CPU/带宽最低）

// 方式 B: worklet 旁路 / GainNode 平滑门控（无瞬断爆音，可做淡入淡出）
const gateGain = ctx.createGain();
source.connect(denoise).connect(gateGain).connect(dest);
function setGate(open) {
  const now = ctx.currentTime;
  gateGain.gain.setTargetAtTime(open ? 1 : 0, now, 0.02); // 20ms 时间常数，防爆音
}
```

PTT 与 VAD 模式切换（状态机）:

```js
const Mode = { PTT: "ptt", VAD: "vad", OPEN: "open" };
let mode = Mode.PTT;

function applyTransmit(shouldSend) {
  // 统一出口：决定是否真正向远端发声
  setGate(shouldSend);          // 平滑
  // 或 setPTT(processedTrack, shouldSend); // 硬切
}

// PTT: 按下发声
button.addEventListener("pointerdown", () => mode === Mode.PTT && applyTransmit(true));
button.addEventListener("pointerup",   () => mode === Mode.PTT && applyTransmit(false));
// VAD: 由第 3 节的 speaking 状态驱动 applyTransmit(speaking)
```

---

## 3. 说话检测（AnalyserNode + RMS + 滞回 + 挂起延迟）

用时域数据 `getFloatTimeDomainData()`（比频域更适合能量检测、响应快），算 RMS，配双阈值滞回 + onset 去抖 + hangover 挂起延迟，防抖动闪烁。

```js
function createSpeakingDetector(ctx, sourceNode, onChange) {
  // 可选: 先带通 300–3000Hz 聚焦语音共振峰，抑制低频隆隆/高频噪
  const hp = ctx.createBiquadFilter(); hp.type = "highpass"; hp.frequency.value = 300;
  const lp = ctx.createBiquadFilter(); lp.type = "lowpass";  lp.frequency.value = 3000;
  const analyser = ctx.createAnalyser();
  analyser.fftSize = 2048;
  analyser.smoothingTimeConstant = 0.2;
  sourceNode.connect(hp).connect(lp).connect(analyser); // 分析旁路，不接 destination

  const buf = new Float32Array(analyser.fftSize);
  const ENTER = 0.04;   // 进入说话阈值（高）
  const EXIT  = 0.02;   // 退出说话阈值（低）—— 滞回防抖
  const ONSET_FRAMES = 3;   // 连续超阈帧数才算开始（去突发噪声，~64ms）
  const HANGOVER_MS = 250;  // 低于阈值后仍判定说话的挂起时长
  const ALPHA = 0.3;        // RMS 一阶 IIR 平滑

  let speaking = false, env = 0, onsetCount = 0, hangTimer = null, raf = 0;

  function tick() {
    analyser.getFloatTimeDomainData(buf);
    let sum = 0;
    for (let i = 0; i < buf.length; i++) sum += buf[i] * buf[i];
    const rms = Math.sqrt(sum / buf.length);
    env = ALPHA * rms + (1 - ALPHA) * env; // 平滑包络

    if (!speaking) {
      if (env > ENTER) {
        if (++onsetCount >= ONSET_FRAMES) { speaking = true; onsetCount = 0; onChange(true); }
      } else onsetCount = 0;
    } else {
      if (env < EXIT) {
        if (!hangTimer) hangTimer = setTimeout(() => {
          speaking = false; hangTimer = null; onChange(false);
        }, HANGOVER_MS);
      } else if (hangTimer) { clearTimeout(hangTimer); hangTimer = null; } // 又说话了，取消挂起
    }
    raf = requestAnimationFrame(tick);
  }
  raf = requestAnimationFrame(tick);

  return { destroy() { cancelAnimationFrame(raf); if (hangTimer) clearTimeout(hangTimer); } };
}
```

参数对照（与 ricky0123/vad 的 ML 版本概念一致）: `ENTER/EXIT` = 双阈值滞回；`ONSET_FRAMES` = 突发去抖（最短语音段）；`HANGOVER_MS` = redemption/挂起，词间停顿不误判结束。生产可考虑把分析放进 AudioWorklet 以脱离主线程，或直接用成熟库（Hark / WeBAD / **ricky0123/vad**，后者基于 Silero ML 模型、可脱离主线程）。

---

## 4. 远端多路音频：逐人 GainNode（音量 / 本地静音）

**关键坑（必读）**: 直接设 `<audio>.volume` 在 Chrome Linux/macOS、跨浏览器不可靠。正确做法是 `MediaStreamSource → GainNode → MediaStreamDestination`，把输出 track 给 `<audio>` 播放。**另一个老 bug**: 仅靠 Web Audio 图，Chrome 不会真正拉取 WebRTC 远端流的音频——必须额外把远端流挂到一个（静音的）`<audio>` 元素上，Web Audio 图才会有数据流动。

```js
// 每个远端参与者一套节点
function attachRemote(ctx, remoteStream) {
  // (a) 反 Chrome bug：挂一个静音 audio 元素"驱动"音频管线
  const sink = new Audio();
  sink.srcObject = remoteStream;
  sink.muted = true;          // 不直接出声，出声走 Web Audio
  sink.play().catch(() => {}); // 受 autoplay 策略限制，见下

  // (b) Web Audio 图：source -> gain -> destination
  const audioTrack = remoteStream.getAudioTracks()[0];
  const src  = ctx.createMediaStreamSource(new MediaStream([audioTrack]));
  const gain = ctx.createGain();
  const dst  = ctx.createMediaStreamDestination();
  src.connect(gain).connect(dst);

  // (c) 真正出声的 audio 元素
  const out = new Audio();
  out.srcObject = dst.stream;
  out.play().catch(() => {});

  return {
    setVolume: (v) => { gain.gain.value = Math.min(v, 4); }, // >5 易破音
    setMuted:  (m) => { gain.gain.value = m ? 0 : 1; },      // 本地静音某人
    destroy:   () => { src.disconnect(); gain.disconnect(); out.srcObject = null; sink.srcObject = null; },
  };
}
```

注意:
- Firefox 用 `createMediaStreamTrackSource()`，Chrome/Safari 用 `createMediaStreamSource()`，需分支兼容。
- 增益 >5 易破音；本地静音直接置 gain=0。
- **Autoplay 策略**: 首次必须在用户手势（click/tap）回调里 `ctx.resume()` 并 `audio.play()`，否则被浏览器拦截。webview 同理。
- 缺点: Web Audio 图会引入额外延迟。

---

## 5. 设备枚举与切换 + 麦克风测试（本地回听）

```js
// 5.1 枚举（需先 getUserMedia 拿权限，否则 label 为空、deviceId 受限）
async function listMics() {
  const all = await navigator.mediaDevices.enumerateDevices();
  return all.filter((d) => d.kind === "audioinput"); // default 设备排在最前
}

// 5.2 切换：方式 A——applyConstraints（在轨切换，无需重建流）
await audioTrack.applyConstraints({ deviceId: { exact: newMicId } });
// exact 切不到会 reject；plain { deviceId: newMicId } 只是偏好不报错

// 5.2 切换：方式 B——重新 getUserMedia + replaceTrack（WebRTC 通话中推荐，免重协商）
async function switchMic(pc, newMicId) {
  const ns = await navigator.mediaDevices.getUserMedia({
    audio: { deviceId: { exact: newMicId }, noiseSuppression: { exact: false },
             echoCancellation: true, autoGainControl: true },
  });
  const newRaw = ns.getAudioTracks()[0];
  // 若有 RNNoise 链：用 newRaw 重建 source->denoise->dest，拿到新的 processedTrack
  const sender = pc.getSenders().find((s) => s.track && s.track.kind === "audio");
  await sender.replaceTrack(processedTrack); // 无需 onnegotiationneeded
}

// 5.3 确认实际生效设备
console.log(audioTrack.getSettings().deviceId);

// 5.4 设备热插拔
navigator.mediaDevices.addEventListener("devicechange", async () => {
  updateMicUI(await listMics());
});

// 5.5 麦克风测试（本地回听）—— 临时把本地链接到扬声器
function startMicTest(ctx, sourceNode) {
  const monitor = ctx.createGain(); monitor.gain.value = 1;
  sourceNode.connect(monitor).connect(ctx.destination); // 注意回声，建议戴耳机
  return () => monitor.disconnect();
}
```

坑: 部分浏览器刷新后 `deviceId` 会变，持久化保存的 id 不保证下次有效，切换前应校验存在性。

---

## 6. 与 SFU 的多 track 重协商（Perfect Negotiation）

一个 WebRTC 会话只来自一次 offer/answer；之后加 track / 切 codec / ICE 重启都需要再一轮。用 **Perfect Negotiation** 模式（polite/impolite 角色）处理 glare。对 SFU 而言，浏览器侧只是与 SFU 这"一个对端"协商，模式照用。**复用同一个 PC，不要每次重协商都新建 PC。**

```js
// polite: 由应用/SFU 约定一侧为 true
function wirePerfectNegotiation(pc, signaler, polite) {
  let makingOffer = false;
  let ignoreOffer = false;
  let isSettingRemoteAnswerPending = false;

  pc.onnegotiationneeded = async () => {
    try {
      makingOffer = true;
      await pc.setLocalDescription();                  // 无参，自动生成 offer
      signaler.send({ description: pc.localDescription });
    } catch (e) { console.error(e); }
    finally { makingOffer = false; }
  };

  pc.onicecandidate = ({ candidate }) => signaler.send({ candidate });

  signaler.onmessage = async ({ description, candidate }) => {
    try {
      if (description) {
        const readyForOffer =
          !makingOffer &&
          (pc.signalingState === "stable" || isSettingRemoteAnswerPending);
        const offerCollision = description.type === "offer" && !readyForOffer;
        ignoreOffer = !polite && offerCollision;       // impolite 直接忽略冲突 offer
        if (ignoreOffer) return;

        isSettingRemoteAnswerPending = description.type === "answer";
        await pc.setRemoteDescription(description);     // polite 遇冲突会隐式 rollback
        isSettingRemoteAnswerPending = false;

        if (description.type === "offer") {
          await pc.setLocalDescription();              // 自动生成 answer
          signaler.send({ description: pc.localDescription });
        }
      } else if (candidate) {
        try { await pc.addIceCandidate(candidate); }
        catch (e) { if (!ignoreOffer) throw e; }
      }
    } catch (e) { console.error(e); }
  };
}
```

SFU 特别注意:
- 很多 SFU（如 Pion 系）会显式 gate 重协商，避免两边同时 offer。如 SFU 返回"正在与你重协商"标志（如 `IsAllowNegotiation()===false`），客户端应等其完成；浏览器侧靠 `onnegotiationneeded` 知道何时该发起。
- `negotiationneeded` 当作"提示"——可能 spurious 触发，发 offer 前确认确有变更。
- 加 sendrecv track 后通常会触发 `negotiationneeded`；进行中再有变更会等当前协商完成才再触发。
- 只能有一方 polite，否则死锁。Safari ≥14、Chrome（已修 rollback）均支持。
- 可设 kill-switch: 若 `have-local-offer` 卡住 >10s 强制 teardown（权衡，约 0.4% 误伤）。

---

## 7. WS 断线重连后重建 PC 的策略（分层）

核心原则: **临时网络抖动用 ICE restart 原地恢复（保留流/DTLS/SRTP），别动不动重建 PC；只有远端真消失或 ICE restart 反复失败才整体重建。** ICE restart 实测约 2/3 成功率，失败主因常是远端已离开。重连前必须确保 WS 信令通道已恢复（新 offer 要走它）。

```js
// 7.1 监控连接状态（同时看 connectionState，Firefox 的 iceConnectionState=failed 不总触发）
function wireRecovery(pc, ensureWsConnected, rebuildPc) {
  pc.oniceconnectionstatechange = () => {
    if (pc.iceConnectionState === "failed") {
      // 可选: 切 TURN —— pc.setConfiguration(newCfg) 必须在 restartIce 之前
      pc.restartIce();           // 触发 onnegotiationneeded，走第6节流程重发 offer(iceRestart)
    }
    // 可选: disconnected 时不立刻动作，给它自愈机会；想更快可在此预先 restartIce（Firefox 慎用）
  };

  pc.onconnectionstatechange = async () => {
    if (pc.connectionState === "failed") {
      await ensureWsConnected();  // 先保证信令活着
      // restartIce 已在跑；若多次仍 failed → 整体重建
    }
  };
}

// 7.2 WS 自动重连（指数退避），重连成功后再考虑重建/重协商
function connectSignaling(url, onReady) {
  let backoff = 500;
  function open() {
    const ws = new WebSocket(url);
    ws.onopen = () => { backoff = 500; onReady(ws); };
    ws.onclose = () => setTimeout(open, backoff = Math.min(backoff * 2, 10000));
    ws.onerror = () => ws.close();
  }
  open();
}

// 7.3 整体重建 PC（ICE restart 反复失败 / 远端消失的兜底）
async function rebuildPc(oldPc, makePc, localProcessedTrack, polite, signaler) {
  try { oldPc.close(); } catch {}
  const pc = makePc();                       // 新建，重新加 ICE servers
  wirePerfectNegotiation(pc, signaler, polite);
  pc.addTrack(localProcessedTrack);          // 重新加本地（已降噪）track
  // SFU 通常会因新 PC 重新下发远端 track，靠 pc.ontrack 重新 attachRemote()
  return pc;
}
```

分层策略小结:
1. `disconnected`: 等自愈或（可选）预先 `restartIce()` 加速（Firefox 慎）。
2. `failed`: 确保 WS 活着 → 可选 `setConfiguration()` 换 TURN → `restartIce()` 原地恢复，保留媒体/数据通道。`restartIce()` 会反复在 `signalingState` 回到 stable 时重发 `negotiationneeded` 直到成功。
3. ICE restart 反复失败（多半远端已走）→ 整体 teardown + 重建 PC + 重跑完整 offer/answer。
4. WS 用指数退避自动重连；PC 复用优先，重建为兜底。
5. 同时监听 `connectionState` 与 `iceConnectionState`（跨浏览器更稳）。

---

## 8. webview / Svelte 集成注意点

- **生命周期**: AudioContext / PC / worklet / detector 在 Svelte 组件 `onDestroy` 中显式 `disconnect()` / `close()` / `track.stop()`，避免泄漏。
- **store 驱动 UI**: 把 `speaking`、每人音量、设备列表、连接状态放到 Svelte store，UI 响应式更新。
- **autoplay/手势**: webview（WKWebView/Android WebView）受 autoplay 策略约束，`ctx.resume()` 与 `audio.play()` 必须在用户手势回调里首次触发。
- **WASM/worklet 资源**: 确保 `.wasm` 与 worklet 脚本以正确 MIME（`application/wasm`）由服务/打包器提供；Vite 用 `?worker&url` 取 worklet URL。Safari 行为下准备 ScriptProcessor 回退。
- **权限**: webview 需在原生层授予麦克风权限，否则 `getUserMedia` reject、`enumerateDevices` label 为空。

---

## [参考链接]

RNNoise / WASM:
- @jitsi/rnnoise-wasm (npm 0.2.1): https://www.npmjs.com/package/@jitsi/rnnoise-wasm
- jitsi/rnnoise-wasm (源码 + README，sync 加载说明): https://github.com/jitsi/rnnoise-wasm
- Jitsi Meet NoiseSuppressorWorklet.ts（环形缓冲/process 实现）: https://github.com/jitsi/jitsi-meet/blob/master/react/features/stream-effects/noise-suppression/NoiseSuppressorWorklet.ts
- @shiguredo/rnnoise-wasm (npm 2025.1.5): https://www.npmjs.com/package/@shiguredo/rnnoise-wasm
- shiguredo/rnnoise-wasm (源码 + DevTools demo): https://github.com/shiguredo/rnnoise-wasm
- @timephy/rnnoise-wasm (drop-in AudioWorkletNode fork): https://www.npmjs.com/package/@timephy/rnnoise-wasm
- Jitsi 降噪博客: https://jitsi.org/blog/enhanced-noise-suppression-in-jitsi-meet/
- RTC 降噪实践（缓冲/延迟数学）: https://tagdiwalaviral.medium.com/struggles-of-noise-reduction-in-rtc-part-4-9499f313604

getUserMedia / 约束 / 设备:
- 约束与能力 (MDN, 2025-09): https://developer.mozilla.org/en-US/docs/Web/API/Media_Capture_and_Streams_API/Constraints
- MediaTrackSettings (MDN, 2025-10): https://developer.mozilla.org/en-US/docs/Web/API/MediaTrackSettings
- enumerateDevices (MDN): https://developer.mozilla.org/en-US/docs/Web/API/MediaDevices/enumerateDevices
- devicechange 事件 (MDN): https://developer.mozilla.org/en-US/docs/Web/API/MediaDevices/devicechange_event
- WebRTC 媒体设备入门: https://webrtc.org/getting-started/media-devices
- getUserMedia 音频约束: https://blog.addpipe.com/getusermedia-audio-constraints/

VAD / RMS:
- AnalyserNode.getFloatTimeDomainData (MDN): https://developer.mozilla.org/en-US/docs/Web/API/AnalyserNode/getFloatTimeDomainData
- WebAudio 静音检测: https://pavi2410.com/blog/detect-silence-using-web-audio/
- ricky0123/vad (浏览器 ML VAD): https://github.com/ricky0123/vad
- ricky0123/vad 算法参数: https://docs.vad.ricky0123.com/user-guide/algorithm/

远端音量 / GainNode:
- 远端 WebRTC 音量控制（GainNode 方案 + Chrome 坑）: https://blog.twoseven.xyz/chrome-webrtc-remote-volume/
- MediaStreamAudioSourceNode (MDN): https://developer.mozilla.org/en-US/docs/Web/API/MediaStreamAudioSourceNode

Perfect Negotiation / SFU 重协商:
- Perfect Negotiation 模式 (MDN): https://developer.mozilla.org/en-US/docs/Web/API/WebRTC_API/Perfect_negotiation
- negotiationneeded 事件 (MDN): https://developer.mozilla.org/en-US/docs/Web/API/RTCPeerConnection/negotiationneeded_event
- 官方 Perfect Negotiation 示例: https://webrtc.github.io/samples/src/content/peerconnection/perfect-negotiation/
- Mozilla WebRTC 博客 Perfect Negotiation: https://blog.mozilla.org/webrtc/perfect-negotiation-in-webrtc/
- setRemoteDescription (MDN, 隐式 rollback): https://developer.mozilla.org/en-US/docs/Web/API/RTCPeerConnection/setRemoteDescription

ICE restart / 重连:
- restartIce() (MDN): https://developer.mozilla.org/en-US/docs/Web/API/RTCPeerConnection/restartIce
- iceConnectionState (MDN): https://developer.mozilla.org/en-US/docs/Web/API/RTCPeerConnection/iceConnectionState
- WebRTC 会话生命周期 (MDN): https://developer.mozilla.org/en-US/docs/Web/API/WebRTC_API/Session_lifetime
- ICE restarts (Philipp Hancke): https://medium.com/@fippo/ice-restarts-5d759caceda6
- ICE restart 词条 (BlogGeek.me): https://bloggeek.me/webrtcglossary/ice-restart/
