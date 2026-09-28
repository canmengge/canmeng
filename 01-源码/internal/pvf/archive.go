package pvf

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"pvfine/internal/rendering"
)

// Header is the decrypted 48-byte archive header (packed, little-endian).
type Header struct {
	Signature     uint32
	Guid          [20]byte
	FileCount     int32
	Padding       int32
	BodySize      int32
	GroupCount    int32
	HashTableSize int32
	NameTableSize int32
}

type fileItem struct {
	nameOff, pathOff int32 // magic offsets into the string pools
	chunk            int32 // body chunk index
	off, size        int32 // location of the file payload inside the decompressed chunk
	typ              int32 // TypeScript / TypeUnicode
}

type removedFileSpan struct {
	off, size int32
}

type groupItem struct{ compSize, origSize int32 }

// Archive is a parsed PVF container. It is not safe for concurrent
// modification, but Chunk decompression is cached and read-only use of
// accessors from multiple goroutines is fine.
type Archive struct {
	cacheMu sync.Mutex

	data  []byte // original file bytes (nil for archives built from scratch)
	hdr   Header
	guard bool
	keys  keySet // per-section LCG seeds (standard or recovered)

	// paged110 marks a container whose 10 MiB page guards were unlocked with
	// the sidecar key files. pageKeys is the unwrapped 32-byte-per-page key
	// table used to re-apply the guards when the archive is written back.
	paged110 bool
	pageKeys []byte

	sourcePath string // file the archive was opened from

	tableOff, hashOff, nameOff, grpiOff, bodyOff int
	hashSize, nameSize, grpiSize                 int

	items  []fileItem
	groups []groupItem // decrypted GRPI

	strA, strW       []byte // decompressed string pools (UTF-8 / UTF-16LE)
	strAIdx, strWIdx map[string]int32
	poolsDirty       bool // pools gained appended strings since parse

	// 读缓存采用「两段滚动」：新条目进 current 段；current 段写满阈值就把 old 段
	// 整体丢弃（一次性释放，O(1)，无需排序），再把 current 变成 old。命中 old 段
	// 的条目会被搬回 current（只搬引用，不复制数据），于是热数据自动留在常驻段。
	//
	// 这样既能保留随机读（打开文件 / 预览）的命中率，又不会让索引构建、保存这类
	// 顺序扫描把整包解压后的内容（可达数 GB）永久堆在内存里 —— 顺序扫描本来就不
	// 复用缓存，淘汰它对速度没有影响。
	resolveCache    map[int32]string // current 段
	resolveCacheOld map[int32]string // 上一批，滚动时整体丢弃
	resolveCount    int              // current 段条数
	chunkCache      map[int32][]byte // current 段
	chunkCacheOld   map[int32][]byte // 上一批，滚动时整体丢弃
	chunkCacheBytes int64            // current 段字节数
	overlay         map[int32][]byte // index -> replacement payload
	pathIndex       map[string]int32
	structuralDirty bool // file entries were added or removed since the last save
	removedSpans    map[int32][]removedFileSpan

	// scriptRenderer controls the user-facing decompiled layout. The
	// canonical renderer is kept stable so version content hashes do not
	// depend on presentation-only configuration.
	scriptRenderer          *rendering.Engine
	canonicalScriptRenderer *rendering.Engine

	// tables caches the lazily loaded string tables used to resolve
	// `<index::key>` placeholders in item names.
	tables struct {
		mu    sync.Mutex
		state *stringTableState
	}
}

// Open reads and parses the archive at path. Newer Paged110 containers keep
// their per-page keys in sibling "sk.dat" / "DFO.exe" files, so the directory
// of path is passed to the parser for sidecar lookup.
func Open(path string) (*Archive, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	a, err := parse(data, filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	a.sourcePath = path
	return a, nil
}

// Parse parses an archive from memory. The byte slice is retained; treat it
// as read-only afterwards. Container variants that need sidecar key files
// cannot be opened this way; use Open for those.
func Parse(data []byte) (*Archive, error) {
	return parse(data, "")
}

func parse(data []byte, sidecarDir string) (*Archive, error) {
	if len(data) < headerSize {
		return nil, ErrTruncated
	}
	renderer := defaultScriptRenderer()
	a := &Archive{
		data:                    data,
		resolveCache:            map[int32]string{},
		chunkCache:              map[int32][]byte{},
		overlay:                 map[int32][]byte{},
		pathIndex:               map[string]int32{},
		removedSpans:            make(map[int32][]removedFileSpan),
		scriptRenderer:          renderer,
		canonicalScriptRenderer: renderer,
	}

	var raw [headerSize]byte
	copy(raw[:], data[:headerSize])
	// The guard XOR only touches bytes [24:28] (the FileCount field), so the
	// signature alone cannot tell the variants apart — the decoded section
	// layout must validate as well.
	a.keys = standardKeys()
	found := false
	for _, guard := range [...]bool{true, false} {
		b := raw
		if guard {
			applyGuard(b[:])
		}
		for _, magic := range [...]uint32{magicMain, magicAlt} {
			bb := b
			crypt(keyHead, magic, bb[:])
			if binary.LittleEndian.Uint32(bb[:]) != MagicSignature {
				continue
			}
			hdr := decodeHeader(bb)
			if hdr.FileCount < 0 || hdr.Padding < 0 || hdr.BodySize < 0 ||
				hdr.GroupCount < 0 || hdr.HashTableSize < 0 || hdr.NameTableSize < 0 {
				continue
			}
			declared := int64(headerSize) + int64(hdr.FileCount)*0x18 +
				int64(hdr.HashTableSize) + int64(hdr.NameTableSize) +
				int64(hdr.GroupCount)*8 + int64(hdr.BodySize)
			if declared > int64(len(data)) {
				continue
			}
			a.guard = guard
			a.hdr = hdr
			a.keys.header = sectionKey{keySeed(keyHead), magic}
			found = true
			break
		}
		if found {
			break
		}
	}
	if !found {
		// Variant archives derive their section seeds differently. The header
		// is recovered from the signature plus the section-size equation, then
		// the remaining seeds are recovered from the sections themselves.
		hdr, guard, keys, ok := recoverHeader(data)
		if !ok {
			// Newest Paged110 containers: the page guards are AES encrypted and
			// the section keys use the newer scheme, so the header is only
			// readable after the guard pages have been unlocked with the
			// sidecar key files (sk.dat / DFO.exe).
			if dec, pageKeys, pagedKeys, phdr, ok := unlockPaged110(data, sidecarDir); ok {
				data = dec
				a.data = dec
				a.paged110 = true
				a.pageKeys = pageKeys
				a.keys = pagedKeys
				a.hdr = phdr
				a.guard = false
				found = true
			} else if hasSealedKeyFile(sidecarDir) {
				// The sidecar keys are right there but did not unlock this
				// archive: say so instead of blaming the header.
				return nil, ErrPaged110Keys
			} else if len(data) >= paged110PageSize {
				// 标准/变体解析都失败、旁边又没有 sk.dat，且体量达到分页容器下限：
				// 极可能是 110US 分页归档缺了 sk.dat（打开失败最常见的原因），
				// 给一个能让人看懂的错误，而不是笼统的"签名无效"。
				return nil, ErrPaged110MissingKeys
			}
		}
		if !ok && !found {
			return nil, ErrBadSignature
		}
		if ok {
			a.guard = guard
			a.hdr = hdr
			a.keys = keys
		}
	}

	// Section layout.
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

	declaredEnd := pos + int(a.hdr.BodySize)
	if declaredEnd > len(data) {
		return nil, ErrOverflow
	}

	// File table (plaintext).
	a.items = make([]fileItem, a.hdr.FileCount)
	for i := range a.items {
		it := &a.items[i]
		base := a.tableOff + i*0x18
		it.nameOff = int32(binary.LittleEndian.Uint32(data[base:]))
		it.pathOff = int32(binary.LittleEndian.Uint32(data[base+4:]))
		it.chunk = int32(binary.LittleEndian.Uint32(data[base+8:]))
		it.off = int32(binary.LittleEndian.Uint32(data[base+12:]))
		it.size = int32(binary.LittleEndian.Uint32(data[base+16:]))
		it.typ = int32(binary.LittleEndian.Uint32(data[base+20:]))
	}

	// GRPI (cumulative compressed chunk sizes).
	if a.grpiSize > 0 {
		grpiRaw := data[a.grpiOff : a.grpiOff+a.grpiSize]
		grpi := make([]byte, a.grpiSize)
		copy(grpi, grpiRaw)
		cryptSeed(a.keys.grpi.seed, a.keys.grpi.magic, grpi)
		if !a.grpiLooksSane(grpi) {
			// Non-standard seed: recover it from the BodySize anchor and the
			// monotonicity invariants of the cumulative size table.
			if key, ok := recoverGRPISeed(grpiRaw, int(a.hdr.GroupCount), a.hdr.BodySize); ok {
				a.keys.grpi = key
				copy(grpi, grpiRaw)
				cryptSeed(key.seed, key.magic, grpi)
			}
		}
		a.groups = make([]groupItem, a.hdr.GroupCount)
		for i := range a.groups {
			a.groups[i].compSize = int32(binary.LittleEndian.Uint32(grpi[i*8:]))
			a.groups[i].origSize = int32(binary.LittleEndian.Uint32(grpi[i*8+4:]))
		}
	}

	// String pools.
	a.parseNameTable(data[a.nameOff : a.nameOff+a.nameSize])

	// Hash section seed: known for the standard key set and the alternate
	// variant family. When it is not (an unidentified variant, the Paged110
	// containers), solve it from the section itself now that the string pools
	// are available to validate candidate offsets.
	if a.keys.hash.seed == 0 && a.hashSize > 0 {
		if key, ok := a.recoverHashSeed(data[a.hashOff:a.hashOff+a.hashSize], int(a.hdr.FileCount)); ok {
			a.keys.hash = key
		}
	}

	// Body seed: for variants the standard key fails, and chunk 0's zlib
	// header plus GRPI's original size let it be recovered from the data.
	if !a.bodyKeyWorks() {
		if key, ok := recoverZlibSeed(a.firstChunkSpan(), a.firstChunkOrigSize()); ok {
			a.keys.body = key
			a.keys.bodyRecovered = true
		}
	}

	// Path index (case-insensitive, mirrors the reference GM tool).
	for i := range a.items {
		p := normalizePath(a.Path(int32(i)))
		if _, exists := a.pathIndex[p]; !exists {
			a.pathIndex[p] = int32(i)
		}
	}
	return a, nil
}

// New returns an empty archive ready for AddFile + SaveTo.
func New() *Archive {
	renderer := defaultScriptRenderer()
	return &Archive{
		hdr:                     Header{Signature: MagicSignature},
		keys:                    standardKeys(),
		strA:                    []byte{0},
		strW:                    []byte{0, 0},
		poolsDirty:              true,
		resolveCache:            map[int32]string{},
		chunkCache:              map[int32][]byte{},
		overlay:                 map[int32][]byte{},
		pathIndex:               map[string]int32{},
		removedSpans:            make(map[int32][]removedFileSpan),
		scriptRenderer:          renderer,
		canonicalScriptRenderer: renderer,
	}
}

func defaultScriptRenderer() *rendering.Engine {
	engine, _ := rendering.LoadDefault()
	return engine
}

func decodeHeader(b [headerSize]byte) Header {
	return Header{
		Signature:     binary.LittleEndian.Uint32(b[0:]),
		FileCount:     int32(binary.LittleEndian.Uint32(b[24:])),
		Padding:       int32(binary.LittleEndian.Uint32(b[28:])),
		BodySize:      int32(binary.LittleEndian.Uint32(b[32:])),
		GroupCount:    int32(binary.LittleEndian.Uint32(b[36:])),
		HashTableSize: int32(binary.LittleEndian.Uint32(b[40:])),
		NameTableSize: int32(binary.LittleEndian.Uint32(b[44:])),
	}
}

// Header returns the (decrypted) archive header.
func (a *Archive) Header() Header { return a.hdr }

// UsesGuard reports whether the archive header uses the 0x55 guard variant.
func (a *Archive) UsesGuard() bool { return a.guard }

// IsPaged110 reports whether the archive uses the 110US page-guard layout.
func (a *Archive) IsPaged110() bool { return a.paged110 }

// FileCount returns the number of file entries.
func (a *Archive) FileCount() int32 { return int32(len(a.items)) }

// File describes one entry.
type File struct {
	Index      int32
	Name, Path string // resolved from the string pools; Path is the directory part
	ChunkIndex int32
	DataOffset int32
	DataSize   int32
	DataType   int32
}

// File returns entry i.
func (a *Archive) File(i int32) File {
	it := &a.items[i]
	size := it.size
	if payload, ok := a.overlay[i]; ok {
		size = int32(len(payload))
	}
	return File{
		Index:      i,
		Name:       a.ResolveString(it.nameOff),
		Path:       a.ResolveString(it.pathOff),
		ChunkIndex: it.chunk,
		DataOffset: it.off,
		DataSize:   size,
		DataType:   it.typ,
	}
}

// FullPath returns the canonical "dir/name" path of entry i.
func (a *Archive) FullPath(i int32) string {
	it := &a.items[i]
	return joinPath(a.ResolveString(it.pathOff), a.ResolveString(it.nameOff))
}

// Path is an alias of FullPath.
func (a *Archive) Path(i int32) string { return a.FullPath(i) }

// Find looks up an entry by "dir/name" path (case-insensitive).
func (a *Archive) Find(path string) (int32, bool) {
	i, ok := a.pathIndex[normalizePath(path)]
	return i, ok
}

// FindList looks up a `.lst` index across client layouts. The 90US clients keep
// a list next to the files it indexes (`equipment/equipment.lst`), while the
// 110US clients collect every list under `list/` (`list/equipment.lst`) and
// store archive-root-relative entry paths inside it. Both layouts are tried in
// that order, so a configured 90US path also resolves on a newer archive.
func (a *Archive) FindList(path string) (int32, bool) {
	for _, candidate := range listLookupCandidates(path) {
		if i, ok := a.Find(candidate); ok {
			return i, true
		}
	}
	return 0, false
}

func listLookupCandidates(path string) []string {
	trimmed := strings.Trim(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"), "/")
	if trimmed == "" {
		return nil
	}
	candidates := []string{trimmed}
	base := trimmed
	if slash := strings.LastIndexByte(base, '/'); slash >= 0 {
		base = base[slash+1:]
	}
	if base == "" || strings.EqualFold(base, trimmed) {
		return candidates
	}
	candidates = append(candidates, "list/"+base)
	// A configured `list/x.lst` also names the 90US location `<stem>/x.lst`,
	// e.g. `list/equipment.lst` -> `equipment/equipment.lst`.
	if dir := trimmed[:strings.LastIndexByte(trimmed, '/')]; strings.EqualFold(dir, "list") {
		stem := strings.TrimSuffix(base, pathExt(base))
		if stem != "" {
			candidates = append(candidates, stem+"/"+base)
		}
	}
	return candidates
}

func pathExt(name string) string {
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		return name[dot:]
	}
	return ""
}

// 读缓存上限。current 段达到上限的一半即滚动，故常驻峰值 ≈ 上限值。
const (
	chunkReadCacheLimit = 512 << 20 // 解压块：512 MB
	resolveReadCacheMax = 2_500_000 // 字符串解析：250 万条
)

// Chunk returns decompressed chunk ci, caching the result. The cache is bounded
// (two-segment rolling eviction); see the Archive field docs.
func (a *Archive) Chunk(ci int32) ([]byte, error) {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()
	if ch, ok := a.chunkCache[ci]; ok {
		return ch, nil
	}
	if ch, ok := a.chunkCacheOld[ci]; ok {
		delete(a.chunkCacheOld, ci)
		a.chunkStore(ci, ch)
		return ch, nil
	}
	raw, err := a.decompressChunk(ci)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	a.chunkStore(ci, raw)
	return raw, nil
}

// chunkStore 写入 current 段并在超阈值时滚动。调用前必须持有 cacheMu。
func (a *Archive) chunkStore(ci int32, raw []byte) {
	if a.chunkCache == nil {
		a.chunkCache = make(map[int32][]byte)
	}
	a.chunkCache[ci] = raw
	a.chunkCacheBytes += int64(len(raw))
	if a.chunkCacheBytes < chunkReadCacheLimit/2 {
		return
	}
	a.chunkCacheOld = a.chunkCache
	a.chunkCacheBytes = 0
	a.chunkCache = make(map[int32][]byte, len(a.chunkCacheOld)/2+1)
}

// resolveStore 写入字符串解析缓存的 current 段并按条数滚动。持有 cacheMu 时调用。
func (a *Archive) resolveStore(off int32, s string) {
	if a.resolveCache == nil {
		a.resolveCache = make(map[int32]string)
	}
	a.resolveCache[off] = s
	a.resolveCount++
	if a.resolveCount < resolveReadCacheMax/2 {
		return
	}
	a.resolveCacheOld = a.resolveCache
	a.resolveCount = 0
	a.resolveCache = make(map[int32]string, len(a.resolveCacheOld)/2+1)
}

// ReleaseReadCaches 丢弃所有解压块与字符串解析缓存（纯缓存，丢弃不影响正确性）。
// 在「索引构建完成」「关闭归档」这些一次性节点调用，把峰值内存还回系统。
func (a *Archive) ReleaseReadCaches() {
	a.cacheMu.Lock()
	a.chunkCache = make(map[int32][]byte)
	a.chunkCacheOld = nil
	a.chunkCacheBytes = 0
	a.resolveCache = make(map[int32]string)
	a.resolveCacheOld = nil
	a.resolveCount = 0
	a.cacheMu.Unlock()
}

// ReadCacheStats 返回读缓存当前的规模（诊断用）。
func (a *Archive) ReadCacheStats() (chunkBytes int64, chunkEntries, resolveEntries int) {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()
	chunkBytes = a.chunkCacheBytes
	for _, raw := range a.chunkCacheOld {
		chunkBytes += int64(len(raw))
	}
	return chunkBytes, len(a.chunkCache) + len(a.chunkCacheOld),
		len(a.resolveCache) + len(a.resolveCacheOld)
}

// chunkSpan returns the raw (still encrypted) body byte range of chunk ci.
func (a *Archive) chunkSpan(ci int32) ([]byte, bool) {
	if ci < 0 || ci >= int32(len(a.groups)) {
		return nil, false
	}
	prev := int64(0)
	if ci > 0 {
		prev = int64(a.groups[ci-1].compSize)
	}
	start := a.bodyOff + int(prev)
	end := a.bodyOff + int(a.groups[ci].compSize)
	if start > end || end > len(a.data) {
		return nil, false
	}
	return a.data[start:end], true
}

// normalizePath mirrors the reference implementation's path normalization.
func normalizePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimSpace(p)
	for strings.HasPrefix(p, "./") || strings.HasPrefix(p, "/") {
		if strings.HasPrefix(p, "./") {
			p = p[2:]
		} else {
			p = p[1:]
		}
	}
	return strings.ToLower(strings.TrimRight(p, "/"))
}

// joinPath joins raw pool strings for display. No normalization: entries
// may legitimately contain odd whitespace (the reference implementation
// preserves it and only normalizes lookup keys).
func joinPath(dir, name string) string {
	switch {
	case dir == "":
		return name
	case name == "":
		return dir
	default:
		return dir + "/" + name
	}
}

// ArchiveInfoView is a UI-facing snapshot of the archive state.
type ArchiveInfoView struct {
	Path          string `json:"path"`
	FileCount     int32  `json:"fileCount"`
	GroupCount    int32  `json:"groupCount"`
	BodySize      int32  `json:"bodySize"`
	ModifiedCount int    `json:"modifiedCount"`
	UsesGuard     bool   `json:"usesGuard"`
	Paged110      bool   `json:"paged110"`
}

// Info returns the current state snapshot.
func (a *Archive) Info() ArchiveInfoView {
	return ArchiveInfoView{
		Path:          a.sourcePath,
		FileCount:     int32(len(a.items)),
		GroupCount:    a.hdr.GroupCount,
		BodySize:      a.hdr.BodySize,
		ModifiedCount: a.ModifiedCount(),
		UsesGuard:     a.guard,
		Paged110:      a.paged110,
	}
}
