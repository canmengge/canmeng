package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestParseLevel 覆盖级别名称解析（含大小写与别名）。
func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"debug": LevelDebug,
		"DBG":   LevelDebug,
		"Info":  LevelInfo,
		"warn":  LevelWarn,
		"ERROR": LevelError,
		"off":   LevelOff,
	}
	for raw, want := range cases {
		got, ok := parseLevel(raw)
		if !ok || got != want {
			t.Fatalf("parseLevel(%q) = %v, %v; want %v, true", raw, got, ok, want)
		}
	}
	if _, ok := parseLevel("verbose"); ok {
		t.Fatal("未知级别应返回 false")
	}
}

// TestFormatLine 校验行格式包含时间戳、级别、模块与键值。
func TestFormatLine(t *testing.T) {
	line := string(formatLine("archive", LevelWarn, "打开失败", []any{"文件", "a.pvf", "错误", "boom"}))
	if !strings.Contains(line, "[WARN ]") {
		t.Fatalf("级别未按 5 字符对齐: %q", line)
	}
	if !strings.Contains(line, "[archive]") || !strings.Contains(line, "打开失败") {
		t.Fatalf("缺少模块或消息: %q", line)
	}
	if !strings.Contains(line, "文件=a.pvf") || !strings.Contains(line, "错误=boom") {
		t.Fatalf("键值未输出: %q", line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("行尾缺少换行: %q", line)
	}
	// 时间戳形如 2026-09-24 08:52:31.123
	if ts := time.Now().Format("2006-01-02"); !strings.HasPrefix(line, ts) {
		t.Fatalf("缺少毫秒时间戳: %q", line)
	}
	// 奇数个参数不能 panic
	odd := string(formatLine("m", LevelInfo, "msg", []any{"只有键"}))
	if !strings.Contains(odd, "只有键=<缺少值>") {
		t.Fatalf("奇数键值未按预期处理: %q", odd)
	}
}

// TestLevelFilter 校验级别过滤与按模块覆盖。
func TestLevelFilter(t *testing.T) {
	lg := &logger{cfg: Config{Level: LevelWarn}}
	if lg.enabled("any", LevelInfo) {
		t.Fatal("INFO 应被 WARN 级别过滤")
	}
	if !lg.enabled("any", LevelError) {
		t.Fatal("ERROR 不应被过滤")
	}
	lg.cfg.ModuleLevels = map[string]Level{"archive": LevelDebug, "noisy": LevelOff}
	if !lg.enabled("Archive", LevelDebug) {
		t.Fatal("模块级覆盖应生效（且大小写不敏感）")
	}
	if lg.enabled("noisy", LevelError) {
		t.Fatal("LevelOff 模块应完全静默")
	}
}

// TestRotateBySize 校验按大小滚动产生序号文件。
func TestRotateBySize(t *testing.T) {
	dir := t.TempDir()
	w := newRotatingWriter(dir, "pvfine", 200, 10)
	payload := []byte(strings.Repeat("x", 120) + "\n")
	for i := 0; i < 4; i++ {
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) < 2 {
		t.Fatalf("超过大小上限后应滚动出新的文件，实际只有 %d 个", len(files))
	}
	found := false
	for _, f := range files {
		if strings.Contains(f.Name(), ".1.log") {
			found = true
		}
	}
	if !found {
		t.Fatalf("未找到序号文件: %v", fileNames(dir))
	}
}

// TestRetainPolicy 校验超量文件会被清理。
func TestRetainPolicy(t *testing.T) {
	dir := t.TempDir()
	// 直接造 6 个历史文件，再触发一次 prune
	for _, name := range []string{
		"pvfine-2026-09-20.log", "pvfine-2026-09-21.log", "pvfine-2026-09-22.log",
		"pvfine-2026-09-23.log", "pvfine-2026-09-24.log", "pvfine-2026-09-24.1.log",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w := newRotatingWriter(dir, "pvfine", 1<<20, 3)
	w.mu.Lock()
	w.pruneLocked()
	w.mu.Unlock()

	remaining := fileNames(dir)
	if len(remaining) != 3 {
		t.Fatalf("保留策略应只留 3 个文件，实际 %d 个: %v", len(remaining), remaining)
	}
	for _, name := range remaining {
		if name == "pvfine-2026-09-20.log" || name == "pvfine-2026-09-21.log" || name == "pvfine-2026-09-22.log" {
			t.Fatalf("最旧的文件未被清理: %v", remaining)
		}
	}
}

// TestAsyncFlushAndClose 校验异步写入在 Close 后不丢日志，且关闭后调用不 panic。
func TestAsyncFlushAndClose(t *testing.T) {
	dir := t.TempDir()
	lg := &logger{cfg: Config{Level: LevelDebug, File: true, Async: true, MaxSizeMB: 10, Keep: 5}, dir: dir}
	lg.sink = newRotatingWriter(dir, "pvfine", 10<<20, 5)
	lg.queue = make(chan []byte, queueCapacity)
	lg.workers.Add(1)
	go lg.runQueue()

	for i := 0; i < 200; i++ {
		lg.log("test", LevelInfo, "异步条目", []any{"i", i})
	}
	lg.log("test", LevelWarn, "同步条目", []any{"k", "v"})

	if err := lg.close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}
	// 关闭后再记录不应 panic
	lg.log("test", LevelError, "关闭之后的日志", nil)

	path := lg.sinkPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志失败: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "异步条目") || !strings.Contains(text, "同步条目") {
		t.Fatalf("Close 后仍有日志丢失")
	}
	if strings.Count(text, "异步条目") != 200 {
		t.Fatalf("异步队列未完整落盘: 期望 200 条，实际 %d", strings.Count(text, "异步条目"))
	}
	if !strings.Contains(text, "日志系统关闭") {
		t.Fatal("缺少关闭记录")
	}
}

// TestEnvOverride 校验环境变量优先于默认值。
func TestEnvOverride(t *testing.T) {
	t.Setenv("PVFINE_LOG_LEVEL", "debug")
	t.Setenv("PVFINE_LOG_KEEP", "7")
	t.Setenv("PVFINE_LOG_CONSOLE", "0")
	t.Setenv("PVFINE_LOG_MODULE_LEVELS", "archive=warn,index=debug")
	t.Setenv("PVFINE_LOG_CONFIG", filepath.Join(t.TempDir(), "missing.json"))

	cfg, source := resolveConfig()
	if cfg.Level != LevelDebug {
		t.Fatalf("级别未被环境变量覆盖: %v", cfg.Level)
	}
	if cfg.Keep != 7 {
		t.Fatalf("保留份数未被覆盖: %d", cfg.Keep)
	}
	if cfg.Console {
		t.Fatal("控制台开关未生效")
	}
	if cfg.ModuleLevels["archive"] != LevelWarn || cfg.ModuleLevels["index"] != LevelDebug {
		t.Fatalf("模块级别覆盖未生效: %v", cfg.ModuleLevels)
	}
	if !strings.Contains(source, "PVFINE_LOG_LEVEL") {
		t.Fatalf("配置来源未记录: %q", source)
	}
}

// TestFileConfigAndPrecedence 校验配置文件生效，且环境变量优先。
func TestFileConfigAndPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "logging.json")
	content := `{"level":"warn","keep":4,"maxSizeMB":3,"console":false,"async":false}`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PVFINE_LOG_CONFIG", cfgPath)

	cfg, _ := resolveConfig()
	if cfg.Level != LevelWarn || cfg.Keep != 4 || cfg.MaxSizeMB != 3 || cfg.Async {
		t.Fatalf("配置文件未生效: %+v", cfg)
	}
	// console 在文件里是 false，且 file 默认为 true，因此不该被"两个都关"的兜底改回 true
	if cfg.Console {
		t.Fatalf("console=false 未生效: %+v", cfg)
	}

	t.Setenv("PVFINE_LOG_LEVEL", "error")
	cfg2, _ := resolveConfig()
	if cfg2.Level != LevelError {
		t.Fatalf("环境变量应优先于配置文件: %v", cfg2.Level)
	}
}

// TestInitAndClose 端到端：初始化、写各级日志、关闭、检查文件内容。
func TestInitAndClose(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PVFINE_LOG_DIR", dir)
	t.Setenv("PVFINE_LOG_LEVEL", "debug")
	t.Setenv("PVFINE_LOG_CONFIG", filepath.Join(dir, "missing.json"))

	handle := Init()
	if handle.Dir() != dir {
		t.Fatalf("日志目录未按配置生效: %q", handle.Dir())
	}
	Debug("dbg", "调试信息", "k", 1)
	Info("app", "启动完成", "耗时", FormatDuration(1500*time.Millisecond))
	Warn("app", "降级运行")
	Error("app", "出现错误", "err", "boom")

	stage := StartStage("index", "构建搜索索引")
	time.Sleep(2 * time.Millisecond)
	if d := stage.Done("条目", 638542); d <= 0 {
		t.Fatal("阶段耗时应大于 0")
	}
	stage2 := StartStage("index", "会失败的阶段")
	stage2.Fail(os.ErrNotExist, "文件", "x.pvf")

	if dropped := handle.Dropped(); dropped != 0 {
		t.Fatalf("不应有丢弃: %d", dropped)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	data, err := os.ReadFile(handle.FilePath())
	if err != nil {
		t.Fatalf("读取日志失败: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"调试信息", "启动完成", "耗时=1.500s", "降级运行", "出现错误",
		"阶段完成: 构建搜索索引", "条目=638542", "阶段失败: 会失败的阶段", "日志系统关闭",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("日志缺少 %q\n----\n%s", want, text)
		}
	}
}

func fileNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}
