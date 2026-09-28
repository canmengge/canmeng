package services

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"pvfine/internal/apppaths"
	"pvfine/internal/pvf"
)

const advancedIndexCacheVersion = 1

// persistedAdvancedIndex is the on-disk form of a string reverse index.
type persistedAdvancedIndex struct {
	Version        int                          `json:"version"`
	ArchivePath    string                       `json:"archivePath"`
	SourceSize     int64                        `json:"sourceSize"`
	SourceModified int64                        `json:"sourceModified"`
	FileCount      int32                        `json:"fileCount"`
	GroupCount     int32                        `json:"groupCount"`
	BodySize       int32                        `json:"bodySize"`
	Paged110       bool                         `json:"paged110"`
	Entries        []pvf.StringPoolEntry        `json:"entries"`
	Refs           map[int32][]pvf.StringPoolFileReference `json:"refs"`
}

func advancedIndexCachePathForArchive(a *pvf.Archive) (string, error) {
	identity, err := searchIndexCacheIdentityForArchive(a)
	if err != nil {
		return "", err
	}
	return advancedIndexCachePath(identity.ArchivePath)
}

func advancedIndexCachePath(sourcePath string) (string, error) {
	canonical, err := canonicalSearchIndexPath(sourcePath)
	if err != nil {
		return "", err
	}
	cacheDir, err := apppaths.SubDir("search-index")
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return filepath.Join(cacheDir, hex.EncodeToString(sum[:])+".advanced.json.gz"), nil
}

func (c *core) loadAdvancedIndexCache(a *pvf.Archive) (*pvf.StringPoolIndex, bool) {
	// Never reuse the cache when there are unsaved edits.
	if a.Modified() {
		return nil, false
	}
	identity, err := searchIndexCacheIdentityForArchive(a)
	if err != nil {
		return nil, false
	}
	path, err := advancedIndexCachePath(identity.ArchivePath)
	if err != nil {
		return nil, false
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return nil, false
	}
	var persisted persistedAdvancedIndex
	if err := json.NewDecoder(reader).Decode(&persisted); err != nil {
		return nil, false
	}
	if err := reader.Close(); err != nil {
		return nil, false
	}
	if persisted.Version != advancedIndexCacheVersion ||
		persisted.ArchivePath != identity.ArchivePath ||
		persisted.SourceSize != identity.SourceSize ||
		persisted.SourceModified != identity.SourceModified ||
		persisted.FileCount != identity.FileCount ||
		persisted.GroupCount != identity.GroupCount ||
		persisted.BodySize != identity.BodySize ||
		persisted.Paged110 != identity.Paged110 {
		return nil, false
	}
	if persisted.Entries == nil || persisted.Refs == nil {
		return nil, false
	}
	return pvf.NewStringPoolIndex(persisted.Entries, persisted.Refs), true
}

func persistAdvancedIndexAsync(c *core, a *pvf.Archive, index *pvf.StringPoolIndex) {
	// Skip caching indexes that carry unsaved edits.
	c.mu.RLock()
	if c.archive != a || c.advancedStatus.State != AdvancedIndexStateReady || a.Modified() {
		c.mu.RUnlock()
		return
	}
	identity, err := searchIndexCacheIdentityForArchive(a)
	if err != nil {
		c.mu.RUnlock()
		return
	}
	entries, refs := index.Snapshot()
	c.mu.RUnlock()

	go saveAdvancedIndexCache(identity, entries, refs)
}

func saveAdvancedIndexCache(identity searchIndexCacheIdentity, entries []pvf.StringPoolEntry, refs map[int32][]pvf.StringPoolFileReference) error {
	path, err := advancedIndexCachePath(identity.ArchivePath)
	if err != nil {
		return err
	}
	data := persistedAdvancedIndex{
		Version:        advancedIndexCacheVersion,
		ArchivePath:    identity.ArchivePath,
		SourceSize:     identity.SourceSize,
		SourceModified: identity.SourceModified,
		FileCount:      identity.FileCount,
		GroupCount:     identity.GroupCount,
		BodySize:       identity.BodySize,
		Paged110:       identity.Paged110,
		Entries:        entries,
		Refs:           refs,
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".advanced-index-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	writer := gzip.NewWriter(temp)
	if err := json.NewEncoder(writer).Encode(data); err != nil {
		_ = writer.Close()
		_ = temp.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
