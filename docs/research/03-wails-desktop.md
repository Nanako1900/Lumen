# Wails v2 在 Windows 的桌面集成能力调研

> 调研日期: 2026-06-29
> 适用版本: Wails v2（v2.x, 本文档 API 以 master / pkg.go.dev 当前为准）
> 目标平台: Windows（重点是全屏游戏场景下的 PTT 门控）
> 说明: 知识截止后信息以本文检索结果为准。Wails v3 仍处 alpha，本文聚焦 v2 生产可用方案，并标注 v3 未来动向。

## 0. 关键背景与版本说明

- **Wails v2 在 Windows 无 CGO 依赖**：v2 已移除 Windows 上的 CGO 要求，使用 WebView2（`edge.Chromium`）作为渲染引擎，且内置纯 Go 版 WebView2Loader（v2.2.0 起默认，不再需要随包分发 `WebView2Loader.dll`）。运行时仍需目标机器安装 WebView2 Runtime（`wails doctor` 可检测）。
- **重要后果**：一旦引入需要 CGO 的库（例如 `energye/systray`、`robotn/gohook`），你在 Windows 上的构建就**重新需要 CGO（`CGO_ENABLED=1` + mingw/gcc）**。这会影响 CI 与交叉编译。下文每个方案都标注了是否需要 CGO。
- `golang.design/x/hotkey` 当前版本 **v0.6.1（2026-06-06）**，纯 Win32 API 封装（`RegisterHotKey`），**Windows 上不需要 CGO**。

---

## 1. 全局热键（应用未聚焦 / 全屏游戏仍生效）+ PTT 门控

### 1.1 三种候选方案对比

| 方案 | 底层机制 | 全屏独占游戏 | 按下/松开 | CGO | 反作弊风险 | 适用 |
|------|---------|------------|----------|-----|----------|------|
| **golang.design/x/hotkey** | Win32 `RegisterHotKey` | **可能被独占全屏吞掉** | v0.6.x 起有 `Keydown()`/`Keyup()` channel | 否 | 低 | 普通全局快捷键 |
| **robotn/gohook** | 低层钩子 `WH_KEYBOARD_LL`（封装 libuiohook，C 库） | **能穿透独占全屏**（与 Discord 同思路） | 原生 `KeyDown`/`KeyHold`/`KeyUp` 事件 | **是** | 中（EAC/BattlEye 可能标记） | 全屏游戏 PTT |
| **直接 Win32 `RegisterHotKey`** | 同上 hotkey 库底层 | 同 hotkey 库 | 需自己处理 `WM_HOTKEY`，松开需额外低层钩子 | 否（用 x/sys） | 低 | 不推荐重复造轮子 |

**核心结论（针对 Lumen 的全屏游戏 PTT 场景）**：

- `RegisterHotKey`（即 `golang.design/x/hotkey` 与裸 Win32）在**独占全屏（exclusive fullscreen）游戏**里会被游戏吞掉键，这是 Windows 既有限制。Discord 的"在全屏独占模式下允许全局快捷键"开关正是靠切换到**低层钩子**来绕过。
- 因此**若 PTT 必须在全屏游戏中生效，推荐 `robotn/gohook`**（低层 `WH_KEYBOARD_LL` 钩子），它天然区分 KeyDown/KeyUp，且不受 `RegisterHotKey` 的"同一组合键只能一个进程占用"限制。
- **代价**：gohook 需 CGO（破坏 Windows 无 CGO 优势）、低层钩子有系统性能成本（回调必须轻量）、且竞技游戏的反作弊（EAC/BattlEye）可能标记第三方全局钩子。
- **务实折中**：很多工具让用户绑定"冷门键"（F13–F24，或把鼠标侧键映射到 F13）来规避冲突；且全屏游戏常以管理员权限运行，**你的应用可能也需以管理员权限运行**，否则钩子/热键在游戏内不触发。

**给 Lumen 的建议**：默认用 `golang.design/x/hotkey`（轻、无 CGO、跨平台），并在设置里提供"全屏游戏兼容模式"开关，开启后切换到 `robotn/gohook` 低层钩子。文档提示用户：全屏游戏内不生效时尝试以管理员身份运行。

### 1.2 Wails 主线程坑（macOS 才有，Windows 无）

- `golang.design/x/hotkey` 在 **macOS 必须在主线程处理事件**（需 `mainthread.Init` 或依赖 Fyne/Ebiten/Gio 的主循环）。**Wails 自己占用主线程**，所以**不要调用 `mainthread.Init`**（会与 Wails 冲突）。社区推荐做法：在 Wails `OnStartup` 回调里**用 goroutine** 注册热键与跑 keydown/keyup 循环。
- **Windows 无主线程限制**，goroutine 里直接注册即可，最省事。

### 1.3 关键 API（golang.design/x/hotkey v0.6.1）

```go
func New(mods []Modifier, key Key) *Hotkey
func (hk *Hotkey) Register() error
func (hk *Hotkey) Unregister() error
func (hk *Hotkey) Keydown() <-chan Event   // 按下
func (hk *Hotkey) Keyup() <-chan Event     // 松开（PTT 关键）
func (hk *Hotkey) String() string
```

修饰键常量（跨平台导出的是 X11 派生的通用名 `ModCtrl/ModShift/Mod1..Mod5`）：

```go
const (
    ModCtrl  Modifier = (1 << 2)
    ModShift Modifier = (1 << 0)
    Mod1     Modifier = (1 << 3)  // Windows 上对应 Alt（MOD_ALT）
    Mod2     Modifier = (1 << 4)
    Mod3     Modifier = (1 << 5)
    Mod4     Modifier = (1 << 6)  // Windows 上对应 Win 键（MOD_WIN）
    Mod5     Modifier = (1 << 7)
)
```

> 注意：库的 Windows 实现里另有 `ModAlt`/`ModWin` 语义（内部 `internal/win`），但跨平台公开常量是 `Mod1..Mod5`。在 Windows 上 **`Mod1=Alt`、`Mod4=Win`**。键常量含 `KeySpace`、`KeyA-Z`、`Key0-9`、`KeyF1-F20`、方向键、媒体键等；未覆盖的键可用原始码 `hotkey.Key(0x15)`。

### 1.4 PTT 代码骨架（方案 A：golang.design/x/hotkey，无 CGO）

```go
package main

import (
	"context"
	"log"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.design/x/hotkey"
)

type App struct {
	ctx context.Context
	hk  *hotkey.Hotkey
}

func NewApp() *App { return &App{} }

// OnStartup 回调
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go a.registerPTT() // Windows 上 goroutine 即可；勿调用 mainthread.Init
}

func (a *App) registerPTT() {
	// 例：Ctrl+Shift+Space。Windows 下 Mod1=Alt, Mod4=Win
	a.hk = hotkey.New([]hotkey.Modifier{hotkey.ModCtrl, hotkey.ModShift}, hotkey.KeySpace)
	if err := a.hk.Register(); err != nil {
		log.Printf("hotkey 注册失败: %v", err)
		// 同组合键被别的进程占用时 RegisterHotKey 会失败
		return
	}
	log.Printf("hotkey %v 已注册", a.hk)

	// PTT 连续循环：按下=开始采集，松开=停止
	for {
		<-a.hk.Keydown()
		runtime.EventsEmit(a.ctx, "ptt:start") // 门控开
		<-a.hk.Keyup()
		runtime.EventsEmit(a.ctx, "ptt:stop")  // 门控关
	}
}

// OnShutdown 回调
func (a *App) shutdown(ctx context.Context) {
	if a.hk != nil {
		a.hk.Unregister()
	}
}
```

前端监听（Wails runtime `EventsOn`）：

```js
import { EventsOn } from "../wailsjs/runtime/runtime";

EventsOn("ptt:start", () => { /* 开始录音 / 取消静音 */ });
EventsOn("ptt:stop",  () => { /* 停止录音 / 静音 */ });
```

### 1.5 PTT 代码骨架（方案 B：robotn/gohook 低层钩子，全屏游戏可穿透，需 CGO）

```go
package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	hook "github.com/robotn/gohook"
)

// 在 OnStartup 里: go a.registerPTTHook()
func (a *App) registerPTTHook() {
	// rawcode 需按目标键的虚拟键码填，可先用 hook.Start() 调试打印
	const pttKey = "f13" // 例：冷门键，规避冲突

	hook.Register(hook.KeyDown, []string{pttKey}, func(e hook.Event) {
		runtime.EventsEmit(a.ctx, "ptt:start")
	})
	hook.Register(hook.KeyUp, []string{pttKey}, func(e hook.Event) {
		runtime.EventsEmit(a.ctx, "ptt:stop")
	})

	s := hook.Start()
	<-hook.Process(s) // 阻塞处理事件，放在 goroutine 中
}
```

> gohook 构建注意：`CGO_ENABLED=1`；Windows 需 mingw/gcc，macOS 需 Xcode。钩子回调要轻量（低层钩子会拖慢全系统输入）。竞技游戏需自测 EAC/BattlEye 是否拦截。

> Wails 官方有 `globalShortcut API` 提案（issue #3112）尚未落地 v2；v3 在跟进，未来或有原生方案。

---

## 2. 系统托盘图标 + 菜单

### 2.1 库选择

- **推荐 `energye/systray`**（getlantern/systray 的维护型 fork，**去掉 GTK 依赖**，支持 Windows/macOS/Linux/BSD；额外提供 `SetOnClick`/`SetOnDClick`/`SetOnRClick`）。源自 Fyne 团队 systray。
- **不推荐 `getlantern/systray`**：macOS 上有 objc 链接冲突；Linux 带 GTK 依赖。
- 备选 `fyne.io/systray`（更活跃，但无右键/双击便捷回调）。
- Wails v2 **无原生托盘 API**；v3 已有原生 systray（`SystemTray.Show()/Hide()` 走 `NIS_HIDDEN`），但本文聚焦 v2。

> **CGO 警告**：`energye/systray` 需 `CGO_ENABLED=1`。引入它会让 Windows 构建重新依赖 CGO。

### 2.2 主循环冲突坑（核心）

Wails 与 systray **都想占用主 OS 线程**，顺序调用会在启动时死锁/冻结。两种解法：

1. **goroutine 法（Windows 实测可行，最常用）**：在 `wails.Run(...)` **之前** `go systray.Run(onReady, onExit)`。托盘在后台 goroutine 监听点击，不与 Wails 抢主循环。
2. **`RunWithExternalLoop` 法（"正规"做法）**：`start, end := systray.RunWithExternalLoop(onReady, onExit)`，由宿主在启动/退出时调用 `start()`/`end()`。注意有历史 issue：用 external loop 时托盘图标偶尔不显示（需保证事件循环存活）。
3. **独立进程 + IPC 法**：托盘做成独立二进制，主程序经 IPC 控制（最稳的跨平台方案，避免 macOS 链接冲突）。

### 2.3 关键 API（energye/systray）

```go
func Run(onReady, onExit func())
func RunWithExternalLoop(onReady, onExit func()) (start, end func())
func SetIcon(iconBytes []byte)   // Windows 需 .ico
func SetTitle(string)
func SetTooltip(string)
func SetTemplateIcon(templateIconBytes, regularIconBytes []byte) // macOS 模板图标
func AddMenuItem(title, tooltip string) *MenuItem
func AddMenuItemCheckbox(title, tooltip string, checked bool) *MenuItem
func AddSubMenuItem(title, tooltip string) *MenuItem
func SetOnClick(func(menu systray.IMenu))   // 左键单击（Win/macOS）
func SetOnDClick(func(menu systray.IMenu))  // 双击
func SetOnRClick(func(menu systray.IMenu))  // 右键（设置后需 menu.ShowMenu() 才弹菜单）
func Quit()
// MenuItem: .ClickedCh (chan), .SetIcon([]byte), .Check(), .Uncheck(), .Disable()
```

### 2.4 托盘 + 显示/隐藏窗口骨架（goroutine 法）

```go
package main

import (
	"os"

	"github.com/energye/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) startTray() {
	go systray.Run(a.onTrayReady, func() {})
}

func (a *App) onTrayReady() {
	systray.SetIcon(trayIconBytes) // //go:embed 的 .ico
	systray.SetTitle("Lumen")
	systray.SetTooltip("Lumen")

	mShow := systray.AddMenuItem("显示主窗口", "")
	mQuit := systray.AddMenuItem("退出", "")

	// 左键单击直接显示窗口（可选）
	systray.SetOnClick(func(menu systray.IMenu) {
		if a.ctx != nil {
			runtime.WindowShow(a.ctx)
		}
	})

	go func() {
		for {
			select {
			case <-mShow.ClickedCh:
				if a.ctx != nil {
					runtime.WindowShow(a.ctx)
				}
			case <-mQuit.ClickedCh:
				systray.Quit()
				runtime.Quit(a.ctx) // 或 os.Exit(0)
				return
			}
		}
	}()
	_ = os.Getpid
}
```

---

## 3. 最小化到托盘 / 关闭按钮不退出

### 3.1 三个相关能力

- **`options.App.HideWindowOnClose bool`**：最简单。设为 `true` 时，点关闭按钮 → 隐藏窗口而非退出应用。
- **`OnBeforeClose func(ctx context.Context) (prevent bool)`**：关闭按钮按下、应用退出前回调。返回 `true` 阻止退出。
- **运行时函数**：`runtime.WindowHide(ctx)` / `runtime.WindowShow(ctx)`；还有 `WindowMinimise`/`WindowUnminimise`/`Show`/`Hide`。

### 3.2 两种实现及坑

**方案 A（推荐，简单）**：`HideWindowOnClose: true` + 托盘 `WindowShow` 还原。

**方案 B（需自定义逻辑）**：`OnBeforeClose` 里 `runtime.WindowHide(ctx)` 后 `return true`。

**坑（重要）**：
- `OnBeforeClose` 与 `HideWindowOnClose` **不能很好共存**——`OnBeforeClose` 仅在 `HideWindowOnClose=false` 时才被调用。二选一。
- 用 `HideWindowOnClose=true` 时**没有专门的"窗口已隐藏"事件**可挂钩，自己维护的"窗口可见"状态可能不同步。
- **macOS bug**：用 `OnBeforeClose` + `WindowHide` 时，有开发者报告点关闭后无法再唤回窗口（UI 卡住）。Windows 不受此影响，但跨平台需测试。

### 3.3 配置骨架

```go
import (
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

err := wails.Run(&options.App{
	Title:             "Lumen",
	Width:             1024,
	Height:            768,
	HideWindowOnClose: true,          // 方案 A：关闭=隐藏到托盘
	OnStartup:         app.startup,
	OnShutdown:        app.shutdown,
	// 方案 B（与 HideWindowOnClose 互斥）：
	// OnBeforeClose: func(ctx context.Context) bool {
	//     runtime.WindowHide(ctx)
	//     return true // 阻止退出
	// },
	Bind: []interface{}{app},
})
```

前端也可主动隐藏：`import { WindowHide } from "../wailsjs/runtime/runtime"`。

---

## 4. 单实例锁 + 第二次启动参数转交首实例

### 4.1 API（Wails v2 原生 `options.SingleInstanceLock`）

```go
type SingleInstanceLock struct {
	UniqueId               string // 用 UUID；Windows/macOS 上做 mutex 名，Linux 做 dbus 名
	OnSecondInstanceLaunch func(secondInstanceData SecondInstanceData)
}

type SecondInstanceData struct {
	Args             []string // 第二实例的命令行参数
	WorkingDirectory string   // 第二实例的工作目录
}
```

- 把 `*options.SingleInstanceLock` 传给 `wails.Run` 的 `options.App`。
- 第二次启动时，新进程退出，参数通过 `OnSecondInstanceLaunch` 回调转交首实例。
- **坑**：`OnSecondInstanceLaunch` **不会自动把窗口拉到前台**，需手动 `runtime.WindowUnminimise` + `runtime.Show`。Linux 上 WM 可能阻止抢焦点。
- **坑（Windows）**：singleton 模式下 `WindowUnminimise` 还原后窗口可能回到旧位置（issue #4109）。
- 常配合自定义协议（custom protocol scheme）/文件关联：协议链接的 URL 经 `SecondInstanceData.Args` 传入。

### 4.2 骨架

```go
import (
	"strings"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) onSecondInstanceLaunch(data options.SecondInstanceData) {
	// 1) 把窗口拉回来（不会自动）
	if runtime.WindowIsMinimised(a.ctx) {
		runtime.WindowUnminimise(a.ctx)
	}
	runtime.Show(a.ctx)
	runtime.WindowShow(a.ctx)
	// 2) 把第二实例参数转交前端处理
	go runtime.EventsEmit(a.ctx, "launchArgs", data.Args)
	_ = strings.Join
}

func main() {
	app := NewApp()
	_ = wails.Run(&options.App{
		Title:     "Lumen",
		OnStartup: app.startup,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "请替换为固定UUID, 如 e3984e08-28dc-4e3d-b70a-45e961589cdc",
			OnSecondInstanceLaunch: app.onSecondInstanceLaunch,
		},
		Bind: []interface{}{app},
	})
}
```

> v3 中改名为 `SingleInstance`/`SingleInstanceOptions`（字段 `UniqueID`），与 v2 不同；v2 用 `SingleInstanceLock`/`UniqueId`。

---

## 5. 开机自启（写注册表 Run 键）

### 5.1 库选择

- **推荐直接用 `golang.org/x/sys/windows/registry`**（官方维护，**无需 CGO**，纯 syscall 封装）。无需第三方库。
- 路径（当前用户，无需管理员权限）：`HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
- 全部用户（需管理员）：用 `registry.LOCAL_MACHINE` 下同名 `Run` 键。
- 一次性自启用 `...\RunOnce`。

### 5.2 关键点 / 坑

1. **写的是"值"不是"子键"**：值名=应用显示名，值数据=可执行文件完整路径。
2. **路径必须相对**：传 `registry.CURRENT_USER` 后，子路径**只写** `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`，**不要**再带 `HKEY_CURRENT_USER\` 前缀（常见错误，会静默失败）。
3. **访问权限**：读写需 `registry.QUERY_VALUE|registry.SET_VALUE`。
4. exe 路径建议用 `os.Executable()` 取当前程序绝对路径；若路径含空格，写入时用引号包裹（尤其带参数时）。
5. 合规提示：Run 键也是恶意软件常用持久化手段，正规软件应在设置里给用户清晰的开/关选项。

### 5.3 骨架

```go
package autostart

import (
	"os"
	"strconv"

	"golang.org/x/sys/windows/registry"
)

const runKeyPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`

// SetAutoStart 开启/关闭当前用户开机自启
func SetAutoStart(enable bool, appName string) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	k, err := registry.OpenKey(
		registry.CURRENT_USER,
		runKeyPath, // 相对路径，勿带 HKEY_CURRENT_USER\
		registry.QUERY_VALUE|registry.SET_VALUE,
	)
	if err != nil {
		return err
	}
	defer k.Close()

	if enable {
		// 路径含空格时建议加引号
		return k.SetStringValue(appName, strconv.Quote(exePath))
	}
	// 关闭：删除值（不存在时返回的错误可忽略）
	if err := k.DeleteValue(appName); err != nil && err != registry.ErrNotExist {
		return err
	}
	return nil
}

// IsAutoStartEnabled 查询是否已设置
func IsAutoStartEnabled(appName string) (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false, err
	}
	defer k.Close()
	_, _, err = k.GetStringValue(appName)
	if err == registry.ErrNotExist {
		return false, nil
	}
	return err == nil, err
}
```

---

## 6. 给 Lumen 的综合建议

1. **PTT 默认 `golang.design/x/hotkey`（v0.6.1，无 CGO）**，设置里加"全屏游戏兼容模式"切到 `robotn/gohook`（低层钩子，需 CGO）。提示用户全屏游戏内不生效时以管理员运行。
2. **托盘用 `energye/systray`**，goroutine 法（`go systray.Run` 在 `wails.Run` 前），注意它引入 CGO 依赖。
3. **关闭=隐藏：`HideWindowOnClose: true`**（与 `OnBeforeClose` 二选一）；托盘菜单 `WindowShow` 还原。
4. **单实例：`options.SingleInstanceLock`** + 固定 UUID，`OnSecondInstanceLaunch` 手动 `Show`/`WindowUnminimise` 并 `EventsEmit` 转交参数。
5. **自启：`golang.org/x/sys/windows/registry`** 写 HKCU Run 键，无需第三方库与 CGO。
6. **CGO 取舍**：若同时引入 `energye/systray` 与/或 `robotn/gohook`，Windows 构建需 `CGO_ENABLED=1` + mingw，CI 与交叉编译要相应配置。若想保住"Windows 无 CGO"，可考虑托盘走独立进程+IPC、PTT 仅用 `RegisterHotKey`。

---

## [参考链接]

- Wails 单实例锁指南: https://wails.io/docs/guides/single-instance-lock/
- Wails Options 参考: https://wails.io/docs/reference/options/
- Wails Window 运行时参考: https://wails.io/docs/reference/runtime/window/
- Wails Events 运行时参考: https://wails.io/docs/reference/runtime/events/
- Wails options 包 (pkg.go.dev): https://pkg.go.dev/github.com/wailsapp/wails/v2/pkg/options
- Wails 全局热键讨论 #2320: https://github.com/wailsapp/wails/discussions/2320
- Wails globalShortcut API 提案 #3112: https://github.com/wailsapp/wails/issues/3112
- Wails 隐藏窗口事件 issue #2989: https://github.com/wailsapp/wails/issues/2989
- Wails v3 systray 文档: https://v3.wails.io/features/menus/systray/
- golang.design/x/hotkey (pkg.go.dev, v0.6.1): https://pkg.go.dev/golang.design/x/hotkey
- golang-design/hotkey (GitHub): https://github.com/golang-design/hotkey
- robotn/gohook (GitHub): https://github.com/robotn/gohook
- robotn/gohook (pkg.go.dev): https://pkg.go.dev/github.com/robotn/gohook
- energye/systray (GitHub): https://github.com/energye/systray
- energye/systray (pkg.go.dev): https://pkg.go.dev/github.com/energye/systray
- energye/systray RunWithExternalLoop issue #6: https://github.com/energye/systray/issues/6
- getlantern/systray: https://github.com/getlantern/systray
- golang.org/x/sys/windows/registry (pkg.go.dev): https://pkg.go.dev/golang.org/x/sys/windows/registry
- x/sys 自启注册表 issue #32748: https://github.com/golang/go/issues/32748
- "Adding a sneaky system tray to Wails v2" 博客: https://www.ullaskunder.com/blogs/adding-a-sneaky-system-tray-to-wails-v2-without-locking-your-threads
- Microsoft: Disabling Shortcut Keys in Games (低层钩子): https://learn.microsoft.com/en-us/windows/win32/dxtecharts/disabling-shortcut-keys-in-games
- Wails v2 Released (Windows 无 CGO/WebView2): https://wails.io/blog/wails-v2-released/
