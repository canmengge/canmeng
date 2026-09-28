package services

import (
	"context"
	"fmt"
	"os"
	"runtime"
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
