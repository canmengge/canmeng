// Package apppaths 统一决定「可再生的缓存/索引」落盘位置。
//
// 目的：把搜索索引、图片索引、日志等**删了会自动重建**的文件集中到一个目录，
// 用户想清理时直接删掉整个目录即可，不会误伤配置（settings/bookmarks/脚本）
// 与版本数据（PVF 旁的 .pvfine 版本库）。
//
// 解析优先级（2026-09-24 按用户要求：集中到 pvfine-main\HC）：
//  1. 环境变量 PVFINE_CACHE_DIR（显式指定，最高优先）
//  2. <exe 同目录>\pvfine-main\HC   —— 当前部署形态（exe 在 04-运行环境\，
//     数据在同级 pvfine-main\），便于「要删就删 HC」
//  3. <exe 同目录>\HC               —— exe 被单独拷走时的退化位置
//  4. os.UserCacheDir()\pvfine       —— 兜底（前两者都不可写时）
//
// 目录按需创建。任何一步失败都不会让程序崩溃：调用方拿到错误后自行降级。
package apppaths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// CacheDirName 是集中缓存目录的默认名字。
const CacheDirName = "HC"

// EnvCacheDir 允许用环境变量覆盖缓存目录。
const EnvCacheDir = "PVFINE_CACHE_DIR"

var (
	cacheMu   sync.Mutex
	cacheOnce bool
	cachePath string
	cacheErr  error
)

// CacheDir 返回缓存根目录（已确保存在可写）。
func CacheDir() (string, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cacheOnce {
		return cachePath, cacheErr
	}
	cacheOnce = true
	cachePath, cacheErr = resolveCacheDir()
	if cacheErr == nil {
		writeReadmeIfMissing(cachePath)
	}
	return cachePath, cacheErr
}

// readmeName 是放在缓存根目录里的说明文件，便于用户放心整体删除。
const readmeName = "README-可删除.txt"

const readmeBody = `本目录（HC）存放 pvfine 编辑器可再生的缓存与索引，可以随时整体删除。

内容说明：
  search-index\        搜索索引缓存（*.json.gz，按 PVF 路径哈希命名）
  logs\                运行日志（pvfine-YYYY-MM-DD.log，按日期与大小滚动）
  npk-image-index.json NPK 图片索引缓存

删除影响：
  - 搜索索引：下次打开 PVF 会重建（首次搜索/索引稍慢），之后照常命中缓存；
  - 日志：历史记录消失，仅影响排查问题；
  - 图片索引：下次启动 NPK 目录时重建。
  配置（settings.json / bookmarks.json / file-sets.json / 脚本）和 PVF 版本数据
  都不在本目录，删除本目录不会丢设置、也不会改动任何 PVF。

想指定其它位置：设环境变量 PVFINE_CACHE_DIR=<目录> 后再启动程序。
`

func writeReadmeIfMissing(dir string) {
	if dir == "" {
		return
	}
	path := filepath.Join(dir, readmeName)
	if _, err := os.Stat(path); err == nil {
		return
	}
	_ = os.WriteFile(path, []byte(readmeBody), 0o644)
}

// SubDir 返回缓存根目录下的子目录（已确保存在可写），例如 SubDir("search-index")。
func SubDir(name string) (string, error) {
	root, err := CacheDir()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		return root, nil
	}
	dir := filepath.Join(root, name)
	if err := ensureWritableDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// CacheDirOrFallback 在缓存目录不可用时返回系统缓存目录，避免调用方完全失去缓存能力。
func CacheDirOrFallback() string {
	if dir, err := CacheDir(); err == nil && dir != "" {
		return dir
	}
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "pvfine")
	}
	return ""
}

// DescribeRoot 返回缓存根目录与选择来源，供启动日志记录（排查"缓存写到哪了"）。
func DescribeRoot() (string, string) {
	if env := strings.TrimSpace(os.Getenv(EnvCacheDir)); env != "" {
		if err := ensureWritableDir(env); err == nil {
			return env, "env:" + EnvCacheDir
		}
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		inner := filepath.Join(exeDir, "pvfine-main", CacheDirName)
		if err := ensureWritableDir(inner); err == nil {
			return inner, "程序目录\\pvfine-main\\" + CacheDirName
		}
		flat := filepath.Join(exeDir, CacheDirName)
		if err := ensureWritableDir(flat); err == nil {
			return flat, "程序目录\\" + CacheDirName
		}
	}
	if dir, err := os.UserCacheDir(); err == nil {
		fallback := filepath.Join(dir, "pvfine")
		if err := ensureWritableDir(fallback); err == nil {
			return fallback, "系统缓存目录"
		}
	}
	return "", "没有可写目录"
}

func resolveCacheDir() (string, error) {
	dir, source := DescribeRoot()
	if dir == "" {
		return "", errors.New("没有可写的缓存目录（" + source + "）")
	}
	return dir, nil
}

// ensureWritableDir 创建目录并验证可写（仅 MkdirAll 无法发现只读目录）。
func ensureWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".write-probe-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}
