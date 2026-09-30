package services

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strings"
	"unsafe"
	"strconv"
	"testing"
	"time"

	annotationrules "pvfine/internal/annotations"
	"pvfine/internal/pvf"
)

// TestPerfBaseline 用同一条路径测量「打开归档」与「建索引」的耗时与内存，
// 供原版源码与优化版源码做同条件对比。
//
// 用法：
//   set PVF_BENCH_FILE=<Script.pvf 绝对路径>
//   go test -run TestPerfBaseline -v -count=1 ./services/
//
// pvf.Open 会在 PVF 同目录查找 sk.dat，因此被测 PVF 所在目录必须有 sk.dat。
func TestPerfBaseline(t *testing.T) {
	path := os.Getenv("PVF_BENCH_FILE")
	if path == "" {
		t.Skip("PVF_BENCH_FILE 未设置")
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("PVF 不存在: %v", err)
	}
	t.Logf("被测文件: %s (%.1f MB)", path, float64(st.Size())/(1<<20))

	tOpen := time.Now()
	a, err := pvf.Open(path)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	openDur := time.Since(tOpen)

	tIndex := time.Now()
	dirs, paths, err := buildIndex(a)
	if err != nil {
		t.Fatalf("建索引失败: %v", err)
	}
	indexDur := time.Since(tIndex)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	t.Logf("文件数=%d  目录节点=%d  路径条目=%d", a.FileCount(), len(dirs), len(paths))
	t.Logf("[t1] pvf.Open（读盘+解密+解析+路径索引） = %v", openDur)
	t.Logf("[t2] buildIndex（建目录树+排序）          = %v", indexDur)
	t.Logf("[内存] HeapAlloc=%.1f MB  TotalAlloc=%.1f MB  Sys=%.1f MB",
		float64(m.HeapAlloc)/(1<<20), float64(m.TotalAlloc)/(1<<20), float64(m.Sys)/(1<<20))
	t.Logf("[合计] Open+buildIndex = %v", openDur+indexDur)

	// 可选：导出堆剖析，用来回答"内存到底被谁占了"（P2 立项依据，不参与日常测试）。
	// 用法：set PVF_BENCH_HEAP_PROFILE=<输出路径>
	if profilePath := os.Getenv("PVF_BENCH_HEAP_PROFILE"); profilePath != "" {
		f, err := os.Create(profilePath)
		if err != nil {
			t.Fatalf("创建堆剖析文件失败: %v", err)
		}
		if err := pprof.WriteHeapProfile(f); err != nil {
			f.Close()
			t.Fatalf("写堆剖析失败: %v", err)
		}
		f.Close()
		t.Logf("[堆剖析] 已写出: %s", profilePath)
	}
}

// TestPerfMemoryReclaim 回答 P2 的核心问题：打开 + 建索引之后，
// "能还给系统但还没还"的内存到底有多少。
//
// 做法与产品里的 releaseArchiveMemory 完全一致（丢读缓存 → runtime.GC → FreeOSMemory），
// 只是把前后数字都打出来，用于判断还有没有优化空间。
//
// 用法：
//   set PVF_BENCH_FILE=<Script.pvf>
//   go test -run TestPerfMemoryReclaim -v -count=1 -timeout 20m ./services/
func TestPerfMemoryReclaim(t *testing.T) {
	path := os.Getenv("PVF_BENCH_FILE")
	if path == "" {
		t.Skip("PVF_BENCH_FILE 未设置")
	}
	a, err := pvf.Open(path)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	// 归档在最后一次使用之后就可被 GC 回收；这里要测的是"丢缓存 + 归还高水位"，
	// 不是"整个归档被丢掉"——必须保活，否则量到的数字毫无意义。
	defer runtime.KeepAlive(a)
	dirs, paths, err := buildIndex(a)
	if err != nil {
		t.Fatalf("建索引失败: %v", err)
	}
	defer runtime.KeepAlive(dirs)
	defer runtime.KeepAlive(paths)

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	a.ReleaseReadCaches()
	runtime.GC()
	debug.FreeOSMemory()
	runtime.ReadMemStats(&after)

	t.Logf("[归还前] HeapAlloc=%.1f MB  HeapInuse=%.1f MB  HeapReleased=%.1f MB  Sys=%.1f MB",
		mb(before.HeapAlloc), mb(before.HeapInuse), mb(before.HeapReleased), mb(before.Sys))
	t.Logf("[归还后] HeapAlloc=%.1f MB  HeapInuse=%.1f MB  HeapReleased=%.1f MB  Sys=%.1f MB",
		mb(after.HeapAlloc), mb(after.HeapInuse), mb(after.HeapReleased), mb(after.Sys))
	t.Logf("[结论] 真实存活=%.1f MB  本步多还回系统=%.1f MB  堆占用下降=%.1f MB",
		mb(after.HeapAlloc),
		mb(uint64(int64(after.HeapReleased)-int64(before.HeapReleased))),
		mb(uint64(int64(before.HeapInuse)-int64(after.HeapInuse))))
}

func mb(bytes uint64) float64 { return float64(bytes) / (1 << 20) }

// unsafeSizeOfPathEntry 只用于估算条目结构本身的体积（pathEntry 含 2 个字符串头
// 与 4 个 int32/string，按 64 位对齐粗算），不参与任何产品逻辑。
func unsafeSizeOfPathEntry() int { return int(unsafe.Sizeof(pathEntry{})) }

// TestPerfIndexSplit 把「构建目录索引」这一步拆开，用于判断 P1（启动耗时）
// 该从哪儿下刀：纯遍历 Path(i) 多少、加小写多少、完整 buildIndex 多少。
//
// 用法：
//   set PVF_BENCH_FILE=<Script.pvf>
//   go test -run TestPerfIndexSplit -v -count=1 -timeout 20m ./services/
func TestPerfIndexSplit(t *testing.T) {
	path := os.Getenv("PVF_BENCH_FILE")
	if path == "" {
		t.Skip("PVF_BENCH_FILE 未设置")
	}
	a, err := pvf.Open(path)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	defer runtime.KeepAlive(a)
	n := a.FileCount()

	// ① 只遍历 Path(i)
	start := time.Now()
	var sink int
	for i := int32(0); i < n; i++ {
		sink += len(a.Path(i))
	}
	walkCost := time.Since(start)

	// ② 遍历 + 小写副本（等价于现在的 lower 字段）
	start = time.Now()
	for i := int32(0); i < n; i++ {
		sink += len(strings.ToLower(a.Path(i)))
	}
	lowerCost := time.Since(start)

	// ③ 完整 buildIndex
	start = time.Now()
	dirs, paths, err := buildIndex(a)
	if err != nil {
		t.Fatalf("建索引失败: %v", err)
	}
	fullCost := time.Since(start)
	runtime.KeepAlive(dirs)
	runtime.KeepAlive(paths)

	t.Logf("只遍历 Path(i)      = %v", walkCost)
	t.Logf("遍历 + 小写副本     = %v（小写增量 %v）", lowerCost, lowerCost-walkCost)
	t.Logf("完整 buildIndex     = %v（其余增量 %v：目录树 + 排序）",
		fullCost, fullCost-lowerCost)
	t.Logf("（参考）条目数=%d  校验和=%d", n, sink)
}

// TestPerfIndexFootprint 量化 P2-b 的可行性：services 层这份索引（438 万条路径）
// 到底占多少内存、以及"不物化路径、按需现算"要付多少时间代价。
//
// 用法：
//   set PVF_BENCH_FILE=<Script.pvf>
//   go test -run TestPerfIndexFootprint -v -count=1 -timeout 20m ./services/
func TestPerfIndexFootprint(t *testing.T) {
	path := os.Getenv("PVF_BENCH_FILE")
	if path == "" {
		t.Skip("PVF_BENCH_FILE 未设置")
	}
	a, err := pvf.Open(path)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	defer runtime.KeepAlive(a)
	dirs, paths, err := buildIndex(a)
	if err != nil {
		t.Fatalf("建索引失败: %v", err)
	}

	var pathBytes, lowerBytes int64
	for _, entry := range paths {
		pathBytes += int64(len(entry.path))
		lowerBytes += int64(len(entry.lower))
	}
	t.Logf("索引规模: 路径条目=%d  目录节点=%d  路径字符串=%.1f MB  小写副本=%.1f MB  条目结构≈%.1f MB",
		len(paths), len(dirs), float64(pathBytes)/(1<<20), float64(lowerBytes)/(1<<20),
		float64(len(paths))*float64(unsafeSizeOfPathEntry())/(1<<20))

	// ① 按需现算的代价：随机取 2000 个条目调 Path(i)
	sampled := 0
	var pathCost time.Duration
	for i := 0; i < len(paths) && sampled < 2000; i += len(paths)/2000 {
		start := time.Now()
		_ = a.Path(int32(i))
		pathCost += time.Since(start)
		sampled++
	}
	if sampled > 0 {
		t.Logf("Path(i) 按需现算: 采样 %d 次，平均 %.3f ms/次（全量 %d 次约 %.1f s）",
			sampled, float64(pathCost.Milliseconds())/float64(sampled),
			len(paths), float64(pathCost.Milliseconds())/float64(sampled)*float64(len(paths))/1000)
	}

	// ② 这份索引占多少内存：丢掉引用后回收了多少
	runtime.GC()
	debug.FreeOSMemory()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	dirs = nil
	paths = nil
	runtime.GC()
	debug.FreeOSMemory()
	runtime.ReadMemStats(&after)
	t.Logf("丢掉索引后: HeapAlloc %.1f MB → %.1f MB（索引约占 %.1f MB）",
		mb(before.HeapAlloc), mb(after.HeapAlloc),
		mb(uint64(int64(before.HeapAlloc)-int64(after.HeapAlloc))))
}

// TestPerfSearchIndex 测量「语义搜索索引」构建耗时（旧版会生成 4.4 GB 级索引，
// 是 GUI 里"打开后很久不能用"的疑似主因）。用 Done/Total 采样进度，超过预算
// 提前结束并按已完成的速率外推总耗时，避免测试干等。
//
// 用法：
//   set PVF_BENCH_FILE=<Script.pvf>
//   set PVF_BENCH_SEARCH_BUDGET=180     # 可选，预算秒数，默认 180
//   go test -run TestPerfSearchIndex -v -count=1 -timeout 30m ./services/
//
// 注意：构建完成会写缓存到 os.UserCacheDir()\pvfine\search-index，
// 两组对比之间需要手动删除该目录。
func TestPerfSearchIndex(t *testing.T) {
	path := os.Getenv("PVF_BENCH_FILE")
	if path == "" {
		t.Skip("PVF_BENCH_FILE 未设置")
	}
	budget := 180.0
	if v := os.Getenv("PVF_BENCH_SEARCH_BUDGET"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil && parsed > 0 {
			budget = parsed
		}
	}

	a, err := pvf.Open(path)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	dirs, paths, err := buildIndex(a)
	if err != nil {
		t.Fatalf("建索引失败: %v", err)
	}

	// 可索引列表来自标注引擎的 relations（装备/道具/技能…），没有它就什么都
	// 索引不到（Total=0），因此这里必须加载。
	engine, err := annotationrules.LoadDefault()
	if err != nil {
		t.Fatalf("加载标注引擎失败: %v", err)
	}
	c := &core{archive: a, dirChildren: dirs, sortedPaths: paths, indexGen: 1, annotationEngine: engine}
	c.indexStatus = IndexStatus{State: IndexStateBuilding}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		// buildSearchIndex 会 emitEvent，在测试环境（无 wails application）可能 panic，
		// 这里兜住，避免测试直接失败。
		defer func() { _ = recover() }()
		c.buildSearchIndex(ctx, 1, a, start, false, false)
	}()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			c.mu.RLock()
			st := c.indexStatus
			c.mu.RUnlock()
			t.Logf("[搜索索引] 完成  用时=%v  Done=%d  Total=%d  Skipped=%d  BuildDurationMs=%.0f",
				time.Since(start), st.Done, st.Total, st.Skipped, st.BuildDurationMs)
			return
		case <-ticker.C:
			c.mu.RLock()
			st := c.indexStatus
			c.mu.RUnlock()
			el := time.Since(start).Seconds()
			rate := 0.0
			if el > 0 && st.Done > 0 {
				rate = float64(st.Done) / el
			}
			eta := ""
			if rate > 0 && st.Total > st.Done {
				eta = fmt.Sprintf("  预计还需 %.0fs（外推总计约 %.0fs）", float64(st.Total-st.Done)/rate, float64(st.Total)/rate)
			}
			t.Logf("[搜索索引] %.0fs  进度 %d/%d  %.0f 条/秒%s", el, st.Done, st.Total, rate, eta)
			if el > budget {
				cancel()
				t.Logf("[搜索索引] 超出预算 %.0fs，提前结束（已完成 %d/%d）", budget, st.Done, st.Total)
				return
			}
		}
	}
}
