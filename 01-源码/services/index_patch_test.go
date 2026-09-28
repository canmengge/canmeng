package services

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"pvfine/internal/pvf"
)

// 增量补丁必须与全量 buildIndex 结果一致：目录集合、每个目录的子节点
// （含 ChildCount）、有序路径表逐条相同。这是"不再全量重建索引"的前提。
func TestPatchArchiveIndexMatchesFullBuild(t *testing.T) {
	built := pvf.New()
	for _, path := range []string{
		"equipment/character/common/amulet/1008.equ",
		"equipment/character/common/amulet/1009.equ",
		"equipment/character/common/belt/2001.equ",
		"misc/readme.txt",
		"sprite/npc/job/a.ani",
	} {
		if _, err := built.AddFileText(path, "[price]\n100", pvf.TypeScript); err != nil {
			t.Fatal(err)
		}
	}

	core := NewCore()
	if err := core.setArchive(built); err != nil {
		t.Fatalf("setArchive: %v", err)
	}

	// 新增（含全新目录链）+ 覆盖 各若干，覆盖到"新建目录"与"更新已有节点"两条路径。
	imports := []importFile{
		{targetPath: "equipment/character/common/amulet/9999.equ", text: "[price]\n999", dataType: pvf.TypeScript},
		{targetPath: "misc/new/deep/nested.equ", text: "[price]\n7", dataType: pvf.TypeScript},
		{targetPath: "brand/new/dir/item.equ", text: "[price]\n5", dataType: pvf.TypeScript},
		{targetPath: "misc/readme.txt", text: "[price]\n200", dataType: pvf.TypeScript},
	}

	stage := core.archive.CloneForBatch()
	result, err := applyPreparedImport(stage, imports, ImportModeText, ImportConflictOverwrite, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	childrenA, pathsA, patched := patchArchiveIndex(core.dirChildren, core.sortedPaths, stage, result.ChangedIndexes)
	if !patched {
		t.Fatal("增量补丁未生效")
	}
	childrenB, pathsB, err := buildIndex(stage)
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}

	if len(pathsA) != len(pathsB) {
		t.Fatalf("路径条数: 增量=%d 全量=%d", len(pathsA), len(pathsB))
	}
	for i := range pathsB {
		if pathsA[i].path != pathsB[i].path || pathsA[i].idx != pathsB[i].idx {
			t.Fatalf("第 %d 条路径不一致: 增量=%v 全量=%v", i, pathsA[i], pathsB[i])
		}
	}

	keysA := make([]string, 0, len(childrenA))
	keysB := make([]string, 0, len(childrenB))
	for key := range childrenA {
		keysA = append(keysA, key)
	}
	for key := range childrenB {
		keysB = append(keysB, key)
	}
	sort.Strings(keysA)
	sort.Strings(keysB)
	if fmt.Sprint(keysA) != fmt.Sprint(keysB) {
		t.Fatalf("目录集合不一致:\n增量=%v\n全量=%v", keysA, keysB)
	}
	for _, key := range keysB {
		left, right := childrenA[key], childrenB[key]
		if len(left) != len(right) {
			t.Fatalf("目录 %q 子节点数: 增量=%d 全量=%d", key, len(left), len(right))
		}
		for i := range right {
			la, lb := left[i], right[i]
			if la.Name != lb.Name || la.Path != lb.Path || la.IsDir != lb.IsDir ||
				la.FileIndex != lb.FileIndex || la.Size != lb.Size ||
				la.DataType != lb.DataType || la.ChildCount != lb.ChildCount {
				t.Fatalf("目录 %q 第 %d 个节点不一致:\n增量=%+v\n全量=%+v", key, i, la, lb)
			}
		}
	}
}

// 取消开关打开后，扫描/读取阶段应立即中止，且活动归档不受影响。
func TestImportJobCancelStopsScanning(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.equ"), []byte("[price]\n100"), 0o644); err != nil {
		t.Fatal(err)
	}
	seed := pvf.New()
	if _, err := seed.AddFileText("misc/keep.equ", "[price]\n1", pvf.TypeScript); err != nil {
		t.Fatal(err)
	}
	core := NewCore()
	if err := core.setArchive(seed); err != nil {
		t.Fatal(err)
	}
	svc := NewArchiveService(core)

	// 空闲时取消应报错（UI 据此提示"当前没有导入"）。
	if err := svc.CancelImport(); err == nil {
		t.Fatal("空闲时 CancelImport 应返回错误")
	}

	job := newImportJob()
	job.cancel.Store(true)
	if err := job.tick("scan", 0, 0, 0, true); !errors.Is(err, ErrImportCancelled) {
		t.Fatalf("tick 未返回取消错误: %v", err)
	}
	if _, err := collectImportFiles([]string{dir}, "misc", job); !errors.Is(err, ErrImportCancelled) {
		t.Fatalf("collectImportFiles 未响应取消: %v", err)
	}
	if _, _, _, err := prepareImportFiles([]string{dir}, "misc", ImportModeText, job); !errors.Is(err, ErrImportCancelled) {
		t.Fatalf("prepareImportFiles 未响应取消: %v", err)
	}
}
