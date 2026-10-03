package services

import (
	"fmt"
	"strings"
)

// ResolveRefNames 把一批 ref 值（如怪物 / 物品编号）翻成中文名。
//
// **为什么要有这个方法**：表格里的中文名是后端投影时用 `refNameResolver` 解析的，
// 而前端给"草稿里的新编号 / 添加掉落表单里填的编号"显示名字时，一开始借用的是
// 「对象视图」的 `ResolveObject` —— 两条路的取数方式不同（对象视图那套对怪物并不总能给出
// 名字），于是出现"物品能出名字、怪物出不来"（用户 2026-10-03 实测）。
//
// 这里直接复用**表格用的那一个解析器**，保证"草稿/表单里看到的名字"和"表格里的名字"
// 完全同源、同规则。
//
// ref 支持 `a|b` 多候选（与规则里的写法一致），逐个候选试到有名字为止。
func (s *FormViewService) ResolveRefNames(ref string, ids []string) (map[string]string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("ref 不能为空")
	}
	if len(ids) == 0 {
		return map[string]string{}, nil
	}

	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	a := s.c.archive
	if a == nil {
		return nil, ErrNoArchive
	}
	// resolver.name 要求持有 core 读锁（内部只读归档），这里正是。
	resolver := s.resolverFor(a)
	if resolver == nil {
		return map[string]string{}, nil
	}

	result := make(map[string]string, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, done := result[id]; done {
			continue
		}
		if name := resolver.name(ref, id); name != "" {
			result[id] = name
		}
	}
	return result, nil
}
