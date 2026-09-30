package services

import (
	"errors"
	"sync"
	"sync/atomic"

	"pvfine/internal/pvf"
)

// ErrSaveCancelled 表示本次保存被用户取消。
//
// 取消只发生在「临时文件 rename 之前」（见 internal/pvf 的 SaveAsHooked），
// 因此源文件保持原样 —— 与导入取消同一套语义（见 import_job.go）。
var ErrSaveCancelled = errors.New("保存已取消")

// SaveProgress 是保存进度快照：既是事件 `save:progress` 的载荷，
// 也是 SaveStatus() 的返回值。
//
// phase: prepare（准备）/ backup（备份源文件）/ rebuild（重建逻辑字节）/
// write（写盘）/ sync（刷盘）/ rename（替换源文件）/ done。
type SaveProgress struct {
	Phase   string `json:"phase"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Running bool   `json:"running"`
}

// saveJob 承载一次保存的「取消开关 + 进度」。
type saveJob struct {
	cancel   atomic.Bool
	progress atomic.Value
	mu       sync.Mutex
	stage    string
}

func newSaveJob() *saveJob {
	job := &saveJob{}
	job.progress.Store(SaveProgress{})
	return job
}

// phase 上报阶段切换（不带数量）。
func (j *saveJob) phase(name string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	j.stage = name
	j.mu.Unlock()
	j.set(SaveProgress{Phase: name})
}

// units 上报当前阶段内的进度（已处理单位 / 总单位）。
func (j *saveJob) units(done, total int) {
	if j == nil {
		return
	}
	j.mu.Lock()
	name := j.stage
	j.mu.Unlock()
	j.set(SaveProgress{Phase: name, Done: done, Total: total})
}

func (j *saveJob) set(progress SaveProgress) {
	if j == nil {
		return
	}
	progress.Running = true
	j.progress.Store(progress)
	emitEvent("save:progress", progress)
}

func (j *saveJob) snapshot() SaveProgress {
	if j == nil {
		return SaveProgress{}
	}
	if value, ok := j.progress.Load().(SaveProgress); ok {
		return value
	}
	return SaveProgress{}
}

// hooks 生成传给内核的回调（内核侧回调全为空时行为与原来完全一致）。
func (j *saveJob) hooks() pvf.SaveHooks {
	return pvf.SaveHooks{
		Phase:    func(phase string) { j.phase(phase) },
		Progress: func(done, total int) { j.units(done, total) },
		Cancel:   func() bool { return j.cancelled() },
	}
}

func (j *saveJob) cancelled() bool {
	if j == nil {
		return false
	}
	return j.cancel.Load()
}

// beginSave 开始一次保存；已有任务在跑时直接拒绝。
func (s *EditorService) beginSave() (*saveJob, error) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if s.saveJob != nil {
		return nil, errors.New("已有保存任务在进行中")
	}
	job := newSaveJob()
	s.saveJob = job
	return job, nil
}

// endSave 结束一次保存并广播收尾事件。
func (s *EditorService) endSave(job *saveJob) {
	s.saveMu.Lock()
	if s.saveJob == job {
		s.saveJob = nil
	}
	s.saveMu.Unlock()
	job.set(SaveProgress{Phase: "done"})
}

// CancelSave 请求取消正在进行的保存。
//
// 这里只动一个 atomic，**不拿 c.mu**：保存期间 c.mu 是被占用的（要防止并发改归档），
// 若取消也去抢同一把锁，取消按钮就会一直转圈（同类教训：踩坑表 #20）。
func (s *EditorService) CancelSave() error {
	s.saveMu.Lock()
	job := s.saveJob
	s.saveMu.Unlock()
	if job == nil {
		return errors.New("当前没有正在进行的保存")
	}
	job.cancel.Store(true)
	return nil
}

// SaveStatus 返回当前/最近一次保存的进度（供晚打开的窗口与轮询使用）。
func (s *EditorService) SaveStatus() SaveProgress {
	s.saveMu.Lock()
	job := s.saveJob
	s.saveMu.Unlock()
	progress := job.snapshot()
	progress.Running = job != nil
	return progress
}
