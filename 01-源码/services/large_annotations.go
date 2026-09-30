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

// GetWindowAnnotations 解析**当前窗口里那段文本**的注解。
//
// text 必须是前端窗口此刻显示的内容（含尚未写回归档的改动）——不能拿归档文本去算：
// 只要用户插了一行，按归档算出来的位置就会整体错开一行，绿色标签与虚线下划线会错位
// （2026-10-01 用户实测发现）。
//
// 仍然只解析这一段（约 130KB），成本只与视口大小有关，与文件多大无关。
func (s *EditorService) GetWindowAnnotations(
	index int32,
	text string,
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
	if text == "" {
		s.c.mu.Unlock()
		return nil, nil
	}
	annotations, err := s.c.editorAnnotationsLocked(index, text)
	s.c.mu.Unlock()
	if err != nil {
		return nil, err
	}

	limit := int32(len(text))
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
