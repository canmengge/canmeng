package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
)

// list 查重的共享用例（前端与后端必须给出一致结果）。
// 用例文件同时被 frontend/src/listDuplicate.ts 一侧的校验脚本读取：
//
//	node 01-源码/scripts/check-listdup.mjs
//
// 任一侧改了规则而没同步，这里或那边就会失败 —— 这就是防漂移的契约。
type listDuplicateFixture struct {
	Cases []struct {
		Name    string `json:"name"`
		Text    string `json:"text"`
		Entries int    `json:"entries"`
		Issues  []struct {
			Kind  string  `json:"kind"`
			Lines []int32 `json:"lines"`
		} `json:"issues"`
	} `json:"cases"`
}

func TestListDuplicateFixture(t *testing.T) {
	path := filepath.Join("testdata", "listdup_cases.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取用例失败: %v", err)
	}
	var fixture listDuplicateFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("解析用例失败: %v", err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("用例为空")
	}

	for _, item := range fixture.Cases {
		report := scanListDuplicates(0, item.Text)
		if report.Entries != item.Entries {
			t.Errorf("[%s] 条目数 = %d, 期望 %d", item.Name, report.Entries, item.Entries)
		}
		got := summarizeIssues(report)
		want := make([]string, 0, len(item.Issues))
		for _, issue := range item.Issues {
			want = append(want, issueKey(issue.Kind, issue.Lines))
		}
		sort.Strings(want)
		if len(got) != len(want) {
			t.Errorf("[%s] 问题条目 = %v, 期望 %v", item.Name, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("[%s] 问题条目 = %v, 期望 %v", item.Name, got, want)
				break
			}
		}
	}
}

// summarizeIssues 把结果压成「kind:行号,行号」的可比较字符串（排序后）。
func summarizeIssues(report ListDuplicateReport) []string {
	out := make([]string, 0, len(report.Issues))
	for _, issue := range report.Issues {
		lines := make([]int32, 0, len(issue.Entries))
		for _, entry := range issue.Entries {
			lines = append(lines, entry.Line)
		}
		out = append(out, issueKey(issue.Kind, lines))
	}
	sort.Strings(out)
	return out
}

func issueKey(kind string, lines []int32) string {
	sorted := append([]int32(nil), lines...)
	sort.Slice(sorted, func(x, y int) bool { return sorted[x] < sorted[y] })
	key := kind + ":"
	for i, line := range sorted {
		if i > 0 {
			key += ","
		}
		key += strconv.Itoa(int(line))
	}
	return key
}
