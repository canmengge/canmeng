package services

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"pvfine/internal/pvf"
)

func utf16leBytes(s string) []byte {
	units := utf16.Encode([]rune(s))
	data := make([]byte, 2*len(units))
	for i, unit := range units {
		data[2*i] = byte(unit)
		data[2*i+1] = byte(unit >> 8)
	}
	return data
}

func utf16beBytes(s string) []byte {
	units := utf16.Encode([]rune(s))
	data := make([]byte, 2*len(units))
	for i, unit := range units {
		data[2*i] = byte(unit >> 8)
		data[2*i+1] = byte(unit)
	}
	return data
}

func TestDecodeImportTextEncodings(t *testing.T) {
	text := "名称name`标签`"
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"utf8", []byte(text), text},
		{"utf8 bom", append([]byte{0xEF, 0xBB, 0xBF}, []byte(text)...), text},
		{"utf16le bom", append([]byte{0xFF, 0xFE}, utf16leBytes(text)...), text},
		{"utf16be bom", append([]byte{0xFE, 0xFF}, utf16beBytes(text)...), text},
		{"utf16le no bom", utf16leBytes("upperset_name_ca"), "upperset_name_ca"},
		{"utf16be no bom", utf16beBytes(text), text},
	}
	for _, tc := range cases {
		got, err := decodeImportText("sample.str", tc.data)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}

	// 未配对代理项的伪 UTF-16 应判为非文本并指引改用原始字节导入。
	if _, err := decodeImportText("x.bin", []byte{0x00, 0xD8, 0x42, 0x00, 0x44, 0x00}); err == nil ||
		!strings.Contains(err.Error(), "原始字节") {
		t.Fatalf("binary decode error = %v", err)
	}
}

func TestArchiveServiceImportFilesTextUTF16Str(t *testing.T) {
	sourceDir := t.TempDir()
	// 模拟真实 .str：UTF-16LE 无 BOM（equipment.uv.str 的实际形态）。
	content := utf16leBytes("upperset_name_ca\n`上衣名称`\n")
	sourcePath := filepath.Join(sourceDir, "equipment.uv.str")
	if err := os.WriteFile(sourcePath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewCore()
	if err := c.setArchive(pvf.New()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)

	result, err := NewArchiveService(c).ImportFiles([]string{sourcePath}, "", ImportModeText)
	if err != nil {
		t.Fatal(err)
	}
	if result.ImportedCount != 1 {
		t.Fatalf("import result = %#v", result)
	}
	index, ok := c.archive.Find("equipment.uv.str")
	if !ok {
		t.Fatal("imported string file missing")
	}
	if c.archive.File(index).DataType != pvf.TypeUnicode {
		t.Fatalf("data type = %d, want %d", c.archive.File(index).DataType, pvf.TypeUnicode)
	}
	if text, err := c.archive.Text(index); err != nil || text != "upperset_name_ca\n`上衣名称`\n" {
		t.Fatalf("string text = %q, err = %v", text, err)
	}
	raw, err := c.archive.RawBytes(index)
	if err != nil {
		t.Fatal(err)
	}
	// 写回应为 UTF-16LE（无 BOM），与归档内 TypeUnicode 存储一致。
	if len(raw) != len(content) || raw[0] != 'u' || raw[1] != 0 || raw[len(raw)-1] != 0 {
		t.Fatalf("stored raw bytes are not UTF-16LE: %v", raw[:min(len(raw), 8)])
	}
}

func TestArchiveServiceImportFilesTextAndDirectoryMapping(t *testing.T) {
	sourceDir := t.TempDir()
	nestedDir := filepath.Join(sourceDir, "nested")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "name.str"), []byte("导入文本"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "item.equ"), []byte("[name]\n`导入脚本`"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewCore()
	a := pvf.New()
	if err := c.setArchive(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)

	service := NewArchiveService(c)
	preview, err := service.PreviewImport(
		[]string{sourceDir, nestedDir},
		"equipment",
		ImportModeText,
	)
	if err != nil {
		t.Fatal(err)
	}
	if preview.TotalFiles != 2 || preview.ImportedCount != 2 || preview.OverwrittenCount != 0 {
		t.Fatalf("import preview = %#v", preview)
	}
	if c.archive.FileCount() != 0 {
		t.Fatalf("preview modified archive: file count = %d", c.archive.FileCount())
	}

	result, err := service.ImportFiles(
		[]string{sourceDir, nestedDir},
		"equipment",
		ImportModeText,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.ImportedCount != 2 || result.OverwrittenCount != 0 {
		t.Fatalf("import result = %#v", result)
	}

	directoryName := filepath.Base(sourceDir)
	strPath := filepath.ToSlash(filepath.Join("equipment", directoryName, "name.str"))
	equPath := filepath.ToSlash(filepath.Join("equipment", directoryName, "nested", "item.equ"))
	strIndex, ok := c.archive.Find(strPath)
	if !ok {
		t.Fatalf("missing imported string file %q", strPath)
	}
	equIndex, ok := c.archive.Find(equPath)
	if !ok {
		t.Fatalf("missing imported script file %q", equPath)
	}
	if c.archive.File(strIndex).DataType != pvf.TypeUnicode {
		t.Fatalf("string data type = %d, want %d", c.archive.File(strIndex).DataType, pvf.TypeUnicode)
	}
	if c.archive.File(equIndex).DataType != pvf.TypeScript {
		t.Fatalf("script data type = %d, want %d", c.archive.File(equIndex).DataType, pvf.TypeScript)
	}
	children, err := service.ListChildren(filepath.ToSlash(filepath.Join("equipment", directoryName)))
	if err != nil {
		t.Fatal(err)
	}
	foundNewKind := false
	for _, child := range children {
		if child.Path == strPath {
			foundNewKind = true
			if child.ChangeKind != ChangeKindAdded {
				t.Fatalf("new file change kind = %q, want %q", child.ChangeKind, ChangeKindAdded)
			}
		}
	}
	if !foundNewKind {
		t.Fatalf("new file tree node missing: %q", strPath)
	}
	if text, err := c.archive.Text(strIndex); err != nil || text != "导入文本" {
		t.Fatalf("string text = %q, err = %v", text, err)
	}
	if text, err := c.archive.Text(equIndex); err != nil || !strings.Contains(text, "导入脚本") {
		t.Fatalf("script text = %q, err = %v", text, err)
	}

	var packed bytes.Buffer
	if err := c.archive.SaveTo(&packed); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := pvf.Parse(packed.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.FileCount() != 2 {
		t.Fatalf("round-trip file count = %d, want 2", roundTrip.FileCount())
	}
	roundTripStringIndex, ok := roundTrip.Find(strPath)
	if !ok {
		t.Fatalf("round-trip string file missing: %q", strPath)
	}
	if text, err := roundTrip.Text(roundTripStringIndex); err != nil || text != "导入文本" {
		t.Fatalf("round-trip string text = %q, err = %v", text, err)
	}
}

func TestArchiveServiceImportFilesRawOverwritesAndUpdatesType(t *testing.T) {
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "same.str")
	raw := []byte{0x4b, 0x00, 0x8b, 0x65}
	if err := os.WriteFile(sourcePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewCore()
	a := pvf.New()
	if _, err := a.AddFileText("same.str", "old", pvf.TypeScript); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "base.pvf")
	if err := a.SaveAs(archivePath); err != nil {
		t.Fatal(err)
	}
	loaded, err := pvf.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.setArchive(loaded); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)

	result, err := NewArchiveService(c).ImportFiles([]string{sourcePath}, "", ImportModeRaw)
	if err != nil {
		t.Fatal(err)
	}
	if result.ImportedCount != 0 || result.OverwrittenCount != 1 || len(result.ChangedPaths) != 1 {
		t.Fatalf("import result = %#v", result)
	}
	index, ok := c.archive.Find("same.str")
	if !ok {
		t.Fatal("overwritten file missing")
	}
	if c.archive.File(index).DataType != pvf.TypeUnicode {
		t.Fatalf("overwritten data type = %d, want %d", c.archive.File(index).DataType, pvf.TypeUnicode)
	}
	got, err := c.archive.RawBytes(index)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("overwritten raw = %v, err = %v", got, err)
	}
	children, err := NewArchiveService(c).ListChildren("")
	if err != nil {
		t.Fatal(err)
	}
	foundModifiedKind := false
	for _, child := range children {
		if child.Path == "same.str" {
			foundModifiedKind = true
			if child.ChangeKind != ChangeKindModified {
				t.Fatalf("overwritten file change kind = %q, want %q", child.ChangeKind, ChangeKindModified)
			}
		}
	}
	if !foundModifiedKind {
		t.Fatal("overwritten file tree node missing")
	}
}

// 文本导入遇到无法解码的二进制文件（.equ/.lst 脚本）时，不再中止整批导入，
// 而是只把这一个文件按原始字节写入，并计入 AutoRawCount；同批的文本文件照常走
// 文本导入。2026-09-29：用户拖入客户提取的 .equ 时被"文本编码不匹配"挡住，
// 现改为自动降级，等价于对该文件单独选择「原始字节」。
func TestArchiveServiceImportFilesAutoRawFallbackOnBinary(t *testing.T) {
	sourceDir := t.TempDir()
	validPath := filepath.Join(sourceDir, "valid.equ")
	invalidPath := filepath.Join(sourceDir, "invalid.equ")
	if err := os.WriteFile(validPath, []byte("[name]\n`valid`"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 奇数长度且非 UTF-8/UTF-16 的字节序列：既非有效文本也无法按 UTF-16 解码。
	binary := []byte{0x81, 0x82, 0x83}
	if err := os.WriteFile(invalidPath, binary, 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewCore()
	a := pvf.New()
	if _, err := a.AddFileText("existing.equ", "[name]\n`existing`", pvf.TypeScript); err != nil {
		t.Fatal(err)
	}
	if err := c.setArchive(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)

	res, err := NewArchiveService(c).ImportFiles(
		[]string{validPath, invalidPath},
		"",
		ImportModeText,
	)
	if err != nil {
		t.Fatalf("文本导入不应因二进制文件而失败: %v", err)
	}
	if res.AutoRawCount != 1 {
		t.Fatalf("AutoRawCount = %d, want 1", res.AutoRawCount)
	}
	idx, ok := c.archive.Find("invalid.equ")
	if !ok {
		t.Fatal("invalid.equ 未被导入")
	}
	got, err := c.archive.RawBytes(idx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, binary) {
		t.Fatalf("二进制内容被改动: got % x, want % x", got, binary)
	}
	if _, ok := c.archive.Find("valid.equ"); !ok {
		t.Fatal("同批的文本文件未被导入")
	}
}

func TestCollectImportFilesRejectsTargetCollision(t *testing.T) {
	left := filepath.Join(t.TempDir(), "same")
	right := filepath.Join(t.TempDir(), "same")
	if err := os.MkdirAll(left, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0o755); err != nil {
		t.Fatal(err)
	}
	leftFile := filepath.Join(left, "item.equ")
	rightFile := filepath.Join(right, "item.equ")
	if err := os.WriteFile(leftFile, []byte("left"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightFile, []byte("right"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := collectImportFiles([]string{left, right}, "", nil)
	if err == nil || !strings.Contains(err.Error(), "批次内归档路径冲突") {
		t.Fatalf("collision error = %v", err)
	}
}
