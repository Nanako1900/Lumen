# shared

（可选）前后端共享的协议类型定义——WebSocket 消息信封与各 `type` 的 payload、REST DTO（`User`/`Channel`/`Message`/`VoiceState`）。

权威定义在 [`../docs/design/protocol-design.md`](../docs/design/protocol-design.md)。本目录用于在实现期沉淀可被服务端（Go `internal/protocol`）与客户端/官网（TypeScript 类型）镜像参照的单一来源；具体形式（生成器 / 手写镜像）在实现时确定。
