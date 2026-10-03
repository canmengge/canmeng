package main

import (
	"bufio"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"
	endpointupdater "github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"
	"golang.org/x/sys/windows"

	"pvfine/internal/apppaths"
	"pvfine/internal/logging"
	"pvfine/internal/mcp"
	"pvfine/services"
)

// Release builds replace appVersion with -ldflags. Local builds keep 0.0.0,
// which disables the updater (no release channel is contacted).
var (
	appVersion = "0.0.0"
	// lastFrontHeartbeat 是前端 JS 最近一次心跳（UnixMilli）。看门狗据此自动发现
	// 界面停摆并写日志，卡死现场不再依赖人工敲命令。
	lastFrontHeartbeat atomic.Int64
	// updaterSvc 是更新服务实例（暴露「检查更新」给前端调用）。
	updaterSvc *services.UpdateService
	// updaterHandle 是更新窗口句柄（懒创建），由 configureUpdater 初始化。
	updaterHandle *updaterWindowHandle
)

// disableConsoleQuickEdit 关闭本控制台的「快速编辑」模式（Windows 默认开启）。
// 该模式下鼠标在窗口内一点就进入选择状态：键盘输入被冻结、stdout 写入阻塞——
// 既是「控制台不能输入命令」的原因，也会把写日志的 goroutine 一起拖住。
// 没有控制台（GUI 子系统 / 输出重定向）时静默跳过。
func disableConsoleQuickEdit() {
	var mode uint32
	h := windows.Handle(os.Stdin.Fd())
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return
	}
	const quickEditMode = 0x0040
	const extendedFlags = 0x0080
	_ = windows.SetConsoleMode(h, (mode&^quickEditMode)|extendedFlags)
}

// updateManifestURL 指向自建更新源：与网页下载共用同一服务器、同一份版本清单，
// 后续发新版只改服务器上的 stable.json 并递增 appVersion，此 URL 保持不变。
// 声明为变量而非常量：仅用于构建期 -ldflags 覆盖，正常构建不得改动默认值。
var updateManifestURL = "https://www.mengfly.fun/pvf/updates/windows/amd64/stable.json"

// devUpdateManifestURL 是「开发人员专用」测试通道清单，与正式通道完全隔离，
// 通过「设置 → 系统维护 → 更新通道」切换；正式版客户端永远走正式通道。
const devUpdateManifestURL = "https://www.mengfly.fun/pvf/dev/updates/windows/amd64/stable.json"

const (
	quitRequestedEvent  = "app:quit-requested"
	closeRequestedEvent = "app:close-requested"
	quitConfirmedEvent  = "app:quit-confirmed"
	closeConfirmedEvent = "app:close-confirmed"
	// closeCancelledEvent 是前端在关窗确认框上点「取消」时回的信号：内核据此
	// 取消这次关闭（也取消兜底），否则兜底会在用户点了"取消"后又把窗口关掉。
	closeCancelledEvent = "app:close-cancelled"

	// closeFallbackDelay 是"前端对关窗请求毫无回应"时内核的兜底放行时长。
	// 存在原因（2026-09-30 实测）：确认框曾因前端链路问题没起来，用户表现为
	// 「点 X 关不掉窗口」。宁可 3 秒后放行，也不能让用户关不掉窗口。
	closeFallbackDelay = 3 * time.Second

	// mainWindowName identifies the archive editor window. It must be set
	// explicitly, otherwise Wails names it "window-N" and it cannot be
	// distinguished from the detached script window by name.
	mainWindowName = "main"
)

type closeCoordinator struct {
	app *application.App
	// allowWindowClosing permits the next close of the window that is actually
	// being closed. A single flag is not enough once a second window exists:
	// app.Window.Current() reports the last interacted window, so approving a
	// close would target whichever window the user touched most recently.
	allowWindowClosing atomic.Bool
	// pendingClose remembers which window asked to close so the confirmation
	// closes that window instead of guessing from window focus.
	pendingClose atomic.Uint64
	// scriptWindow lets the quit path release the detached script window without
	// waiting for its own close confirmation.
	scriptWindow *services.ScriptWindowService
}

func newCloseCoordinator(app *application.App, scriptWindow *services.ScriptWindowService) *closeCoordinator {
	coordinator := &closeCoordinator{app: app, scriptWindow: scriptWindow}
	app.Event.On(closeConfirmedEvent, func(*application.CustomEvent) {
		logging.For("window").Warn("收到关闭确认：放行关闭")
		coordinator.allowWindowClosing.Store(true)
		// Close the window that started this handshake; falling back to the
		// focused window would close the wrong one when two windows are open.
		id := uint(coordinator.pendingClose.Swap(0))
		addressable := false
		if id != 0 {
			if window, ok := app.Window.GetByID(id); ok && window != nil {
				// 主窗口关闭时，独立脚本窗口不能留下：它已经没有回到归档编辑的
				// 入口，留下来就是一个无法操作的窗口。
				if coordinator.scriptWindow != nil && window.Name() == mainWindowName {
					coordinator.scriptWindow.AllowScriptWindowClose()
					coordinator.scriptWindow.CloseScriptWindowIfOpen()
				}
				window.Close()
				addressable = true
			}
		}
		if !addressable {
			if window := app.Window.Current(); window != nil {
				window.Close()
			}
		}
	})
	app.Event.On(closeCancelledEvent, func(*application.CustomEvent) {
		logging.For("window").Warn("收到关闭取消：保留窗口")
		coordinator.pendingClose.Store(0)
		coordinator.allowWindowClosing.Store(false)
	})
	app.Event.On(quitConfirmedEvent, func(*application.CustomEvent) {
		logging.For("window").Warn("收到退出确认：放行退出")
		// The detached script window keeps its own close confirmation and its own
		// Pinia store; release it first so quitting cannot stall on it.
		if coordinator.scriptWindow != nil {
			coordinator.scriptWindow.AllowScriptWindowClose()
			coordinator.scriptWindow.CloseScriptWindowIfOpen()
		}
		app.Quit()
	})
	return coordinator
}

func (c *closeCoordinator) requestQuit() {
	_ = c.app.Event.Emit(quitRequestedEvent)
}

// handlerFor returns the close handler for one specific window. Binding the
// window keeps the confirmation handshake pointing at the window the user
// actually tried to close.
func (c *closeCoordinator) handlerFor(window application.Window) func(*application.WindowEvent) {
	return func(event *application.WindowEvent) {
		if c.allowWindowClosing.CompareAndSwap(true, false) {
			return
		}
		name := ""
		var id uint64
		if window != nil {
			name = window.Name()
			id = uint64(window.ID())
			c.pendingClose.Store(id)
		}
		event.Cancel()
		// 关窗确认这条链是"数据安全"路径：拦下与放行都留痕，出问题时能一眼看出
		// 断在"内核没拦"、"前端没收到"还是"前端没弹框"。
		logging.For("window").Warn("窗口关闭请求：已拦下，等待前端确认", "窗口", name)
		_ = c.app.Event.Emit(closeRequestedEvent)
		c.armCloseFallback(id, name)
	}
}

// armCloseFallback 给这次关窗请求上"兜底"：前端若在 closeFallbackDelay 内既没确认
// 也没取消（例如确认框没起来），就直接放行关闭 —— 用户永远不该遇到"点 X 关不掉窗口"。
func (c *closeCoordinator) armCloseFallback(expectedID uint64, name string) {
	go func() {
		time.Sleep(closeFallbackDelay)
		// 已被确认（pendingClose 清零）或被取消（同样清零）→ 不兜底。
		if c.pendingClose.Load() != expectedID {
			return
		}
		if !c.allowWindowClosing.CompareAndSwap(false, true) {
			return
		}
		logging.For("window").Warn("前端未回应关窗请求，兜底放行关闭", "窗口", name)
		if expectedID != 0 {
			if window, ok := c.app.Window.GetByID(uint(expectedID)); ok && window != nil {
				window.Close()
				return
			}
		}
		if window := c.app.Window.Current(); window != nil {
			window.Close()
		}
	}()
}

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS

//go:embed updater_window.html
var updaterWindowHTML string

// devAssetsDir 是「开发迭代」用的磁盘资源目录（环境变量 PVFINE_DEV_DIST）。
// 为空时走二进制内嵌资源（正常/交付行为）；指向 frontend/dist 时，界面资源按请求
// 实时从磁盘读取 —— 前端改动只需重新 `vite build`，不必再 go build 打包 exe，
// 省掉一轮几十秒的 Go 链接 + 产物复制，只为让测试版能立刻看到改动。
var devAssetsDir = strings.TrimSpace(os.Getenv("PVFINE_DEV_DIST"))

// assetRoot 返回界面资源的来源：优先磁盘目录（存在且为目录时），否则回退到内嵌资源。
func assetRoot() fs.FS {
	if devAssetsDir != "" {
		if stat, err := os.Stat(devAssetsDir); err == nil && stat.IsDir() {
			return os.DirFS(devAssetsDir)
		}
	}
	return assets
}

// 更新窗口（BYO 自建无边框小窗）的完整事件链（2026-09-27 彻查定案）：
//
//  1. HTML(updaterWindowHTML) + AllowSimpleEventEmit=true ⇒ 框架在创建窗口时把
//     `window.wails.Events` shim 注入页面（inline_event_shim.go），页面才有事件总线；
//  2. updaterBootJS 经 AddScriptToExecuteOnDocumentCreated 在「文档创建时」最先执行，
//     提前给出 `window._wails.invoke`。没有它就是死锁：runtime 核心（含 invoke）要等
//     navigationCompleted 后经 execJS 注入，而 execJS 在 runtimeLoaded=false 时进
//     pendingJS 队列，等的恰是 "wails:runtime:ready"——ready 又只能由持有 invoke 的
//     页面发出。结果事件全部积压，界面永远停在「正在连接更新服务器…」。
//
// 历史教训：BYO + URL("/updater.html") 加载既没有 AllowSimpleEventEmit 也没有
// InitialHTML，shim 不注入，事件同样全断（同一症状的另一种死法）。
const (
	updaterWindowName   = "updater"
	updaterWindowWidth  = 460
	updaterWindowHeight = 260
)

// updaterBootJS 见上方更新窗口事件链说明（第 2 条）。
const updaterBootJS = "window._wails=window._wails||{};window._wails.invoke=window.chrome.webview.postMessage;"

// updaterWindowHandle 把应用自己的 WebviewWindow 适配成 updater.WindowHandle。
// BYO 模式下框架既不创建也不显示窗口，因此**懒创建**：第一次需要显示时才建窗
// （创建即可见、居中），之后复用；Close() 只隐藏，保证下次「检查更新」还能弹出来。
type updaterWindowHandle struct {
	app *application.App
	mu  sync.Mutex
	win *application.WebviewWindow
}

// ensure 返回窗口，必要时创建（必须在主线程之外也安全：Wails 内部会派发到主线程）。
func (h *updaterWindowHandle) ensure() *application.WebviewWindow {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.win != nil {
		return h.win
	}
	if h.app == nil {
		return nil
	}
	h.win = h.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:                 updaterWindowName,
		Title:                "软件更新",
		Width:                updaterWindowWidth,
		Height:               updaterWindowHeight,
		Frameless:            true,
		InitialPosition:      application.WindowCentered,
		HTML:                 updaterWindowHTML,
		AllowSimpleEventEmit: true,
		JS:                   updaterBootJS,
	})
	return h.win
}

func (h *updaterWindowHandle) EmitEvent(name string, data ...any) bool {
	win := h.ensure()
	if win == nil {
		return false
	}
	return win.EmitEvent(name, data...)
}

func (h *updaterWindowHandle) Show() {
	win := h.ensure()
	if win == nil {
		return
	}
	win.Show()
	win.Focus()
}

func (h *updaterWindowHandle) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	win := h.win
	h.mu.Unlock()
	if win != nil {
		win.Hide()
	}
}

// SetSize 让框架按状态调整窗口尺寸（框架做类型断言，不实现则静默跳过）。
func (h *updaterWindowHandle) SetSize(width, height int) {
	win := h.ensure()
	if win == nil {
		return
	}
	win.SetSize(width, height)
}

func main() {
	// 日志系统最先初始化：之后的所有阶段都能带时间戳落盘，便于排查启动问题。
	logHandle := logging.Init()
	mainLog := logging.For("main")
	bootStage := logging.StartStage("main", "程序启动")
	cacheDir, cacheSource := apppaths.DescribeRoot()
	mainLog.Info("进程启动",
		"版本", appVersion,
		"参数", fmt.Sprintf("%v", os.Args[1:]),
		"日志目录", orDash(logHandle.Dir()),
		"日志文件", orDash(logHandle.FilePath()),
		"缓存目录", orDash(cacheDir),
		"缓存目录来源", orDash(cacheSource))

	serviceStage := logging.StartStage("main", "初始化服务")
	core := services.NewCore()
	settingsService := services.NewSettingsService()
	// One file set service backs both the sidebar and the script API, so a
	// scripted change and a manual save target the same document.
	fileSetService := services.NewFileSetService()
	serviceStage.Done("服务", "core/settings/fileset")

	appStage := logging.StartStage("main", "创建应用与窗口")
	app := application.New(application.Options{
		Name:             "pvfine",
		Description:      "PVF 归档编辑器",
		FileAssociations: []string{".pvf"},
		Services: []application.Service{
			application.NewService(services.NewArchiveService(core)),
			application.NewService(services.NewEditorService(core, settingsService)),
			application.NewService(services.NewBatchService(core)),
			application.NewService(services.NewScriptService(core, fileSetService)),
			application.NewService(services.NewVersionService(core)),
			application.NewService(services.NewAnnotationService(core)),
			application.NewService(services.NewPathAnnotationService(core)),
			application.NewService(services.NewRenderingService(core)),
			application.NewService(services.NewPreviewService(core)),
			application.NewService(services.NewImageService(core, settingsService)),
			application.NewService(settingsService),
			application.NewService(fileSetService),
			application.NewService(services.NewBookmarkService()),
			application.NewService(services.NewObjectViewService(core)),
			application.NewService(services.NewFormViewService(core)),
			application.NewService(services.NewDoctorService(core)),
			application.NewService(services.NewAIService(core, settingsService)),
		},
		Assets: application.AssetOptions{
			// 开发态可从磁盘目录取资源（PVFINE_DEV_DIST），正式/交付态恒为内嵌资源。
			Handler: application.AssetFileServerFS(assetRoot()),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	updaterSvc = services.NewUpdateService(app)
	app.RegisterService(application.NewService(updaterSvc))
	scriptWindowService := services.NewScriptWindowService(app)
	app.RegisterService(application.NewService(scriptWindowService))
	app.RegisterService(application.NewService(services.NewLogService()))
	closeCoordinator := newCloseCoordinator(app, scriptWindowService)

	// MCP 服务：默认关闭；环境变量 PVFINE_MCP=1 或设置里的「MCP 只读服务」开关开启时，
	// 以 HTTP 方式开放（仅本机回环、只暴露只读工具）。用于让外部 AI 客户端读取归档
	// 信息，不开放任何写能力。监听在启动时绑定，运行时切换设置需重启应用生效。
	// 监听地址可以是一个或多个，见 mcpListenAddrs（PVFINE_MCP_ADDR 支持逗号列表与
	// 「host:起始端口-结束端口」区间写法）。
	if mcp.Exposed() || mcp.WriteExposed() || mcpEnabledFromSettings(settingsService) {
		toolSource := services.NewAIToolSource(core, settingsService)
		// 写工具门禁：写通道已授权（UI 开关或 PVFINE_MCP_WRITE=1）且「AI 写保护」已关闭。
		writeGate := func() bool {
			cfg, err := settingsService.GetSettings()
			if err != nil {
				return false
			}
			return (cfg.MCPWriteEnabled || mcp.WriteExposed()) && !cfg.AI.WriteProtection
		}
		// 同一份 handler 挂到全部监听地址：所有端口都是「同一个 MCP 服务」的入口，
		// 工具注册表与写门禁只有一份，因此不存在按端口隔离——谁连哪个端口权限都一样。
		handler := mcp.New(toolSource, writeGate)
		for _, addr := range mcpListenAddrs() {
			go func(a string) {
				logging.For("mcp").Info("MCP 服务已启动", "地址", a)
				if err := http.ListenAndServe(a, handler); err != nil {
					// 单个地址失败（端口被占 / 地址非法）只记日志，不影响其它端口。
					logging.For("mcp").Error("MCP 监听失败", "地址", a, "错误", err.Error())
				}
			}(addr)
		}
	}

	// 把日志推给前端「输出日志」面板：与文件日志同源同格式，避免"界面说一套、
	// 日志说另一套"。回调只做入队，绝不阻塞业务线程（见 logging.events.go）。
	logging.SetEntrySink(func(entry logging.Entry) {
		app.Event.Emit("app:log", entry)
	})

	// 诊断命令通道：前端「输出日志」面板输入 SCRZ 之类的短命令时触发。
	// 把现场状态（卡住的打开动作 / 最近打开耗时 / goroutine 栈）写进日志，
	// 用于「界面卡死」取证。走事件而不是新增 binding，避免多一个 IPC 方法。
	app.Event.On("dev:diag", func(event *application.CustomEvent) {
		input := services.DiagInput{}
		if payload, ok := event.Data.(map[string]any); ok {
			if value, ok := payload["cmd"].(string); ok {
				input.Cmd = value
			}
			if lines, ok := payload["uiTrace"].([]any); ok {
				for _, line := range lines {
					if text, ok := line.(string); ok {
						input.UITrace = append(input.UITrace, text)
					}
				}
			}
		}
		services.RunDiagnostics(input)
	})

	// 前端操作时间线实时同步：JS 侧每次记录步骤都推一份过来，控制台 SCRZ
	//（拿不到 JS 数据）也能输出「界面停在哪一步」，不再依赖前端能否响应。
	app.Event.On("dev:trace-sync", func(event *application.CustomEvent) {
		lastFrontHeartbeat.Store(time.Now().UnixMilli())
		if payload, ok := event.Data.(map[string]any); ok {
			if raw, ok := payload["lines"].([]any); ok {
				lines := make([]string, 0, len(raw))
				for _, item := range raw {
					if text, ok := item.(string); ok {
						lines = append(lines, text)
					}
				}
				services.SetLastUITrace(lines)
			}
		}
	})

	// 控制台命令通道（开发人员专用版是控制台子系统，有真实 stdin）：界面卡死时
	// 前端整个不可用，此时在黑色控制台窗口输入 SCRZ 回车，仍能输出诊断现场——
	// 不依赖界面线程，是最可靠的取证入口。正式 GUI 版无控制台，stdin 立即 EOF，
	// 本协程随之退出，无副作用。
	// 先关掉「快速编辑」：Windows 默认开启，鼠标在黑窗口一点就进入选择状态——
	// 输入被冻结、stdout 写入阻塞（连日志都可能被拖住），正是"控制台不能输入"的原因。
	disableConsoleQuickEdit()
	// 前端心跳看门狗：JS 侧每秒上报一次；界面主线程被卡住时心跳停止，
	// 这里自动把「停摆开始 + 最后操作时间线」写进日志——卡死无需人工取证。
	go func() {
		const stallAfterMs = int64(4000)
		stallStart := int64(0)
		for {
			time.Sleep(time.Second)
			hb := lastFrontHeartbeat.Load()
			if hb == 0 {
				continue // 前端尚未上报过（启动中）
			}
			now := time.Now().UnixMilli()
			if now-hb > stallAfterMs {
				if stallStart == 0 {
					stallStart = hb
					logging.For("watchdog").Warn("★前端疑似停摆（界面卡死）",
						"最后心跳", time.UnixMilli(hb).Format("15:04:05.000"),
						"已静默", logging.FormatDuration(time.Since(time.UnixMilli(hb))),
						"最后操作时间线", strings.Join(services.LastUITraceSnapshot(), " ｜ "))
				}
				continue
			}
			if stallStart != 0 {
				logging.For("watchdog").Warn("前端已恢复响应",
					"停摆时长", logging.FormatDuration(time.Since(time.UnixMilli(stallStart))))
				stallStart = 0
			}
		}
	}()
	go func() {
		fmt.Println("提示：界面卡死时，在本窗口输入 SCRZ 回车，立即输出诊断现场（不依赖界面）")
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			switch strings.ToUpper(strings.TrimSpace(scanner.Text())) {
			case "SCRZ":
				services.RunDiagnostics(services.DiagInput{Cmd: "SCRZ(控制台)"})
			case "HELP", "?":
				fmt.Println("可用命令：SCRZ = 输出卡死诊断现场（进程状态/卡住的打开动作/慢打开记录/goroutine 栈）")
			default:
				fmt.Println(`未知命令（可用：SCRZ / HELP）`)
			}
		}
		// 读 stdin 失败时记录原因再结束该读线程：它只是控制台命令入口，
		// 结束不应影响 GUI，所以这里不能 Fatal。
		// 用 Debug 而非 Warn：正式包是 `-H windowsgui`（无控制台），stdin 句柄无效属预期情况，
		// 若用 Warn 会让每个客户机的日志都出现一条看起来很严重的报错。
		if err := scanner.Err(); err != nil {
			logging.For("console").Debug("控制台命令读取结束", "错误", err.Error())
		}
	}()

	// 自动更新：走自建服务器（与网页下载同一版本源）；通道由设置决定。
	// dev/未注入版本号时 configureUpdater 返回 false，等同关闭。
	updaterEnabled := configureUpdater(app, settingsService)

	menu := app.Menu.New()
	app.Menu.SetApplicationMenu(menu)
	appMenu := menu.AddSubmenu("应用")
	if updaterEnabled {
		// 只检查并在界面弹提示窗（含官网下载地址）；不再走框架的自动安装流程
		// ——该流程在本机实测会卡在更新窗口事件握手（2026-09-27）。
		appMenu.Add("检查更新").OnClick(func(*application.Context) {
			go func() {
				if _, err := updaterSvc.CheckAndNotify(); err != nil {
					logging.For("updater").Error("检查更新失败", "错误", err.Error())
				}
			}()
		})
	}
	appMenu.AddSeparator()
	appMenu.Add("退出").SetAccelerator("CmdOrCtrl+q").OnClick(func(*application.Context) {
		closeCoordinator.requestQuit()
	})
	// 诊断子菜单：菜单回调跑在 Go 侧（不经 WebView 主线程），界面卡顿/卡死时
	// 仍可点击，是比界面按钮更可靠的现场取证入口。
	diagMenu := menu.AddSubmenu("诊断")
	diagMenu.Add("输出卡死现场（SCRZ）").OnClick(func(*application.Context) {
		go services.RunDiagnostics(services.DiagInput{Cmd: "SCRZ(菜单)"})
	})
	diagMenu.Add("清空前端操作时间线（QK）").OnClick(func(*application.Context) {
		app.Event.Emit("dev:diag-qk", true)
	})
	menu.AddRole(application.EditMenu)

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:          "PVF工坊 · 基于 pvfine 修改 · By 残梦断忆",
		Name:           mainWindowName,
		Width:          1440,
		Height:         900,
		EnableFileDrop: true,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropLiquidGlass,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(24, 26, 32),
		URL:              "/",
	})
	var openPathMu sync.Mutex
	pendingOpenPath := ""
	openPathReady := false
	emitPVFOpenPath := func(path string) {
		if !strings.HasSuffix(strings.ToLower(path), ".pvf") {
			return
		}
		openPathMu.Lock()
		if !openPathReady {
			pendingOpenPath = path
			openPathMu.Unlock()
			return
		}
		openPathMu.Unlock()
		app.Event.Emit("archive:open-path", path)
	}
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		openPathMu.Lock()
		openPathReady = true
		path := pendingOpenPath
		pendingOpenPath = ""
		openPathMu.Unlock()
		if path != "" {
			app.Event.Emit("archive:open-path", path)
		}
	})
	window.RegisterHook(events.Common.WindowClosing, closeCoordinator.handlerFor(window))
	window.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		ctx := event.Context()
		if ctx == nil {
			return
		}
		// 转发给前端：导入窗口的拖拽导入消费这些路径（wails 不会自动向 JS
		// 派发该事件，官方 file-drop 示例也是这样手动转发的）。
		files := ctx.DroppedFiles()
		if len(files) > 0 {
			app.Event.Emit("common:WindowFilesDropped", map[string]any{"files": files})
		}
		for _, file := range files {
			if strings.HasSuffix(strings.ToLower(file), ".pvf") {
				emitPVFOpenPath(file)
				break
			}
		}
	})
	app.Event.OnApplicationEvent(events.Common.ApplicationOpenedWithFile, func(event *application.ApplicationEvent) {
		if event == nil || event.Context() == nil {
			return
		}
		path := event.Context().Filename()
		emitPVFOpenPath(path)
	})

	if updaterEnabled {
		startBackgroundUpdateCheck()
	}

	appStage.Done("窗口", mainWindowName)
	bootStage.Done("提示", "以下时间由各服务在运行期记录")
	mainLog.Info("进入事件循环")

	runErr := app.Run()
	if runErr != nil {
		mainLog.Error("应用异常退出", "错误", runErr.Error())
	} else {
		mainLog.Info("应用正常退出", "本次运行时长", logging.FormatDuration(bootStage.Elapsed()))
	}
	if dropped := logHandle.Dropped(); dropped > 0 {
		mainLog.Warn("部分日志因队列已满被丢弃（只可能是 DEBUG/INFO）", "丢弃条数", dropped)
	}
	if err := logHandle.Close(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "关闭日志系统失败: %v\n", err)
	}
	if runErr != nil {
		os.Exit(1)
	}
}

// orDash 用于日志里避免空值造成的误读。
func orDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

// mcpEnabledFromSettings 返回设置里的「MCP 只读服务」开关是否开启。
// 读取失败时按关闭处理（与 MCP 默认关闭的语义一致），不影响应用启动。
func mcpEnabledFromSettings(settings *services.SettingsService) bool {
	if settings == nil {
		return false
	}
	cfg, err := settings.GetSettings()
	if err != nil {
		return false
	}
	return cfg.MCPEnabled
}

// mcpAddrEnvName 是 MCP 监听地址的环境变量名，与 internal/mcp.Addr() 读的是同一个。
// 监听地址列表属于应用装配逻辑（不改内核），所以这里独立声明、不依赖内核常量。
const mcpAddrEnvName = "PVFINE_MCP_ADDR"

// mcpAddrSeparator 分隔多个监听地址。
const mcpAddrSeparator = ","

// mcpRangeMaxPorts 限制一次端口区间能展开出的地址数量：误填 "8000-65535" 会瞬间占满
// 大量端口，超过上限的区间整项丢弃并记日志。
const mcpRangeMaxPorts = 32

// mcpListenAddrs 返回本次要监听的 MCP 地址列表。
//
// 来源优先级：环境变量 PVFINE_MCP_ADDR → 内核默认（internal/mcp.Addr()，127.0.0.1:17650）。
// 取值支持以下写法，可用逗号混用：
//
//	127.0.0.1:17650                 单个地址
//	127.0.0.1:8000-8004             host:起始端口-结束端口（含首尾，展开成 5 个）
//	127.0.0.1:8000,127.0.0.1:9000   多个地址
//
// 非法项与非法区间只跳过并记日志，绝不让整个 MCP 起不来；全部非法时退回内核默认。
func mcpListenAddrs() []string {
	raw := strings.TrimSpace(os.Getenv(mcpAddrEnvName))
	if raw == "" {
		return []string{mcp.Addr()}
	}
	addrs, rejected := parseMCPAddrs(raw)
	for _, item := range rejected {
		logging.For("mcp").Warn("忽略非法的 MCP 监听地址", "取值", item)
	}
	if len(addrs) == 0 {
		logging.For("mcp").Warn("MCP 监听地址全部非法，退回默认", "默认", mcp.Addr())
		return []string{mcp.Addr()}
	}
	return addrs
}

// parseMCPAddrs 把 PVFINE_MCP_ADDR 的取值解析成逐个可监听地址，
// 返回（按出现顺序去重后的地址, 被忽略的非法项）。
func parseMCPAddrs(raw string) ([]string, []string) {
	var addrs []string
	var rejected []string
	seen := make(map[string]struct{}, 4)
	add := func(addr string) {
		if _, exists := seen[addr]; exists {
			return
		}
		seen[addr] = struct{}{}
		addrs = append(addrs, addr)
	}
	for _, token := range strings.Split(raw, mcpAddrSeparator) {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		host, portText, ok := splitMCPHostPort(token)
		if !ok {
			rejected = append(rejected, token)
			continue
		}
		startText, endText, isRange := strings.Cut(portText, "-")
		start, err := strconv.Atoi(strings.TrimSpace(startText))
		if err != nil || !validMCPPort(start) {
			rejected = append(rejected, token)
			continue
		}
		if !isRange {
			add(host + ":" + strconv.Itoa(start))
			continue
		}
		end, err := strconv.Atoi(strings.TrimSpace(endText))
		if err != nil || !validMCPPort(end) || end < start || end-start+1 > mcpRangeMaxPorts {
			rejected = append(rejected, token)
			continue
		}
		for port := start; port <= end; port++ {
			add(host + ":" + strconv.Itoa(port))
		}
	}
	return addrs, rejected
}

// splitMCPHostPort 按最后一个冒号切出 host 与「端口或端口区间」文本。
// 必须显式写 host（不接受 ":8000"）；IPv6 写成 "[::1]:8000"。
func splitMCPHostPort(token string) (host, portText string, ok bool) {
	index := strings.LastIndex(token, ":")
	if index <= 0 || index == len(token)-1 {
		return "", "", false
	}
	host = strings.TrimSpace(token[:index])
	portText = strings.TrimSpace(token[index+1:])
	if host == "" || portText == "" {
		return "", "", false
	}
	return host, portText, true
}

// validMCPPort 判断端口号是否落在 TCP 合法范围内。
func validMCPPort(port int) bool { return port >= 1 && port <= 65535 }

func configureUpdater(app *application.App, settings *services.SettingsService) bool {
	version := strings.TrimPrefix(appVersion, "v")

	// 更新通道：正式（默认）走官方清单，开发人员专用走测试清单，两者互不影响。
	manifestURL := updateManifestURL
	channel := "stable"
	channelName := "正式通道"
	if settings != nil {
		if cfg, err := settings.GetSettings(); err == nil && cfg.UpdateChannel == services.UpdateChannelDev {
			manifestURL = devUpdateManifestURL
			channel = "dev"
			channelName = "开发人员专用（测试通道）"
		}
	}

	// 无论是否启用自动更新，都把「当前版本 + 清单地址 + 通道」注入更新服务：
	// 开发者面板的「测试更新通道」用它直接拉清单验证连通性（2026-09-29 用户要求）。
	// 注意这里**不创建框架更新器**、也不改变设置里「检查更新」的原有行为。
	if updaterSvc != nil {
		updaterSvc.SetCurrentVersion(version)
		updaterSvc.SetManifestSource(manifestURL, channel)
	}

	// 开发版（appVersion 为 "" / 0.0.0 / dev）：与以往一致 —— 不初始化更新器。
	// 设置里的「检查更新」仍会提示"更新功能未初始化"；要验证更新通道请用开发者面板。
	if version == "" || version == "0.0.0" || version == "dev" {
		logging.For("updater").Info("开发版：未启用自动更新（可在开发者面板测试更新通道）",
			"通道", channelName, "清单", manifestURL)
		return false
	}

	logging.For("updater").Info("自动更新已启用", "通道", channelName, "清单", manifestURL)

	provider, err := endpointupdater.New(endpointupdater.Config{
		URL:     manifestURL,
		Channel: channel,
		// 更新包较大（10MB+），默认 30s HTTP 超时会在慢速网络下下载超时；
		// 放宽到 15 分钟（manifest 与 artifact 下载共用此 client）。
		HTTPClient: &http.Client{Timeout: 15 * time.Minute},
	})
	if err != nil {
		logging.For("updater").Warn("初始化更新源失败", "错误", err.Error())
		return false
	}

	// 更新窗口句柄（懒创建）+ 把"显示窗口"注入更新服务：BYO 模式下只有我们自己
	// 显示窗口，框架不会代劳。
	updaterHandle = &updaterWindowHandle{app: app}
	if updaterSvc != nil {
		// 当前版本号注入更新服务：提示窗要显示「当前版本 → 最新版本」。
		updaterSvc.SetCurrentVersion(version)
		updaterSvc.SetWindowPresenter(func() {
			if updaterHandle != nil {
				updaterHandle.Show()
			}
		})
	}

	if err := app.Updater.Init(updater.Config{
		CurrentVersion: version,
		Providers:      []updater.Provider{provider},
		// 更新窗口：BYO 自建窗口。HTML + AllowSimpleEventEmit 使框架在创建时注入
		// window.wails.Events shim（页面事件收发依赖它）；JS(updaterBootJS) 经
		// AddScriptToExecuteOnDocumentCreated 提前注入 invoke，解开
		// pendingJS ↔ runtime:ready 死锁（见 updaterBootJS 处注释）。
		Window: updater.BYOWindow(updaterHandle),
	}); err != nil {
		logging.For("updater").Warn("初始化更新器失败", "错误", err.Error())
		return false
	}

	// 2026-09-27 用户裁定：放弃"自动下载安装"这条链路（实测即便把最后一步
	// 自动重启补上仍不可靠），改为检查到新版本后弹提示窗、引导用户去官网下载。
	// 因此这里不再监听 update-ready 做自动换包。
	return true
}

func startBackgroundUpdateCheck() {
	version := strings.TrimPrefix(appVersion, "v")
	if version == "" || version == "dev" || version == "0.0.0" {
		return
	}

	go func() {
		// 等首窗进入事件循环后再检查，避开启动期资源争抢。
		time.Sleep(3 * time.Second)

		info, err := updaterSvc.CheckAndNotify()
		if err != nil {
			// 以前这里的失败是静默的，排查只能靠猜；现在把结果与错误都落到日志。
			logging.For("updater").Warn("自动检查更新失败", "错误", err.Error())
			return
		}
		if info == nil || !info.HasUpdate {
			logging.For("updater").Info("自动检查更新：当前已是最新版本", "当前", appVersion)
			return
		}
		// 发现新版本：CheckAndNotify 已发 app:update-available，界面据此弹出
		// 「提示更新」窗口（含官网下载地址：可点击打开、可复制），不再自动安装。
		logging.For("updater").Info("自动检查更新：发现新版本",
			"当前", info.CurrentVersion, "最新", info.LatestVersion)
	}()
}
