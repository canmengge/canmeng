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
//go:embed objectview.json
var ObjectViewJSON []byte

// ProtectedStringTablesJSON 是客户端字符串表写保护清单（上级规则 §6.12）的默认副本。
//go:embed protected-string-tables.json
var ProtectedStringTablesJSON []byte
