package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"pvfine/internal/apppaths"
)

const (
	defaultMaxSizeMB = 10
	defaultKeep      = 10
	configFileName   = "logging.json"
	logDirName       = "logs"
)

// Config 是日志系统的最终生效配置。
type Config struct {
	// Level 为最低输出级别，低于它的日志会被直接丢弃（零成本）。
	Level Level
	// Dir 为日志目录；为空时自动选择（exe 同目录 logs，不可写则回退用户缓存目录）。
	Dir string
	// Console 控制是否输出到控制台。
	Console bool
	// File 控制是否输出到文件。
	File bool
	// MaxSizeMB 是单个日志文件的大小上限（MB）。
	MaxSizeMB int
	// Keep 是要保留的历史日志文件数量。
	Keep int
	// Async 为 true 时，Debug/Info 走异步队列（不阻塞调用方）。
	// Warn/Error 始终同步写入，保证不丢。
	Async bool
	// ModuleLevels 按模块名（小写）覆盖级别，用于压制噪音模块或单独放大某模块。
	ModuleLevels map[string]Level
	// Source 记录配置来源，便于排查（例如 "env:PVFINE_LOG_LEVEL"）。
	Source string
}

// FileConfig 对应可选的 logging.json 配置文件。
// 使用指针类型区分「未配置」与「配置为 false」。
type FileConfig struct {
	Level     string `json:"level,omitempty"`
	Dir       string `json:"dir,omitempty"`
	Console   *bool  `json:"console,omitempty"`
	File      *bool  `json:"file,omitempty"`
	MaxSizeMB int    `json:"maxSizeMB,omitempty"`
	Keep      int    `json:"keep,omitempty"`
	Async     *bool  `json:"async,omitempty"`
}

func defaultConfig() Config {
	return Config{
		Level: LevelInfo,
		// 只有真的连着控制台才默认往 stderr 写：GUI 子系统双击启动没有控制台，
		// 日志一律进文件 + 界面「输出日志」面板（可用 PVFINE_LOG_CONSOLE 覆盖）。
		Console:   consoleAttached(),
		File:      true,
		MaxSizeMB: defaultMaxSizeMB,
		Keep:      defaultKeep,
		Async:     true,
	}
}

// resolveConfig 按「环境变量 > 配置文件 > 默认值」合成配置。
// 任何一步出错都只影响该项，不会中断初始化。
func resolveConfig() (Config, string) {
	cfg := defaultConfig()
	notes := make([]string, 0, 4)

	if fileCfg, path, err := loadFileConfig(); err != nil {
		notes = append(notes, "配置文件读取失败("+path+"): "+err.Error())
	} else if path != "" {
		if fileCfg.Level != "" {
			if lv, ok := parseLevel(fileCfg.Level); ok {
				cfg.Level = lv
			}
		}
		if fileCfg.Dir != "" {
			cfg.Dir = fileCfg.Dir
		}
		if fileCfg.Console != nil {
			cfg.Console = *fileCfg.Console
		}
		if fileCfg.File != nil {
			cfg.File = *fileCfg.File
		}
		if fileCfg.MaxSizeMB > 0 {
			cfg.MaxSizeMB = fileCfg.MaxSizeMB
		}
		if fileCfg.Keep > 0 {
			cfg.Keep = fileCfg.Keep
		}
		if fileCfg.Async != nil {
			cfg.Async = *fileCfg.Async
		}
		notes = append(notes, "配置文件:"+path)
	}

	if v := strings.TrimSpace(os.Getenv("PVFINE_LOG_LEVEL")); v != "" {
		if lv, ok := parseLevel(v); ok {
			cfg.Level = lv
			notes = append(notes, "env:PVFINE_LOG_LEVEL="+v)
		}
	}
	if v := strings.TrimSpace(os.Getenv("PVFINE_LOG_DIR")); v != "" {
		cfg.Dir = v
		notes = append(notes, "env:PVFINE_LOG_DIR")
	}
	if v, ok := envBool("PVFINE_LOG_CONSOLE"); ok {
		cfg.Console = v
		notes = append(notes, "env:PVFINE_LOG_CONSOLE")
	}
	if v, ok := envBool("PVFINE_LOG_FILE"); ok {
		cfg.File = v
		notes = append(notes, "env:PVFINE_LOG_FILE")
	}
	if v, ok := envInt("PVFINE_LOG_MAX_MB"); ok && v > 0 {
		cfg.MaxSizeMB = v
		notes = append(notes, "env:PVFINE_LOG_MAX_MB")
	}
	if v, ok := envInt("PVFINE_LOG_KEEP"); ok && v > 0 {
		cfg.Keep = v
		notes = append(notes, "env:PVFINE_LOG_KEEP")
	}
	if v, ok := envBool("PVFINE_LOG_ASYNC"); ok {
		cfg.Async = v
		notes = append(notes, "env:PVFINE_LOG_ASYNC")
	}
	if v := strings.TrimSpace(os.Getenv("PVFINE_LOG_MODULE_LEVELS")); v != "" {
		cfg.ModuleLevels = parseModuleLevels(v)
		notes = append(notes, "env:PVFINE_LOG_MODULE_LEVELS")
	}
	if !cfg.Console && !cfg.File {
		// 两个出口都关掉等于静默，通常不是本意：保留控制台，避免"没有任何输出"
		// 造成的排查困难。
		cfg.Console = true
		notes = append(notes, "警告:console 与 file 同时为 false，已回退为仅控制台")
	}
	cfg.Source = strings.Join(notes, " | ")
	return cfg, cfg.Source
}

// parseModuleLevels 解析 "archive=debug,index=warn" 形式的按模块级别覆盖。
func parseModuleLevels(raw string) map[string]Level {
	result := make(map[string]Level)
	for _, part := range strings.Split(raw, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(kv[0]))
		if name == "" {
			continue
		}
		if lv, ok := parseLevel(kv[1]); ok {
			result[name] = lv
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// loadFileConfig 读取配置文件。返回空路径表示没有配置文件（不是错误）。
func loadFileConfig() (FileConfig, string, error) {
	path := strings.TrimSpace(os.Getenv("PVFINE_LOG_CONFIG"))
	if path == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return FileConfig{}, "", nil
		}
		path = filepath.Join(dir, "pvfine", configFileName)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return FileConfig{}, "", nil
		}
		return FileConfig{}, path, err
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, path, err
	}
	return cfg, path, nil
}

func envBool(name string) (bool, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, false
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on", "y":
		return true, true
	case "0", "false", "no", "off", "n":
		return false, true
	}
	return false, false
}

func envInt(name string) (int, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return v, true
}

func parseLevel(raw string) (Level, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug", "dbg", "trace":
		return LevelDebug, true
	case "info", "information":
		return LevelInfo, true
	case "warn", "warning":
		return LevelWarn, true
	case "error", "err", "fatal":
		return LevelError, true
	case "off", "none", "silent", "disable":
		return LevelOff, true
	}
	return LevelInfo, false
}

// resolveLogDir 决定日志目录：显式配置 > 集中缓存目录\logs（可写时）> exe 同目录 logs > 用户缓存目录。
func resolveLogDir(configured string) (string, string) {
	if strings.TrimSpace(configured) != "" {
		if err := ensureWritableDir(configured); err == nil {
			return configured, "配置指定"
		}
		// 配置的目录不可写时继续尝试默认位置，避免完全丢失文件日志
		if fallback := userCacheLogDir(); fallback != "" {
			if err := ensureWritableDir(fallback); err == nil {
				return fallback, "配置目录不可写，回退用户缓存目录"
			}
		}
		return "", "配置目录不可写且无法回退"
	}
	// 2026-09-24：默认与索引缓存放在同一个可整体删除的目录（…\pvfine-main\HC\logs），
	// 用户清理缓存时日志一并带走，不会散落在程序目录里。
	if dir, err := apppaths.SubDir(logDirName); err == nil {
		return dir, "集中缓存目录"
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Join(filepath.Dir(exe), logDirName)
		if err := ensureWritableDir(dir); err == nil {
			return dir, "程序目录"
		}
	}
	if dir := userCacheLogDir(); dir != "" {
		if err := ensureWritableDir(dir); err == nil {
			return dir, "用户缓存目录"
		}
	}
	return "", "没有可写目录"
}

func userCacheLogDir() string {
	// 与索引缓存共用同一个解析逻辑（含系统缓存目录兜底）。
	dir := apppaths.CacheDirOrFallback()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, logDirName)
}

// ensureWritableDir 创建目录并验证可写（仅靠 MkdirAll 不能发现只读目录）。
func ensureWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".write-probe-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}
