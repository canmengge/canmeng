// Package logging 提供带级别、模块名与阶段计时的轻量日志系统。
//
// 设计要点：
//   - 分级：DEBUG / INFO / WARN / ERROR，可整体或按模块覆盖；
//   - 时间：每条带毫秒时间戳，并提供 StartStage 辅助记录阶段耗时；
//   - 输出：控制台 + 文件双通道，行格式 = 时间 [级别] [模块] 消息 key=value；
//   - 管理：按「日期 + 大小」滚动，超出保留数量自动删除最旧文件；
//   - 可配：环境变量 > logging.json > 默认值，详见 config.go；
//   - 性能：WARN/ERROR 同步写（绝不丢），DEBUG/INFO 走异步队列（满则丢弃并
//     计数），因此高频调用不会阻塞业务线程。
//
// 日志系统自身的任何失败都不会中断程序：初始化失败时降级为「仅控制台」。
package logging

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Level 是日志级别，数值越大越严重。
type Level int8

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
	// LevelOff 关闭全部输出（仍可被模块级配置单独打开）。
	LevelOff
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "OFF"
	}
}

const (
	// queueCapacity 是异步队列长度。按 200 字节/条估算，满载约 1.6 MB。
	queueCapacity = 8192
	timeLayout    = "2006-01-02 15:04:05.000"
)

type logger struct {
	cfg     Config
	sink    *rotatingWriter
	dir     string
	dirNote string

	// writeMu 串行化所有实际写操作（控制台 + 文件）。
	writeMu sync.Mutex

	// queueMu 保证「投递」与「关闭队列」不会竞争（向已关闭 channel 发送会 panic）。
	queueMu     sync.RWMutex
	queue       chan []byte
	queueClosed bool
	workers     sync.WaitGroup

	dropped   atomic.Uint64
	closeOnce sync.Once
	closed    atomic.Bool
	logErrors atomic.Uint64
}

// Handle 暴露初始化结果，便于启动期记录路径、退出前刷新。
type Handle struct {
	lg *logger
}

// Dir 返回实际使用的日志目录（可能为空 = 未启用文件日志）。
func (h *Handle) Dir() string {
	if h == nil || h.lg == nil {
		return ""
	}
	return h.lg.dir
}

// FilePath 返回当前正在写入的日志文件路径。
func (h *Handle) FilePath() string {
	if h == nil || h.lg == nil || h.lg.sink == nil {
		return ""
	}
	return h.lg.sink.CurrentPath()
}

// Dropped 返回因队列已满而被丢弃的日志条数（只可能影响 DEBUG/INFO）。
func (h *Handle) Dropped() uint64 {
	if h == nil || h.lg == nil {
		return 0
	}
	return h.lg.dropped.Load()
}

// Config 返回生效配置，便于启动日志里记录。
func (h *Handle) Config() Config {
	if h == nil || h.lg == nil {
		return defaultConfig()
	}
	return h.lg.cfg
}

// Close 刷新并释放日志资源；可重复调用。
func (h *Handle) Close() error {
	if h == nil || h.lg == nil {
		return nil
	}
	return h.lg.close()
}

var defaultLogger atomic.Pointer[logger]

// Init 读取配置并初始化全局日志。返回的 Handle 用于关闭与查询。
// 重复调用会先关闭上一个实例（便于测试与热切换）。
func Init() *Handle {
	return initLogger(true)
}

// InitOnce 与 Init 相同，但忽略重复初始化（供 main 之外的调用方使用）。
func InitOnce() *Handle {
	if lg := defaultLogger.Load(); lg != nil {
		return &Handle{lg: lg}
	}
	return initLogger(false)
}

func initLogger(replace bool) *Handle {
	cfg, source := resolveConfig()
	dir, dirNote := "", ""
	if cfg.File {
		dir, dirNote = resolveLogDir(cfg.Dir)
		if dir == "" {
			cfg.File = false
		}
	}
	cfg.Dir = dir

	lg := &logger{cfg: cfg, dir: dir, dirNote: dirNote}
	if cfg.File {
		lg.sink = newRotatingWriter(dir, "pvfine", int64(cfg.MaxSizeMB)<<20, cfg.Keep)
	}
	if cfg.Async {
		lg.queue = make(chan []byte, queueCapacity)
		lg.workers.Add(1)
		go lg.runQueue()
	}

	previous := defaultLogger.Swap(lg)
	if previous != nil && replace {
		_ = previous.close()
	}

	// 让标准库 log（以及使用它的第三方库）的输出也落到同一个文件里，
	// 避免 Wails/运行时日志散落在看不见的地方。
	log.SetFlags(0)
	log.SetOutput(&stdLogWriter{lg: lg})

	lg.writeSync(formatLine("logging", LevelInfo, "日志系统已初始化",
		[]any{
			"程序", executablePath(),
			"目录", orNone(dir),
			"目录来源", orNone(dirNote),
			"级别", cfg.Level.String(),
			"控制台", cfg.Console,
			"文件", cfg.File,
			"分片上限MB", cfg.MaxSizeMB,
			"保留份数", cfg.Keep,
			"异步", cfg.Async,
			"配置来源", orNone(source),
			"PID", os.Getpid(),
		}))
	return &Handle{lg: lg}
}

// 当前 logger；未初始化时返回 nil（所有日志调用变成空操作）。
func current() *logger { return defaultLogger.Load() }

func (lg *logger) enabled(module string, l Level) bool {
	if lg == nil {
		return false
	}
	if lv, ok := lg.cfg.ModuleLevels[strings.ToLower(module)]; ok {
		if lv == LevelOff {
			return false
		}
		return l >= lv
	}
	if lg.cfg.Level == LevelOff {
		return false
	}
	return l >= lg.cfg.Level
}

func (lg *logger) log(module string, l Level, msg string, kv []any) {
	if !lg.enabled(module, l) {
		return
	}
	line := formatLine(module, l, msg, kv)
	// 同源推给前端「输出日志」面板（带每秒上限，回调必须立即返回；详见 events.go）。
	publish(module, l, msg, kv, line)
	if !lg.cfg.Async || l >= LevelWarn {
		// 警告与错误同步写：慢一点也要保证落盘。
		lg.writeSync(line)
		return
	}
	lg.queueMu.RLock()
	defer lg.queueMu.RUnlock()
	if lg.queueClosed {
		return
	}
	select {
	case lg.queue <- line:
	default:
		// 队列已满：丢弃并计数，绝不阻塞调用方。
		lg.dropped.Add(1)
	}
}

func (lg *logger) runQueue() {
	defer lg.workers.Done()
	// 队列排空用的是直写通道：close() 会先置 closed=true 阻止新的外部写入，
	// 但已经入队的条目必须在关闭前落盘，不能被 closed 检查挡掉。
	for line := range lg.queue {
		lg.writeDirect(line)
	}
}

// writeSync 对外使用：一旦关闭就丢弃，避免关闭后再写导致日志文件被重新打开
// （那样会留下未释放的文件句柄，Windows 上表现为文件无法删除/占用）。
func (lg *logger) writeSync(line []byte) {
	if lg.closed.Load() {
		return
	}
	lg.writeDirect(line)
}

// writeDirect 是实际写实现，不做关闭检查，仅供 close() 写最后一条记录。
func (lg *logger) writeDirect(line []byte) {
	lg.writeMu.Lock()
	defer lg.writeMu.Unlock()
	if lg.cfg.Console {
		// Windows GUI 程序通常没有控制台，写失败属正常情况，忽略即可。
		_, _ = os.Stderr.Write(line)
	}
	if lg.sink != nil {
		if _, err := lg.sink.Write(line); err != nil {
			// 只汇报有限次数，避免磁盘故障时日志风暴。
			if lg.logErrors.Add(1) <= 3 {
				_, _ = fmt.Fprintf(os.Stderr, "%s [WARN ] [logging] 写入日志文件失败: %v\n",
					time.Now().Format(timeLayout), err)
			}
		}
		// 额外刷盘由 Close 统一处理：逐条 Sync 会拖慢高频写入。
	}
}

func (lg *logger) close() error {
	if lg == nil {
		return nil
	}
	var err error
	lg.closeOnce.Do(func() {
		lg.closed.Store(true)
		lg.queueMu.Lock()
		if lg.queue != nil {
			lg.queueClosed = true
			close(lg.queue)
		}
		lg.queueMu.Unlock()
		lg.workers.Wait()

		dropped := lg.dropped.Load()
		lg.writeDirect(formatLine("logging", LevelInfo, "日志系统关闭",
			[]any{"丢弃条数", dropped, "当前文件", orNone(lg.sinkPath())}))
		if lg.sink != nil {
			err = lg.sink.Close()
		}
	})
	return err
}

func (lg *logger) sinkPath() string {
	if lg.sink == nil {
		return ""
	}
	return lg.sink.CurrentPath()
}

// formatLine 组装一行日志：
// 2026-09-24 08:52:31.123 [INFO ] [archive] 打开归档 | 文件=... 耗时=6.28s
func formatLine(module string, l Level, msg string, kv []any) []byte {
	var b bytes.Buffer
	b.Grow(160)
	b.WriteString(time.Now().Format(timeLayout))
	b.WriteString(" [")
	b.WriteString(l.String())
	// 级别对齐成 5 字符，便于肉眼看列
	for i := len(l.String()); i < 5; i++ {
		b.WriteByte(' ')
	}
	b.WriteString("] [")
	if module == "" {
		module = "-"
	}
	b.WriteString(module)
	b.WriteString("] ")
	b.WriteString(msg)
	for i := 0; i < len(kv); i += 2 {
		b.WriteString(" ")
		b.WriteString(fmt.Sprint(kv[i]))
		b.WriteByte('=')
		if i+1 < len(kv) {
			b.WriteString(fmt.Sprint(kv[i+1]))
		} else {
			b.WriteString("<缺少值>")
		}
	}
	b.WriteByte('\n')
	return b.Bytes()
}

// stdLogWriter 把标准库 log 的输出转成一条 INFO 日志。
type stdLogWriter struct{ lg *logger }

func (w *stdLogWriter) Write(p []byte) (int, error) {
	if w == nil || w.lg == nil {
		return len(p), nil
	}
	text := strings.TrimRight(string(p), "\r\n")
	if text != "" {
		w.lg.log("stdlog", LevelInfo, text, nil)
	}
	return len(p), nil
}

func executablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return "<未知>"
	}
	return exe
}

func orNone(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

// ---------------------------------------------------------------------------
// 全局便捷 API
// ---------------------------------------------------------------------------

// Debug 输出调试日志（异步，可能因队列满被丢弃）。
func Debug(module, msg string, kv ...any) { current().log(module, LevelDebug, msg, kv) }

// Info 输出常规信息（异步）。
func Info(module, msg string, kv ...any) { current().log(module, LevelInfo, msg, kv) }

// Warn 输出警告（同步写，不丢）。
func Warn(module, msg string, kv ...any) { current().log(module, LevelWarn, msg, kv) }

// Error 输出错误（同步写，不丢）。
func Error(module, msg string, kv ...any) { current().log(module, LevelError, msg, kv) }

// Errorf 便捷格式化错误日志。
func Errorf(module, format string, args ...any) {
	current().log(module, LevelError, fmt.Sprintf(format, args...), nil)
}

// Warnf 便捷格式化警告日志。
func Warnf(module, format string, args ...any) {
	current().log(module, LevelWarn, fmt.Sprintf(format, args...), nil)
}

// Infof 便捷格式化信息日志。
func Infof(module, format string, args ...any) {
	current().log(module, LevelInfo, fmt.Sprintf(format, args...), nil)
}

// Enabled 判断某模块的某级别是否会输出（用于避免昂贵的日志参数构造）。
func Enabled(module string, l Level) bool { return current().enabled(module, l) }

// Module 是把模块名绑定好的日志入口，避免每行重复写模块名。
type Module struct{ name string }

// For 返回绑定模块名的日志入口。
func For(name string) *Module { return &Module{name: name} }

func (m *Module) Debug(msg string, kv ...any) { Debug(m.name, msg, kv...) }
func (m *Module) Info(msg string, kv ...any)  { Info(m.name, msg, kv...) }
func (m *Module) Warn(msg string, kv ...any)  { Warn(m.name, msg, kv...) }
func (m *Module) Error(msg string, kv ...any) { Error(m.name, msg, kv...) }
func (m *Module) Errorf(format string, args ...any) { Errorf(m.name, format, args...) }
func (m *Module) Infof(format string, args ...any)  { Infof(m.name, format, args...) }

// ---------------------------------------------------------------------------
// 阶段计时
// ---------------------------------------------------------------------------

// Stage 记录一个阶段的耗时。用它包住「打开归档」这类长任务，
// 即可在日志里得到统一的开始/完成/失败时间线。
type Stage struct {
	module string
	name   string
	start  time.Time
}

// StartStage 开始计时（开始本身只记 DEBUG，避免噪音）。
func StartStage(module, name string, kv ...any) *Stage {
	s := &Stage{module: module, name: name, start: time.Now()}
	Debug(module, "阶段开始: "+name, kv...)
	return s
}

// Elapsed 返回从开始到现在的耗时。
func (s *Stage) Elapsed() time.Duration {
	if s == nil {
		return 0
	}
	return time.Since(s.start)
}

// Done 记录阶段完成与耗时（INFO）。
func (s *Stage) Done(kv ...any) time.Duration {
	if s == nil {
		return 0
	}
	d := s.Elapsed()
	Info(s.module, "阶段完成: "+s.name, append([]any{"耗时", FormatDuration(d)}, kv...)...)
	return d
}

// Fail 记录阶段失败、耗时与错误（ERROR）。
func (s *Stage) Fail(err error, kv ...any) time.Duration {
	if s == nil {
		return 0
	}
	d := s.Elapsed()
	kv = append([]any{"耗时", FormatDuration(d)}, kv...)
	if err != nil {
		kv = append(kv, "错误", err.Error())
	}
	Error(s.module, "阶段失败: "+s.name, kv...)
	return d
}

// FormatDuration 把耗时格式化成人类易读的字符串。
func FormatDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "0s"
	case d < time.Millisecond:
		return fmt.Sprintf("%.3fms", float64(d)/float64(time.Millisecond))
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%.3fs", d.Seconds())
	}
}
