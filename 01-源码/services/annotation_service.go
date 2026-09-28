package services

import (
	"os"
	"time"

	annotationrules "pvfine/internal/annotations"
)

type AnnotationReloadResult struct {
	RuleCount     int                            `json:"ruleCount"`
	RelationCount int                            `json:"relationCount"`
	DurationMs    int64                          `json:"durationMs"`
	External      annotationrules.ExternalSummary `json:"external"`
}

type AnnotationService struct {
	c       *core
	path    string
	initErr error
}

// NewAnnotationService 使用默认加载方式：内置基础注解 + 外置「注释数据」目录。
func NewAnnotationService(c *core) *AnnotationService {
	return &AnnotationService{c: c}
}

// loadEngine 决定用哪个引擎：显式指定了规则文件时按文件加载（测试/调试用），
// 否则走「内置基础 + 外置目录」的分层加载。
func (s *AnnotationService) loadEngine() (*annotationrules.Engine, annotationrules.ExternalSummary, error) {
	if s.path != "" {
		if info, err := os.Stat(s.path); err == nil && !info.IsDir() {
			engine, loadErr := annotationrules.LoadFile(s.path)
			return engine, annotationrules.ExternalSummary{}, loadErr
		}
	}
	return annotationrules.LoadPreferredWithSummary()
}

func newAnnotationService(c *core, path string) *AnnotationService {
	return &AnnotationService{c: c, path: path}
}

// ReloadRules 重新加载「内置基础注解 + 外置注释目录（注释数据\）」，并用新引擎原子替换
// 旧引擎；加载失败时保留当前引擎不动（调用方可见错误）。前端改完注释数据后可即时生效。
func (s *AnnotationService) ReloadRules() (AnnotationReloadResult, error) {
	if s.initErr != nil {
		return AnnotationReloadResult{}, s.initErr
	}
	started := time.Now()
	engine, external, err := s.loadEngine()
	if err != nil {
		return AnnotationReloadResult{}, err
	}
	document := engine.Document()

	s.c.mu.Lock()
	oldSpecsFingerprint := searchIndexSpecFingerprint(s.c.searchableListSpecsLocked())
	s.c.annotationEngine = engine
	s.c.annotationErr = nil
	s.c.annotationExternal = external
	s.c.annotationRelations = make(map[string]map[string]*relationTarget)
	s.c.editorAnnotation = editorAnnotationCache{}
	archiveOpen := s.c.archive != nil
	if archiveOpen {
		// 标注规则已被替换 ⇒ 旧的按需缓存整体作废，下次访问按新规则重算（P-2006）。
		s.c.resetPathAnnotationsLocked()
	}
	newSpecsFingerprint := searchIndexSpecFingerprint(s.c.searchableListSpecsLocked())
	s.c.mu.Unlock()
	if archiveOpen && oldSpecsFingerprint != newSpecsFingerprint {
		s.c.startSearchIndex()
	}

	result := AnnotationReloadResult{
		RuleCount:     len(document.Rules),
		RelationCount: len(document.Relations),
		DurationMs:    time.Since(started).Milliseconds(),
		External:      external,
	}
	emitEvent("annotations:reloaded", result)
	return result, nil
}

// AnnotationSources 返回当前生效的注释来源与规模（只读，供设置面板显示）。
// 不重新加载，直接读 core 记录的上一次加载统计。
func (s *AnnotationService) AnnotationSources() (AnnotationReloadResult, error) {
	if s.initErr != nil {
		return AnnotationReloadResult{}, s.initErr
	}
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	ruleCount := 0
	relationCount := 0
	if s.c.annotationEngine != nil {
		document := s.c.annotationEngine.Document()
		ruleCount = len(document.Rules)
		relationCount = len(document.Relations)
	}
	return AnnotationReloadResult{
		RuleCount:     ruleCount,
		RelationCount: relationCount,
		External:      s.c.annotationExternal,
	}, nil
}
