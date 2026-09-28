package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// rotatingWriter 是「按日期 + 按大小」滚动的日志文件写入器。
//
//   - 每天一个主文件：pvfine-2026-09-24.log
//   - 当天超过大小上限时滚动序号：pvfine-2026-09-24.1.log、.2.log ……
//   - 超过保留数量时删除最旧的文件（按文件名的日期与序号排序，再按修改时间兜底）
//
// 所有写操作串行化，可安全并发调用。
type rotatingWriter struct {
	dir      string
	baseName string // 不含日期与后缀，默认 pvfine
	maxBytes int64
	keep     int

	mu     sync.Mutex
	file   *os.File
	size   int64
	day    string
	seq    int
	path   string
	closed bool
}

func newRotatingWriter(dir, baseName string, maxBytes int64, keep int) *rotatingWriter {
	if baseName == "" {
		baseName = "pvfine"
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxSizeMB << 20
	}
	if keep <= 0 {
		keep = defaultKeep
	}
	return &rotatingWriter{dir: dir, baseName: baseName, maxBytes: maxBytes, keep: keep}
}

// Write 写入一条已格式化好的日志行，必要时先滚动。
func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// 关闭后拒绝写入：若允许继续写，会重新打开文件并留下未释放的句柄。
	if w.closed {
		return 0, os.ErrClosed
	}

	today := time.Now().Format("2006-01-02")
	if w.file == nil {
		if err := w.openLocked(today, 0); err != nil {
			return 0, err
		}
	} else if w.day != today {
		// 跨天：换新的主文件
		if err := w.rotateLocked(today, 0); err != nil {
			return 0, err
		}
	} else if w.size+int64(len(p)) > w.maxBytes {
		// 当天超限：滚动到下一个序号
		if err := w.rotateLocked(today, w.seq+1); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) openLocked(day string, seq int) error {
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return err
	}
	path := w.filePath(day, seq)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file = f
	w.size = info.Size()
	w.day = day
	w.seq = seq
	w.path = path
	return nil
}

func (w *rotatingWriter) rotateLocked(day string, seq int) error {
	if w.file != nil {
		_ = w.file.Sync()
		_ = w.file.Close()
		w.file = nil
	}
	if err := w.openLocked(day, seq); err != nil {
		return err
	}
	w.pruneLocked()
	return nil
}

func (w *rotatingWriter) filePath(day string, seq int) string {
	if seq <= 0 {
		return filepath.Join(w.dir, fmt.Sprintf("%s-%s.log", w.baseName, day))
	}
	return filepath.Join(w.dir, fmt.Sprintf("%s-%s.%d.log", w.baseName, day, seq))
}

type logFileInfo struct {
	path string
	day  string
	seq  int
	mod  time.Time
}

// pruneLocked 只保留最近 keep 个日志文件（含当前正在写的那个）。
func (w *rotatingWriter) pruneLocked() {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	prefix := w.baseName + "-"
	files := make([]logFileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		day, seq := parseLogFileName(e.Name(), w.baseName)
		files = append(files, logFileInfo{
			path: filepath.Join(w.dir, e.Name()),
			day:  day,
			seq:  seq,
			mod:  info.ModTime(),
		})
	}
	if len(files) <= w.keep {
		return
	}
	// 新的在前：日期大者优先，同日按序号大者优先，最后用修改时间兜底
	sort.Slice(files, func(i, j int) bool {
		if files[i].day != files[j].day {
			return files[i].day > files[j].day
		}
		if files[i].seq != files[j].seq {
			return files[i].seq > files[j].seq
		}
		return files[i].mod.After(files[j].mod)
	})
	for _, f := range files[w.keep:] {
		_ = os.Remove(f.path)
	}
}

// parseLogFileName 从 "pvfine-2026-09-24.3.log" 解析出日期与序号。
func parseLogFileName(name, baseName string) (string, int) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(name, baseName+"-"), ".log")
	if idx := strings.LastIndexByte(trimmed, '.'); idx >= 0 {
		if seq, err := strconv.Atoi(trimmed[idx+1:]); err == nil {
			return trimmed[:idx], seq
		}
	}
	return trimmed, 0
}

// Close 关闭当前文件；可重复调用。
func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.file == nil {
		return nil
	}
	err := w.file.Sync()
	if closeErr := w.file.Close(); err == nil {
		err = closeErr
	}
	w.file = nil
	return err
}

// CurrentPath 返回当前正在写入的文件路径（可能为空）。
func (w *rotatingWriter) CurrentPath() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.path
}
