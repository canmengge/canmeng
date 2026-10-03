package config

import _ "embed"

//go:embed annotations.json
var AnnotationsJSON []byte

//go:embed rendering.json
var RenderingJSON []byte

//go:embed lists.json
var ListsJSON []byte

//go:embed bookmarks.json
var BookmarksJSON []byte

// ObjectViewJSON 是对象视图规则（对象类型 → 关联文件与显示文本）的默认副本。
//
//go:embed objectview.json
var ObjectViewJSON []byte

// ProtectedStringTablesJSON 是客户端字符串表写保护清单（上级规则 §6.12）的默认副本。
//
//go:embed protected-string-tables.json
var ProtectedStringTablesJSON []byte

// ExternalLinksJSON 是「外部登记表链接」规则的默认副本：把脚本字段里的编号按登记表
// 解析成归档路径（Ctrl+单击跳转、悬停显示目标路径）。
//
//go:embed external_links.json
var ExternalLinksJSON []byte

// FormatsJSON 是「结构化视图规则」的默认副本：文件族 → 段 → 行/列定义。
// 用于把脚本文件投影成只读表格（如 etc/independent_drop.etc 的 17 列掉落行）。
//
//go:embed formats.json
var FormatsJSON []byte
