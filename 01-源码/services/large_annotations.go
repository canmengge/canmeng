package services

// 本文件是「大文件注解」的按需通道。
//
// 背景：大文件的整份文本不进窗口（这是秒开的前提，见 services/large_text.go），
// 而注解是按文本解析出来的，于是改走 TXT 视图之后中文名标签 / 绿色关联框全部消失
// （后端 GetFile 的大文件分支连注解一起跳过了）。
//
// 做法：**只解析当前视口那一段**。段内约 130KB / 几千行，走的是与普通文件完全
// 相同的注解引擎，成本只与视口大小有关，与文件多大无关 —— 秒开性能不受影响。

// WindowAnnotation 是大文件当前视口那一段的注解。
// Start / End 是**相对段首**的字符偏移（窗口里的文本就是这一段的全部内容）。
type WindowAnnotation struct {
	Start           int32  `json:"start"`
	End             int32  `json:"end"`
	Type            string `json:"type"`
	Title           string `json:"title"`
	Content         string `json:"content"`
	TargetFileIndex int32  `json:"targetFileIndex"`
}

// GetWindowAnnotations 解析第 [startLine, startLine+lineCount) 行这段的注解。
//
// 只在滚动换窗后调用一次（前端负责），段内编辑期间不重算，避免每敲一个键都解析。
func (s *EditorService) GetWindowAnnotations(
	index int32,
	startLine int32,
	lineCount int32,
) ([]WindowAnnotation, error) {
	s.c.mu.Lock()
	a := s.c.archive
	if a == nil {
		s.c.mu.Unlock()
		return nil, ErrNoArchive
	}
	if err := validateAnnotationIndex(a, index); err != nil {
		s.c.mu.Unlock()
		return nil, err
	}
	text, err := s.currentLargeTextLocked(index)
	if err != nil {
		s.c.mu.Unlock()
		return nil, err
	}

	// 与 GetFileLines 同一套行偏移表（带缓存，定位 O(1)）。
	offsets := lineOffsetsFor(index, text)
	total := int32(len(offsets)) - 1
	if startLine < 1 {
		startLine = 1
	}
	if startLine-1 > total {
		startLine = total + 1
	}
	if lineCount <= 0 {
		lineCount = largeTextScrollChunk
	}
	endIdx := startLine - 1 + lineCount
	if endIdx > total {
		endIdx = total
	}
	slice := text[offsets[startLine-1]:offsets[endIdx]]

	annotations, err := s.c.editorAnnotationsLocked(index, slice)
	s.c.mu.Unlock()
	if err != nil {
		return nil, err
	}

	limit := int32(len(slice))
	out := make([]WindowAnnotation, 0, len(annotations))
	for _, item := range annotations {
		if item.Start < 0 || item.End > limit || item.End <= item.Start {
			continue
		}
		out = append(out, WindowAnnotation{
			Start:           item.Start,
			End:             item.End,
			Type:            item.Type,
			Title:           item.Title,
			Content:         item.Content,
			TargetFileIndex: item.TargetFileIndex,
		})
	}
	return out, nil
}
