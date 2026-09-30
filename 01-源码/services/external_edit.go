package services

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"pvfine/internal/logging"
)

// =============================================================================
// 大文件「外部编辑」会话（导出 → 外部程序改 → 回填归档）
//
// 场景：像 list/equipment.lst 这种 27MB / 41 万行的清单，在编辑器里打开很慢
// （解析 + 标注 + 前端渲染）。用户现在的土办法是「导出到电脑上 → 用系统自带文本
// 编辑器改 → 再导回来」，本服务把这条流程内置成一步：导出到固定工作目录 +
// 调起系统默认程序 + 记住基线 + 变更检测 + 一键回填。
//
// 只对**纯文本类清单**开放（见 externalEditAllowedExts）；.equ/.stk 等二进制
// token 流不在内（外部改文本再回写的风险不划算）。
//
// 回填走与「编辑器内保存」完全相同的路径（a.SetText），失败就报错、不做兜底，
// 避免把文本按原始字节写进 token 流文件里。
// =============================================================================

// externalEditWorkRootName 是工作副本根目录名，默认放在「用户文档」下，方便用户自己找到。
const externalEditWorkRootName = "pvfine 外部编辑"

// externalEditAllowedExts 允许外部文本编辑的扩展名（纯文本类清单/配置）。
// 需要放开更多类型时改这里即可（改完要确认回填链路对它有 a.SetText 支持）。
var externalEditAllowedExts = []string{
	".lst", ".etc", ".txt", ".tbl", ".co", ".cos",
	".csv", ".md", ".json", ".lua", ".nut",
}

// ExternalEditSession 是一次「外部编辑」会话的快照。
type ExternalEditSession struct {
	Path      string `json:"path"`      // 归档内路径
	LocalPath string `json:"localPath"` // 本地工作副本
	Dir       string `json:"dir"`       // 工作副本所在目录（打开文件夹用）
	StartedAt string `json:"startedAt"` // 导出时间
	Baseline  string `json:"baseline"`  // 导出时的 sha256（变更检测基线）
	Hash      string `json:"hash"`      // 本地文件当前 sha256
	Changed   bool   `json:"changed"`   // 本地文件相对基线是否已改
	Opened    bool   `json:"opened"`    // 是否成功调起外部程序
	Size      int64  `json:"size"`      // 本地文件大小
	ModTime   string `json:"modTime"`   // 本地文件修改时间
	Note      string `json:"note,omitempty"`
}

// ExternalEditResult 是回填结果。
type ExternalEditResult struct {
	Path      string `json:"path"`
	LocalPath string `json:"localPath"`
	Applied   bool   `json:"applied"` // 是否真的写进了归档（内存）
	Reason    string `json:"reason"`  // 未写回时的说明
	Bytes     int    `json:"bytes"`   // 写入的文本长度
	Modified  bool   `json:"modified"` // 归档当前是否处于"有未保存修改"状态
	SavedHint string `json:"savedHint,omitempty"`
}

var (
	externalEditMu       sync.Mutex
	externalEditSessions = map[string]*ExternalEditSession{}
)

// ---------------------------------------------------------------- 对外 API

// ExternalEditStart 把归档内文件导出到工作目录、调起系统默认程序，并登记会话。
func (s *EditorService) ExternalEditStart(path string) (*ExternalEditSession, error) {
	path = normalizeExportPath(path)
	if path == "" {
		return nil, fmt.Errorf("未指定要外部编辑的文件")
	}
	if !externalEditAllowed(path) {
		return nil, fmt.Errorf("该文件类型暂不支持外部编辑（目前支持：%s）",
			strings.Join(externalEditAllowedExts, " "))
	}

	s.c.mu.RLock()
	a := s.c.archive
	if a == nil {
		s.c.mu.RUnlock()
		return nil, ErrNoArchive
	}
	index, ok := a.Find(path)
	if !ok {
		s.c.mu.RUnlock()
		return nil, fmt.Errorf("归档里找不到「%s」", path)
	}
	text, err := a.Text(index)
	s.c.mu.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("渲染「%s」失败: %w", path, err)
	}

	root, err := externalEditRoot()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, time.Now().Format("20060102-150405"))
	local, err := safeExportPath(dir, path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return nil, fmt.Errorf("创建工作目录失败: %w", err)
	}
	payload := []byte(text)
	if err := os.WriteFile(local, payload, 0o644); err != nil {
		return nil, fmt.Errorf("导出工作副本失败: %w", err)
	}

	sum := sha256.Sum256(payload)
	session := &ExternalEditSession{
		Path:      path,
		LocalPath: local,
		Dir:       dir,
		StartedAt: time.Now().Format("2006-01-02 15:04:05"),
		Baseline:  hex.EncodeToString(sum[:]),
		Hash:      hex.EncodeToString(sum[:]),
	}
	if err := openWithDefaultApp(local); err != nil {
		session.Note = "自动打开失败，请手动打开该文件：" + err.Error()
	} else {
		session.Opened = true
	}
	refreshExternalSessionStat(session)

	externalEditMu.Lock()
	externalEditSessions[path] = session
	externalEditMu.Unlock()

	logging.For("external-edit").Info("已导出外部编辑副本",
		"路径", path, "本地", local, "字节", len(payload), "已调起", session.Opened)
	return session, nil
}

// ExternalEditList 返回全部会话（按导出时间倒序）。
func (s *EditorService) ExternalEditList() []*ExternalEditSession {
	externalEditMu.Lock()
	defer externalEditMu.Unlock()
	out := make([]*ExternalEditSession, 0, len(externalEditSessions))
	for _, session := range externalEditSessions {
		refreshExternalSessionStat(session)
		out = append(out, session)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out
}

// ExternalEditCheck 重新比对外部文件与基线，返回更新后的会话。
func (s *EditorService) ExternalEditCheck(path string) (*ExternalEditSession, error) {
	path = normalizeExportPath(path)
	externalEditMu.Lock()
	session := externalEditSessions[path]
	externalEditMu.Unlock()
	if session == nil {
		return nil, fmt.Errorf("没有「%s」的外部编辑会话", path)
	}
	refreshExternalSessionStat(session)
	return session, nil
}

// ExternalEditApply 把外部文件的内容写回归档（内存），等待用户照常「保存 PVF」落盘。
func (s *EditorService) ExternalEditApply(path string) (*ExternalEditResult, error) {
	path = normalizeExportPath(path)
	externalEditMu.Lock()
	session := externalEditSessions[path]
	externalEditMu.Unlock()
	if session == nil {
		return nil, fmt.Errorf("没有「%s」的外部编辑会话", path)
	}

	payload, err := os.ReadFile(session.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("读取外部文件失败: %w", err)
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	result := &ExternalEditResult{Path: path, LocalPath: session.LocalPath}
	if hash == session.Baseline {
		refreshExternalSessionStat(session)
		result.Reason = "外部文件与导出时一致，没有需要回填的内容"
		return result, nil
	}

	text, err := decodeImportText(session.LocalPath, payload)
	if err != nil {
		return nil, fmt.Errorf("外部文件编码无法识别（应为 UTF-8 或 UTF-16）: %w", err)
	}

	s.c.mu.Lock()
	a := s.c.archive
	if a == nil {
		s.c.mu.Unlock()
		return nil, ErrNoArchive
	}
	index, ok := a.Find(path)
	if !ok {
		s.c.mu.Unlock()
		return nil, fmt.Errorf("归档里找不到「%s」（可能已关闭或换过归档）", path)
	}
	if a.IsModified(index) {
		s.c.mu.Unlock()
		result.Reason = "归档里该文件已有未保存的修改，请先在编辑器里保存或刷新后再回填"
		return result, nil
	}
	if err := a.SetText(index, text); err != nil {
		s.c.mu.Unlock()
		return nil, fmt.Errorf("写回「%s」失败: %w", path, err)
	}
	info := a.Info()
	s.c.mu.Unlock()

	session.Baseline = hash
	session.Hash = hash
	session.Changed = false
	refreshExternalSessionStat(session)

	result.Applied = true
	result.Bytes = len(text)
	result.Modified = true
	result.SavedHint = "回填已写入内存，请照常「保存 PVF」才会落盘"
	emitEvent("external-edit:applied", map[string]any{"path": path, "localPath": session.LocalPath})
	emitEvent("archive:changed", info)
	logging.For("external-edit").Info("回填外部编辑内容", "路径", path, "字节", len(text))
	return result, nil
}

// ExternalEditReveal 在资源管理器里选中工作副本。
func (s *EditorService) ExternalEditReveal(path string) error {
	path = normalizeExportPath(path)
	externalEditMu.Lock()
	session := externalEditSessions[path]
	externalEditMu.Unlock()
	if session == nil {
		return fmt.Errorf("没有「%s」的外部编辑会话", path)
	}
	cmd := exec.Command("explorer.exe", "/select,"+session.LocalPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		// explorer 对参数挑剔时退一步：只打开所在目录。
		cmd = exec.Command("explorer.exe", session.Dir)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		return cmd.Start()
	}
	return nil
}

// ExternalEditFinish 结束会话；discardLocal 为真时连同工作副本目录一起删除。
func (s *EditorService) ExternalEditFinish(path string, discardLocal bool) error {
	path = normalizeExportPath(path)
	externalEditMu.Lock()
	session := externalEditSessions[path]
	delete(externalEditSessions, path)
	externalEditMu.Unlock()
	if session == nil {
		return nil
	}
	if discardLocal && session.Dir != "" {
		if err := os.RemoveAll(session.Dir); err != nil {
			return fmt.Errorf("删除工作副本失败: %w", err)
		}
	}
	logging.For("external-edit").Info("结束外部编辑会话", "路径", path, "删除副本", discardLocal)
	return nil
}

// ---------------------------------------------------------------- 内部实现

func externalEditAllowed(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, allowed := range externalEditAllowedExts {
		if ext == allowed {
			return true
		}
	}
	return false
}

// externalEditRoot 返回（并确保存在）工作副本根目录：
// 优先「用户文档\pvfine 外部编辑」，不行就退到程序目录下的同名目录。
func externalEditRoot() (string, error) {
	var candidates []string
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		base := filepath.Join(home, "Documents")
		if st, statErr := os.Stat(base); statErr != nil || !st.IsDir() {
			base = home
		}
		candidates = append(candidates, filepath.Join(base, externalEditWorkRootName))
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), externalEditWorkRootName))
	}
	var lastErr error
	for _, dir := range candidates {
		if err := ensureWritableDir(dir); err != nil {
			lastErr = err
			continue
		}
		return dir, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可写的工作目录")
	}
	return "", fmt.Errorf("无法准备工作目录: %w", lastErr)
}

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

// refreshExternalSessionStat 刷新本地文件的大小/时间/hash/是否已改。
func refreshExternalSessionStat(session *ExternalEditSession) {
	if session == nil {
		return
	}
	st, err := os.Stat(session.LocalPath)
	if err != nil {
		session.Note = "工作副本已不存在（可能被移动或删除）"
		return
	}
	session.Size = st.Size()
	session.ModTime = st.ModTime().Format("2006-01-02 15:04:05")
	payload, err := os.ReadFile(session.LocalPath)
	if err != nil {
		return
	}
	sum := sha256.Sum256(payload)
	session.Hash = hex.EncodeToString(sum[:])
	session.Changed = session.Hash != session.Baseline
}

// openWithDefaultApp 用系统默认关联打开文件（等价双击）。
func openWithDefaultApp(path string) error {
	cmd := exec.Command("cmd", "/c", "start", "", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err == nil {
		return nil
	}
	fallback := exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	fallback.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return fallback.Start()
}
