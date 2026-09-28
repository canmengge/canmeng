package annotations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pvfine/internal/pvf"
)

// 目录注释分片（paths/directories.json）只作用于目录节点：像
// equipment/character/** 这样的条目不该跟着每个文件行显示（文件行只保留
// 自身的 ID / 名称等标签）。文件自身的路径注释来自其它分片，保持生效。
func TestDirectoryAnnotationShardOnlyAppliesToDirectories(t *testing.T) {
	dir := t.TempDir()
	paths := filepath.Join(dir, "paths")
	if err := os.MkdirAll(paths, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(paths, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("directories.json", `{"version":1,"rules":[{"id":"dir","match":{"glob":"equipment/character/**"},"target":{"kind":"path"},"annotation":{"title":"职业装备","type":"text"}}]}`)
	write("files.json", `{"version":1,"rules":[{"id":"file","match":{"glob":"equipment/character/archer/**"},"target":{"kind":"path"},"annotation":{"title":"文件注释","type":"text"}}]}`)

	t.Setenv(EnvAnnotationDir, dir)
	engine, _, err := LoadPreferredWithSummary()
	if err != nil {
		t.Fatal(err)
	}

	titles := func(results []Result) []string {
		values := make([]string, 0, len(results))
		for _, item := range results {
			values = append(values, item.Title)
		}
		return values
	}
	has := func(values []string, want string) bool {
		for _, value := range values {
			if value == want {
				return true
			}
		}
		return false
	}

	if got := titles(engine.AnnotatePath("equipment/character/archer", true)); !has(got, "职业装备") {
		t.Fatalf("目录节点的目录注释丢失: %v", got)
	}
	fileTitles := titles(engine.AnnotatePath("equipment/character/archer/avatar/belt/11753002.equ", false))
	if has(fileTitles, "职业装备") {
		t.Fatalf("文件节点仍带上级目录注释: %v", fileTitles)
	}
	if !has(fileTitles, "文件注释") {
		t.Fatalf("文件自身的路径注释丢失: %v", fileTitles)
	}
}

// writeExternalFixture 造一个最小外置注释目录：1 条段规则 + 1 条路径规则 + 1 个引用关系。
func writeExternalFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, folder := range []string{"fields", "paths", "relations"} {
		if err := os.MkdirAll(filepath.Join(dir, folder), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile := func(relative, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, relative), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("fields/equ.json", `{
	  "version": 1,
	  "rules": [{
	    "id": "equ.section.packagable",
	    "description": "来源：测试",
	    "match": { "extensions": [".equ"] },
	    "target": { "kind": "section", "section": "packagable" },
	    "annotation": { "title": "外置标题", "type": "text", "content": "外置正文" }
	  }]
	}`)
	writeFile("paths/directories.json", `{
	  "version": 1,
	  "rules": [{
	    "id": "path.dir.equipment",
	    "match": { "glob": "equipment/**" },
	    "target": { "kind": "path" },
	    "annotation": { "title": "装备目录", "type": "text" }
	  }]
	}`)
	writeFile("relations/lists.json", `{
	  "version": 1,
	  "relations": {
	    "equipment": {
	      "kind": "list", "listPath": "equipment/equipment.lst",
	      "idToken": 0, "pathToken": 1, "recordTokens": 2, "nameSection": "name"
	    }
	  }
	}`)
	return dir
}

func TestLoadExternalDocumentsCounts(t *testing.T) {
	dir := writeExternalFixture(t)
	document, summary, err := loadExternalDocuments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(document.Rules))
	}
	if summary.PathRules != 1 || summary.Rules != 2 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(document.Relations) != 1 {
		t.Fatalf("relations = %d, want 1", len(document.Relations))
	}
}

func TestLoadWithExternalKeepsBuiltinPreviewAndOverridesTitle(t *testing.T) {
	dir := writeExternalFixture(t)
	t.Setenv(EnvAnnotationDir, dir)

	document, summary, err := LoadWithExternal()
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Found {
		t.Fatalf("外置目录未被识别: %#v", summary)
	}

	engine, err := Compile(document)
	if err != nil {
		t.Fatal(err)
	}
	// 路径注释（外置）生效：目录命中、无关文件不命中。
	if got := engine.AnnotatePath("equipment/character", true); len(got) == 0 {
		t.Fatal("目录注释未命中 equipment/character")
	}
	if got := engine.AnnotatePath("stackable/item.stk", false); len(got) != 0 {
		t.Fatalf("无关文件不该命中: %#v", got)
	}
	// 段注释（外置）生效，标题取自外置。
	results := engine.Annotate("a.equ", pvf.ParseScriptView("[packagable]\n0"), nil)
	hit := false
	for _, result := range results {
		if strings.Contains(result.Title, "外置标题") {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("段注释未生效: %#v", results)
	}
}

func TestFindExternalDirPrefersEnv(t *testing.T) {
	dir := writeExternalFixture(t)
	t.Setenv(EnvAnnotationDir, dir)
	got, ok := FindExternalDir()
	if !ok || got != dir {
		t.Fatalf("FindExternalDir() = %q, %v", got, ok)
	}
	t.Setenv(EnvAnnotationDir, filepath.Join(dir, "not-exists"))
	if _, ok := FindExternalDir(); ok {
		t.Fatal("不存在的目录不该被判定为可用")
	}
}
