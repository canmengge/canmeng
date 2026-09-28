package services

import (
	"os"
	"strings"
	"testing"

	"pvfine/internal/pvf"
)

// TestStringTableProbe 是**只读诊断探针**：仅在设置 PVF_TESTFILE 时运行。
//
// 用途：排查"某张安全表没有可写入的 .str"这类问题——逐张打印
// 「n_string.lst 映射到的路径 / 归档内是否存在 / 是否可解析 / 能否定位写入目标」。
// 与 perf_baseline_test.go 一样按环境变量门控，平时跳过。
func TestStringTableProbe(t *testing.T) {
	path := os.Getenv("PVF_TESTFILE")
	if path == "" {
		t.Skip("PVF_TESTFILE not set; skipping string table probe")
	}
	a, err := pvf.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}

	tables := []int{1, 5, 8, 27}

	// ① n_string.lst 实际把这几张表映射到哪个路径，路径在归档内是否存在。
	if nIndex, ok := a.FindList("list/n_string.lst"); ok {
		pairs, err := a.ListPairs(nIndex)
		if err != nil {
			t.Errorf("ListPairs(n_string.lst): %v", err)
		}
		seen := map[string]bool{}
		for _, pair := range pairs {
			if !containsTableID(tables, pair.ID) || seen[pair.ID] {
				continue
			}
			seen[pair.ID] = true
			index, exists := a.Find(pair.Path)
			detail := "归档内不存在"
			if exists {
				detail = "存在(index=" + itoa(int(index)) + ")"
			}
			t.Logf("n_string.lst: 表 %s -> %q  %s", pair.ID, pair.Path, detail)
		}
		for _, table := range tables {
			if !seen[itoa(table)] {
				t.Logf("n_string.lst: 表 %d **没有**映射（可能映射在覆盖层 .lst 里）", table)
			}
		}
	} else {
		t.Error("归档内找不到 list/n_string.lst")
	}

	// ② 每张安全表：能否定位写入目标（用一个必然不存在的键，走到"追加到基准表"分支）。
	for _, table := range tables {
		const probeKey = "pvfine_probe_missing_key"
		entryIndex, ok := a.StringTableEntryIndex(table, probeKey)
		if !ok {
			t.Logf("表 %d: StringTableEntryIndex = false ⇒ **无法写入**（这就是报错来源）", table)
			continue
		}
		target := a.Path(entryIndex)
		t.Logf("表 %d: 写入目标 = %q (index=%d)", table, target, entryIndex)
		raw, err := a.RawBytes(entryIndex)
		if err != nil {
			t.Logf("        读取失败: %v", err)
			continue
		}
		text, err := a.Text(entryIndex)
		if err != nil {
			t.Logf("        解码失败: %v", err)
			continue
		}
		t.Logf("        载荷 %d 字节；前 100 字符 = %q", len(raw), firstRunes(text, 100))
	}

	// ③ 已知键的解析来源（说明界面上的名字到底从哪张表来）。
	for _, probe := range []struct {
		table int
		key   string
	}{
		{3, "name_10018"},
		{1, "name_10018"},
	} {
		if resolution, ok := a.ResolveStringTable(probe.table, probe.key); ok {
			t.Logf("ResolveStringTable(%d, %q) = %q  来源=%q 覆盖层=%v",
				probe.table, probe.key, firstRunes(resolution.Text, 40), resolution.Source, resolution.Fallback)
		} else {
			t.Logf("ResolveStringTable(%d, %q) 解析不到", probe.table, probe.key)
		}
	}

	// ④ 四张安全表的映射路径载荷：区分"空表"与"解析不了"。
	guard, guardErr := stringGuard()
	if guardErr != nil {
		t.Errorf("写保护清单加载失败: %v", guardErr)
		return
	}
	for _, table := range tables {
		tablePath := guard.CheckTableIndex(table).TablePath
		index, ok := a.Find(tablePath)
		if !ok {
			t.Logf("表 %d: %q 归档内不存在", table, tablePath)
			continue
		}
		raw, rawErr := a.RawBytes(index)
		if rawErr != nil {
			t.Logf("表 %d: 读取失败 %v", table, rawErr)
			continue
		}
		text, textErr := a.Text(index)
		if textErr != nil {
			t.Logf("表 %d: 解码失败 %v", table, textErr)
			continue
		}
		preview := firstRunes(strings.ReplaceAll(text, "\r\n", "|"), 90)
		t.Logf("表 %d: %q  %d 字节，出现 '>' %d 次，前 90 字符=%q",
			table, tablePath, len(raw), strings.Count(text, ">"), preview)
	}
}

func containsTableID(tables []int, id string) bool {
	for _, table := range tables {
		if itoa(table) == strings.TrimSpace(id) {
			return true
		}
	}
	return false
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

func firstRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
