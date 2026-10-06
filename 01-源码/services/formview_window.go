package services

import (
	"errors"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	// FormViewWindowName identifies the detached structured-view window. 与脚本
	// 窗口同理：名字必须显式设置，否则 Wails 会命名成 "window-N"，按名字永远找不到。
	FormViewWindowName = "form-view"

	formViewWindowTitle = "可视化编辑区 — pvfine"

	// FormViewWindowClosedEvent tells the main window the detached view is gone.
	FormViewWindowClosedEvent = "form-view:closed"
)

// FormViewSession 是主窗口交给独立结构化视图窗口的初始参数。
//
// 每个 webview 有各自的 Pinia store，所以「看哪个文件族 / 哪个文件」必须显式传过去；
// **归档本身不用传** —— Go 侧的 core 是两个窗口共用的，投影直接读主窗口已打开的归档。
type FormViewSession struct {
	FormatID string `json:"formatId"`
	FilePath string `json:"filePath"`
}

var errFormViewWindowUnavailable = errors.New("当前环境不支持多窗口")

// FormViewWindowService 管理「结构化视图」的独立窗口。
//
// 做法与脚本工作区窗口完全一致（复用同包的 scriptWindowHost / scriptWindowHandle）：
// 按名字查找，已存在就聚焦；否则按 formViewWindowOptions 新建一个窗口。
//
// 与脚本窗口的**关键区别**：本窗口是**只读**的、没有未保存内容，所以关闭**不需要**
// 前端二次确认 —— 关窗钩子直接放行，不会拖住退出流程。
type FormViewWindowService struct {
	host scriptWindowHost

	mu         sync.Mutex
	session    *FormViewSession
	allowClose bool
}

// NewFormViewWindowService creates the service against the running application.
func NewFormViewWindowService(app *application.App) *FormViewWindowService {
	return &FormViewWindowService{host: wailsScriptWindowHost{app: app}}
}

// OpenFormViewWindow opens the detached structured-view window, or focuses the
// existing one. The session is staged first so a freshly created window can pick
// it up while mounting.
func (s *FormViewWindowService) OpenFormViewWindow(session FormViewSession) error {
	if s == nil || s.host == nil {
		return errFormViewWindowUnavailable
	}
	s.storeSession(session)
	if window, ok := s.host.findWindow(FormViewWindowName); ok {
		window.Focus()
		// 2026-10-06 修「壳和数据混搭」：窗口的 initFromSession **只在启动时跑一次**，
		// 复用窗口时之前只 Focus，新板块参数进不去 ⇒ 出现「独立掉落的壳 + 商店的表」
		//（用户多次实测）。这里把新 session 推给窗口，由窗口前端完整重初始化。
		emitEvent("form-view:session-changed", session)
		return nil
	}

	window, err := s.host.createWindow(formViewWindowOptions())
	if err != nil {
		return err
	}
	window.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
		// 只读窗口：不 Cancel，直接放行关窗，并通知主窗口。
		s.consumeAllowClose()
		emitEvent(FormViewWindowClosedEvent)
	})
	return nil
}

// FocusFormViewWindow brings the detached window forward, reporting whether it
// was there so the caller can fall back to the embedded panel.
func (s *FormViewWindowService) FocusFormViewWindow() bool {
	window, ok := s.findWindow()
	if !ok {
		return false
	}
	window.Focus()
	return true
}

// IsFormViewWindowOpen reports whether the detached window currently exists.
func (s *FormViewWindowService) IsFormViewWindowOpen() bool {
	_, ok := s.findWindow()
	return ok
}

// LoadFormViewSession returns the staged session, or nil when none was staged.
func (s *FormViewWindowService) LoadFormViewSession() *FormViewSession {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return nil
	}
	staged := *s.session
	return &staged
}

// StageFormViewSession 只更新暂存参数（窗口已开着时用于同步"看哪个文件"）。
func (s *FormViewWindowService) StageFormViewSession(session FormViewSession) {
	s.storeSession(session)
}

// CloseFormViewWindow closes the detached window from the frontend.
func (s *FormViewWindowService) CloseFormViewWindow() error {
	if s == nil {
		return nil
	}
	window, ok := s.findWindow()
	if !ok {
		return nil
	}
	s.AllowFormViewWindowClose()
	window.Close()
	return nil
}

// CloseFormViewWindowIfOpen closes the detached window without a frontend round
// trip: 主窗口要关掉时调用（它已经没有回到归档编辑的入口）。
func (s *FormViewWindowService) CloseFormViewWindowIfOpen() {
	if s == nil {
		return
	}
	window, ok := s.findWindow()
	if !ok {
		return
	}
	s.AllowFormViewWindowClose()
	window.Close()
}

// AllowFormViewWindowClose lets the window close without asking the frontend.
func (s *FormViewWindowService) AllowFormViewWindowClose() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.allowClose = true
	s.mu.Unlock()
}

func (s *FormViewWindowService) findWindow() (scriptWindowHandle, bool) {
	if s == nil || s.host == nil {
		return nil, false
	}
	return s.host.findWindow(FormViewWindowName)
}

func (s *FormViewWindowService) storeSession(session FormViewSession) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	staged := session
	s.session = &staged
}

func (s *FormViewWindowService) consumeAllowClose() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	allowed := s.allowClose
	s.allowClose = false
	return allowed
}

// formViewWindowOptions 独立浏览窗口：比堆在侧栏里宽得多（17 列要放得下），
// 并允许用户左右拉伸到更宽（没有上限，只设下限）。
func formViewWindowOptions() application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:  FormViewWindowName,
		Title: formViewWindowTitle,
		Width: 1480,
		// 表格行多，默认给足高度，减少滚动。
		Height:    920,
		MinWidth:  720,
		MinHeight: 460,
		// 本窗口只看归档内容，不接收 .pvf 拖放。
		EnableFileDrop:     false,
		UseApplicationMenu: true,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropLiquidGlass,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(24, 26, 32),
		// 资产服务器没有 SPA 回退，视图用 query 选择（与脚本窗口同一手法）。
		URL: "/?view=formview",
	}
}
