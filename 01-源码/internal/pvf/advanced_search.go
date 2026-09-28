package pvf

import (
	"bytes"
	"context"
	"encoding/binary"
	"regexp"
	"sort"
	"strings"
)

// StringPoolEntry is one decoded value in sTrA or sTrW.
type StringPoolEntry struct {
	Pool   string `json:"pool"`
	Offset int32  `json:"offset"`
	Value  string `json:"value"`
}

// StringPoolFileReference describes how one file references a pool entry.
type StringPoolFileReference struct {
	FileIndex   int32
	Occurrences int
	TokenTypes  []int32
	FileFields  []string
}

// StringPoolMatch is one matching pool value referenced by one file.
type StringPoolMatch struct {
	FileIndex   int32
	Pool        string
	Offset      int32
	Value       string
	Occurrences int
	TokenTypes  []int32
	FileFields  []string
}

type stringPoolEntry struct {
	StringPoolEntry
	lower string
}

// StringPoolIndex is an in-memory reverse index from string-pool offsets to
// archive file indexes. It is built from the current archive overlay and is
// immutable once published: mutations produce a fresh copy.
type StringPoolIndex struct {
	entries    []stringPoolEntry
	filesByOff map[int32][]StringPoolFileReference
	offByFile  map[int32][]int32
}

// MatchFiles returns file indexes whose referenced pool values match query.
// Text matching is case-insensitive substring matching. Regex matching uses
// Go's RE2-compatible regexp engine and is case-sensitive by default.
func (idx *StringPoolIndex) MatchFiles(query string, regex bool) ([]int32, error) {
	matches, err := idx.MatchDetails(query, regex)
	if err != nil {
		return nil, err
	}
	seen := make(map[int32]struct{}, len(matches))
	for _, match := range matches {
		seen[match.FileIndex] = struct{}{}
	}
	files := make([]int32, 0, len(seen))
	for fileIndex := range seen {
		files = append(files, fileIndex)
	}
	sort.Slice(files, func(i, j int) bool { return files[i] < files[j] })
	return files, nil
}

// MatchDetails returns matching pool values together with their file-level
// reference details. A file may have multiple entries when duplicate values
// exist at different pool offsets.
func (idx *StringPoolIndex) MatchDetails(query string, regex bool) ([]StringPoolMatch, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []StringPoolMatch{}, nil
	}

	var match func(stringPoolEntry) bool
	if regex {
		re, err := regexp.Compile(query)
		if err != nil {
			return nil, err
		}
		match = func(entry stringPoolEntry) bool { return re.MatchString(entry.Value) }
	} else {
		lower := strings.ToLower(query)
		match = func(entry stringPoolEntry) bool { return strings.Contains(entry.lower, lower) }
	}

	matches := make([]StringPoolMatch, 0)
	for _, entry := range idx.entries {
		if !match(entry) {
			continue
		}
		for _, ref := range idx.filesByOff[entry.Offset] {
			matches = append(matches, StringPoolMatch{
				FileIndex:   ref.FileIndex,
				Pool:        entry.Pool,
				Offset:      entry.Offset,
				Value:       entry.Value,
				Occurrences: ref.Occurrences,
				TokenTypes:  append([]int32(nil), ref.TokenTypes...),
				FileFields:  append([]string(nil), ref.FileFields...),
			})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].FileIndex != matches[j].FileIndex {
			return matches[i].FileIndex < matches[j].FileIndex
		}
		return matches[i].Offset < matches[j].Offset
	})
	return matches, nil
}

// BuildStringPoolIndex scans the current archive payloads without retaining
// decompressed chunks in the regular chunk cache. Optional hooks receive each
// file's path and raw bytes so a single scan can serve several consumers; the
// hook must not retain raw after returning.
func (a *Archive) BuildStringPoolIndex(ctx context.Context, hooks ...func(index int32, path string, raw []byte)) (*StringPoolIndex, error) {
	entries := a.stringPoolEntries()
	validOffsets := make(map[int32]struct{}, len(entries))
	for _, entry := range entries {
		validOffsets[entry.Offset] = struct{}{}
	}

	refsByOffset := make(map[int32]map[int32]*StringPoolFileReference, len(entries))
	err := a.ForEachRawFile(ctx, func(index int32, path string, _ File, raw []byte) bool {
		collectStringPoolRefs(a, index, raw, validOffsets, refsByOffset)
		for _, hook := range hooks {
			hook(index, path, raw)
		}
		return true
	})
	if err != nil {
		return nil, err
	}

	refs, offByFile := finalizeStringPoolRefs(refsByOffset)
	return &StringPoolIndex{entries: entries, filesByOff: refs, offByFile: offByFile}, nil
}

// collectStringPoolRefs adds one file's string-pool references to refsByOffset.
func collectStringPoolRefs(a *Archive, index int32, raw []byte, validOffsets map[int32]struct{}, refsByOffset map[int32]map[int32]*StringPoolFileReference) {
	item := &a.items[index]
	addReference := func(offset int32) *StringPoolFileReference {
		if _, ok := validOffsets[offset]; !ok {
			return nil
		}
		byFile := refsByOffset[offset]
		if byFile == nil {
			byFile = make(map[int32]*StringPoolFileReference)
			refsByOffset[offset] = byFile
		}
		ref := byFile[index]
		if ref == nil {
			ref = &StringPoolFileReference{FileIndex: index}
			byFile[index] = ref
		}
		ref.Occurrences++
		return ref
	}
	if ref := addReference(item.nameOff); ref != nil {
		ref.FileFields = appendUniqueString(ref.FileFields, "name")
	}
	if ref := addReference(item.pathOff); ref != nil {
		ref.FileFields = appendUniqueString(ref.FileFields, "path")
	}
	if item.typ == TypeScript {
		for pos := 0; pos+5 <= len(raw); pos += 5 {
			switch raw[pos] {
			case 3, 5, 6, 7:
				offset := int32(binary.LittleEndian.Uint32(raw[pos+1:]))
				if ref := addReference(offset); ref != nil {
					ref.TokenTypes = appendUniqueInt32(ref.TokenTypes, int32(raw[pos]))
				}
			}
		}
	}
}

func finalizeStringPoolRefs(refsByOffset map[int32]map[int32]*StringPoolFileReference) (map[int32][]StringPoolFileReference, map[int32][]int32) {
	refs := make(map[int32][]StringPoolFileReference, len(refsByOffset))
	offByFile := make(map[int32][]int32)
	for offset, byFile := range refsByOffset {
		list := make([]StringPoolFileReference, 0, len(byFile))
		for _, ref := range byFile {
			copyRef := *ref
			copyRef.TokenTypes = append([]int32(nil), ref.TokenTypes...)
			copyRef.FileFields = append([]string(nil), ref.FileFields...)
			list = append(list, copyRef)
			offByFile[copyRef.FileIndex] = append(offByFile[copyRef.FileIndex], offset)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].FileIndex < list[j].FileIndex })
		refs[offset] = list
	}
	return refs, offByFile
}

// Snapshot returns detached copies of the raw index data for persistence.
func (idx *StringPoolIndex) Snapshot() ([]StringPoolEntry, map[int32][]StringPoolFileReference) {
	entries := make([]StringPoolEntry, 0, len(idx.entries))
	for _, entry := range idx.entries {
		entries = append(entries, entry.StringPoolEntry)
	}
	refs := make(map[int32][]StringPoolFileReference, len(idx.filesByOff))
	for offset, list := range idx.filesByOff {
		refs[offset] = append([]StringPoolFileReference(nil), list...)
	}
	return entries, refs
}

// NewStringPoolIndex rebuilds an index from Snapshot data. The reference
// slices in refs are copied so the new index owns its storage.
func NewStringPoolIndex(entries []StringPoolEntry, refs map[int32][]StringPoolFileReference) *StringPoolIndex {
	withLower := make([]stringPoolEntry, 0, len(entries))
	for _, entry := range entries {
		withLower = append(withLower, stringPoolEntry{StringPoolEntry: entry, lower: strings.ToLower(entry.Value)})
	}
	filesByOff := make(map[int32][]StringPoolFileReference, len(refs))
	offByFile := make(map[int32][]int32)
	for offset, list := range refs {
		copied := append([]StringPoolFileReference(nil), list...)
		filesByOff[offset] = copied
		for _, ref := range copied {
			offByFile[ref.FileIndex] = append(offByFile[ref.FileIndex], offset)
		}
	}
	return &StringPoolIndex{entries: withLower, filesByOff: filesByOff, offByFile: offByFile}
}

// RefreshFiles rescans the given files against the current archive and returns
// an updated copy. The receiver is left unchanged so concurrent readers stay
// consistent. Callers must serialize access to the archive (the services layer
// holds its write lock around this call).
func (idx *StringPoolIndex) RefreshFiles(a *Archive, indexes []int32) (*StringPoolIndex, error) {
	entries := a.stringPoolEntries()
	validOffsets := make(map[int32]struct{}, len(entries))
	for _, entry := range entries {
		validOffsets[entry.Offset] = struct{}{}
	}

	next := &StringPoolIndex{
		entries:    entries,
		filesByOff: make(map[int32][]StringPoolFileReference, len(idx.filesByOff)),
		offByFile:  make(map[int32][]int32, len(idx.offByFile)),
	}
	for offset, list := range idx.filesByOff {
		next.filesByOff[offset] = list
	}
	for fileIndex, offsets := range idx.offByFile {
		next.offByFile[fileIndex] = offsets
	}

	seen := make(map[int32]struct{}, len(indexes))
	for _, index := range indexes {
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}

		// Drop this file's previous references.
		for _, offset := range idx.offByFile[index] {
			list := next.filesByOff[offset]
			kept := make([]StringPoolFileReference, 0, len(list))
			for _, ref := range list {
				if ref.FileIndex != index {
					kept = append(kept, ref)
				}
			}
			if len(kept) == 0 {
				delete(next.filesByOff, offset)
			} else {
				next.filesByOff[offset] = kept
			}
		}

		// Rescan and re-add.
		raw, err := a.RawBytes(index)
		if err != nil {
			return nil, err
		}
		local := make(map[int32]map[int32]*StringPoolFileReference)
		collectStringPoolRefs(a, index, raw, validOffsets, local)
		for offset, byFile := range local {
			ref := byFile[index]
			if ref == nil {
				continue
			}
			copyRef := *ref
			copyRef.TokenTypes = append([]int32(nil), ref.TokenTypes...)
			copyRef.FileFields = append([]string(nil), ref.FileFields...)
			next.filesByOff[offset] = append(append([]StringPoolFileReference(nil), next.filesByOff[offset]...), copyRef)
		}
		next.offByFile[index] = sortedOffsetsForFile(local)
	}
	return next, nil
}

func sortedOffsetsForFile(local map[int32]map[int32]*StringPoolFileReference) []int32 {
	offsets := make([]int32, 0, len(local))
	for offset := range local {
		offsets = append(offsets, offset)
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	return offsets
}

// EncodeScriptQuery compiles readable script text using only existing string
// pool offsets. It never appends to the archive string pools.
func (a *Archive) EncodeScriptQuery(text string) ([]byte, error) {
	return a.encodeScriptWithResolver(text, func(value string) (int32, error) {
		a.cacheMu.Lock()
		defer a.cacheMu.Unlock()
		a.ensureStringIndexesLocked()
		if offset, ok := a.strAIdx[value]; ok {
			return offset, nil
		}
		if offset, ok := a.strWIdx[value]; ok {
			return offset, nil
		}
		return 0, ErrQueryStringNotInPool
	})
}

// ForEachRawFile visits every file with a decompressed payload. A chunk is
// decompressed once for the duration of its callbacks and is not added to the
// regular chunk cache. The callback must not retain raw after returning.
func (a *Archive) ForEachRawFile(ctx context.Context, fn func(index int32, path string, file File, raw []byte) bool) error {
	byChunk := make([][]int32, len(a.groups))
	var detached []int32
	for i, item := range a.items {
		index := int32(i)
		if item.chunk >= 0 && item.chunk < int32(len(byChunk)) {
			byChunk[item.chunk] = append(byChunk[item.chunk], index)
		} else {
			detached = append(detached, index)
		}
	}

	visit := func(index int32, chunk []byte) bool {
		item := &a.items[index]
		raw := a.overlay[index]
		if raw == nil && chunk != nil && item.off >= 0 && item.size >= 0 &&
			int64(item.off)+int64(item.size) <= int64(len(chunk)) {
			raw = chunk[item.off : item.off+item.size]
		}
		return fn(index, a.Path(index), a.File(index), raw)
	}

	for chunkIndex, indexes := range byChunk {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(indexes) == 0 {
			continue
		}
		chunk, err := a.decompressChunk(int32(chunkIndex))
		if err != nil {
			return err
		}
		for _, index := range indexes {
			if !visit(index, chunk) {
				return nil
			}
		}
	}

	for _, index := range detached {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !visit(index, nil) {
			return nil
		}
	}
	return nil
}

func (a *Archive) decompressChunk(ci int32) ([]byte, error) {
	if ci < 0 || ci >= int32(len(a.groups)) {
		return nil, nil
	}
	prev := int64(0)
	if ci > 0 {
		prev = int64(a.groups[ci-1].compSize)
	}
	cur := int64(a.groups[ci].compSize)
	size := cur - prev
	if size <= 0 || a.data == nil {
		return nil, nil
	}
	start := a.bodyOff + int(prev)
	end := a.bodyOff + int(cur)
	if start < 0 || start > end || end > len(a.data) {
		return nil, ErrOverflow
	}
	enc := make([]byte, size)
	copy(enc, a.data[start:end])
	cryptSeed(a.keys.body.seed, a.keys.body.magic, enc)
	raw, err := zlibDecompress(enc)
	if err == nil {
		return raw, nil
	}
	if a.keys.bodyRecovered {
		return nil, err
	}
	// Non-standard seed: chunk 0's zlib header gives two known plaintext bytes
	// and GRPI pins the inflated length, so the Body seed is recoverable once.
	first := a.firstChunkSpan()
	if first == nil {
		return nil, err
	}
	key, ok := recoverZlibSeed(first, int(a.groups[0].origSize))
	if !ok {
		return nil, err
	}
	a.keys.body = key
	a.keys.bodyRecovered = true
	copy(enc, a.data[start:end])
	cryptSeed(key.seed, key.magic, enc)
	return zlibDecompress(enc)
}

// firstChunkSpan returns the raw encrypted bytes of chunk 0.
func (a *Archive) firstChunkSpan() []byte {
	if len(a.groups) == 0 || a.data == nil {
		return nil
	}
	end := a.bodyOff + int(a.groups[0].compSize)
	if a.bodyOff < 0 || end > len(a.data) || end <= a.bodyOff {
		return nil
	}
	return a.data[a.bodyOff:end]
}

func (a *Archive) stringPoolEntries() []stringPoolEntry {
	entries := make([]stringPoolEntry, 0)
	for pos := 0; pos < len(a.strA); {
		end := bytes.IndexByte(a.strA[pos:], 0)
		valueEnd := len(a.strA)
		if end >= 0 {
			valueEnd = pos + end
		}
		if valueEnd > pos {
			value := string(a.strA[pos:valueEnd])
			entries = append(entries, stringPoolEntry{
				StringPoolEntry: StringPoolEntry{Pool: "sTrA", Offset: int32(pos << 1), Value: value},
				lower:           strings.ToLower(value),
			})
		}
		if end < 0 {
			break
		}
		pos = valueEnd + 1
	}

	for pos := 0; pos+1 < len(a.strW); {
		end := pos
		for end+1 < len(a.strW) && !(a.strW[end] == 0 && a.strW[end+1] == 0) {
			end += 2
		}
		if end > pos {
			value := readUTF16(a.strW, pos)
			entries = append(entries, stringPoolEntry{
				StringPoolEntry: StringPoolEntry{Pool: "sTrW", Offset: int32((pos>>1)<<1) | 1, Value: value},
				lower:           strings.ToLower(value),
			})
		}
		pos = end + 2
	}
	return entries
}

func appendUniqueInt32(values []int32, value int32) []int32 {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
