# 调研报告 04 · Wails v2 Windows 应用的内置自动更新方案

> 调研日期: 2026-06-29 · 适配本项目 (Wails v2 客户端 + Coolify 自托管更新文件)
> 说明: 模型知识截止 2026-01，本文结论以检索到的官方文档 / GitHub README / pkg.go.dev 为准，已尽量标注版本与发布日期。

---

## 0. TL;DR (关键结论)

1. **Wails v2 没有官方内置自动更新**。内置 updater 是 **Wails v3 (alpha)** 的特性，v2 必须自己实现。
2. 两条主流实务路线: (A) **NSIS 安装包 + 自托管 version manifest (JSON)** —— 检查更新 → 下载新安装包 → 静默安装 → 重启; (B) **selfupdate 类库直接替换 exe** —— `minio/selfupdate` / `creativeprojects/go-selfupdate`。
3. 因为本项目是 **Wails NSIS 打包 + WebView2 桌面应用**，且更新文件托管在 **Coolify** (普通 HTTP 静态托管，不是 GitHub Release)，推荐 **路线 A (NSIS 安装包 + 自托管 JSON manifest)**；如果想要"无感热替换"，可叠加路线 B 的 `creativeprojects/go-selfupdate` + `HttpSource`。
4. Windows 替换正在运行的 exe 的核心技巧: **先 rename 旧 exe (运行中也允许 rename)，再写入新 exe，下次启动清理**；安装包路线则用 NSIS `taskkill /F /IM app.exe /T` 先杀进程再覆盖。
5. 版本检查 endpoint 应返回: 最新版本号 (semver)、平台/架构对应下载 URL、校验和 (SHA256) 与可选签名 (ed25519 / minisign / ECDSA)，并 **强制校验** 后再执行。

---

## 1. Wails 官方是否提供内置更新 (结论)

| 版本 | 内置 updater | 说明 |
|---|---|---|
| **Wails v2** | ❌ 无 | 官方 long-standing gap。社区方案: `minio/selfupdate`、sidecar 进程、自定义 launcher (如 `marcus-crane/wails-autoupdater`)、自己拉 manifest。见 issue [#1178 "Support Self-Updating"] 与 discussion [#2720 "Different approach to self-update"]。 |
| **Wails v3 (alpha)** | ✅ 有 | 官方文档 `v3.wails.io/guides/distribution/auto-updates/`。提供自动检查 / 下载 / 安装；支持 **bsdiff 二进制 delta 补丁**（只下载版本差异，体积小）；官方 demo 仓库用 **SHA256SUMS** sidecar 做完整性校验，发布 darwin/arm64、linux/amd64、windows/amd64 预编译产物。 |

**对本项目的影响**: 本项目 DESIGN.md 第 2 节明确选型 Wails v2，因此 **无法依赖官方内置更新**，必须走下面的自实现方案。是否升级到 v3 是一个独立决策（v3 仍为 alpha，API 基本稳定但有破坏性变更风险）。

---

## 2. 实务方案对比

### 路线 A: NSIS 安装包 + 自托管 version manifest (JSON)

**流程**: 应用启动 / 定时 → GET manifest JSON → 比较版本 → 若有新版，下载新 NSIS 安装包 (.exe) → 校验 SHA256/签名 → 静默运行安装包 (`/S`) → 安装包杀掉旧进程并覆盖 → 重启应用。

| 优点 | 缺点 |
|---|---|
| 与现有 Wails NSIS 打包流程天然契合 (`wails build -nsis`) | 需要弹 UAC (安装到 Program Files 时) 或安装到用户目录避免提权 |
| 安装包能处理 WebView2 引导、注册表、快捷方式、卸载项 | 更新粒度粗 (整包下载，无 delta) |
| 不用自己处理"替换运行中 exe"的脏活，交给安装包 | 静默安装需正确处理"应用正在运行"提示 |
| Coolify 静态托管即可 (放 JSON + .exe 两个文件) | 安装包体积比纯 exe 大 |

**适配 Coolify**: Coolify 部署一个静态文件服务 (或在现有 Go 服务端加一个 `/updates/` 静态目录)，托管 `latest.json` + `Lumen-Setup-x.y.z.exe` + `.sha256`/`.sig`。无需 GitHub。

### 路线 B: selfupdate 类库直接替换 exe

直接把磁盘上的 `app.exe` 替换为新二进制，不走安装包。三个候选库:

#### B1. `minio/selfupdate` (v0.6.0, 2023-01-22, Apache-2.0)

最底层、最灵活。你自己负责"检查版本 + 拿到 io.Reader"，库负责"安全替换二进制"。

核心 API (来自 pkg.go.dev):

```go
func Apply(update io.Reader, opts Options) error          // 一步到位
func PrepareAndCheckBinary(update io.Reader, opts Options) error  // 写 .target.new + 校验
func CommitBinary(opts Options) error                     // rename 提交
func RollbackError(err error) error                       // 失败回滚检查

type Options struct {
    TargetPath  string        // 空=当前可执行文件
    TargetMode  os.FileMode   // 默认 0755
    Checksum    []byte        // SHA256 校验和；nil=不校验
    Verifier    *Verifier     // 签名校验 (minisign)；nil=不校验
    Hash        crypto.Hash   // 默认 crypto.SHA256
    Patcher     Patcher       // bsdiff 增量补丁；NewBSDiffPatcher()
    OldSavePath string        // 旧文件保留路径；空=删除
}
```

- **Windows 替换原理**: 内部就是 `target → .target.old` (rename，运行中允许)、`.target.new → target`，失败回滚；Windows 上旧文件 hide 处理 (`hide_windows.go`)。
- **签名**: `Verifier` 走 **minisign**；`NewVerifier()` + `LoadFromFile/LoadFromURL` + `Verify(bin)`。
- **注意**: 这是 inconshreveable/go-update 的 minio fork，更活跃。`rhysd/go-update` 是另一支 fork，定位类似。

#### B2. `creativeprojects/go-selfupdate` (v1.5.2, 2025-12-19) —— **本项目首选的"换 exe"库**

比 minio 高一层: 内置"版本发现 (DetectLatest) + 多 Source + 多 Validator + 解压 + 替换 + 回滚"。**支持自定义 HTTP Source**，正好契合 Coolify 自托管。

```go
type Config struct {
    Source        Source     // GitHubSource / GiteaSource / GitLab / HttpSource
    Validator     Validator  // ChecksumValidator / SHAValidator / ECDSAValidator / PGPValidator / PatternValidator
    Filters       []string   // 资产名正则过滤
    OS, Arch      string     // 覆盖 runtime.GOOS/GOARCH
    Prerelease    bool
    Draft         bool
    OldSavePath   string
}

// 自托管 HTTP 源 (Coolify):
type HttpConfig struct {
    BaseURL   string          // 必填，如 https://updates.example.com/
    Transport *http.Transport
    Headers   http.Header
}

source, _ := selfupdate.NewHttpSource(selfupdate.HttpConfig{BaseURL: "https://updates.lumen.app/"})
updater, _ := selfupdate.NewUpdater(selfupdate.Config{
    Source:    source,
    Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"}, // goreleaser 风格
})
rel, found, _ := updater.DetectLatest(ctx, selfupdate.NewRepositorySlug("lumen", "client"))
if found && rel.GreaterThan(currentVersion) {
    exe, _ := selfupdate.ExecutablePath()
    _ = updater.UpdateTo(ctx, rel, exe)
}
```

- **资产命名约定**: `{cmd}_{goos}_{goarch}{.ext}`，压缩支持 `.zip/.gz/.bz2/.tar.gz/.tar.xz`，可用 `-` 代替 `_`。
- **校验器**: `ChecksumValidator{UniqueFilename}` (单 checksums 文件)、`SHAValidator` (每资产一个 `.sha256`)、`ECDSAValidator.WithPublicKey(pem)` (ECDSA 签名)、`PGPValidator`、`PatternValidator` (组合)。校验文件找不到会 `ErrValidationAssetNotFound`，**默认强制校验**。
- **版本比较**: `rel.GreaterThan / LessOrEqual / Equal(version)`。

#### 三库选型小结

| 库 | 版本/日期 | 抽象层级 | 自托管 HTTP | 签名 | 适合 |
|---|---|---|---|---|---|
| `minio/selfupdate` | v0.6.0 / 2023-01 | 低 (只管替换) | 自己拼 | minisign | 想完全自控流程 |
| `creativeprojects/go-selfupdate` | v1.5.2 / 2025-12 | 高 (发现+校验+替换) | ✅ `HttpSource` | ECDSA/PGP/SHA | **本项目换-exe 首选** |
| `rhysd/go-update` (= inconshreveable fork) | 维护较弱 | 低 | 自己拼 | RSA/ECDSA | 历史项目兼容 |

> 注: `rhysd` 还有一个 `rhysd/go-github-selfupdate`，但强绑 GitHub Release，不适合 Coolify 自托管。

### 路线 A vs B 总评 (对本项目)

- 本项目已用 NSIS + WebView2，**路线 A 更稳妥**: 安装包帮你处理进程占用、WebView2、注册表、卸载项，且 Coolify 只需托管静态文件。
- 若追求"启动即静默热更、零 UAC"，可用 **路线 B + `creativeprojects/go-selfupdate` + HttpSource + 安装到用户目录 (`%LOCALAPPDATA%`)**，避免提权。
- 折中推荐 (见第 5 节): **A 为主，B 作为可选小补丁通道**。

---

## 3. Windows 下替换正在运行的 exe 的坑与常见解法

### 核心机制 (必懂)

> Windows 不允许 **删除/写入** 正在运行的 exe，但 **允许 rename/move**。这是所有"自替换"方案的基石 (Go 工具链自身 `go build` 也用此: `main.exe → main.exe~`)。

标准自替换四步:
1. **rename** 当前运行的 `app.exe → app.exe.old` (运行中允许)。
2. **写入** 新 `app.exe` 到原路径。
3. **重启**: 启动新 exe，旧进程退出。
4. **清理**: 下次启动时删除 `app.exe.old` (运行时删不掉)。

### 常见坑

| 坑 | 现象 | 解法 |
|---|---|---|
| 直接覆盖运行中 exe | `Access is denied` / `text file busy` | 改用 rename-then-replace (库已封装) |
| FAT32 不支持 | rename 失败 | 实际 NTFS 普遍，可忽略；安装到 NTFS 路径 |
| `os.Executable()` rename 后返回旧路径 | 重启时路径错 | rename 前先记录原始绝对路径再 spawn |
| 旧 `.old` 文件残留 | 占空间 | 启动时清理；或库的 `OldSavePath` |
| 子进程占用 (WebView2 子进程) | 文件被锁 | 用 `taskkill /T` 杀进程树 |
| 长进程名 (>15 字符) nsProcess 失效 | 杀不掉旧进程 | 改用 `taskkill /F /IM app.exe /T` |

### 三种解法

**解法 1: selfupdate 库自动处理** (路线 B) —— `minio`/`creativeprojects` 内部就是 rename-then-replace + 回滚，最省事。替换后需自己重启进程。

**解法 2: 重启脚本 (batch)** —— 替换完写一个 `.bat`，等父进程退出后启动新 exe 并自删:

```bat
:: restart.bat  (替换完成后由 Go 启动，传入 PID 与 exe 路径)
:waitloop
tasklist /FI "PID eq %1" | findstr /I "%1" >nul
if not errorlevel 1 ( timeout /t 1 /nobreak >nul & goto waitloop )
start "" "%2"
del "%~f0"
```

```go
// Go 侧: 替换完二进制后重启
cmd := exec.Command("cmd", "/C", "restart.bat", strconv.Itoa(os.Getpid()), exePath)
cmd.Start()
os.Exit(0)
```

**解法 3: 安装包静默升级** (路线 A，本项目推荐) —— 下载新 NSIS 安装包，静默运行，安装包内杀进程+覆盖+重启:

```nsis
; project.nsi (改这里，不要改会被覆盖的 wails_tools.nsh)
; 安装前杀掉运行中的应用 (含子进程树)
Section
  nsExec::Exec 'taskkill /F /IM "Lumen.exe" /T'
  Sleep 800
  ; ... SetOutPath / File 覆盖文件 ...
SectionEnd
```

- 静默安装命令行: `Lumen-Setup-x.y.z.exe /S` (区分大小写，`/s` 无效)。
- 自定义安装目录: `... /S /D=C:\Path\To\App` (`/D` 必须最后、无引号、绝对路径)。
- 升级时调旧卸载器: `Uninstall.exe /S _?=C:\Program Files\Lumen` (`_?=` 让卸载器原地运行，父进程可等待)。
- `taskkill /T` 杀进程树可同时清理 WebView2 子进程，规避 nsProcess 的 15 字符名长限制与子进程残留问题。

---

## 4. 版本检查 endpoint 设计 + 签名校验建议

### manifest JSON 设计 (路线 A 用)

托管在 Coolify，例如 `https://updates.lumen.app/latest.json`:

```json
{
  "version": "1.4.2",
  "releasedAt": "2026-06-20T08:00:00Z",
  "notes": "修复语音重连；优化降噪",
  "minSupportedVersion": "1.0.0",
  "platforms": {
    "windows/amd64": {
      "url": "https://updates.lumen.app/Lumen-Setup-1.4.2.exe",
      "size": 28475392,
      "sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
      "signature": "RWQ...base64-ed25519-or-minisign...",
      "installerType": "nsis"
    }
  }
}
```

字段建议:
- `version`: semver，客户端用 `golang.org/x/mod/semver` 或库自带比较。
- `platforms[goos/goarch]`: 多平台分支 (本项目当前只 `windows/amd64`)。
- `url`: 下载地址 (Coolify 静态)。
- `size` + `sha256`: **必填**，下载后强制校验。
- `signature`: **强烈建议**，对二进制做非对称签名，防止 manifest/CDN 被篡改。
- `minSupportedVersion`: 可选，低于此版本强制更新。
- `notes`: 更新日志，给前端弹窗展示。

### endpoint 行为建议

- 走 **HTTPS** (Coolify 自带 TLS / Caddy 反代)。
- 加 `Cache-Control` 短缓存或 ETag，避免 CDN 缓存住旧 manifest。
- 可选: `GET /latest.json?current=1.4.1&os=windows&arch=amd64` 由服务端决定是否推送，便于灰度。
- 校验和文件也可走 goreleaser 风格的 `checksums.txt` (配合 `creativeprojects` 的 `ChecksumValidator`)。

### 签名校验建议 (优先级)

1. **首选 ed25519 / minisign**: 密钥短、验证快、无 CGO。`minio/selfupdate` 的 `Verifier` 原生支持 minisign。私钥离线保管，公钥硬编码进客户端。
2. **次选 ECDSA**: `creativeprojects/go-selfupdate` 的 `ECDSAValidator.WithPublicKey(pem)` 原生支持。
3. **底线: 至少 SHA256 校验和**，防传输损坏 (但不防主动篡改，必须配合 HTTPS + 签名才安全)。
4. **流程**: 先校验和 (完整性) → 再签名 (真实性) → 都过才执行替换/安装。**任一失败立即中止并回滚**，不可静默忽略 (遵循本项目安全规范: 不吞错)。
5. 公钥不要从同一 endpoint 动态下载 (否则等于没签)；硬编码或随安装包分发。

---

## 5. 推荐流程与代码骨架 (Wails v2 + Coolify)

### 推荐架构

- **主通道 = 路线 A (NSIS 安装包 + 自托管 JSON manifest)**: 稳、能处理 WebView2/进程占用/卸载项。
- Coolify 托管 3 类文件: `latest.json` + `Lumen-Setup-x.y.z.exe` + (签名内嵌在 json 或独立 `.sig`)。
- 客户端 Go 后端负责: 拉 manifest → 比较版本 → 下载 → 校验 SHA256+签名 → 静默安装 → 退出由安装包重启。
- 前端 (webview) 负责: 弹"发现新版本 v1.4.2"对话框，调用 binding 触发更新，显示下载进度。
- 版本号注入: `wails build -ldflags "-X main.Version=1.4.2"`，与 NSIS `productVersion` 保持一致。

### 代码骨架 — 客户端 Go 后端 (路线 A)

```go
package updater

import (
    "context"
    "crypto/ed25519"
    "crypto/sha256"
    "encoding/base64"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"

    "golang.org/x/mod/semver"
)

const manifestURL = "https://updates.lumen.app/latest.json"

// 公钥硬编码 (离线私钥签名), 32 字节 ed25519
var updatePublicKey = ed25519.PublicKey( /* 32 bytes */ )

type PlatformAsset struct {
    URL       string `json:"url"`
    Size      int64  `json:"size"`
    SHA256    string `json:"sha256"`
    Signature string `json:"signature"` // base64(ed25519 over installer bytes)
}

type Manifest struct {
    Version   string                   `json:"version"`
    Notes     string                   `json:"notes"`
    Platforms map[string]PlatformAsset `json:"platforms"`
}

type UpdateInfo struct {
    Available bool   `json:"available"`
    Version   string `json:"version"`
    Notes     string `json:"notes"`
}

// CheckForUpdate: 前端按钮 / 启动时调用 (Wails binding)
func (s *Service) CheckForUpdate(ctx context.Context, current string) (UpdateInfo, error) {
    m, err := fetchManifest(ctx)
    if err != nil {
        return UpdateInfo{}, fmt.Errorf("拉取更新清单失败: %w", err)
    }
    // semver 需要前缀 v
    if semver.Compare("v"+m.Version, "v"+current) <= 0 {
        return UpdateInfo{Available: false}, nil
    }
    return UpdateInfo{Available: true, Version: m.Version, Notes: m.Notes}, nil
}

// DownloadAndInstall: 用户确认后调用
func (s *Service) DownloadAndInstall(ctx context.Context) error {
    m, err := fetchManifest(ctx)
    if err != nil {
        return err
    }
    key := runtime.GOOS + "/" + runtime.GOARCH // "windows/amd64"
    asset, ok := m.Platforms[key]
    if !ok {
        return fmt.Errorf("无该平台更新: %s", key)
    }

    tmp, err := os.CreateTemp("", "Lumen-Setup-*.exe")
    if err != nil {
        return err
    }
    defer tmp.Close()

    // 下载 + 同时算 SHA256
    body, err := httpGet(ctx, asset.URL)
    if err != nil {
        return err
    }
    defer body.Close()
    h := sha256.New()
    if _, err := io.Copy(io.MultiWriter(tmp, h), body); err != nil {
        return err
    }
    raw, _ := os.ReadFile(tmp.Name())

    // 1) 完整性: SHA256
    if hex.EncodeToString(h.Sum(nil)) != asset.SHA256 {
        return errors.New("校验和不匹配, 已中止")
    }
    // 2) 真实性: ed25519 签名
    sig, err := base64.StdEncoding.DecodeString(asset.Signature)
    if err != nil || !ed25519.Verify(updatePublicKey, raw, sig) {
        return errors.New("签名校验失败, 已中止")
    }

    // 3) 静默安装 (NSIS): 安装包自身杀旧进程 + 覆盖 + 重启
    //    /S 静默; 安装到用户目录可免 UAC
    cmd := exec.Command(tmp.Name(), "/S")
    cmd.SysProcAttr = detachAttr() // Windows: 不随父进程退出而被杀
    if err := cmd.Start(); err != nil {
        return fmt.Errorf("启动安装包失败: %w", err)
    }
    // 让安装包接管, 退出当前应用
    os.Exit(0)
    return nil
}

func fetchManifest(ctx context.Context) (Manifest, error) {
    var m Manifest
    body, err := httpGet(ctx, manifestURL)
    if err != nil {
        return m, err
    }
    defer body.Close()
    err = json.NewDecoder(body).Decode(&m)
    return m, err
}

func httpGet(ctx context.Context, url string) (io.ReadCloser, error) {
    req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return nil, err
    }
    if resp.StatusCode != http.StatusOK {
        resp.Body.Close()
        return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
    }
    return resp.Body, nil
}

// _ = filepath  // (若用到安装目录拼接)
var _ = filepath.Join
```

> `detachAttr()` (Windows) 用 `&syscall.SysProcAttr{CreationFlags: 0x00000008 /*DETACHED_PROCESS*/ | 0x00000200 /*CREATE_NEW_PROCESS_GROUP*/}`，放在 `*_windows.go` 文件里，避免安装包随主进程退出被杀。

### 代码骨架 — 可选 B 通道 (热替换 exe, 用 creativeprojects/go-selfupdate)

适合"小补丁、免 UAC、安装在 %LOCALAPPDATA%"的场景:

```go
import "github.com/creativeprojects/go-selfupdate"

func selfReplace(ctx context.Context, current string) error {
    src, err := selfupdate.NewHttpSource(selfupdate.HttpConfig{
        BaseURL: "https://updates.lumen.app/bin/",
    })
    if err != nil {
        return err
    }
    up, err := selfupdate.NewUpdater(selfupdate.Config{
        Source:    src,
        Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
    })
    if err != nil {
        return err
    }
    rel, found, err := up.DetectLatest(ctx, selfupdate.NewRepositorySlug("lumen", "client"))
    if err != nil || !found {
        return err
    }
    if rel.LessOrEqual(current) {
        return nil // 已是最新
    }
    exe, err := selfupdate.ExecutablePath()
    if err != nil {
        return err
    }
    if err := up.UpdateTo(ctx, rel, exe); err != nil {
        return err // 库内部 rename-then-replace + 回滚
    }
    return restartSelf(exe) // 见第 3 节解法 2 的 restart.bat
}
```

### NSIS 侧改动 (项目 build/windows/installer/project.nsi)

```nsis
; 安装前杀掉运行中的 Lumen (含 WebView2 子进程树)
Section "Install"
  nsExec::Exec 'taskkill /F /IM "Lumen.exe" /T'
  Sleep 800
  SetOutPath "$INSTDIR"
  File "Lumen.exe"
  ; ... 其余文件 / 快捷方式 / 注册表 ...
  ; 静默安装结束后自动重启
  ${IfNot} ${Silent}
  ${Else}
    Exec '"$INSTDIR\Lumen.exe"'
  ${EndIf}
SectionEnd
```

### Coolify 托管要点

- 用 Coolify 部署一个静态文件服务 (Nginx/Caddy 容器，挂载 `updates/` 目录)，或在现有 Go 服务端 (DESIGN.md 第 5 节的单二进制) 加一个 `http.FileServer` 暴露 `/updates/`。
- 发布脚本: `wails build -nsis -ldflags "-X main.Version=$VER"` → 生成 `Lumen-Setup-$VER.exe` → 算 SHA256 → 用离线 ed25519 私钥签名 → 生成 `latest.json` → 上传到 Coolify 卷 / 触发部署。
- HTTPS 由 Coolify 的反代 (Traefik/Caddy) 自动签发证书。
- manifest 设短 `Cache-Control` 或 ETag，避免发版后客户端读到旧 manifest。

### 发布流水线 (建议)

```
1. bump 版本号 (semver) → 同步 wails.json productVersion + ldflags
2. wails build -platform windows/amd64 -nsis
3. sha256sum Lumen-Setup-x.y.z.exe  → 填入 latest.json
4. minisign/ed25519 离线签名 .exe   → signature 填入 latest.json
5. 上传 .exe + latest.json 到 Coolify 静态目录
6. (可选) 验证: 旧客户端能检测到、下载、校验通过、静默安装、重启
```

---

## 6. 给本项目的最终建议

1. **当前阶段 (v0/v1)**: 采用 **路线 A**。最贴合现有 Wails v2 NSIS + WebView2 + Coolify 架构，风险最低。manifest 用第 4 节 JSON，签名用 **ed25519**，校验顺序 SHA256 → 签名 → 安装。
2. **替换运行中 exe 的活交给 NSIS 安装包**: `taskkill /F /IM Lumen.exe /T` + `/S` 静默 + 安装包内重启，避开自己处理文件锁。
3. **免 UAC 诉求**: 安装到 `%LOCALAPPDATA%\Lumen` (NSIS `InstallDir "$LOCALAPPDATA\Lumen"`)，则静默更新无需提权。
4. **可选增强**: 引入 `creativeprojects/go-selfupdate` (v1.5.2) + `HttpSource` 做小补丁热替换通道，重大版本仍走安装包。
5. **不建议**: 现在为了内置 updater 而升级到 Wails v3 alpha；待 v3 稳定 (GA) 再评估其 bsdiff delta 更新带来的带宽收益。
6. **安全红线** (遵循项目规范): 公钥硬编码、私钥离线、强制校验且失败即中止、全程 HTTPS、错误不静默吞掉。

---

## [参考链接]

- Wails issue #1178 "Support Self-Updating": https://github.com/wailsapp/wails/issues/1178
- Wails discussion #2720 "Different approach to self-update": https://github.com/wailsapp/wails/discussions/2720
- Wails v3 Auto-Updates 官方指南: https://v3.wails.io/guides/distribution/auto-updates/
- Wails NSIS Installer 官方文档: https://wails.io/docs/guides/windows-installer/
- Wails Manual Builds: https://wails.io/docs/guides/manual-builds/
- marcus-crane/wails-autoupdater (社区 MVP): https://github.com/marcus-crane/wails-autoupdater
- "Do you use Wails and need automatic updates?" (sidecar 方案): https://blog.stackademic.com/do-you-use-wails-and-need-automatic-updates-5fdba1485692
- minio/selfupdate (GitHub, v0.6.0): https://github.com/minio/selfupdate
- minio/selfupdate (pkg.go.dev API): https://pkg.go.dev/github.com/minio/selfupdate
- creativeprojects/go-selfupdate (GitHub): https://github.com/creativeprojects/go-selfupdate
- creativeprojects/go-selfupdate (pkg.go.dev, v1.5.2): https://pkg.go.dev/github.com/creativeprojects/go-selfupdate
- rhysd/go-github-selfupdate: https://github.com/rhysd/go-github-selfupdate
- Go issue #21997 (Windows rename 运行中 exe): https://github.com/golang/go/issues/21997
- NSIS Silent Install Parameters 参考: https://silentinstallhq.com/nsis-silent-install-parameters-reference-guide/
- NSIS Reference/SilentInstall: https://nsis.sourceforge.io/Reference/SilentInstall
- NSIS nsProcess plugin: https://nsis.sourceforge.io/NsProcess_plugin
- "Replace a Running Application with a New Version" (Visual Studio Magazine): https://visualstudiomagazine.com/articles/2017/12/15/replace-running-app.aspx
