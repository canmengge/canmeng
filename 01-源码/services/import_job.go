package services

import (
	"errors"
	"sync/atomic"
)

// ErrImportCancelled 表示本次导入被用户取消。取消发生在「活动归档被改动之前」，
// 因此活动归档保持原样（变更都写在独立的 stage 上）。
var ErrImportCancelled = errors.New("导入已取消")

// ErrImportStale 表示准备阶段期间归档被其它操作改动，本次导入放弃安装。
// 同样不会改动活动归档。
var ErrImportStale = errors.New("导入准备期间归档已被其它操作修改，请重试")

// importProgressStep 控制进度事件频率（按文件数），避免大目录刷屏。
const importProgressStep = 256

// ImportProgress 是导入进度快照：既是事件 `import:progress` 的载荷，
// 也是 ImportStatus() 的返回值。
//
// phase: scan（扫描目录）/ read（读取文件）/ stage（写入暂存归档）/
// index（整理索引）/ install（安装）/ done。
type ImportProgress struct {
	Phase   string `json:"phase"`
	Scanned int    `json:"scanned"`
	Total   int    `json:"total"`
	Bytes   int64  `json:"bytes"`
	Running bool   `json:"running"`
}

// importJob 承载一次导入的「取消开关 + 进度」。
type importJob struct {
	cancel   atomic.Bool
	progress atomic.Value
}

func newImportJob() *importJob {
	job := &importJob{}
	job.progress.Store(ImportProgress{})
	return job
}

// cancelled 报告是否已请求取消（nil 接收者视为未取消，便于测试直接传 nil）。
func (j *importJob) cancelled() bool {
	if j == nil {
		return false
	}
	return j.cancel.Load()
}

// set 更新进度并广播事件。
func (j *importJob) set(progress ImportProgress) {
	if j == nil {
		return
	}
	progress.Running = true
	j.progress.Store(progress)
	emitEvent("import:progress", progress)
}

// tick 上报一次进度；已请求取消时返回 ErrImportCancelled。
func (j *importJob) tick(phase string, scanned, total int, bytes int64, force bool) error {
	if j == nil {
		return nil
	}
	if j.cancelled() {
		return ErrImportCancelled
	}
	if !force && scanned%importProgressStep != 0 {
		return nil
	}
	j.set(ImportProgress{Phase: phase, Scanned: scanned, Total: total, Bytes: bytes})
	return nil
}

// snapshot 返回当前进度（nil 安全）。
func (j *importJob) snapshot() ImportProgress {
	if j == nil {
		return ImportProgress{}
	}
	if value, ok := j.progress.Load().(ImportProgress); ok {
		return value
	}
	return ImportProgress{}
}

// beginImport 开始一次导入；已有任务在跑时直接拒绝（避免两个 stage 同时安装）。
func (s *ArchiveService) beginImport() (*importJob, error) {
	s.importMu.Lock()
	defer s.importMu.Unlock()
	if s.importJob != nil {
		return nil, errors.New("已有导入任务在进行中")
	}
	job := newImportJob()
	s.importJob = job
	return job, nil
}

// endImport 结束一次导入并广播收尾事件。
func (s *ArchiveService) endImport(job *importJob) {
	s.importMu.Lock()
	if s.importJob == job {
		s.importJob = nil
	}
	s.importMu.Unlock()
	job.set(ImportProgress{Phase: "done"})
}

// CancelImport 请求取消正在进行的导入。
//
// 取消点只在「扫描 / 读取 / 写入暂存归档」阶段；这三步都发生在活动归档被改动之前，
// 因此取消后活动归档与索引都保持原样。
func (s *ArchiveService) CancelImport() error {
	s.importMu.Lock()
	job := s.importJob
	s.importMu.Unlock()
	if job == nil {
		return errors.New("当前没有正在进行的导入")
	}
	job.cancel.Store(true)
	return nil
}

// ImportStatus 返回当前/最近一次导入的进度（供晚打开的窗口与轮询使用）。
func (s *ArchiveService) ImportStatus() ImportProgress {
	s.importMu.Lock()
	job := s.importJob
	s.importMu.Unlock()
	progress := job.snapshot()
	progress.Running = job != nil
	return progress
}
