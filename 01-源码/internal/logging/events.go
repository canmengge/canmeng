package logging

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Entry 是一条结构化日志，供前端「输出日志」面板使用。
//
// 它与文件/控制台通道同源同格式：`Text` 就是写进文件的那一行，`Fields` 是
// key=value 部分单独拆出来，便于界面把「消息」和「参数」分色显示。
type Entry struct {
	Time    string `json:"time"`  // HH:MM:SS.mmm
	Level   string `json:"level"` // DEBUG / INFO / WARN / ERROR
	Module  string `json:"module"`
	Message string `json:"message"`
	Fields  string `json:"fields,omitempty"`
	Text    string `json:"text,omitempty"`
}

const (
	// entryBufferSize 是给「后打开的面板」补历史用的环形缓冲长度。
	entryBufferSize = 512
	// entryMaxPerSec 是每秒推给 UI 的条数上限：宁可丢一部分界面行，也不能让
	// 界面通道反过来拖慢业务线程（日志系统的原则是"绝不阻塞调用方"）。
	entryMaxPerSec = 400
)

var (
	entrySinkMu sync.RWMutex
	entrySink   func(Entry)

	entryBufferMu sync.Mutex
	entryBuffer   []Entry

	entryWindowSecond atomic.Int64
	entryWindowCount  atomic.Int64
	entryThrottled    atomic.Uint64
)

// SetEntrySink 注册 UI 接收器（进程内唯一；传 nil 取消注册）。
//
// ⚠️ 回调在**写日志的调用线程**上同步执行，必须立刻返回（Wails 的
// `Event.Emit` 只是入队，符合要求）；任何阻塞都会拖慢业务线程。
func SetEntrySink(fn func(Entry)) {
	entrySinkMu.Lock()
	entrySink = fn
	entrySinkMu.Unlock()
}

// History 返回最近的结构化日志（旧 → 新）。
func History() []Entry {
	entryBufferMu.Lock()
	defer entryBufferMu.Unlock()
	if len(entryBuffer) == 0 {
		return nil
	}
	out := make([]Entry, len(entryBuffer))
	copy(out, entryBuffer)
	return out
}

// ClearHistory 清空历史缓冲（不影响日志文件，也不影响后续推送）。
func ClearHistory() {
	entryBufferMu.Lock()
	entryBuffer = nil
	entryBufferMu.Unlock()
}

// Throttled 返回因每秒上限被丢弃的界面日志条数（文件通道不受影响）。
func Throttled() uint64 { return entryThrottled.Load() }

// publish 把一条日志放进环形缓冲并推给 UI。
func publish(module string, l Level, msg string, kv []any, line []byte) {
	if module == "" {
		module = "-"
	}
	now := time.Now()
	if !allowEntry(now) {
		return
	}

	entry := Entry{
		Time:    now.Format("15:04:05.000"),
		Level:   l.String(),
		Module:  module,
		Message: msg,
		Fields:  formatFields(kv),
		Text:    strings.TrimRight(string(line), "\r\n"),
	}

	entryBufferMu.Lock()
	if len(entryBuffer) >= entryBufferSize {
		// 简单丢弃最旧的一条：面板只关心最近发生了什么。
		copy(entryBuffer, entryBuffer[1:])
		entryBuffer[len(entryBuffer)-1] = entry
	} else {
		entryBuffer = append(entryBuffer, entry)
	}
	entryBufferMu.Unlock()

	entrySinkMu.RLock()
	sink := entrySink
	entrySinkMu.RUnlock()
	if sink != nil {
		sink(entry)
	}
}

// allowEntry 做每秒配额；超限只计数，不做任何阻塞。
func allowEntry(now time.Time) bool {
	second := now.Unix()
	if entryWindowSecond.Load() != second {
		entryWindowSecond.Store(second)
		entryWindowCount.Store(0)
	}
	if entryWindowCount.Add(1) > entryMaxPerSec {
		entryThrottled.Add(1)
		return false
	}
	return true
}

// formatFields 把 key=value 参数拼成一行（与文件日志的写法一致）。
func formatFields(kv []any) string {
	if len(kv) == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(toString(kv[i]))
		b.WriteByte('=')
		if i+1 < len(kv) {
			b.WriteString(toString(kv[i+1]))
		} else {
			b.WriteString("<缺少值>")
		}
	}
	return b.String()
}

func toString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case error:
		return typed.Error()
	default:
		return fmt.Sprint(value)
	}
}
