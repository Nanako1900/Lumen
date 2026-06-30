# Lumen 客户端（Wails v2 + Svelte，仅 Windows）

Wails v2 Go 外壳（原生能力）+ Svelte 前端（UI / WebRTC / Web Audio）。登录委托官网完成（回环 handoff），桌面只持 `desktop_session_id`。

> 详细设计：[`../docs/design/client-design.md`](../docs/design/client-design.md)。

## 结构（骨架）

```
main.go / app.go        Wails 入口与 App（bindings 聚合）  ← 待落地
auth/                   Web 中介登录（回环 handoff）、desktop_session_id 存储、token 刷新
desktop/                全局 PTT 热键、托盘、单实例、开机自启（v1）
updater/                自动更新（manifest/校验/安装，v1）
config/                 客户端配置（LUMEN_WEB_BASE_URL / API / WS）
frontend/               Svelte 前端
  src/lib/              api(rest/ws)、voice(pipeline/peer/...)、stores、bridge(wails)
  src/components/       UI 组件（登录/侧栏/文字/语音/设置）
```

## 工具链

Wails v2、Go、Node 20、WebView2（Win10/11 自带）。降噪：`@timephy/rnnoise-wasm`（v1）。

## 本地开发

`wails dev`（需先在 IdP/官网就绪后才能完成完整登录链路）。
