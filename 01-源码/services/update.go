package services

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// UpdateDownloadPage 是「手动下载最新版」的官网地址。
//
// 2026-09-27 用户裁定：检查到新版本后不再走框架自带的自动安装流程
// （实测能检测到新版本，但框架更新窗口起来后拿不到下载进度、主界面还会假死），
// 改为弹一个提示窗，把官网下载地址给用户：可一键用系统浏览器打开，也可复制。
const UpdateDownloadPage = "https://www.mengfly.fun/"

// UpdateInfo 是「检查更新」的结果，只用于界面提示，不含任何自动安装动作。
type UpdateInfo struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	HasUpdate      bool   `json:"hasUpdate"`
	DownloadURL    string `json:"downloadUrl"`
}

// UpdateService exposes the native updater to platforms without a visible
// application menu, such as the Windows desktop shell.
type UpdateService struct {
	app *application.App
	// presentWindow 在流程开始前显示更新窗口。窗口是宿主自建的（BYO 模式），
	// 框架按契约不会自己 Show/Hide（见 updater 包 window_lifecycle.go：
	// "User-managed window — we don't show/hide it"），所以必须由这里先显示，
	// 否则窗口永远不出现、用户看不到进度与按钮（2026-09-27 实测确认）。
	presentWindow func()
	// version 是编译期注入的 appVersion（去掉前缀 v），用于界面显示「当前版本」。
	version string
	mu      sync.Mutex
}

func NewUpdateService(app *application.App) *UpdateService {
	return &UpdateService{app: app}
}

// SetCurrentVersion 由 main 在初始化更新器时注入当前版本号。
func (s *UpdateService) SetCurrentVersion(v string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.version = strings.TrimPrefix(strings.TrimSpace(v), "v")
	s.mu.Unlock()
}

// SetWindowPresenter 由 main 注入"显示更新窗口"的动作（窗口创建后调用一次）。
func (s *UpdateService) SetWindowPresenter(present func()) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.presentWindow = present
	s.mu.Unlock()
}

// PresentWindow 显示更新窗口（没有窗口时静默跳过，不影响更新流程本身）。
func (s *UpdateService) PresentWindow() {
	if s == nil {
		return
	}
	s.mu.Lock()
	present := s.presentWindow
	s.mu.Unlock()
	if present != nil {
		present()
	}
}

// CheckForUpdates 检查更新源并把结果交给界面；发现新版本时**不做任何自动安装**。
func (s *UpdateService) CheckForUpdates() (*UpdateInfo, error) {
	if s == nil || s.app == nil || s.app.Updater == nil {
		return nil, errors.New("更新功能未初始化")
	}
	release, err := s.app.Updater.Check(context.Background())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	current := s.version
	s.mu.Unlock()

	info := &UpdateInfo{
		CurrentVersion: current,
		DownloadURL:    UpdateDownloadPage,
		HasUpdate:      release != nil,
	}
	if release != nil {
		info.LatestVersion = release.Version
	}
	return info, nil
}

// CheckAndNotify 检查更新，并在发现新版本时向界面发事件 —— 界面据此弹出
// 「提示更新」窗口（含官网下载地址）。后台自动检查与菜单「检查更新」都走这里。
func (s *UpdateService) CheckAndNotify() (*UpdateInfo, error) {
	info, err := s.CheckForUpdates()
	if err != nil || info == nil || !info.HasUpdate {
		return info, err
	}
	emitEvent("app:update-available", info)
	return info, nil
}

// OpenDownloadPage 用系统默认浏览器打开官网下载页。
func (s *UpdateService) OpenDownloadPage() error {
	app := s.app
	if app == nil {
		app = application.Get()
	}
	if app == nil || app.Browser == nil {
		return errors.New("无法打开系统浏览器")
	}
	return app.Browser.OpenURL(UpdateDownloadPage)
}

// CopyDownloadURL 把官网下载地址写入系统剪贴板，返回是否成功。
func (s *UpdateService) CopyDownloadURL() bool {
	app := s.app
	if app == nil {
		app = application.Get()
	}
	if app == nil || app.Clipboard == nil {
		return false
	}
	return app.Clipboard.SetText(UpdateDownloadPage)
}
