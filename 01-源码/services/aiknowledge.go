package services

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// 内置知识库：与「注释数据」同款的 exe 同目录外置资料（DNF/PVF 社区教程、
// 字段注释、脚本范例），供 AI 助手在回答语法/字段/写法问题时检索参考。
// 可用环境变量 PVFINE_KNOWLEDGE_DIR 显式指定目录；目录缺失时工具报友好错误，
// 不影响其余功能。

const (
	// EnvKnowledgeDir 可用环境变量显式指定知识库目录。
	EnvKnowledgeDir = "PVFINE_KNOWLEDGE_DIR"
	// KnowledgeDirName 是 exe 同目录下的默认知识库目录名。
	KnowledgeDirName = "知识库"
	// knowledgeMaxFileBytes 单文件读取上限（超过则截断，防止异常大文件撑爆内存）。
	knowledgeMaxFileBytes = 4 << 20
	// knowledgeMaxTotalBytes 全库文本缓存总量上限。
	knowledgeMaxTotalBytes = 192 << 20
)

// knowledgeTextExts 参与全文检索的扩展名白名单（其余文件只按文件名参与检索）。
var knowledgeTextExts = map[string]bool{
	".md": true, ".txt": true, ".json": true, ".nut": true,
	".js": true, ".mjs": true, ".cs": true, ".csv": true, ".ps1": true,
}

type knowledgeFile struct {
	rel       string // 相对路径（正斜杠）
	lowerRel  string
	lowerName string
	text      string // 解码后的全文（仅白名单扩展名缓存）
}

type knowledgeStore struct {
	once  sync.Once
	dir   string
	found bool
	files []knowledgeFile
}

// aiKnowledge 进程级单例：buildAITools 每轮对话都会重建工具表，检索缓存必须共享。
var aiKnowledge knowledgeStore

// knowledgeDir 解析知识库目录：环境变量优先，其次 <exe 同目录>\知识库。
func knowledgeDir() string {
	if env := strings.TrimSpace(os.Getenv(EnvKnowledgeDir)); env != "" {
		return env
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), KnowledgeDirName)
}

// load 懒加载全库：只走一次，后续复用内存缓存。
func (k *knowledgeStore) load() (dir string, found bool) {
	k.once.Do(func() {
		k.dir = knowledgeDir()
		if info, err := os.Stat(k.dir); err != nil || !info.IsDir() {
			return
		}
		k.found = true
		total := 0
		_ = filepath.WalkDir(k.dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil // 跳过无权限/损坏条目，不让单个坏文件拖垮整库
			}
			ext := strings.ToLower(filepath.Ext(d.Name()))
			if !knowledgeTextExts[ext] {
				return nil
			}
			if info, err := d.Info(); err != nil || info.Size() > knowledgeMaxFileBytes || total+int(info.Size()) > knowledgeMaxTotalBytes {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			text := decodeKnowledgeText(raw)
			rel, err := filepath.Rel(k.dir, path)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			total += len(text)
			k.files = append(k.files, knowledgeFile{
				rel:       rel,
				lowerRel:  strings.ToLower(rel),
				lowerName: strings.ToLower(d.Name()),
				text:      text,
			})
			return nil
		})
	})
	return k.dir, k.found
}

// decodeKnowledgeText UTF-8 优先，非法序列按 GBK 解码（社区资料常见两种编码）。
func decodeKnowledgeText(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw)
	}
	if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(raw); err == nil {
		return string(decoded)
	}
	return string(raw)
}

// knowledgeSummary 返回系统提示用的一句话概况；目录缺失时返回空串。
func knowledgeSummary() string {
	dir, found := aiKnowledge.load()
	if !found || len(aiKnowledge.files) == 0 {
		return ""
	}
	dirs := map[string]bool{}
	for _, f := range aiKnowledge.files {
		if top, _, ok := strings.Cut(f.rel, "/"); ok {
			dirs[top] = true
		}
	}
	names := make([]string, 0, len(dirs))
	for name := range dirs {
		names = append(names, name)
	}
	sortStrings(names)
	return fmt.Sprintf("内置知识库（%s）共 %d 个文档，目录：%s；PVF 语法、字段含义、脚本写法类问题请先用 search_knowledge 检索。",
		dir, len(aiKnowledge.files), strings.Join(names, "、"))
}

// sortStrings 就地升序（避免仅为一个排序引入 slices 包依赖差异）。
func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// searchKnowledge 全文检索：按「文件名全含 > 路径全含 > 内容命中」分桶排序，
// 返回命中文件与摘录行。terms 为按空白拆分的小写关键词，要求全部命中才算档内。
func (k *knowledgeStore) searchKnowledge(query string, limit int) (string, error) {
	dir, found := k.load()
	if !found {
		return "", fmt.Errorf("知识库目录不存在：%s（请将知识库放到该目录，或设置环境变量 %s）", dir, EnvKnowledgeDir)
	}
	if len(aiKnowledge.files) == 0 {
		return "", fmt.Errorf("知识库为空：%s", dir)
	}
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return "", fmt.Errorf("query 不能为空")
	}
	if limit <= 0 || limit > 30 {
		limit = 10
	}
	type hit struct {
		file     knowledgeFile
		snippets []map[string]any
		matched  int
	}
	var nameHits, pathHits, textHits []hit
	scanFile := func(f knowledgeFile) (hit, bool) {
		result := hit{file: f, matched: 0}
		for _, term := range terms {
			if strings.Contains(f.lowerRel, term) {
				result.matched++
			}
		}
		if result.matched == len(terms) {
			if strings.Contains(f.lowerName, terms[0]) || allInName(f.lowerName, terms) {
				return result, true
			}
			return result, false
		}
		// 内容逐行检索：包含任一关键词的行视为命中，命中关键词越多的行排越前。
		result.matched = 0
		lines := strings.Split(f.text, "\n")
		for i, line := range lines {
			lower := strings.ToLower(line)
			count := 0
			for _, term := range terms {
				if strings.Contains(lower, term) {
					count++
				}
			}
			if count == 0 {
				continue
			}
			if count > result.matched {
				result.matched = count
			}
			if len(result.snippets) < 3 {
				result.snippets = append(result.snippets, map[string]any{
					"line": i + 1,
					"text": aiTruncate(strings.TrimSpace(line), 240),
				})
			}
		}
		if len(result.snippets) == 0 {
			return hit{}, false
		}
		return result, true
	}
	for _, f := range aiKnowledge.files {
		if h, ok := scanFile(f); ok {
			if len(h.snippets) == 0 {
				nameHits = append(nameHits, h)
			} else if h.matched == len(terms) && len(nameHits) < limit*3 {
				// 全部关键词都在路径里的归入次高档
				pathHits = append(pathHits, h)
			} else {
				textHits = append(textHits, h)
			}
		}
	}
	hits := make([]map[string]any, 0, limit)
	appendHit := func(h hit) {
		if len(hits) >= limit {
			return
		}
		hits = append(hits, map[string]any{
			"file":     h.file.rel,
			"snippets": h.snippets,
		})
	}
	for _, bucket := range [][]hit{nameHits, pathHits, textHits} {
		for _, h := range bucket {
			appendHit(h)
			if len(hits) >= limit {
				break
			}
		}
		if len(hits) >= limit {
			break
		}
	}
	return aiJSON(map[string]any{"dir": dir, "count": len(hits), "hits": hits}), nil
}

// allInName 判断所有关键词是否都出现在文件名中。
func allInName(lowerName string, terms []string) bool {
	for _, term := range terms {
		if !strings.Contains(lowerName, term) {
			return false
		}
	}
	return true
}

// readKnowledge 读取知识库文件内容（带行号，从 offset 行起），供 AI 引用原文。
func (k *knowledgeStore) readKnowledge(path string, offset int) (string, error) {
	dir, found := k.load()
	if !found {
		return "", fmt.Errorf("知识库目录不存在：%s（请将知识库放到该目录，或设置环境变量 %s）", dir, EnvKnowledgeDir)
	}
	clean := strings.TrimSpace(path)
	clean = strings.ReplaceAll(clean, "\\", "/")
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || strings.Contains(clean, "..") {
		return "", fmt.Errorf("path 非法：%q", path)
	}
	target := strings.ToLower(clean)
	for _, f := range aiKnowledge.files {
		if f.lowerRel != target {
			continue
		}
		lines := strings.Split(f.text, "\n")
		if offset < 0 {
			offset = 0
		}
		if offset >= len(lines) {
			return fmt.Sprintf("文件共 %d 行，offset=%d 超出范围。", len(lines), offset), nil
		}
		var sb strings.Builder
		for i := offset; i < len(lines); i++ {
			fmt.Fprintf(&sb, "%d:%s\n", i+1, lines[i])
			if sb.Len() > aiMaxOutputRunes*3 {
				break
			}
		}
		return aiTruncate(sb.String(), aiMaxOutputRunes), nil
	}
	return "", fmt.Errorf("知识库中找不到文件：%s（请用 search_knowledge 获取准确路径）", clean)
}
