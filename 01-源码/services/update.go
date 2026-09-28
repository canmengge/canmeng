package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

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
	// manifestURL / channel 是当前更新通道的清单地址与通道名，由 main 注入。
	// 开发版不会创建框架更新器，但这两个值照常注入 —— 开发者面板的连通性测试
	// 直接拿它们去拉清单（2026-09-29 用户要求：更新测试只在开发者面板做）。
	manifestURL string
	channel     string
	mu          sync.Mutex
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

// SetManifestSource 注入当前更新通道的清单地址与通道名。main 在初始化更新器时调用；
// **开发版照常调用**（即便不创建框架更新器），好让开发者面板能直接测连通性。
func (s *UpdateService) SetManifestSource(manifestURL string, channel string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.manifestURL = strings.TrimSpace(manifestURL)
	s.channel = strings.TrimSpace(channel)
	s.mu.Unlock()
}

// updateManifest 是更新清单（stable.json）里本服务关心的字段。
type updateManifest struct {
	Version string `json:"version"`
	Channel string `json:"channel"`
	Name    string `json:"name"`
}

// compareVersions 比较两段版本号（形如 4.3.10）：a > b 返回 1，a < b 返回 -1，相等返回 0。
// 段数不同时缺失段按 0 处理（"4.3" 与 "4.3.0" 视为相等）；非数字段退化为字符串比较。
func compareVersions(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(strings.TrimSpace(a), "v"), ".")
	pb := strings.Split(strings.TrimPrefix(strings.TrimSpace(b), "v"), ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		va, vb := "0", "0"
		if i < len(pa) {
			va = pa[i]
		}
		if i < len(pb) {
			vb = pb[i]
		}
		na, ea := strconv.Atoi(va)
		nb, eb := strconv.Atoi(vb)
		if ea == nil && eb == nil {
			if na != nb {
				if na > nb {
					return 1
				}
				return -1
			}
			continue
		}
		if cmp := strings.Compare(va, vb); cmp != 0 {
			return cmp
		}
	}
	return 0
}

// TestCheckUpdateNow 供**开发者面板**使用：不依赖框架更新器，直接请求当前通道的
// 更新清单并比较版本；发现新版本时发 `app:update-available` 事件，界面据此弹出
// 「提示更新」窗口 —— 效果与真实的「检查更新」一致。
//
// 为什么单独实现：开发版不创建框架更新器（设置里的「检查更新」仍提示"更新功能
// 未初始化"，该行为保持不变），但开发期需要一个能验证「服务器是否可达、清单是否
// 可读」的入口（2026-09-29 用户要求：更新测试只在开发者面板做）。
func (s *UpdateService) TestCheckUpdateNow() (*UpdateInfo, error) {
	if s == nil {
		return nil, errors.New("更新服务不可用")
	}
	s.mu.Lock()
	url := s.manifestURL
	current := s.version
	s.mu.Unlock()
	if url == "" {
		return nil, errors.New("更新清单地址未注入")
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("请求更新清单失败：%w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("更新清单返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("读取更新清单失败：%w", err)
	}
	var m updateManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("解析更新清单失败：%w", err)
	}
	if strings.TrimSpace(m.Version) == "" {
		return nil, errors.New("更新清单里没有 version 字段")
	}

	if current == "" {
		current = "0.0.0"
	}
	info := &UpdateInfo{
		CurrentVersion: current,
		LatestVersion:  m.Version,
		HasUpdate:      compareVersions(m.Version, current) > 0,
		DownloadURL:    UpdateDownloadPage,
	}
	if info.HasUpdate {
		emitEvent("app:update-available", info)
	}
	return info, nil
}

// SimulateUpdateAvailable 供**开发者面板**使用：模拟"发布了新版本"，走与真实检查
// 完全相同的事件链路，让界面弹出「提示更新」窗口（不发任何网络请求，用于界面自测）。
func (s *UpdateService) SimulateUpdateAvailable() (*UpdateInfo, error) {
	if s == nil {
		return nil, errors.New("更新服务不可用")
	}
	s.mu.Lock()
	current := s.version
	s.mu.Unlock()
	if current == "" {
		current = "0.0.0"
	}
	info := &UpdateInfo{
		CurrentVersion: current,
		LatestVersion:  "9.9.9（模拟）",
		HasUpdate:      true,
		DownloadURL:    UpdateDownloadPage,
	}
	emitEvent("app:update-available", info)
	return info, nil
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
