package services

import (
	"sort"
	"strings"

	"pvfine/internal/pvf"
)

// patchArchiveIndex 在**已安装的**目录索引上做增量补丁：
//
//   - 只重建「被改动路径所在的目录」及其全部祖先的 children 切片；
//   - 把新增/更新的条目归并进有序路径表（旧表按 path 有序，一次线性归并）；
//   - 不再对全归档（本归档 431 万条）执行 buildIndex（实测 8.7 s）。
//
// 约定：调用方须在持读锁（或独占 stage）的前提下取到 old/oldPaths，
// 并在安装时重新校验归档代际；本函数自身不改动传入的旧结构
// （需要改的目录会先复制出自己的切片），因此可以在锁外准备、锁内安装。
//
// changed 是新增或被覆盖的文件索引（stage 上的索引）。
// 返回 false 表示无法增量，调用方应退回 buildIndex。
func patchArchiveIndex(
	old map[string][]*TreeNode,
	oldPaths []pathEntry,
	a *pvf.Archive,
	changed []int32,
) (map[string][]*TreeNode, []pathEntry, bool) {
	if len(oldPaths) == 0 || len(changed) == 0 || a == nil {
		return nil, nil, false
	}

	entries := make([]pathEntry, 0, len(changed))
	nodes := make([]*TreeNode, 0, len(changed))
	replace := make(map[string]bool, len(changed))
	parentDirs := make([]string, 0, 8)

	for _, index := range changed {
		path := a.Path(index)
		if path == "" {
			continue
		}
		file := a.File(index)
		kind := archiveChangeKind(a, index)
		parent, name := splitParent(path)
		entries = append(entries, pathEntry{
			path:       path,
			lower:      strings.ToLower(path),
			idx:        index,
			size:       file.DataSize,
			typ:        file.DataType,
			changeKind: kind,
		})
		nodes = append(nodes, &TreeNode{
			Name: name, Path: path, IsDir: false,
			Size: file.DataSize, DataType: file.DataType,
			FileIndex: index, ChangeKind: kind,
		})
		replace[path] = true
		parentDirs = append(parentDirs, parent)
	}
	if len(entries) == 0 {
		return nil, nil, false
	}

	// 目录处理顺序：祖先在前（前缀在字典序里更小），保证建父先于建子。
	sort.Slice(parentDirs, func(i, j int) bool { return parentDirs[i] < parentDirs[j] })
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })

	next := make(map[string][]*TreeNode, len(old)+8)
	for key, list := range old {
		next[key] = list
	}

	// 受影响目录 = 直接父目录 + 它们的全部祖先（祖先的目录节点要补 ChildCount）。
	touched := make(map[string]bool, len(parentDirs)*2)
	for _, dir := range parentDirs {
		for p := dir; ; {
			if touched[p] {
				break
			}
			touched[p] = true
			if p == "" {
				break
			}
			p, _ = splitParent(p)
		}
	}
	// 根目录本身不需要节点，但从"被独占"集合里去掉会漏掉它的 children 重建。
	delete(touched, "")

	owned := make(map[string]bool, len(touched))
	own := func(dir string) []*TreeNode {
		if owned[dir] {
			return next[dir]
		}
		source := next[dir]
		list := make([]*TreeNode, len(source))
		copy(list, source)
		next[dir] = list
		owned[dir] = true
		return list
	}

	// 补建尚不存在的目录节点（含祖先链）。
	var ensureDir func(dir string)
	ensureDir = func(dir string) {
		if dir == "" {
			return
		}
		if _, exists := next[dir]; exists {
			return
		}
		parent, name := splitParent(dir)
		ensureDir(parent)
		list := own(parent)
		next[parent] = append(list, &TreeNode{Name: name, Path: dir, IsDir: true, FileIndex: -1})
		next[dir] = []*TreeNode{}
		owned[dir] = true
	}
	dirs := make([]string, 0, len(touched))
	for dir := range touched {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		ensureDir(dir)
		// 已存在的目录同样要"独占"：它的子节点里可能挂着受影响目录的节点，
		// 那些节点必须换成新副本才能带上新的 ChildCount（旧节点是只读共享的）。
		own(dir)
	}

	// 按目录分组写入变更：覆盖的旧节点换新节点，新增的追加。
	byDir := make(map[string][]*TreeNode, len(parentDirs))
	for _, node := range nodes {
		parent, _ := splitParent(node.Path)
		byDir[parent] = append(byDir[parent], node)
	}
	for dir, additions := range byDir {
		list := own(dir)
		out := make([]*TreeNode, 0, len(list)+len(additions))
		for _, node := range list {
			if !node.IsDir && replace[node.Path] {
				// 覆盖：旧节点作废，用下面追加的新节点代替。
				continue
			}
			if node.IsDir && owned[node.Path] {
				// 该目录的 children 变了：换新节点（旧节点可能被其它读者共享，不能原地改）。
				fresh := *node
				out = append(out, &fresh)
				continue
			}
			out = append(out, node)
		}
		out = append(out, additions...)
		next[dir] = out
	}

	// 目录内排序（目录在前、同级按名）+ ChildCount，与 buildIndex 保持一致。
	for dir := range owned {
		list := next[dir]
		sort.Slice(list, func(x, y int) bool {
			if list[x].IsDir != list[y].IsDir {
				return list[x].IsDir
			}
			return list[x].Name < list[y].Name
		})
		for _, node := range list {
			if node.IsDir {
				node.ChildCount = int32(len(next[node.Path]))
			}
		}
	}

	// 有序路径表：一次线性归并（旧表按 path 有序，新条目同样有序）。
	merged := make([]pathEntry, 0, len(oldPaths)+len(entries))
	i, j := 0, 0
	for i < len(oldPaths) && j < len(entries) {
		switch {
		case oldPaths[i].path < entries[j].path:
			merged = append(merged, oldPaths[i])
			i++
		case oldPaths[i].path > entries[j].path:
			merged = append(merged, entries[j])
			j++
		default:
			// 同路径（覆盖）→ 取新条目，旧条目丢弃。
			merged = append(merged, entries[j])
			i++
			j++
		}
	}
	merged = append(merged, oldPaths[i:]...)
	merged = append(merged, entries[j:]...)

	return next, merged, true
}
