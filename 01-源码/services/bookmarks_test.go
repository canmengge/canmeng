package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBookmarkServiceLoadsBuiltinListGroup(t *testing.T) {
	service := newBookmarkService(filepath.Join(t.TempDir(), "bookmarks.json"))
	document, err := service.LoadBookmarks()
	if err != nil {
		t.Fatal(err)
	}
	if document.ActiveBookID != builtinBookmarkBookID || len(document.Books) != 1 {
		t.Fatalf("document = %#v", document)
	}
	builtin := document.Books[0]
	// 内置书签内容随版本维护演进（当前已含用户整理的 759 条分组数据），
	// 测试只锁结构语义：builtin 标记 + 不可编辑 + 至少一个分组 + 每条都有路径，
	// 不再锁定具体条目清单（原 15 条 .lst 断言已过期）。
	if !builtin.Builtin || builtin.Editable || len(builtin.Groups) == 0 {
		t.Fatalf("builtin = %#v", builtin)
	}
	for _, entry := range flattenTestBookmarkEntries(builtin.Groups) {
		if entry.Path == "" {
			t.Fatalf("builtin entry without path: %#v", entry)
		}
	}
}

func flattenTestBookmarkEntries(groups []BookmarkGroup) []BookmarkEntry {
	var out []BookmarkEntry
	var walk func(groups []BookmarkGroup)
	walk = func(groups []BookmarkGroup) {
		for _, group := range groups {
			out = append(out, group.Entries...)
			walk(group.Groups)
		}
	}
	walk(groups)
	return out
}

func TestBookmarkServiceRoundTripNestedGroups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "bookmarks.json")
	service := newBookmarkService(path)
	document, err := service.LoadBookmarks()
	if err != nil {
		t.Fatal(err)
	}
	document.Books = append(document.Books, BookmarkBook{
		ID:       "bookmark-1",
		Name:     "测试书签",
		Editable: true,
		Groups: []BookmarkGroup{{
			ID:   "group-1",
			Name: "一级",
			Groups: []BookmarkGroup{{
				ID:      "group-2",
				Name:    "二级",
				Entries: []BookmarkEntry{{Path: `dir\\item.equ`, Name: "自定义名称"}},
			}},
		}},
		Entries: []BookmarkEntry{{Path: "root.txt"}},
	})
	document.ActiveBookID = "bookmark-1"
	if err := service.SaveBookmarks(document); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.LoadBookmarks()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ActiveBookID != "bookmark-1" || len(loaded.Books) != 2 {
		t.Fatalf("loaded = %#v", loaded)
	}
	custom := loaded.Books[1]
	if custom.Name != "测试书签" || len(custom.Groups) != 1 || len(custom.Groups[0].Groups) != 1 {
		t.Fatalf("custom = %#v", custom)
	}
	entry := custom.Groups[0].Groups[0].Entries[0]
	if entry.Path != "dir/item.equ" || entry.Name != "自定义名称" {
		t.Fatalf("entry = %#v", entry)
	}
	if custom.Entries[0].Name != "root.txt" {
		t.Fatalf("default entry name = %#v", custom.Entries[0])
	}
	assertPrivateFileMode(t, path)
}

func TestBookmarkServiceRejectsBuiltinMutationInProduction(t *testing.T) {
	service := newBookmarkService(filepath.Join(t.TempDir(), "bookmarks.json"))
	document, err := service.LoadBookmarks()
	if err != nil {
		t.Fatal(err)
	}
	document.Books[0].Name = "被修改"
	if err := service.SaveBookmarks(document); err == nil || !strings.Contains(err.Error(), "不可编辑") {
		t.Fatalf("save builtin mutation error = %v", err)
	}
}

func TestBookmarkServiceWritesBuiltinConfigInDevelopment(t *testing.T) {
	root := t.TempDir()
	userPath := filepath.Join(root, "user", "bookmarks.json")
	sourcePath := filepath.Join(root, "config", "bookmarks.json")
	source := []byte(`{
  "version": 1,
  "name": "开发内置",
  "groups": [{
    "id": "list",
    "name": "列表",
    "entries": [{"path": "old.lst", "name": "旧列表"}]
  }]
}`)
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, source, 0o644); err != nil {
		t.Fatal(err)
	}
	service := newBookmarkServiceWithSource(userPath, sourcePath)
	document, err := service.LoadBookmarks()
	if err != nil {
		t.Fatal(err)
	}
	if !document.Books[0].Editable {
		t.Fatal("development builtin is not editable")
	}
	document.Books[0].Name = "开发内置已改"
	document.Books[0].Groups[0].Entries[0].Path = "new.lst"
	if err := service.SaveBookmarks(document); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "开发内置已改") || !strings.Contains(string(updated), "new.lst") {
		t.Fatalf("updated source = %s", updated)
	}
	if _, err := os.Stat(sourcePath + ".bak"); err != nil {
		t.Fatalf("builtin backup missing: %v", err)
	}
}

func TestBookmarkFileNormalizationAndValidation(t *testing.T) {
	file, err := parseBookmarkBookFile([]byte(`{
  "version": 1,
  "name": "导入簿",
  "groups": [{
    "id": "outer",
    "name": "外层",
    "groups": [{"id": "inner", "name": "内层", "entries": [
      {"path": "a\\\\b.txt"},
      {"path": "a/b.txt", "name": "重复"}
    ]}]
  }]
}`))
	if err != nil {
		t.Fatal(err)
	}
	entries := file.Groups[0].Groups[0].Entries
	if len(entries) != 1 || entries[0].Path != "a/b.txt" || entries[0].Name != "b.txt" {
		t.Fatalf("normalized entries = %#v", entries)
	}

	_, err = parseBookmarkBookFile([]byte(`{"version":1,"name":"非法","entries":[{"path":"../outside"}]}`))
	if err == nil || !strings.Contains(err.Error(), "非法分段") {
		t.Fatalf("invalid path error = %v", err)
	}
	_, err = parseBookmarkBookFile([]byte(`{"version":1,"name":"非法","groups":[{"id":"a","name":"同名"},{"id":"b","name":"同名"}]}`))
	if err == nil || !strings.Contains(err.Error(), "同级分组名称不能重复") {
		t.Fatalf("duplicate group error = %v", err)
	}
}

func TestBookmarkServiceRejectsUnsupportedVersion(t *testing.T) {
	service := newBookmarkService(filepath.Join(t.TempDir(), "bookmarks.json"))
	if err := service.SaveBookmarks(BookmarkDocument{Version: 2}); err == nil || !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("unsupported save version error = %v", err)
	}
	if _, err := parseBookmarkBookFile([]byte(`{"version":2,"name":"未来"}`)); err == nil || !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("unsupported import version error = %v", err)
	}
}
