package main

import (
	"context"
	"embed"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"
	endpointupdater "github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"

	"pvfine/internal/apppaths"
	"pvfine/internal/logging"
	"pvfine/internal/mcp"
	"pvfine/services"
)

// Release builds replace appVersion with -ldflags. Local builds keep 0.0.0,
// which disables the updater (no release channel is contacted).
var (
	appVersion = "0.0.0"
)

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
	app.Event.On(quitConfirmedEvent, func(*application.CustomEvent) {
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
		if window != nil {
			c.pendingClose.Store(uint64(window.ID()))
		}
		event.Cancel()
		_ = c.app.Event.Emit(closeRequestedEvent)
	}
}

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS

// updaterWindowHTML 是自动更新窗口的自定义界面（全中文、深色主题）。
// 通过 updater.BuiltinWindow.HTML 替换框架默认英文模板，窗口的开关、
// 下载、校验、替换流程仍由框架驱动。
//
//go:embed updater_window.html
var updaterWindowHTML string

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
			application.NewService(services.NewDoctorService(core)),
			application.NewService(services.NewAIService(core, settingsService)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	app.RegisterService(application.NewService(services.NewUpdateService(app)))
	scriptWindowService := services.NewScriptWindowService(app)
	app.RegisterService(application.NewService(scriptWindowService))
	app.RegisterService(application.NewService(services.NewLogService()))
	closeCoordinator := newCloseCoordinator(app, scriptWindowService)

	// MCP 服务：默认关闭；环境变量 PVFINE_MCP=1 或设置里的「MCP 只读服务」开关开启时，
	// 以 HTTP 方式开放（仅本机回环、只暴露只读工具）。用于让外部 AI 客户端读取归档
	// 信息，不开放任何写能力。监听在启动时绑定，运行时切换设置需重启应用生效。
	if mcp.Exposed() || mcpEnabledFromSettings(settingsService) {
		toolSource := services.NewAIToolSource(core, settingsService)
		go func() {
			addr := mcp.Addr()
			logging.For("mcp").Info("MCP 服务已启动", "地址", addr)
			if err := http.ListenAndServe(addr, mcp.New(toolSource)); err != nil {
				logging.For("mcp").Error("MCP 服务停止", "错误", err.Error())
			}
		}()
	}

	// 把日志推给前端「输出日志」面板：与文件日志同源同格式，避免"界面说一套、
	// 日志说另一套"。回调只做入队，绝不阻塞业务线程（见 logging.events.go）。
	logging.SetEntrySink(func(entry logging.Entry) {
		app.Event.Emit("app:log", entry)
	})

	// 自动更新：走自建服务器（与网页下载同一版本源）；通道由设置决定。
	// dev/未注入版本号时 configureUpdater 返回 false，等同关闭。
	updaterEnabled := configureUpdater(app, settingsService)

	menu := app.Menu.New()
	app.Menu.SetApplicationMenu(menu)
	appMenu := menu.AddSubmenu("应用")
	if updaterEnabled {
		appMenu.Add("检查更新").OnClick(func(*application.Context) {
			go func() {
				if err := app.Updater.CheckAndInstall(context.Background()); err != nil {
					logging.For("updater").Error("检查更新失败", "错误", err.Error())
				}
			}()
		})
	}
	appMenu.AddSeparator()
	appMenu.Add("退出").SetAccelerator("CmdOrCtrl+q").OnClick(func(*application.Context) {
		closeCoordinator.requestQuit()
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
		startBackgroundUpdateCheck(app)
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

func configureUpdater(app *application.App, settings *services.SettingsService) bool {
	version := strings.TrimPrefix(appVersion, "v")
	if version == "" || version == "0.0.0" || version == "dev" {
		return false
	}

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

	if err := app.Updater.Init(updater.Config{
		CurrentVersion: version,
		Providers:      []updater.Provider{provider},
		// 自定义中文更新窗口：只替换界面内容与标题，窗口的开关/下载/
		// 校验/替换流程仍由框架驱动（尺寸自适应保留框架默认）。
		Window: &updater.BuiltinWindow{
			HTML:    updaterWindowHTML,
			Options: updater.WindowOptions{Title: "软件更新"},
		},
	}); err != nil {
		logging.For("updater").Warn("初始化更新器失败", "错误", err.Error())
		return false
	}

	return true
}

func startBackgroundUpdateCheck(app *application.App) {
	version := strings.TrimPrefix(appVersion, "v")
	if version == "" || version == "dev" || version == "0.0.0" {
		return
	}

	go func() {
		// Give the first window time to enter the event loop before opening the
		// updater window when a newer release is available.
		time.Sleep(3 * time.Second)

		release, err := app.Updater.Check(context.Background())
		if err != nil || release == nil {
			return
		}

		if err := app.Updater.CheckAndInstall(context.Background()); err != nil {
			logging.For("updater").Warn("自动更新失败", "错误", err.Error())
		}
	}()
}
