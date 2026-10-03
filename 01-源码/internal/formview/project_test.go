package formview

import (
	"strings"
	"testing"

	"pvfine/internal/pvf"
)

// realIndependentDropHead 是**真实归档** etc/independent_drop.etc 的开头片段
// （2026-10-03 用 pvf-cli 读出后原样摘录，制表符与 CRLF 保持不变）：
//
//	[independent drop] 下第 1 行 = 17 个 token，第 17 位 = 1（内联列表），
//	紧随其后就是一个 [list] 块（14400 / 1000）。
const realIndependentDropHead = "[independent drop]\r\n" +
	"\t0\t20\t0\t1000000\t1000000\t1000000\t1000000\t1000000\t1\t1\t1\t1\t1\t0\t0\t-1\t1\r\n" +
	"\t[list]\r\n" +
	"\t\t14400\t1000\r\n" +
	"\t[/list]\r\n" +
	"[/independent drop]\r\n"

// TestLoadDefaultRules 保证编译进二进制的内置规则能通过解析与校验。
func TestLoadDefaultRules(t *testing.T) {
	catalog, err := LoadDefault()
	if err != nil {
		t.Fatalf("内置规则校验失败: %v", err)
	}
	if len(catalog.Formats) == 0 {
		t.Fatal("内置规则里没有任何文件族")
	}
	if _, ok := catalog.Lookup("independent_drop"); !ok {
		t.Fatal("内置规则里找不到 independent_drop")
	}
}

// TestLookupFileByArchivePath 保证文件族能按归档路径命中（大小写与反斜杠不敏感）。
func TestLookupFileByArchivePath(t *testing.T) {
	catalog, err := LoadDefault()
	if err != nil {
		t.Fatalf("内置规则校验失败: %v", err)
	}
	for _, candidate := range []string{
		"etc/independent_drop.etc",
		"etc\\independent_drop.etc",
		"ETC/Independent_Drop.ETC",
		"/etc/independent_drop.etc",
	} {
		format, ok := catalog.LookupFile(candidate)
		if !ok {
			t.Errorf("路径 %q 未命中任何文件族", candidate)
			continue
		}
		if format.ID != "independent_drop" {
			t.Errorf("路径 %q 命中了 %q，期望 independent_drop", candidate, format.ID)
		}
	}
	if _, ok := catalog.LookupFile("etc/independentdrop.lst"); ok {
		t.Error("etc/independentdrop.lst 不应命中 independent_drop 规则")
	}
}

// TestProjectRealIndependentDropHead 是**真实数据**端到端：内核词法投影 + 规则切表。
func TestProjectRealIndependentDropHead(t *testing.T) {
	catalog, err := LoadDefault()
	if err != nil {
		t.Fatalf("内置规则校验失败: %v", err)
	}
	format, ok := catalog.Lookup("independent_drop")
	if !ok {
		t.Fatal("内置规则里找不到 independent_drop")
	}

	view := pvf.ParseScriptView(realIndependentDropHead)
	projection := Project("etc/independent_drop.etc", format, view)

	if projection.TokenCount != 19 {
		t.Fatalf("token 总数 = %d，期望 19（17 + 2）", projection.TokenCount)
	}

	var drop *ProjectedSection
	for i := range projection.Sections {
		if strings.EqualFold(projection.Sections[i].Section, "independent drop") {
			drop = &projection.Sections[i]
		}
	}
	if drop == nil {
		t.Fatal("没有投影出 [independent drop] 段")
	}
	// [list] 已被行关联认领、从输出里移除，改为按需取（与界面双击同一路径）。
	list := ProjectSection(format, view, "list", 1)
	if list == nil {
		t.Fatal("ProjectSection 取不到 [list] 第 1 次出现")
	}

	// 独立掉落：1 行、17 格、完整。
	if len(drop.Rows) != 1 {
		t.Fatalf("独立掉落有 %d 行，期望 1 行", len(drop.Rows))
	}
	if !drop.Rows[0].Complete {
		t.Error("独立掉落第 1 行应为完整行（17 格）")
	}
	if len(drop.Rows[0].Cells) != 17 {
		t.Fatalf("独立掉落第 1 行有 %d 格，期望 17 格", len(drop.Rows[0].Cells))
	}
	if len(drop.Columns) != 17 {
		t.Fatalf("表头有 %d 列，期望 17 列", len(drop.Columns))
	}

	wantValues := []string{
		"0", "20", "0",
		"1000000", "1000000", "1000000", "1000000", "1000000",
		"1", "1", "1", "1", "1",
		"0", "0", "-1", "1",
	}
	for i, want := range wantValues {
		if got := drop.Rows[0].Cells[i].Value; got != want {
			t.Errorf("第 %d 格 = %q，期望 %q", i+1, got, want)
		}
	}

	// 可读文本：枚举翻译与百分比换算。
	cases := []struct {
		index   int
		display string
		reason  string
	}{
		{0, "怪物", "类型按 enum 翻译"},
		{3, "100%", "掉落率按 scale=1000000 换算成百分比"},
		{15, "（不限）", "职业限制 -1 显示为不限"},
		{16, "内联列表", "掉落方式按 enum 翻译"},
	}
	for _, item := range cases {
		if got := drop.Rows[0].Cells[item.index].Display; got != item.display {
			t.Errorf("第 %d 格 display = %q，期望 %q（%s）", item.index+1, got, item.display, item.reason)
		}
	}
	// 数值格不该被翻译成文本。
	if got := drop.Rows[0].Cells[1].Display; got != "" {
		t.Errorf("怪物 ID 列不应有翻译文本，实际 = %q", got)
	}

	// [list]：1 行 2 格。
	if len(list.Rows) != 1 || len(list.Rows[0].Cells) != 2 {
		t.Fatalf("[list] 期望 1 行 2 格，实际 %d 行", len(list.Rows))
	}
	if list.Rows[0].Cells[0].Value != "14400" || list.Rows[0].Cells[1].Value != "1000" {
		t.Errorf("[list] 第 1 行 = %q / %q，期望 14400 / 1000",
			list.Rows[0].Cells[0].Value, list.Rows[0].Cells[1].Value)
	}

	if len(projection.Warnings) != 0 {
		t.Errorf("本片段不含未定义段，不该有告警，实际: %v", projection.Warnings)
	}
}

// TestProjectReportsUnknownSections 保证规则没覆盖的段会被明确报出来（而不是静默丢弃）。
func TestProjectReportsUnknownSections(t *testing.T) {
	catalog, err := LoadDefault()
	if err != nil {
		t.Fatalf("内置规则校验失败: %v", err)
	}
	format, _ := catalog.Lookup("independent_drop")

	text := "[independent drop]\r\n" +
		"\t0\t20\t0\t1000000\t1000000\t1000000\t1000000\t1000000\t1\t1\t1\t1\t1\t0\t0\t-1\t0\r\n" +
		"\t[dungeon condition]\r\n" +
		"\t\t1\t2\t3\r\n" +
		"[/independent drop]\r\n"
	projection := Project("etc/independent_drop.etc", format, pvf.ParseScriptView(text))

	found := false
	for _, warning := range projection.Warnings {
		if strings.Contains(warning, "dungeon condition") {
			found = true
		}
	}
	if !found {
		t.Fatalf("未定义段 dungeon condition 应当被告警，实际告警: %v", projection.Warnings)
	}
}

// TestValidateRejectsBadRules 保证规则写错时能在加载期就被拦下。
func TestValidateRejectsBadRules(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{
			name: "列数与 rowTokens 不等",
			data: `{"version":1,"formats":[{"id":"x","label":"X","files":["a.etc"],
			       "sections":[{"section":"s","label":"S","rowTokens":2,
			       "columns":[{"label":"A"}]}]}]}`,
			want: "columns 长度",
		},
		{
			name: "enum 缺 values",
			data: `{"version":1,"formats":[{"id":"x","label":"X","files":["a.etc"],
			       "sections":[{"section":"s","label":"S","rowTokens":1,
			       "columns":[{"label":"A","type":"enum"}]}]}]}`,
			want: "values",
		},
		{
			name: "未知字段被拒",
			data: `{"version":1,"unknownKey":1,"formats":[]}`,
			want: "unknownKey",
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			_, err := Parse([]byte(item.data))
			if err == nil {
				t.Fatal("期望校验失败，实际通过")
			}
			if !strings.Contains(err.Error(), item.want) {
				t.Errorf("错误信息应包含 %q，实际: %v", item.want, err)
			}
		})
	}
}

// linkedIndependentDrop 是三行 + 两个 [list] 的片段：第 1 行内联、第 2 行单一物品、
// 第 3 行内联。用来验证关联**按文本偏移配对**（第 3 行必须配到第 2 个 list，而不是
// 被第 2 行"占位"后错位）。
const linkedIndependentDrop = "[independent drop]\r\n" +
	"\t0\t20\t0\t1000000\t1000000\t1000000\t1000000\t1000000\t1\t1\t1\t1\t1\t0\t0\t-1\t1\r\n" +
	"\t[list]\r\n" +
	"\t\t14400\t1000\r\n" +
	"\t[/list]\r\n" +
	"\t0\t21\t3015\t1000000\t1000000\t1000000\t1000000\t1000000\t1\t1\t1\t1\t1\t0\t0\t-1\t0\r\n" +
	"\t0\t22\t0\t1000000\t1000000\t1000000\t1000000\t1000000\t1\t1\t1\t1\t1\t0\t0\t-1\t1\r\n" +
	"\t[list]\r\n" +
	"\t\t25500\t500\r\n" +
	"\t[/list]\r\n" +
	"[/independent drop]\r\n"

// TestProjectLinksInlineList 用真实归档片段验证「掉落方式 = 内联列表」的行会关联到
// 紧跟其后的 [list]。
func TestProjectLinksInlineList(t *testing.T) {
	format := mustFormat(t, "independent_drop")
	view := pvf.ParseScriptView(realIndependentDropHead)
	projection := Project("etc/independent_drop.etc", format, view)

	drop := mustSection(t, projection, "independent drop")
	if len(drop.Rows) != 1 {
		t.Fatalf("独立掉落有 %d 行，期望 1 行", len(drop.Rows))
	}
	link := drop.Rows[0].Link
	if link == nil {
		t.Fatal("掉落方式 = 1（内联列表）的行应当关联到 [list]，实际没有 link")
	}
	if link.TargetSection != "list" || link.Occurrence != 1 {
		t.Errorf("link 指向 %q #%d，期望 list #1", link.TargetSection, link.Occurrence)
	}
	if link.Column != 16 {
		t.Errorf("link.Column = %d，期望 16（掉落方式列）", link.Column)
	}
	// 交互落在「掉落物品」列（规则里的 displayColumn=2），触发列只当标签 ——
	// 内联列表时那一列原本显示的是无意义的 0（被解析成「金币 0」）。
	if link.DisplayColumn != 2 {
		t.Errorf("link.DisplayColumn = %d，期望 2（掉落物品列）", link.DisplayColumn)
	}
	if link.Title != "掉落候选" {
		t.Errorf("link.Title = %q，期望 %q", link.Title, "掉落候选")
	}

	// 被认领的目标段要从输出里移除（体积），改由 ProjectSection 按需取。
	if projection.LinkedTargets != 1 {
		t.Errorf("LinkedTargets = %d，期望 1", projection.LinkedTargets)
	}
	assertNoSection(t, projection, "list")

	list := ProjectSection(format, view, "list", 1)
	if list == nil {
		t.Fatal("ProjectSection 取不到 [list] 第 1 次出现")
	}
	if len(list.Rows) != 1 || len(list.Rows[0].Cells) != 2 {
		t.Fatalf("[list] 期望 1 行 2 格，实际 %d 行", len(list.Rows))
	}
	if list.Rows[0].Cells[0].Value != "14400" || list.Rows[0].Cells[1].Value != "1000" {
		t.Errorf("[list] 第 1 行 = %q / %q，期望 14400 / 1000",
			list.Rows[0].Cells[0].Value, list.Rows[0].Cells[1].Value)
	}
	// 超范围的出现序号返回 nil，不 panic。
	if ProjectSection(format, view, "list", 99) != nil {
		t.Error("ProjectSection 取不存在的出现序号应当返回 nil")
	}
}

// TestProjectLinksPairsByOffset 验证关联按**文本偏移**配对：中间那行是「单一物品」，
// 不该抢占列表，第 3 行要配到第 2 个 [list]。
func TestProjectLinksPairsByOffset(t *testing.T) {
	format := mustFormat(t, "independent_drop")
	view := pvf.ParseScriptView(linkedIndependentDrop)
	projection := Project("etc/independent_drop.etc", format, view)

	drop := mustSection(t, projection, "independent drop")
	if len(drop.Rows) != 3 {
		t.Fatalf("独立掉落有 %d 行，期望 3 行", len(drop.Rows))
	}

	if row := drop.Rows[0]; row.Link == nil || row.Link.Occurrence != 1 {
		t.Errorf("第 1 行（内联）应关联 list #1，实际 %+v", row.Link)
	}
	if row := drop.Rows[1]; row.Link != nil {
		t.Errorf("第 2 行（单一物品）不该有 link，实际 %+v", row.Link)
	}
	if row := drop.Rows[2]; row.Link == nil || row.Link.Occurrence != 2 {
		t.Errorf("第 3 行（内联）应关联 list #2，实际 %+v", row.Link)
	}

	// 两个 [list] 都被认领 → 都从输出里移除，但按序号仍能按需取到各自内容。
	if projection.LinkedTargets != 2 {
		t.Errorf("LinkedTargets = %d，期望 2", projection.LinkedTargets)
	}
	assertNoSection(t, projection, "list")

	first := ProjectSection(format, view, "list", 1)
	second := ProjectSection(format, view, "list", 2)
	if first == nil || len(first.Rows) == 0 || first.Rows[0].Cells[0].Value != "14400" {
		t.Errorf("list #1 期望首格 14400，实际 %+v", first)
	}
	if second == nil || len(second.Rows) == 0 || second.Rows[0].Cells[0].Value != "25500" {
		t.Errorf("list #2 期望首格 25500，实际 %+v", second)
	}
	if len(projection.Warnings) != 0 {
		t.Errorf("本片段不含未定义段，不该有告警，实际: %v", projection.Warnings)
	}
}

// TestProjectLinkAbsentWhenSingleItem 保证「单一物品」的行不会凭空生成 link。
func TestProjectLinkAbsentWhenSingleItem(t *testing.T) {
	format := mustFormat(t, "independent_drop")
	text := "[independent drop]\r\n" +
		"\t0\t20\t3015\t1000000\t1000000\t1000000\t1000000\t1000000\t1\t1\t1\t1\t1\t0\t0\t-1\t0\r\n" +
		"[/independent drop]\r\n"
	projection := Project("etc/independent_drop.etc", format, pvf.ParseScriptView(text))

	// 本片段本来就没有 [list]，所以不能用 findSections（它要求 list 存在）。
	var drop *ProjectedSection
	for i := range projection.Sections {
		if strings.EqualFold(projection.Sections[i].Section, "independent drop") {
			drop = &projection.Sections[i]
		}
	}
	if drop == nil {
		t.Fatal("没有投影出 [independent drop] 段")
	}
	if len(drop.Rows) != 1 {
		t.Fatalf("独立掉落有 %d 行，期望 1 行", len(drop.Rows))
	}
	if drop.Rows[0].Link != nil {
		t.Errorf("掉落方式 = 0（单一物品）不该有 link，实际 %+v", drop.Rows[0].Link)
	}
}

// TestValidateRejectsBadLinks 保证 links 写错时在加载期就被拦下。
func TestValidateRejectsBadLinks(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{
			name: "link 列下标越界",
			data: `{"version":1,"formats":[{"id":"x","label":"X","files":["a.etc"],"sections":[
			       {"section":"s","label":"S","rowTokens":1,"columns":[{"label":"A"}],
			        "links":[{"column":3,"when":["1"],"targetSection":"t"}]},
			       {"section":"t","label":"T","rowTokens":1,"columns":[{"label":"B"}]}]}]}`,
			want: "超出列范围",
		},
		{
			name: "link 目标段未定义",
			data: `{"version":1,"formats":[{"id":"x","label":"X","files":["a.etc"],"sections":[
			       {"section":"s","label":"S","rowTokens":1,"columns":[{"label":"A"}],
			        "links":[{"column":0,"when":["1"],"targetSection":"nope"}]}]}]}`,
			want: "指向未定义的段",
		},
		{
			name: "link when 为空",
			data: `{"version":1,"formats":[{"id":"x","label":"X","files":["a.etc"],"sections":[
			       {"section":"s","label":"S","rowTokens":1,"columns":[{"label":"A"}],
			        "links":[{"column":0,"when":[],"targetSection":"t"}]},
			       {"section":"t","label":"T","rowTokens":1,"columns":[{"label":"B"}]}]}]}`,
			want: ".when 不能为空",
		},
		{
			name: "link 指向本段自身",
			data: `{"version":1,"formats":[{"id":"x","label":"X","files":["a.etc"],"sections":[
			       {"section":"s","label":"S","rowTokens":1,"columns":[{"label":"A"}],
			        "links":[{"column":0,"when":["1"],"targetSection":"s"}]}]}]}`,
			want: "不能指向本段自身",
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			_, err := Parse([]byte(item.data))
			if err == nil {
				t.Fatal("期望校验失败，实际通过")
			}
			if !strings.Contains(err.Error(), item.want) {
				t.Errorf("错误信息应包含 %q，实际: %v", item.want, err)
			}
		})
	}
}

// TestProjectHonoursMaxOccurrences 保证规则里的 maxOccurrences 能放宽段出现上限。
//
// 回归背景（2026-10-03 实测）：etc/independent_drop.etc 的 [list] 出现 **862 次**，
// 而默认上限 50 会让 854 个「内联列表」行里只有 50 个能双击查看 —— 那正是用户报的
// "内联列表打不开"的根因。
func TestProjectHonoursMaxOccurrences(t *testing.T) {
	rule := `{"version":1,"formats":[{"id":"x","label":"X","files":["a.etc"],"sections":[
	          {"section":"s","label":"S","rowTokens":1,"columns":[{"label":"A"}],"maxOccurrences":2}]}]}`
	catalog, err := Parse([]byte(rule))
	if err != nil {
		t.Fatalf("规则解析失败: %v", err)
	}
	format, _ := catalog.Lookup("x")

	text := "[s]\r\n\t1\r\n\t[/s]\r\n\t[s]\r\n\t2\r\n\t[/s]\r\n\t[s]\r\n\t3\r\n\t[/s]\r\n"
	projection := Project("a.etc", format, pvf.ParseScriptView(text))

	if len(projection.Sections) != 2 {
		t.Fatalf("maxOccurrences=2 时应当只投影 2 次出现，实际 %d", len(projection.Sections))
	}
	found := false
	for _, warning := range projection.Warnings {
		if strings.Contains(warning, "只投影前 2 次") {
			found = true
		}
	}
	if !found {
		t.Errorf("应当告警「只投影前 2 次」，实际: %v", projection.Warnings)
	}
}

// TestValidateRejectsNegativeMaxOccurrences 保证上限写负数被拦下。
func TestValidateRejectsNegativeMaxOccurrences(t *testing.T) {
	data := `{"version":1,"formats":[{"id":"x","label":"X","files":["a.etc"],"sections":[
	         {"section":"s","label":"S","rowTokens":1,"columns":[{"label":"A"}],"maxOccurrences":-1}]}]}`
	if _, err := Parse([]byte(data)); err == nil {
		t.Fatal("maxOccurrences 为负应当校验失败")
	} else if !strings.Contains(err.Error(), "maxOccurrences") {
		t.Errorf("错误信息应提到 maxOccurrences，实际: %v", err)
	}
}

// mustFormat 取出内置规则里的某个文件族。
func mustFormat(t *testing.T, id string) Format {
	t.Helper()
	catalog, err := LoadDefault()
	if err != nil {
		t.Fatalf("内置规则校验失败: %v", err)
	}
	format, ok := catalog.Lookup(id)
	if !ok {
		t.Fatalf("内置规则里找不到 %s", id)
	}
	return format
}

// mustSection 取出某个段第一次出现（不存在直接失败）。
func mustSection(t *testing.T, projection *Projection, name string) *ProjectedSection {
	t.Helper()
	for i := range projection.Sections {
		if strings.EqualFold(projection.Sections[i].Section, name) {
			return &projection.Sections[i]
		}
	}
	t.Fatalf("没有投影出 [%s] 段", name)
	return nil
}

// assertNoSection 断言某个段没有出现在输出里（被认领的目标段应当被移除）。
func assertNoSection(t *testing.T, projection *Projection, name string) {
	t.Helper()
	for i := range projection.Sections {
		if strings.EqualFold(projection.Sections[i].Section, name) {
			t.Errorf("段 [%s] #%d 不该留在输出里（应已随关联移除）",
				name, projection.Sections[i].Occurrence)
		}
	}
}
