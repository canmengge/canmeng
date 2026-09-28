package services

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"pvfine/internal/pvf"
)

func waitForSearchCacheFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("search index cache was not written: %s", path)
}

func TestSearchIndexCacheHit(t *testing.T) {
	archivePath := writeSearchFixture(t, "cached-search.pvf")
	cachePath := filepath.Join(t.TempDir(), "search-index.json.gz")

	first := NewCore()
	first.searchIndexCachePath = cachePath
	firstService := NewArchiveService(first)
	if _, err := firstService.Open(archivePath); err != nil {
		t.Fatal(err)
	}
	waitForSearchIndex(t, first)
	waitForSearchCacheFile(t, cachePath)
	first.closeArchive()

	second := NewCore()
	second.searchIndexCachePath = cachePath
	secondService := NewArchiveService(second)
	if _, err := secondService.Open(archivePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.closeArchive)
	status := waitForSearchIndex(t, second)
	if !status.CacheHit || status.Stage != "ready-cache" {
		t.Fatalf("cache status = %#v", status)
	}
	result, err := secondService.Search("烈火之心项链", 0, 10)
	if err != nil || len(result.Hits) != 2 {
		t.Fatalf("cached search = %d hits, err = %v", len(result.Hits), err)
	}
}

func TestSearchIndexCacheInvalidatesOnSourceChange(t *testing.T) {
	archivePath := writeSearchFixture(t, "invalidated-search.pvf")
	cachePath := filepath.Join(t.TempDir(), "search-index.json.gz")

	first := NewCore()
	first.searchIndexCachePath = cachePath
	if _, err := NewArchiveService(first).Open(archivePath); err != nil {
		t.Fatal(err)
	}
	waitForSearchIndex(t, first)
	waitForSearchCacheFile(t, cachePath)
	first.closeArchive()

	file, err := os.OpenFile(archivePath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("x"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	second := NewCore()
	second.searchIndexCachePath = cachePath
	if _, err := NewArchiveService(second).Open(archivePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.closeArchive)
	status := waitForSearchIndex(t, second)
	if status.CacheHit {
		t.Fatalf("stale cache was used: %#v", status)
	}
}

func TestSearchIndexCacheIgnoresUnsavedEdits(t *testing.T) {
	archivePath := writeSearchFixture(t, "unsaved-search.pvf")
	cachePath := filepath.Join(t.TempDir(), "search-index.json.gz")

	seed := NewCore()
	seed.searchIndexCachePath = cachePath
	seedService := NewArchiveService(seed)
	if _, err := seedService.Open(archivePath); err != nil {
		t.Fatal(err)
	}
	waitForSearchIndex(t, seed)
	waitForSearchCacheFile(t, cachePath)
	seed.closeArchive()

	edited := NewCore()
	edited.searchIndexCachePath = cachePath
	editedService := NewArchiveService(edited)
	if _, err := editedService.Open(archivePath); err != nil {
		t.Fatal(err)
	}
	waitForSearchIndex(t, edited)
	hits, err := editedService.Search("烈火之心项链", 0, 10)
	if err != nil || len(hits.Hits) == 0 {
		t.Fatalf("seed search = %d hits, err = %v", len(hits.Hits), err)
	}
	if err := NewEditorService(edited).SetText(hits.Hits[0].FileIndex, "[name]\n`未保存名称`"); err != nil {
		t.Fatal(err)
	}
	waitForSearchRefresh(t, edited)
	edited.closeArchive()

	reopened := NewCore()
	reopened.searchIndexCachePath = cachePath
	reopenedService := NewArchiveService(reopened)
	if _, err := reopenedService.Open(archivePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.closeArchive)
	status := waitForSearchIndex(t, reopened)
	if !status.CacheHit {
		t.Fatalf("cache was unexpectedly replaced by unsaved edit: %#v", status)
	}
	old, err := reopenedService.Search("烈火之心项链", 0, 10)
	if err != nil || len(old.Hits) == 0 {
		t.Fatalf("reopened old search = %d hits, err = %v", len(old.Hits), err)
	}
}

func TestSearchIndexNewFileUsesAsyncDelta(t *testing.T) {
	archivePath := writeSearchFixture(t, "delta-search.pvf")
	a, err := pvf.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCore()
	if err := c.setArchive(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)
	c.startSearchIndex()
	waitForSearchIndex(t, c)

	service := NewArchiveService(c)
	if _, err := service.CreateFile("misc/generated.txt", pvf.TypeUnicode); err != nil {
		t.Fatal(err)
	}
	status := service.IndexStatus()
	if status.State != IndexStateReady {
		t.Fatalf("index became unavailable during delta: %#v", status)
	}
	old, err := service.Search("烈火之心项链", 0, 10)
	if err != nil || len(old.Hits) != 2 {
		t.Fatalf("old search during delta = %d hits, err = %v", len(old.Hits), err)
	}
	waitForSearchRefresh(t, c)
	newHits, err := service.Search("generated.txt", 0, 10)
	if err != nil || len(newHits.Hits) != 1 {
		t.Fatalf("new path search = %d hits, err = %v", len(newHits.Hits), err)
	}
}

func TestSearchIndexListRegistrationUsesAsyncDelta(t *testing.T) {
	archivePath := writeSearchFixture(t, "registration-delta.pvf")
	a, err := pvf.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCore()
	if err := c.setArchive(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)
	c.startSearchIndex()
	waitForSearchIndex(t, c)
	fileIndex, ok := a.Find("misc/readme.txt")
	if !ok {
		t.Fatal("unregistered file missing")
	}

	service := NewArchiveService(c)
	if _, err := service.RegisterFileToList(fileIndex, "equipment/equipment.lst", "2000"); err != nil {
		t.Fatal(err)
	}
	if status := service.IndexStatus(); status.State != IndexStateReady {
		t.Fatalf("index became unavailable during registration: %#v", status)
	}
	waitForSearchRefresh(t, c)
	result, err := service.Search("2000", 0, 10)
	if err != nil || len(result.Hits) != 1 || result.Hits[0].FileIndex != fileIndex {
		t.Fatalf("registered search = %#v, err = %v", result.Hits, err)
	}
}

func TestSearchIndexDeleteUsesAsyncDelta(t *testing.T) {
	archivePath := writeSearchFixture(t, "delete-delta.pvf")
	a, err := pvf.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCore()
	if err := c.setArchive(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)
	c.startSearchIndex()
	waitForSearchIndex(t, c)
	fileIndex, ok := a.Find("misc/readme.txt")
	if !ok {
		t.Fatal("file to delete missing")
	}

	service := NewArchiveService(c)
	if _, err := service.DeleteFiles([]int32{fileIndex}); err != nil {
		t.Fatal(err)
	}
	if status := service.IndexStatus(); status.State != IndexStateReady {
		t.Fatalf("index became unavailable during delete: %#v", status)
	}
	waitForSearchRefresh(t, c)
	deleted, err := service.Search("misc/readme.txt", 0, 10)
	if err != nil || len(deleted.Hits) != 0 {
		t.Fatalf("deleted path search = %#v, err = %v", deleted.Hits, err)
	}
	remaining, err := service.Search("烈火之心项链", 0, 10)
	if err != nil || len(remaining.Hits) != 2 {
		t.Fatalf("remaining search = %d hits, err = %v", len(remaining.Hits), err)
	}
}

// seedSearchIndexCache 先正常打开一次归档，生成一份合法缓存后关闭。
func seedSearchIndexCache(t *testing.T, archivePath, cachePath string) {
	t.Helper()
	seed := NewCore()
	seed.searchIndexCachePath = cachePath
	if _, err := NewArchiveService(seed).Open(archivePath); err != nil {
		t.Fatal(err)
	}
	waitForSearchIndex(t, seed)
	waitForSearchCacheFile(t, cachePath)
	seed.closeArchive()
}

// A-02：缓存文件被破坏（垃圾字节/截断）时必须自动重建，且不能 panic。
func TestSearchIndexCacheCorruptedFileRebuilds(t *testing.T) {
	archivePath := writeSearchFixture(t, "corrupted-search.pvf")
	cachePath := filepath.Join(t.TempDir(), "search-index.json.gz")
	seedSearchIndexCache(t, archivePath, cachePath)

	if err := os.WriteFile(cachePath, []byte("not-a-gzip-payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	reopened := NewCore()
	reopened.searchIndexCachePath = cachePath
	service := NewArchiveService(reopened)
	if _, err := service.Open(archivePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.closeArchive)
	status := waitForSearchIndex(t, reopened)
	if status.CacheHit {
		t.Fatalf("corrupted cache was treated as a hit: %#v", status)
	}
	if status.State != IndexStateReady {
		t.Fatalf("index did not rebuild after corruption: %#v", status)
	}
	result, err := service.Search("烈火之心项链", 0, 10)
	if err != nil || len(result.Hits) != 2 {
		t.Fatalf("search after rebuild = %d hits, err = %v", len(result.Hits), err)
	}
	// 自愈：坏文件被丢弃，重建后重新写出合法缓存。
	waitForSearchCacheFile(t, cachePath)
}

// A-02：缓存版本不匹配时必须重建（而不是静默使用旧结构）。
func TestSearchIndexCacheVersionMismatchRebuilds(t *testing.T) {
	archivePath := writeSearchFixture(t, "version-search.pvf")
	cachePath := filepath.Join(t.TempDir(), "search-index.json.gz")
	seedSearchIndexCache(t, archivePath, cachePath)
	tamperSearchCacheVersion(t, cachePath, searchIndexCacheVersion+1)

	reopened := NewCore()
	reopened.searchIndexCachePath = cachePath
	service := NewArchiveService(reopened)
	if _, err := service.Open(archivePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.closeArchive)
	status := waitForSearchIndex(t, reopened)
	if status.CacheHit || status.State != IndexStateReady {
		t.Fatalf("version-mismatched cache was not rebuilt: %#v", status)
	}
}

// tamperSearchCacheVersion 只替换缓存里的 version 字段，其余字段原样保留：
// 整份解码再编码会因 JSON 数字转 float64 而改变大整数（纳秒时间戳），导致测不到版本分支。
func tamperSearchCacheVersion(t *testing.T, path string, version int) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(file)
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	decodeErr := json.NewDecoder(reader).Decode(&payload)
	_ = reader.Close()
	_ = file.Close()
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	payload["version"] = json.RawMessage(strconv.Itoa(version))

	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := gzip.NewWriter(out)
	if err := json.NewEncoder(writer).Encode(payload); err != nil {
		_ = writer.Close()
		_ = out.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
