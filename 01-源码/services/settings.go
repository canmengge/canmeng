package services

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	AnnotationTagAfterTarget = "after-target"
	AnnotationTagLineEnd     = "line-end"
	AnnotationTagHidden      = "hidden"
	ExplorerOpenSingleClick  = "single-click"
	ExplorerOpenDoubleClick  = "double-click"
	ThemeDark                = "dark"
	ThemeLight               = "light"
	ThemeSystem              = "system"

	// 更新通道：stable = 正式更新源（默认）；dev = 开发人员专用测试源。
	// 供「设置 → 系统维护 → 更新通道」切换，用于测试自动更新流程。
	UpdateChannelStable = "stable"
	UpdateChannelDev    = "dev"

	// AI 服务商预设（OpenAI 兼容 endpoint；custom = 用户自填地址）。
	AIProviderOpenAI   = "openai"
	AIProviderDeepSeek = "deepseek"
	AIProviderQwen     = "qwen"
	AIProviderKimi     = "kimi"
	AIProviderOllama   = "ollama"
	AIProviderCustom   = "custom"
)

// AIAssistantSettings 是「AI 助手」配置（方案见 AI镶嵌.md §四）。
// 不预设模型：用户选服务商（或自定义 OpenAI 兼容地址）+ 填模型与 key 即可接入。
type AIAssistantSettings struct {
	Enabled bool `json:"enabled"`
	// Provider 是服务商预设名；custom 表示用户自填 BaseURL。
	Provider string `json:"provider"`
	// BaseURL 是 OpenAI 兼容 endpoint（如 https://api.deepseek.com/v1）。
	BaseURL string `json:"baseURL"`
	Model   string `json:"model"`
	// APIKey 只保存在本地 settings.json；UI 用密码框，日志永不打印。
	APIKey string `json:"apiKey"`
	// WriteProtection 是「AI 写保护」开关：默认开启，AI 只能读；
	// 用户在 UI 明确关闭后，AI 才能执行写类工具（仍受既有字符串表保护/备份约束）。
	WriteProtection bool `json:"writeProtection"`
	// CustomActions 是用户自定义的快捷命令（P3：自定义规则引擎的落地形式），
	// 显示在 AI 面板输入框上方；留空则为空。
	CustomActions []AICustomAction `json:"customActions,omitempty"`
	// QuickActionPrompts 覆盖内置快捷命令的提示词（键为快捷命令 id，值为自定义提示词）；
	// 未覆盖的 id 用前端内置默认值，设置页可逐条修改。
	QuickActionPrompts map[string]string `json:"quickActionPrompts,omitempty"`
}

// AICustomAction 是一条用户自定义的快捷命令。
type AICustomAction struct {
	Label  string `json:"label"`
	Prompt string `json:"prompt"`
}

// DefaultAIAssistantSettings 返回 AI 配置默认值：总开关关、写保护开（需求 1）。
func DefaultAIAssistantSettings() AIAssistantSettings {
	return AIAssistantSettings{
		Enabled:         false,
		Provider:        AIProviderCustom,
		BaseURL:         "",
		Model:           "",
		APIKey:          "",
		WriteProtection: true,
	}
}

type AppSettings struct {
	AnnotationTagPlacement string `json:"annotationTagPlacement"`
	ExplorerOpenMode       string `json:"explorerOpenMode"`
	VimMode                bool   `json:"vimMode"`
	BackupSourceOnSave     bool   `json:"backupSourceOnSave"`
	NPKDirectory           string `json:"npkDirectory"`
	Theme                  string `json:"theme"`
	// ProtectedStringTableGuard 是「字符串表写保护」总开关：
	// 开启后拦截对客户端汉化禁动字符串表的写入，关闭时不做任何拦截。
	ProtectedStringTableGuard bool `json:"protectedStringTableGuard"`
	// AI 是「AI 助手」配置（模型接入 + AI 写保护开关），见 AI镶嵌.md。
	AI AIAssistantSettings `json:"ai"`
	// UpdateChannel 是「更新通道」：stable 走正式更新源，dev 走开发人员专用测试源。
	// 更新器在启动时绑定更新源，切换后需重启应用生效。
	UpdateChannel string `json:"updateChannel"`
	// MCPEnabled 是「MCP 只读服务」开关：开启后应用启动时监听本机回环地址，
	// 以 MCP（Model Context Protocol）协议向外部 AI 客户端提供只读归档工具。
	// 与更新通道同理，监听在启动时绑定，切换后需重启应用生效。
	MCPEnabled bool `json:"mcpEnabled"`
	// MCPWriteEnabled 是「MCP 写能力」开关（默认关闭）：只有显式打开，外部 AI 客户端
	// 才可能调用写工具（edit_file / apply_replace）。调用时还要过「AI 写保护」这道
	// 人类门禁（WriteProtection 必须为 false），两道都放行才真正写入内存覆盖层；
	// save_archive 永远拒绝。该开关可在运行时切换，无需重启。
	MCPWriteEnabled bool `json:"mcpWriteEnabled"`
}

func DefaultAppSettings() AppSettings {
	return AppSettings{
		AnnotationTagPlacement: AnnotationTagAfterTarget,
		ExplorerOpenMode:       ExplorerOpenSingleClick,
		VimMode:                false,
		// 2026-09-24 性能优化：保存前整包备份（545 MB 级同步复制）是保存卡顿的主因，
		// 默认关闭；需要时用户可在设置里重新开启。
		BackupSourceOnSave: false,
		NPKDirectory:       "",
		Theme:              ThemeDark,
		// 2026-09-24 用户要求：字符串表写保护**默认关闭**（默认不限制写入），
		// 需要拦截时到「设置 → 交互与文件 → 字符串表写保护」打开开关。
		ProtectedStringTableGuard: false,
		AI:                         DefaultAIAssistantSettings(),
		UpdateChannel:              UpdateChannelStable,
		MCPEnabled:                 false,
		MCPWriteEnabled:            false,
	}
}

type SettingsService struct {
	mu      sync.Mutex
	path    string
	initErr error
}

func NewSettingsService() *SettingsService {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return &SettingsService{initErr: fmt.Errorf("获取用户配置目录失败: %w", err)}
	}
	service := newSettingsService(filepath.Join(configDir, "pvfine", "settings.json"))
	// 启动即同步写保护总开关，避免"设置尚未被界面读过，保护却按默认值放行"的窗口期。
	service.applyStringTableGuardSetting()
	return service
}

func newSettingsService(path string) *SettingsService {
	return &SettingsService{path: path}
}

// applyStringTableGuardSetting 把设置里的写保护开关同步到进程内总开关。
// 读盘失败时保持当前状态（默认关闭），不影响应用启动。
func (s *SettingsService) applyStringTableGuardSetting() {
	s.mu.Lock()
	settings, err := s.getSettingsLocked()
	s.mu.Unlock()
	if err != nil {
		return
	}
	setStringTableGuardEnabled(settings.ProtectedStringTableGuard)
}

func (s *SettingsService) GetSettings() (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getSettingsLocked()
}

func (s *SettingsService) getSettingsLocked() (AppSettings, error) {
	if s.initErr != nil {
		return AppSettings{}, s.initErr
	}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return DefaultAppSettings(), nil
	}
	if err != nil {
		return AppSettings{}, fmt.Errorf("读取设置失败: %w", err)
	}
	settings := DefaultAppSettings()
	if err := json.Unmarshal(data, &settings); err != nil {
		return AppSettings{}, fmt.Errorf("解析设置失败: %w", err)
	}
	if err := validateSettings(settings); err != nil {
		return AppSettings{}, err
	}
	return settings, nil
}

func (s *SettingsService) SaveSettings(settings AppSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.saveSettingsLocked(settings); err != nil {
		return err
	}
	// 开关立即生效：写保护是进程内状态，不必等下次读设置。
	setStringTableGuardEnabled(settings.ProtectedStringTableGuard)
	return nil
}

func (s *SettingsService) saveSettingsLocked(settings AppSettings) error {
	if s.initErr != nil {
		return s.initErr
	}
	if err := validateSettings(settings); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("创建设置目录失败: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("创建设置临时文件失败: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("写入设置失败: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("同步设置失败: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("关闭设置文件失败: %w", err)
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("替换设置文件失败: %w", err)
	}
	return nil
}

// UpdateNPKDirectory changes only the image resource directory while
// preserving settings added by newer versions of the application.
func (s *SettingsService) UpdateNPKDirectory(directory string) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.getSettingsLocked()
	if err != nil {
		return AppSettings{}, err
	}
	settings.NPKDirectory = directory
	if err := s.saveSettingsLocked(settings); err != nil {
		return AppSettings{}, err
	}
	return settings, nil
}

func validateSettings(settings AppSettings) error {
	switch settings.AnnotationTagPlacement {
	case AnnotationTagAfterTarget, AnnotationTagLineEnd, AnnotationTagHidden:
	default:
		return fmt.Errorf("无效的标注 Tag 显示位置: %q", settings.AnnotationTagPlacement)
	}
	switch settings.ExplorerOpenMode {
	case ExplorerOpenSingleClick, ExplorerOpenDoubleClick:
		// Keep theme validation beside the other persisted enum settings so
		// invalid values cannot be written to the user configuration.
	default:
		return fmt.Errorf("无效的资源管理器打开方式: %q", settings.ExplorerOpenMode)
	}
	switch settings.Theme {
	case ThemeDark, ThemeLight, ThemeSystem:
	default:
		return fmt.Errorf("无效的主题: %q", settings.Theme)
	}
	switch settings.UpdateChannel {
	case UpdateChannelStable, UpdateChannelDev:
	default:
		return fmt.Errorf("无效的更新通道: %q", settings.UpdateChannel)
	}
	// AI 配置不在这里做硬校验（2026-09-25 修复）：此前的「启用时地址/模型必填」会把
	// 整个设置保存一起拒掉，造成"字段填不进去"的死锁；缺字段的后果改由
	// AIService.aiConfig() 在真正发起对话时报运行时错误，指引补全。
	return nil
}
