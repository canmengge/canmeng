package pvf

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrCancelled is returned by ExtractTo when the cancel callback fires.
var ErrCancelled = errors.New("pvf: unpack cancelled")

// ErrSaveCancelled is returned when a save was aborted through SaveHooks.Cancel.
// It only ever fires before the temp file is renamed over the source, so the
// source archive is always left untouched.
var ErrSaveCancelled = errors.New("pvf: save cancelled")

// SaveHooks 保存过程的可选回调（全部可留空；留空时行为与 Save / SaveAs 完全一致）。
//
// 只用于「进度上报 + 取消」，不参与写回算法：回调只在原有的安全点上被调用，
// 取消也不会留下半成品——临时文件会被删除、rename 不会执行，源文件保持原样。
type SaveHooks struct {
	// Phase 在阶段切换时回调：prepare / rebuild / write / sync / rename。
	Phase func(phase string)
	// Progress 在重建与写盘阶段周期性回调（已处理单位, 总单位）。
	Progress func(done, total int)
	// Cancel 返回 true 时在下一个安全点中止保存（返回 ErrSaveCancelled）。
	Cancel func() bool
}

func (h SaveHooks) phase(name string) {
	if h.Phase != nil {
		h.Phase(name)
	}
}

func (h SaveHooks) progress(done, total int) {
	if h.Progress != nil {
		h.Progress(done, total)
	}
}

func (h SaveHooks) cancelled() bool {
	if h.Cancel == nil {
		return false
	}
	return h.Cancel()
}

// SaveAs writes the archive (with pending edits applied) to path.
// The write is atomic: data lands in a temp file that is renamed over path,
// so a failed write never destroys an existing archive.
func (a *Archive) SaveAs(path string) error {
	return a.SaveAsHooked(path, SaveHooks{})
}

// SaveAsHooked 同 SaveAs，只是每到一个阶段/安全点回调一次 SaveHooks。
//
// 取消点全部位于 rename 之前：一旦请求取消，临时文件会被删除、源文件保持原样。
func (a *Archive) SaveAsHooked(path string, h SaveHooks) error {
	if h.cancelled() {
		return ErrSaveCancelled
	}
	h.phase("prepare")
	tmp := path + ".pvftmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	h.phase("write")
	err = a.SaveToHooked(bw, h)
	if err == nil {
		h.phase("sync")
		err = bw.Flush()
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if h.cancelled() {
		os.Remove(tmp)
		return ErrSaveCancelled
	}
	h.phase("rename")
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if a.sourcePath == "" {
		a.sourcePath = path
	}
	return nil
}

// Save writes the archive back to the file it was opened from.
func (a *Archive) Save() error {
	if a.sourcePath == "" {
		return errors.New("pvf: archive has no source path")
	}
	return a.SaveAs(a.sourcePath)
}

// SourcePath returns the file the archive was opened from ("" for archives
// built from scratch that have not been saved yet).
func (a *Archive) SourcePath() string { return a.sourcePath }

// ModifiedCount returns a non-zero modification count for both payload and
// structural edits. Structural edits do not belong to a single entry, so they
// contribute one indicator entry when no payload overlay exists.
func (a *Archive) ModifiedCount() int {
	if len(a.overlay) > 0 {
		return len(a.overlay)
	}
	if a.structuralDirty {
		return 1
	}
	return 0
}

// ExtractTo unpacks every entry under dir, recreating the internal directory
// structure. Characters illegal in file names are replaced; the destination
// is never escaped. progress (optional) receives (done, total) periodically;
// cancel (optional) aborts with ErrCancelled.
func (a *Archive) ExtractTo(dir string, progress func(done, total int), cancel func() bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	total := len(a.items)
	createdDirs := map[string]bool{}
	for i := range a.items {
		if cancel != nil && cancel() {
			return ErrCancelled
		}
		p := a.Path(int32(i))
		if p == "" {
			continue
		}
		dst, err := safeJoin(dir, p)
		if err != nil {
			continue // skip entries that would escape the destination
		}
		if d := filepath.Dir(dst); !createdDirs[d] {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
			createdDirs[d] = true
		}
		raw, err := a.RawBytes(int32(i))
		if err == nil && len(raw) > 0 {
			if err := os.WriteFile(dst, raw, 0o644); err != nil {
				return err
			}
		}
		if progress != nil && (i%500 == 0 || i == total-1) {
			progress(i+1, total)
		}
	}
	return nil
}

// safeJoin joins an internal slash path onto base, rejecting traversal and
// sanitizing characters that are illegal in file names.
func safeJoin(base, internal string) (string, error) {
	parts := strings.Split(internal, "/")
	var cleaned []string
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", fmt.Errorf("pvf: path %q escapes destination", internal)
		}
		cleaned = append(cleaned, sanitizeName(part))
	}
	if len(cleaned) == 0 {
		return "", fmt.Errorf("pvf: empty path")
	}
	return filepath.Join(append([]string{base}, cleaned...)...), nil
}

func sanitizeName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7F:
			return '_'
		case strings.ContainsRune(`<>:"|?*`, r):
			return '_'
		}
		return r
	}, name)
}

// SaveTo writes the archive to w. Unmodified archives are reproduced
// byte-identical; edited ones rebuild only the touched chunks plus derived
// sections. A Paged110 container gets its page guards re-encrypted on the way
// out, so the result is a file the client accepts.
func (a *Archive) SaveTo(w io.Writer) error {
	return a.SaveToHooked(w, SaveHooks{})
}

// SaveToHooked 同 SaveTo，带进度/取消钩子（钩子留空时行为完全一致）。
func (a *Archive) SaveToHooked(w io.Writer, h SaveHooks) error {
	if a.paged110 {
		return a.savePaged110Hooked(w, h)
	}
	if !a.Modified() && a.data != nil && int32(len(a.items)) == a.hdr.FileCount {
		h.phase("write")
		h.progress(0, len(a.data))
		_, err := w.Write(a.data)
		h.progress(len(a.data), len(a.data))
		return err
	}
	out, err := a.rebuildHooked(h)
	if err != nil {
		return err
	}
	if _, err := w.Write(out); err != nil {
		return err
	}
	a.adoptRebuilt(out)
	return nil
}

// savePaged110 writes a Paged110 container back: the logical bytes (page guards
// decrypted) are rebuilt as usual and the guards are re-applied page by page
// with the key table recovered when the archive was opened.
func (a *Archive) savePaged110(w io.Writer) error {
	return a.savePaged110Hooked(w, SaveHooks{})
}

// savePaged110Hooked 同 savePaged110，带进度/取消钩子。
func (a *Archive) savePaged110Hooked(w io.Writer, h SaveHooks) error {
	if len(a.pageKeys) == 0 {
		return ErrPaged110ReadOnly
	}
	if (a.structuralDirty || a.poolsDirty) && a.keys.hash.seed == 0 {
		// Without the HASH seed the section can only be copied through verbatim,
		// and a rebuilt name pool would leave it pointing at stale offsets.
		return ErrPaged110StructureLocked
	}
	logical := a.data
	if !a.Modified() && logical != nil && int32(len(a.items)) == a.hdr.FileCount {
		return writePageGuardedHooked(w, logical, a.pageKeys, h)
	}

	// rebuild() 会就地改写 a.hdr（FileCount / BodySize / GroupCount / …）以及新增
	// 条目的 chunk/off/size。若随后的守卫校验拒绝保存（最常见：页密钥不足以覆盖
	// 全部页），这些改动必须回滚 —— 否则归档会停在「半保存」状态：逻辑字节还是旧的、
	// 头信息与条目已是新的，之后的 Info()/编辑都基于错误的头。
	// 2026-09-29 发布前审查发现；该失败路径在 4.4 的客户环境里很常见（零售 sk.dat
	// 恰好只覆盖 52 页，任何增长型编辑都会触发拒绝）。
	savedHdr := a.hdr
	savedItems := append([]fileItem(nil), a.items...)
	restore := func() {
		a.hdr = savedHdr
		a.items = savedItems
	}

	out, err := a.rebuildHooked(h)
	if err != nil {
		restore()
		return err
	}
	if err := writePageGuardedHooked(w, out, a.pageKeys, h); err != nil {
		restore()
		return err
	}
	a.adoptRebuilt(out)
	return nil
}

type chunkOut struct {
	raw      []byte // untouched: still-encrypted slice to copy verbatim
	enc      []byte // rebuilt: encrypted+compressed replacement (nil when raw)
	origSize int32
	compSize int32 // cumulative, as stored in GRPI
}

// rebuildProgressStep 控制重建阶段的进度上报频率（按 chunk 数），避免大归档刷屏。
const rebuildProgressStep = 256

// rebuild produces the complete archive bytes with edits applied.
func (a *Archive) rebuild() ([]byte, error) {
	return a.rebuildHooked(SaveHooks{})
}

// rebuildHooked 同 rebuild，带进度/取消钩子（钩子留空时行为完全一致）。
func (a *Archive) rebuildHooked(h SaveHooks) ([]byte, error) {
	h.phase("rebuild")
	// Classify edits.
	modifiedChunks := map[int32]bool{}
	var newFiles []int32
	for i := range a.items {
		it := &a.items[i]
		if it.chunk < 0 || it.chunk >= int32(len(a.groups)) {
			newFiles = append(newFiles, int32(i))
			continue
		}
		if _, ok := a.overlay[int32(i)]; ok {
			modifiedChunks[it.chunk] = true
		}
	}
	for chunk := range a.removedSpans {
		modifiedChunks[chunk] = true
	}

	// Pass 1: per-chunk output. Untouched chunks are copied as raw encrypted
	// bytes; modified chunks are rebuilt, re-compressed and re-encrypted.
	groups := len(a.groups)
	outs := make([]chunkOut, 0, groups+1)
	var cumulative int32
	for ci := int32(0); ci < int32(groups); ci++ {
		if h.cancelled() {
			return nil, ErrSaveCancelled
		}
		if ci%rebuildProgressStep == 0 {
			h.progress(int(ci), groups)
		}
		if !modifiedChunks[ci] {
			raw, ok := a.chunkSpan(ci)
			if !ok {
				return nil, fmt.Errorf("pvf: chunk %d out of bounds in source data", ci)
			}
			cumulative += int32(len(raw))
			outs = append(outs, chunkOut{raw: raw, origSize: a.groups[ci].origSize, compSize: cumulative})
			continue
		}
		rebuilt, err := a.rebuildChunk(ci)
		if err != nil {
			return nil, err
		}
		enc, err := zlibCompress(rebuilt)
		if err != nil {
			return nil, err
		}
		cryptSeed(a.keys.body.seed, a.keys.body.magic, enc)
		cumulative += int32(len(enc))
		outs = append(outs, chunkOut{enc: enc, origSize: int32(len(rebuilt)), compSize: cumulative})
	}

	h.progress(groups, groups)
	if h.cancelled() {
		return nil, ErrSaveCancelled
	}

	// New files land in one appended chunk.
	if len(newFiles) > 0 {
		var nb bytes.Buffer
		newChunk := int32(len(outs))
		for _, i := range newFiles {
			it := &a.items[i]
			data := a.overlay[i]
			it.chunk = newChunk
			it.off = int32(nb.Len())
			it.size = int32(len(data))
			nb.Write(data)
		}
		enc, err := zlibCompress(nb.Bytes())
		if err != nil {
			return nil, err
		}
		cryptSeed(a.keys.body.seed, a.keys.body.magic, enc)
		cumulative += int32(len(enc))
		outs = append(outs, chunkOut{enc: enc, origSize: int32(nb.Len()), compSize: cumulative})
	}

	// File table (plaintext).
	table := make([]byte, len(a.items)*0x18)
	for i := range a.items {
		item := a.items[i]
		if payload, ok := a.overlay[int32(i)]; ok {
			item.size = int32(len(payload))
		}
		marshalItem(table[i*0x18:], &item)
	}

	// Hash section.
	//
	// It is regenerated whenever its seed is known (the standard key set, the
	// alternate variant's "hash" key, or a seed solved from the section itself),
	// so a rebuild indexes the current file list — including added or removed
	// files. Only an archive whose seed could not be established keeps its
	// original bytes, which is the best a rebuild can do there.
	var hashBytes []byte
	if a.keys.hash.seed != 0 || a.data == nil || a.hashSize == 0 {
		hashBytes = a.buildHashTable()
	} else {
		hashBytes = a.data[a.hashOff : a.hashOff+a.hashSize]
	}

	// Name table: rebuild only when pools changed.
	var nameBytes []byte
	if a.poolsDirty || a.data == nil {
		nb, err := a.buildNameTable()
		if err != nil {
			return nil, err
		}
		nameBytes = nb
	} else {
		nameBytes = a.data[a.nameOff : a.nameOff+a.nameSize]
	}

	// GRPI section.
	grpi := make([]byte, len(outs)*8)
	for i := range outs {
		binary.LittleEndian.PutUint32(grpi[i*8:], uint32(outs[i].compSize))
		binary.LittleEndian.PutUint32(grpi[i*8+4:], uint32(outs[i].origSize))
	}
	cryptSeed(a.keys.grpi.seed, a.keys.grpi.magic, grpi)

	// Header.
	a.hdr.FileCount = int32(len(a.items))
	a.hdr.BodySize = cumulative
	a.hdr.GroupCount = int32(len(outs))
	a.hdr.HashTableSize = int32(len(hashBytes))
	a.hdr.NameTableSize = int32(len(nameBytes))
	hdr := encodeHeader(a.hdr)
	cryptSeed(a.keys.header.seed, a.keys.header.magic, hdr)
	if a.guard {
		applyGuard(hdr)
	}

	// Assemble.
	var buf bytes.Buffer
	buf.Grow(headerSize + len(table) + len(hashBytes) + len(nameBytes) + len(grpi) + int(cumulative))
	buf.Write(hdr)
	buf.Write(table)
	buf.Write(hashBytes)
	buf.Write(nameBytes)
	buf.Write(grpi)
	for i := range outs {
		if outs[i].enc != nil {
			buf.Write(outs[i].enc)
		} else {
			buf.Write(outs[i].raw)
		}
	}
	return buf.Bytes(), nil
}

// rebuildChunk returns chunk ci with overlay payloads spliced in, ported from
// the reference implementation: segments are written in offset order, gaps
// are copied from the original chunk except ranges belonging to deleted files.
func (a *Archive) rebuildChunk(ci int32) ([]byte, error) {
	orig, err := a.Chunk(ci)
	if err != nil {
		return nil, err
	}
	type seg struct {
		off, size, idx int32
		hasNew         bool
	}
	var segs []seg
	for i := range a.items {
		it := &a.items[i]
		if it.chunk != ci {
			continue
		}
		_, hasNew := a.overlay[int32(i)]
		if it.size <= 0 && !hasNew {
			continue
		}
		segs = append(segs, seg{off: it.off, size: it.size, idx: int32(i), hasNew: hasNew})
	}
	sort.Slice(segs, func(x, y int) bool { return segs[x].off < segs[y].off })
	removed := append([]removedFileSpan(nil), a.removedSpans[ci]...)
	sort.Slice(removed, func(x, y int) bool { return removed[x].off < removed[y].off })

	var buf bytes.Buffer
	var srcPos int32
	for _, s := range segs {
		writeOriginalRange(&buf, orig, srcPos, s.off, removed)
		it := &a.items[s.idx]
		it.off = int32(buf.Len())
		if s.hasNew {
			d := a.overlay[s.idx]
			buf.Write(d)
			it.size = int32(len(d))
		} else if orig != nil && int64(s.off)+int64(s.size) <= int64(len(orig)) {
			buf.Write(orig[s.off : s.off+s.size])
		}
		srcPos = s.off + s.size
	}
	writeOriginalRange(&buf, orig, srcPos, int32(len(orig)), removed)
	return buf.Bytes(), nil
}

func writeOriginalRange(buf *bytes.Buffer, orig []byte, start, end int32, removed []removedFileSpan) {
	if len(orig) == 0 || end <= start {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > int32(len(orig)) {
		end = int32(len(orig))
	}
	if end <= start {
		return
	}

	pos := start
	for _, span := range removed {
		spanStart := span.off
		spanEnd := span.off + span.size
		if spanEnd <= pos {
			continue
		}
		if spanStart >= end {
			break
		}
		if spanStart > pos {
			from, to := pos, minInt32(spanStart, end)
			buf.Write(orig[from:to])
		}
		if spanEnd > pos {
			pos = spanEnd
		}
		if pos >= end {
			return
		}
	}
	if pos < end {
		buf.Write(orig[pos:end])
	}
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// adoptRebuilt refreshes in-memory state so the archive keeps working after a
// save: sections move, chunks re-derive from the new layout.
func (a *Archive) adoptRebuilt(out []byte) {
	a.data = out
	pos := headerSize
	a.tableOff = pos
	pos += int(a.hdr.FileCount) * 0x18
	a.hashOff = pos
	a.hashSize = int(a.hdr.HashTableSize)
	pos += a.hashSize
	a.nameOff = pos
	a.nameSize = int(a.hdr.NameTableSize)
	pos += a.nameSize
	a.grpiOff = pos
	a.grpiSize = int(a.hdr.GroupCount) * 8
	pos += a.grpiSize
	a.bodyOff = pos

	a.groups = make([]groupItem, a.hdr.GroupCount)
	grpi := make([]byte, a.grpiSize)
	copy(grpi, out[a.grpiOff:a.grpiOff+a.grpiSize])
	cryptSeed(a.keys.grpi.seed, a.keys.grpi.magic, grpi)
	for i := range a.groups {
		a.groups[i].compSize = int32(binary.LittleEndian.Uint32(grpi[i*8:]))
		a.groups[i].origSize = int32(binary.LittleEndian.Uint32(grpi[i*8+4:]))
	}

	a.chunkCache = map[int32][]byte{}
	a.overlay = map[int32][]byte{}
	a.poolsDirty = false
	a.structuralDirty = false
	a.removedSpans = make(map[int32][]removedFileSpan)
}

func marshalItem(b []byte, it *fileItem) {
	binary.LittleEndian.PutUint32(b[0:], uint32(it.nameOff))
	binary.LittleEndian.PutUint32(b[4:], uint32(it.pathOff))
	binary.LittleEndian.PutUint32(b[8:], uint32(it.chunk))
	binary.LittleEndian.PutUint32(b[12:], uint32(it.off))
	binary.LittleEndian.PutUint32(b[16:], uint32(it.size))
	binary.LittleEndian.PutUint32(b[20:], uint32(it.typ))
}

func encodeHeader(h Header) []byte {
	b := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(b[0:], h.Signature)
	copy(b[4:24], h.Guid[:])
	binary.LittleEndian.PutUint32(b[24:], uint32(h.FileCount))
	binary.LittleEndian.PutUint32(b[28:], uint32(h.Padding))
	binary.LittleEndian.PutUint32(b[32:], uint32(h.BodySize))
	binary.LittleEndian.PutUint32(b[36:], uint32(h.GroupCount))
	binary.LittleEndian.PutUint32(b[40:], uint32(h.HashTableSize))
	binary.LittleEndian.PutUint32(b[44:], uint32(h.NameTableSize))
	return b
}

// Revert drops the pending edit for entry i.
func (a *Archive) Revert(i int32) { delete(a.overlay, i) }
