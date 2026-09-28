package services

import "testing"

// A-01：打开归档后语义索引保持"未构建"，只有显式请求才构建。
func TestSearchIndexStartsDeferred(t *testing.T) {
	archivePath := writeSearchFixture(t, "deferred-search.pvf")
	c := NewCore()
	service := NewArchiveService(c)
	if _, err := service.Open(archivePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)

	status := service.IndexStatus()
	if status.State != IndexStateIdle {
		t.Fatalf("打开归档后索引应保持未构建，实际 = %#v", status)
	}
	if _, err := service.Search("烈火之心项链", 0, 10); err == nil {
		t.Fatal("未构建时搜索应返回索引未就绪错误")
	}

	if _, err := service.RebuildSearchIndex(); err != nil {
		t.Fatal(err)
	}
	final := waitForSearchIndex(t, c)
	if final.State != IndexStateReady {
		t.Fatalf("显式构建后索引应就绪，实际 = %#v", final)
	}
	hits, err := service.Search("烈火之心项链", 0, 10)
	if err != nil || len(hits.Hits) != 2 {
		t.Fatalf("构建后搜索 = %d hits, err = %v", len(hits.Hits), err)
	}
}
