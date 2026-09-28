package services

// 打开文件的耗时追踪：用于「打开列表慢 / 点路径后卡死」的现场取证。
//
// 设计要点：
//   - 追踪本身必须极廉价（几次 Now + 一次 append），不能反过来拖慢打开；
//   - 只保留最近若干条（环形丢弃），避免长时间运行堆积；
//   - 卡死时的关键证据是「停在哪个文件、停了多久、停在哪一步」——由 inFlight 提供；
//   - 所有内容由 logging 落盘（HC\logs\pvfine-*.log）并实时推给前端「输出日志」面板。

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"pvfine/internal/logging"
)

// openTraceLimit 是保留的最近打开记录条数（超出丢最旧的）。
const openTraceLimit = 40

// openSlowThreshold 是「慢打开」阈值：超过则以 WARN 输出到面板，便于肉眼发现。
const openSlowThreshold = 800 * time.Millisecond

// OpenStage 是一次「进行中」的打开动作；卡死时据此判断停在哪一步。
type OpenStage struct {
	Index   int32
	Path    string
	Start   time.Time
	Step    string    // text / annotations / done
	Updated time.Time
}

// OpenTrace 是一次已完成的打开记录。
type OpenTrace struct {
	Index       int32
	Path        string
	Bytes       int64
	Lines       int
	StepMs      map[string]string
	TotalMs     string
	Annotations int
	Skipped     string // 标注被跳过的原因；空串表示未跳过
	Slow        bool
	FinishedAt time.Time
}

var (
	openTraceMu  sync.Mutex
	openTraces   []OpenTrace
	openInFlight = map[int32]*OpenStage{}

	// lastUITrace 缓存前端最近一次同步的操作时间线：控制台 SCRZ 走不到 JS，
	// 靠前端每次更新时主动推过来，任何诊断入口都能带上"界面停在哪一步"。
	lastUITraceMu   sync.Mutex
	lastUITraceList []string
)

// SetLastUITrace 供 main 的事件桥接调用：缓存前端操作时间线（覆盖式，保留最近一份）。
func SetLastUITrace(lines []string) {
	lastUITraceMu.Lock()
	lastUITraceList = lines
	lastUITraceMu.Unlock()
}

// LastUITraceSnapshot 返回前端最近一次同步的操作时间线（供看门狗/诊断输出）。
func LastUITraceSnapshot() []string {
	lastUITraceMu.Lock()
	defer lastUITraceMu.Unlock()
	return append([]string(nil), lastUITraceList...)
}

// traceBegin 开始一次打开追踪；同一 index 重复调用会覆盖（保留最新起点）。
func traceBegin(index int32, path string) *OpenStage {
	now := time.Now()
	stage := &OpenStage{Index: index, Path: path, Start: now, Step: "text", Updated: now}
	openTraceMu.Lock()
	openInFlight[index] = stage
	openTraceMu.Unlock()
	return stage
}

// traceStep 更新当前进度（text → annotations → done）。
func traceStep(stage *OpenStage, step string) {
	if stage == nil {
		return
	}
	openTraceMu.Lock()
	stage.Step = step
	stage.Updated = time.Now()
	openTraceMu.Unlock()
}

// traceDone 结束追踪并记录明细；超过阈值以 WARN 输出（面板可见）。
func traceDone(stage *OpenStage, trace OpenTrace) {
	now := time.Now()
	trace.FinishedAt = now
	var total time.Duration
	if stage != nil {
		total = now.Sub(stage.Start)
	} else {
		total = 0
	}
	trace.TotalMs = fmt.Sprintf("%.1fms", float64(total.Microseconds())/1000)
	trace.Slow = total >= openSlowThreshold

	openTraceMu.Lock()
	delete(openInFlight, trace.Index)
	openTraces = append(openTraces, trace)
	if len(openTraces) > openTraceLimit {
		openTraces = openTraces[len(openTraces)-openTraceLimit:]
	}
	openTraceMu.Unlock()

	if trace.Slow {
		logging.For("editor").Warn("打开慢（超过阈值）",
			"路径", trace.Path, "字节", trace.Bytes, "行数", trace.Lines,
			"标注数", trace.Annotations, "跳过原因", orDashTrace(trace.Skipped),
			"耗时", trace.TotalMs, "阶段", trace.StepMs)
		return
	}
	logging.For("editor").Debug("打开完成", "路径", trace.Path, "耗时", trace.TotalMs)
}

// DiagInput 描述一次前端发起的诊断命令（供日志辨认来源）。
type DiagInput struct {
	Cmd     string
	UITrace []string
}

// RunDiagnostics 输出诊断快照：进程状态 + 卡住的打开动作 + 最近打开明细 + goroutine 栈。
// 由前端「输出日志」面板输入 SCRZ 触发（app 事件），也可被其它诊断入口复用。
func RunDiagnostics(input DiagInput) {
	log := logging.For("diag")
	cmd := strings.TrimSpace(input.Cmd)
	if cmd == "" {
		cmd = "SCRZ"
	}
	log.Warn("===== 收到诊断命令 =====", "命令", cmd, "时间", time.Now().Format("2006-01-02 15:04:05.000"))

	lines := input.UITrace
	if len(lines) == 0 {
		// 控制台 SCRZ 拿不到 JS 侧数据：改用前端最近一次同步过来的时间线。
		lines = LastUITraceSnapshot()
	}
	if len(lines) > 0 {
		for index, line := range lines {
			log.Warn("前端时间线", "序", index+1, "记录", line)
		}
	} else {
		log.Warn("前端时间线：暂无（前端尚未产生操作记录）")
	}

	// 1) 进程状态
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	log.Warn("进程状态",
		"goroutine", runtime.NumGoroutine(),
		"堆内存MB", stats.HeapAlloc>>20,
		"堆占用MB", stats.HeapInuse>>20,
		"已归还MB", stats.HeapReleased>>20,
		"系统内存MB", stats.Sys>>20,
		"GC次数", stats.NumGC)

	// 2) 进行中的打开动作（卡死时的首要证据）
	openTraceMu.Lock()
	stuck := make([]OpenStage, 0, len(openInFlight))
	for _, stage := range openInFlight {
		stuck = append(stuck, *stage)
	}
	recent := append([]OpenTrace(nil), openTraces...)
	openTraceMu.Unlock()

	if len(stuck) == 0 {
		log.Warn("进行中的打开动作：无（后端当前空闲）")
	} else {
		for _, stage := range stuck {
			log.Warn("★进行中（疑似卡住）",
				"路径", stage.Path, "索引", stage.Index,
				"当前步骤", stage.Step,
				"已耗时", fmt.Sprintf("%.1fms", float64(time.Since(stage.Start).Microseconds())/1000),
				"本步已停留", fmt.Sprintf("%.1fms", float64(time.Since(stage.Updated).Microseconds())/1000))
		}
	}

	// 3) 最近完成的打开明细（慢的排前面）
	slows := make([]OpenTrace, 0, len(recent))
	for _, trace := range recent {
		if trace.Slow {
			slows = append(slows, trace)
		}
	}
	if len(slows) > 0 {
		for index := len(slows) - 1; index >= 0 && len(slows) > 0; index-- {
			trace := slows[index]
			log.Warn("慢打开记录", "路径", trace.Path, "字节", trace.Bytes, "行数", trace.Lines,
				"标注数", trace.Annotations, "跳过原因", orDashTrace(trace.Skipped),
				"耗时", trace.TotalMs, "阶段", trace.StepMs)
		}
	}
	if len(recent) > 0 {
		last := recent[len(recent)-1]
		log.Warn("最后一次打开", "路径", last.Path, "耗时", last.TotalMs,
			"行数", last.Lines, "标注数", last.Annotations, "阶段", last.StepMs)
	}

	// 4) goroutine 栈：卡死时能直接看出停在哪个函数
	buf := make([]byte, 1<<22)
	n := runtime.Stack(buf, true)
	log.Warn("goroutine 栈已记录", "字节", n)
	if n > 0 {
		logging.For("diag-stack").Warn("goroutine dump\n" + string(buf[:n]))
	}

	log.Warn("===== 诊断输出结束 =====", "命令", cmd)
}

func orDashTrace(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
