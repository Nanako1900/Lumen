# 部署：官网前端 → 腾讯 EdgeOne Pages（步骤清单）

> 目标：把 `website/`（**纯静态 React SPA**）部署到腾讯 **EdgeOne Pages**。
> ✅ 已按 EdgeOne 官方文档核实（2026-07）。

> **本项目实际域名（本文所有占位均已替换为下列真实值）**
> - 官网前端（EdgeOne 上的静态 SPA）：`https://lumen.nanako.com.cn`
> - 后端 = 资源服务器 + OIDC 中介 + 账户中心（Coolify 上的 Go 服务器）：`https://api.lumen.nanako.com.cn`
> - WebSocket：`wss://api.lumen.nanako.com.cn/ws`
> - IdP（自建 OIDC 提供方，**非 Keycloak**）：`https://www.nanako.org`（issuer 就是根，注意带 `www`，无 `/realms` 路径）
> - `lumen.nanako.com.cn` 与 `api.lumen.nanako.com.cn` 同属可注册域 `nanako.com.cn`（`.com.cn` 是公共后缀）→ **同站（same-site）**，因此 `SameSite=Lax` host-only cookie + CORS 设计成立。

> **架构提示（EdgeOne = 纯静态）**：EdgeOne Pages **只**托管构建后的 React SPA（`website/dist/`）——**没有 Pages Functions、没有 EdgeOne KV、EdgeOne 上没有任何密钥**。所有登录中介 / 账户中心 / OIDC / `client_secret` / `refresh_token` 都在 Coolify 上的 **Go 服务器**（`api.lumen.nanako.com.cn`）——见 [`deploy-coolify.md`](./deploy-coolify.md)。`website/edgeone.json` 只保留**构建配置 + SPA 回退（rewrites）**。

## 0. 已核实的关键事实（决定下面每一步）
- **单仓多项目（monorepo）**：官网在 `website/` 子目录 → 必须在**控制台设「根目录 Root directory = `website`」**。EdgeOne 的「根目录」只有控制台有，`edgeone.json` **没有** `root` 字段。构建/输出目录都相对该根目录。
- **`edgeone.json` 只放构建配置 + 回退**：真实字段是顶层 `buildCommand` / `installCommand` / `outputDirectory` / `nodeVersion`（及 `redirects`/`rewrites`/`headers`）。**本项目不再用 Functions/KV/加密变量**——EdgeOne 上没有任何服务端逻辑或密钥。
- **纯静态 SPA，无边缘函数**：`website/` 现在只产出静态资源（`dist/`）。所有 `/auth/*`、`/desktop/*`、`/api/*` 端点都在 `api.lumen.nanako.com.cn` 的 Go 服务器上，**不在 EdgeOne**。SPA 通过跨源 XHR（`credentials:'include'`）与顶层导航访问它们。
- **下载页直取远端 JSON**：SPA 直接 `fetch https://api.lumen.nanako.com.cn/updates/latest.json`（原先的 `/api/download/latest` 代理已删除）。
- **SPA 深链回退**：客户端路由（`/download` `/account` `/help` `/privacy` `/terms` `/about`）用 `edgeone.json` 的 `rewrites` **逐条精确**回退到 `/index.html`；`public/_redirects` 的 `/*` 作额外兜底（EdgeOne 沿用 `_redirects` 则覆盖未知路径，不沿用则被忽略）。因为没有边缘函数路由，回退不会遮蔽任何后端端点（它们都在另一个域名 `api.lumen.nanako.com.cn`）。语法依据：Tencent `pages-templates/examples/custom-rules/edgeone.json`（`rewrites` 支持字面量精确 source）。

## 1. 建 EdgeOne Pages 项目（连 Git）
1. EdgeOne 控制台 → **Pages → 新建项目 → 导入 Git 仓库** → 选 `Nanako1900/Lumen`，分支 `main`。
2. **构建部署配置**（关键，逐项填）：
   - **根目录（Root directory）= `website`** ← 最关键；不填会在仓库根跑构建，报 `npm install ... no package.json`。
   - **安装命令 = `npm ci`**、**构建命令 = `npm run build`**、**输出目录 = `dist`**（均相对根目录）。
   - **Node 版本 = 20.18.0**（EdgeOne 预装版本之一）。
   - 说明：以上四项 `website/edgeone.json` 已声明可覆盖控制台；但**根目录仍须在控制台设**（配置文件无此字段）。
   - **框架预设**：选「无 / Others」（本项目是 Vite，不要选 Next.js 等，以免覆盖构建/输出）。

## 2. 环境变量（控制台，仅构建期公开变量）
EdgeOne 上**没有任何密钥、没有 OIDC 变量、没有 KV 绑定**——它们全部搬到了 Coolify 的 Go 服务器（见 [`deploy-coolify.md`](./deploy-coolify.md)）。这里只需要**构建期**注入的 `VITE_*` 公开变量（会被打进前端产物，故**只能放非敏感值**）：

```
VITE_WEB_BASE_URL   = https://lumen.nanako.com.cn               # 官网自身
VITE_API_BASE_URL   = https://api.lumen.nanako.com.cn          # 后端 = 资源服务器 + 中介 + 账户中心
VITE_UPDATES_LATEST_URL = https://api.lumen.nanako.com.cn/updates/latest.json   # 下载页直取
```
> 精确变量名以 `website/.env.example` 为准（Vite 只暴露 `VITE_` 前缀变量到前端）。**这些是构建期公开值，不是密钥**；任何 `client_secret` / 会话密钥 / `refresh` 密钥都在 Go 服务器上（[`deploy-coolify.md`](./deploy-coolify.md)）。

## 3. 域名与 HTTPS
- [ ] Pages 项目绑定 `lumen.nanako.com.cn`（EdgeOne 边缘自动签发证书）。
- [ ] 按 EdgeOne 指引把 `lumen.nanako.com.cn` 解析/接入 EdgeOne。
- [ ] `lumen.nanako.com.cn`（官网）与 `api.lumen.nanako.com.cn`（后端）是**同站**（same-site，同属可注册域 `nanako.com.cn`；`.com.cn` 为公共后缀）——账户中心的跨源 cookie/CORS 依赖这一点，CORS/cookie 策略在 Go 服务器侧配置（见 [`deploy-coolify.md`](./deploy-coolify.md)）。

## 4. IdP 登记（回调在后端域名）
- IdP 为**自建 OIDC 提供方**（**非 Keycloak**），issuer 就在根：`https://www.nanako.org`（注意带 `www`，**无** `/realms` 路径，必须与 `LUMEN_OAUTH_ISSUER` **完全一致**）。发现文档在 `https://www.nanako.org/.well-known/openid-configuration`。
- [ ] IdP 回调（为机密 client）登记为 **`https://api.lumen.nanako.com.cn/desktop/callback`** 与 **`https://api.lumen.nanako.com.cn/auth/callback`**（回调已从 `lumen.nanako.com.cn` 迁到 `api.lumen.nanako.com.cn`——中介在 Go 服务器上）。
- [ ] 在 IdP 侧把机密 client 配好，令 `access_token.aud` 含 `lumen-api`（本项目 audience = `lumen-api`）。
- [ ] IdP 通告的 scope 为 `openid profile email phone`——**注意没有 `offline_access`**。⚠️ 因此桌面端刷新流程**可能拿不到 `refresh_token`**；若需长期免登录，需在 IdP 侧确认是否有等效机制（否则桌面端将回退到重新走授权码流程）。
- 参考端点（自建 IdP）：`authorize` = `https://www.nanako.org/oauth/authorize`；`token` = `https://www.nanako.org/api/v1/oauth/token`；`userinfo` = `https://www.nanako.org/api/v1/oauth/userinfo`；`jwks_uri` = `https://www.nanako.org/.well-known/jwks.json`。
- 详见 [`idp-setup.md`](./idp-setup.md)。IdP 侧 OIDC 变量的注入在 Go 服务器（[`deploy-coolify.md`](./deploy-coolify.md)），关键项：`LUMEN_OAUTH_ISSUER = https://www.nanako.org`、`LUMEN_OAUTH_JWKS_URL = https://www.nanako.org/.well-known/jwks.json`、`LUMEN_WEB_BASE_URL = https://lumen.nanako.com.cn`（前端 origin，供 CORS）、两个 `*_REDIRECT_URI = https://api.lumen.nanako.com.cn/{desktop,auth}/callback`；`client_id/client_secret` 等仍为占位（见 [`deploy-coolify.md`](./deploy-coolify.md)）。

## 5. 部署 + 验证
1. 触发部署，等构建完成。构建日志应能看到 `npm ci` 在 `website/` 成功、`vite build` 产出 `dist/`。（**不再**期望识别到 `functions/`——本项目已无边缘函数。）
2. **静态与深链**：浏览器地址栏直达 `https://lumen.nanako.com.cn/account` 并刷新 → 应加载 SPA（非 404）。若深链 404，检查 `edgeone.json` 的 `rewrites` 是否生效（第 0 节 SPA 回退）。
3. **账户中心（跨源，后端在 `api.lumen.nanako.com.cn`）**：访问 `https://lumen.nanako.com.cn/account` → 未登录跳 `/auth/login`（顶层导航到 `api.lumen.nanako.com.cn`）→ IdP 登录 → 回 `lumen.nanako.com.cn/account` 显示头像/昵称；SPA 的 `GET https://api.lumen.nanako.com.cn/api/me`（`credentials:'include'`）返回资料。这些端点的部署/验证细节见 [`deploy-coolify.md`](./deploy-coolify.md)。
4. **下载页**：SPA 直接 `fetch https://api.lumen.nanako.com.cn/updates/latest.json`（无本地代理）。

## 6. 客户端对接（后续，客户端就绪时）
- 客户端配置 `LUMEN_WEB_BASE_URL = https://lumen.nanako.com.cn`、`LUMEN_API_BASE_URL = https://api.lumen.nanako.com.cn/api/v1`、`LUMEN_WS_URL = wss://api.lumen.nanako.com.cn/ws`（服务器侧对应 `LUMEN_PUBLIC_WS_URL = wss://api.lumen.nanako.com.cn/ws`）。
- 桌面登录中介在 `api.lumen.nanako.com.cn`；握手线格式**逐字节不变**，未来客户端改动仅 base URL。

## 7. 排障：根目录没设对
现象（构建日志）：
```
[builder] InstallCommand: npm install
npm error enoent Could not read package.json: /dev/shm/repo/lumen-fn-xxxx/package.json
[builder] "npm install" failed, exit code: 254
```
根因与修复：
- **根目录没设成 `website`** → 构建在仓库根执行，找不到 `package.json`（在 `website/`）。**修复：控制台把根目录设为 `website`**（第 1 步）。

## 8. 变量总表（复制到控制台）
| 类别 | 名称 | 值/来源 |
|---|---|---|
| 构建 | 根目录 | `website` |
| 构建 | 安装命令 | `npm ci` |
| 构建 | 构建命令 | `npm run build` |
| 构建 | 输出目录 | `dist` |
| 构建 | Node 版本 | `20.18.0` |
| 构建期公开变量 | `VITE_WEB_BASE_URL` | `https://lumen.nanako.com.cn` |
| 构建期公开变量 | `VITE_API_BASE_URL` | `https://api.lumen.nanako.com.cn` |
| 构建期公开变量 | `VITE_UPDATES_LATEST_URL` | `https://api.lumen.nanako.com.cn/updates/latest.json` |

> **不再有** KV 绑定、加密变量、OIDC 环境变量、Functions——全部搬到 Go 服务器：见 [`deploy-coolify.md`](./deploy-coolify.md)。
