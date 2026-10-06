package services

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	annotationrules "pvfine/internal/annotations"
	"pvfine/internal/pvf"
)

// EquipmentPreviewAttribute is one visible line in an equipment tooltip.
type EquipmentPreviewAttribute struct {
	Label    string `json:"label"`
	Value    string `json:"value"`
	Negative bool   `json:"negative"`
}

// EquipmentSkillLevelup is one profession-aware skill level bonus.
type EquipmentSkillLevelup struct {
	Job   string `json:"job"`
	Skill string `json:"skill"`
	Level int32  `json:"level"`
}

// EquipmentSetPieceBonus is the attribute list for one piece-count threshold
// of a set bonus (e.g. "3-piece bonus: +50 physical attack").
type EquipmentSetPieceBonus struct {
	PieceCount int32                       `json:"pieceCount"`
	Attributes []EquipmentPreviewAttribute `json:"attributes"`
}

// EquipmentSetBonus describes a full equipment part set: its display name and
// the per-threshold attribute bonuses (3-piece, 5-piece, 8-piece, etc.).
type EquipmentSetBonus struct {
	SetName string                   `json:"setName"`
	Pieces  []EquipmentSetPieceBonus `json:"pieces"`
}

// EquipmentAppendageEffectEntry is one stat modifier from an appendage effect.
type EquipmentAppendageEffectEntry struct {
	StatName string  `json:"statName"`
	Value    float64 `json:"value"`
	Negative bool    `json:"negative"`
}

// EquipmentAppendageEffect is the appendage (词条) effect attached to an
// equipment. TypeName is the translated effect category (e.g. "状态变化"),
// Entries lists the individual stat modifications.
type EquipmentAppendageEffect struct {
	AppendageName string                          `json:"appendageName"`
	TypeName      string                          `json:"typeName"`
	Entries       []EquipmentAppendageEffectEntry `json:"entries"`
}

// EquipmentAvatarSelectAbility is one entry in the [avatar select ability]
// block of a costume .equ file. Stat entries have Operator/Value; skill
// entries have SkillJob/SkillID/SkillLevel (key is "SKILL_LEVEL").
type EquipmentAvatarSelectAbility struct {
	Key        string  `json:"key"`
	Label      string  `json:"label"`
	Operator   string  `json:"operator"`
	Value      float64 `json:"value"`
	IsSkill    bool    `json:"isSkill"`
	SkillJob   string  `json:"skillJob,omitempty"`
	SkillID    string  `json:"skillID,omitempty"`
	SkillLevel int32   `json:"skillLevel,omitempty"`
}

// EquipmentPreviewDocument is the game-style data model rendered by the
// frontend. Optional sections are represented by empty strings/slices.
type EquipmentPreviewDocument struct {
	Icon             *ImageReference             `json:"icon"`
	Name             string                      `json:"name"`
	Name2            string                      `json:"name2"`
	Rarity           int32                       `json:"rarity"`
	RarityLabel      string                      `json:"rarityLabel"`
	QualityText      string                      `json:"qualityText"`
	EquipmentType    string                      `json:"equipmentType"`
	ItemGroupName    string                      `json:"itemGroupName"`
	AttachType       string                      `json:"attachType"`
	MinimumLevelText string                      `json:"minimumLevelText"`
	// GradeText 是 [grade]（装备实际等级）：卡片顶部按用户要求显示「等级 N」。
	GradeText string `json:"gradeText"`
	UsableJobs       []string                    `json:"usableJobs"`
	BaseAttributes   []EquipmentPreviewAttribute `json:"baseAttributes"`
	FourDimensions   []EquipmentPreviewAttribute `json:"fourDimensions"`
	OtherAttributes  []EquipmentPreviewAttribute `json:"otherAttributes"`
	SkillLevelups    []EquipmentSkillLevelup     `json:"skillLevelups"`
	BaseExplain      string                      `json:"baseExplain"`
	DetailExplain    string                      `json:"detailExplain"`
	FlavorText       string                      `json:"flavorText"`
	DurabilityText   string                      `json:"durabilityText"`
	WeightText       string                      `json:"weightText"`
	PriceText        string                      `json:"priceText"`
	Issues           []PreviewIssue              `json:"issues"`
	// SetBonuses 是装备所属套装的各件数属性加成（来自 [part set index] →
	// etc/equipmentpartset.etc → 套装 .equ 的 [piece set ability] 段）。
	SetBonuses []EquipmentSetBonus `json:"setBonuses"`
	// AppendageEffect 是装备的词条效果（来自 [appendage] → list/appendage.lst
	// → .apd 文件里的属性修正）。
	AppendageEffect *EquipmentAppendageEffect `json:"appendageEffect,omitempty"`
	// AvatarSelectAbilities 是时装的选择能力（[avatar select ability] 段），
	// 仅在时装类 .equ 里有值。
	AvatarSelectAbilities []EquipmentAvatarSelectAbility `json:"avatarSelectAbilities"`
}

// ParseEQU parses the current editor text. Archive state is only used for the
// file path and relation lookups; text itself always comes from the caller so
// unsaved changes are reflected immediately.
func (s *PreviewService) ParseEQU(fileIndex int32, text string) (*EquipmentPreviewDocument, error) {
	if s == nil || s.c == nil {
		engine, err := annotationrules.LoadDefault()
		if err != nil {
			return nil, err
		}
		return buildEquipmentPreview("preview.equ", text, engine, nil), nil
	}

	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	if s.c.archive == nil {
		engine, err := annotationrules.LoadDefault()
		if err != nil {
			return nil, err
		}
		return buildEquipmentPreview("preview.equ", text, engine, nil), nil
	}
	if err := validateAnnotationIndex(s.c.archive, fileIndex); err != nil {
		return nil, err
	}
	if s.c.annotationEngine == nil {
		if s.c.annotationErr != nil {
			return nil, s.c.annotationErr
		}
		return nil, fmt.Errorf("标注引擎未初始化")
	}
	doc := buildEquipmentPreview(
		s.c.archive.Path(fileIndex), text, s.c.annotationEngine,
		s.c.resolveAnnotationReferenceContextLocked,
	)
	// Newer clients store `<table::key>` placeholders instead of display text:
	// 名称、说明、详细介绍、风味文本都可能是 `{N=`<表号::键名>`}` 这种形式，
	// 显示前必须走**同一条**解析路径（2026-09-24：此前只解析了名称，导致
	// 预览图里名称正常、下方说明仍显示 `<3::basic_explain_...>`）。
	doc.Name = resolvePreviewText(s.c.archive, doc.Name)
	doc.Name2 = resolvePreviewText(s.c.archive, doc.Name2)
	// 说明类字段解析完还要再规范化一次：表里的译文自带 `%%`（脚本转义）与字面
	// `\n`，只解析不规范化会显示成 `+6%%`。normalizeExplain 是幂等的，非占位符
	// 的老路径重复走一遍不会改变结果。
	doc.BaseExplain = normalizeExplain(resolvePreviewText(s.c.archive, doc.BaseExplain))
	doc.DetailExplain = normalizeExplain(resolvePreviewText(s.c.archive, doc.DetailExplain))
	// 风味文本沿用原语义（不做 `%%`→`%`），只补占位符解析与换行规范化。
	doc.FlavorText = normalizeDisplayText(resolvePreviewText(s.c.archive, doc.FlavorText))
	// 套装属性与词条效果：从当前装备脚本里取 [part set index] / [appendage]
	// 的 ID，跨文件查到套装 .equ 或 .apd 并解析属性加成（2026-10-07 C2）。
	view := pvf.ParseScriptView(text)
	doc.SetBonuses = s.resolveSetBonusesLocked(view)
	doc.AppendageEffect = s.resolveAppendageEffectLocked(view)
	return doc, nil
}

// untranslatedMark is appended to a name that only a language overlay could
// answer: this client's own localization has no text for that key (its entry is
// an empty `key=`), so the fallback text is the Korean/translated original
// rather than what the game displays.
const untranslatedMark = "（未翻译）"

// resolvePreviewText turns a raw name value into display text: an optional
// `{N=`...`}` block marker is unwrapped and `<table::key>` placeholders are
// resolved through the archive's string tables.
func resolvePreviewText(a *pvf.Archive, text string) string {
	s := strings.TrimSpace(text)
	if s == "" {
		return text
	}
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		if eq := strings.IndexByte(s, '='); eq > 0 {
			s = strings.Trim(strings.TrimSpace(s[eq+1:len(s)-1]), "`")
		}
	}
	if a == nil {
		return s
	}
	return a.ResolvePlaceholdersMarked(s, untranslatedMark)
}

func buildEquipmentPreview(filePath, text string, engine *annotationrules.Engine, resolver annotationrules.ContextResolver) *EquipmentPreviewDocument {
	document := &EquipmentPreviewDocument{
		QualityText:     "最上级(100%)",
		UsableJobs:      make([]string, 0),
		BaseAttributes:  make([]EquipmentPreviewAttribute, 0),
		FourDimensions:  make([]EquipmentPreviewAttribute, 0),
		OtherAttributes: make([]EquipmentPreviewAttribute, 0),
		SkillLevelups:   make([]EquipmentSkillLevelup, 0),
		Issues:          make([]PreviewIssue, 0),
	}
	view := pvf.ParseScriptView(text)
	occurrences := engine.ExtractPreviewFields(filePath, view, "equ")

	roleValues := make(map[string][]annotationrules.PreviewFieldValue)
	for _, occurrence := range occurrences {
		role := ""
		if occurrence.Field.Preview != nil {
			role = occurrence.Field.Preview.Role
		}
		if role != "" {
			roleValues[role] = append(roleValues[role], occurrence)
		}
	}
	first := func(role string) (annotationrules.PreviewFieldValue, bool) {
		items := roleValues[role]
		if len(items) == 0 {
			return annotationrules.PreviewFieldValue{}, false
		}
		return items[0], true
	}

	if value, ok := first("name"); ok {
		document.Name = joinFieldValues(value.Values)
	}
	if value, ok := first("name2"); ok {
		document.Name2 = joinFieldValues(value.Values)
	}
	if value, ok := first("icon"); ok {
		readEquipmentIcon(document, value, text, &document.Issues)
	}
	if value, ok := first("rarity"); ok {
		raw := firstValue(value.Values)
		if parsed, ok := strconv.ParseInt(strings.TrimSpace(raw), 10, 32); ok == nil {
			document.Rarity = int32(parsed)
		} else {
			addPreviewIssue(&document.Issues, text, value.Start, "warning", "rarity", "稀有度不是有效整数: "+raw)
		}
		document.RarityLabel = enumValue(value.Field, raw, document, text, value.Start)
	}
	if value, ok := first("equipment-type"); ok {
		document.EquipmentType = enumValue(value.Field, firstValue(value.Values), document, text, value.Start)
	}
	if value, ok := first("item-group-name"); ok {
		document.ItemGroupName = enumValue(value.Field, firstValue(value.Values), document, text, value.Start)
	}
	if value, ok := first("attach-type"); ok {
		document.AttachType = enumValue(value.Field, firstValue(value.Values), document, text, value.Start)
	}
	if value, ok := first("minimum-level"); ok {
		document.MinimumLevelText = formatMinimumLevel(value, text, &document.Issues)
	}
	// [grade]：装备实际等级（用户 2026-10-01 要求显示在卡片顶部，取代稀有度数字）
	if value, ok := first("grade"); ok {
		document.GradeText = strings.TrimSpace(firstValue(value.Values))
	}
	if values := roleValues["usable-jobs"]; len(values) > 0 {
		for _, occurrence := range values {
			for _, raw := range occurrence.Values {
				raw = strings.TrimSpace(raw)
				if raw == "" || strings.EqualFold(raw, "[all]") {
					continue
				}
				document.UsableJobs = append(document.UsableJobs, enumValue(occurrence.Field, raw, document, text, occurrence.Start))
			}
		}
	}

	for _, occurrence := range roleValues["base-attribute"] {
		if attribute, ok := parseEquipmentAttribute(occurrence, text, &document.Issues); ok {
			document.BaseAttributes = append(document.BaseAttributes, attribute)
		}
	}
	for _, occurrence := range roleValues["four-dimension"] {
		if attribute, ok := parseEquipmentAttribute(occurrence, text, &document.Issues); ok {
			document.FourDimensions = append(document.FourDimensions, attribute)
		}
	}
	for _, occurrence := range roleValues["other-attribute"] {
		if attribute, ok := parseEquipmentAttribute(occurrence, text, &document.Issues); ok {
			document.OtherAttributes = append(document.OtherAttributes, attribute)
		}
	}

	for _, occurrence := range roleValues["skill-levelup"] {
		if len(occurrence.Values) < 3 {
			addPreviewIssue(&document.Issues, text, occurrence.Start, "error", "skill levelup", "技能等级加成记录需要职业、技能 ID 和等级")
			continue
		}
		job := enumValue(occurrence.Field, occurrence.Values[0], document, text, occurrence.Start)
		skillID := strings.TrimSpace(occurrence.Values[1])
		skill := skillID
		if resolver != nil {
			if reference, ok := resolver("技能", skillID, occurrence.Context); ok {
				if strings.TrimSpace(reference.Name) != "" {
					skill = reference.Name
				}
			} else {
				addPreviewIssue(&document.Issues, text, occurrence.Start, "warning", "skill levelup", "未找到技能 ID: "+skillID)
			}
		} else if skillID != "" {
			addPreviewIssue(&document.Issues, text, occurrence.Start, "warning", "skill levelup", "未配置技能关联，显示原始 ID: "+skillID)
		}
		level, err := strconv.ParseInt(strings.TrimSpace(occurrence.Values[2]), 10, 32)
		if err != nil {
			addPreviewIssue(&document.Issues, text, occurrence.Start, "warning", "skill levelup", "技能等级不是有效整数: "+occurrence.Values[2])
			continue
		}
		document.SkillLevelups = append(document.SkillLevelups, EquipmentSkillLevelup{Job: job, Skill: skill, Level: int32(level)})
	}

	for _, occurrence := range roleValues["base-explain"] {
		if document.BaseExplain == "" {
			document.BaseExplain = normalizeExplain(joinFieldValues(occurrence.Values))
		}
	}
	if value, ok := first("detail-explain"); ok {
		document.DetailExplain = normalizeExplain(joinFieldValues(value.Values))
	}
	if value, ok := first("flavor-text"); ok {
		document.FlavorText = normalizeDisplayText(joinFieldValuesPreserve(value.Values))
	}
	if value, ok := first("durability"); ok {
		document.DurabilityText = formatDurability(value, text, &document.Issues)
	}
	if value, ok := first("weight"); ok {
		document.WeightText = formatWeight(value, text, &document.Issues)
	}
	if value, ok := first("price"); ok {
		document.PriceText = formatPrice(value, text, &document.Issues)
	}

	document.AvatarSelectAbilities = extractAvatarSelectAbilities(view)
	return document
}

func readEquipmentIcon(document *EquipmentPreviewDocument, occurrence annotationrules.PreviewFieldValue, text string, issues *[]PreviewIssue) {
	pathIndex := occurrence.Field.Target.ImagePathToken
	imageIndex := occurrence.Field.Target.Index
	if pathIndex == nil || imageIndex == nil || *pathIndex < 0 || *imageIndex < 0 ||
		*pathIndex >= len(occurrence.Values) || *imageIndex >= len(occurrence.Values) {
		addPreviewIssue(issues, text, occurrence.Start, "warning", "icon", "图标记录缺少图片路径或索引")
		return
	}
	imagePath := strings.TrimSpace(occurrence.Values[*pathIndex])
	parsedIndex, err := strconv.ParseInt(strings.TrimSpace(occurrence.Values[*imageIndex]), 10, 32)
	if imagePath == "" || err != nil || parsedIndex < 0 {
		// An empty icon is a valid, non-blocking state. Do not manufacture a
		// broken image reference for the frontend.
		return
	}
	document.Icon = &ImageReference{Path: imagePath, Index: int32(parsedIndex)}
}

func enumValue(field annotationrules.FieldDefinition, raw string, document *EquipmentPreviewDocument, text string, start int) string {
	// 取值在脚本里有多种书写习惯（`` `[trade]` `` / `[trade]` / `HA WAIST`）：
	// 先去反引号精确匹配，再退一步做「忽略反引号与大小写」的兜底匹配，
	// 否则预览会把本该翻译成一等中文的词条原样显示成 `[trade]`、`ha waist`。
	value := strings.Trim(strings.TrimSpace(raw), "`")
	if label, ok := field.Annotation.Values[value]; ok {
		return label
	}
	// 兜底匹配可能命中多个同形键（如 `ha waist` 与 `HA WAIST`）：按归一化后的
	// 字典序取最小那个，保证同一份数据每次得到相同结果 —— map 迭代顺序是随机的，
	// 不排序会让同一行偶尔翻译成不同词。
	matchedKey, matchedLabel := "", ""
	for key, label := range field.Annotation.Values {
		normalized := strings.Trim(key, "`")
		if !strings.EqualFold(normalized, value) {
			continue
		}
		if matchedKey == "" || normalized < matchedKey {
			matchedKey, matchedLabel = normalized, label
		}
	}
	if matchedKey != "" {
		return matchedLabel
	}
	if len(field.Annotation.Values) > 0 && value != "" {
		addPreviewIssue(&document.Issues, text, start, "warning", field.Annotation.Title, "未知枚举值: "+value)
	}
	return value
}

func parseEquipmentAttribute(occurrence annotationrules.PreviewFieldValue, text string, issues *[]PreviewIssue) (EquipmentPreviewAttribute, bool) {
	label := occurrence.Field.Annotation.Title
	if occurrence.Field.Preview != nil && strings.TrimSpace(occurrence.Field.Preview.Label) != "" {
		label = occurrence.Field.Preview.Label
	}
	if strings.TrimSpace(label) == "" {
		return EquipmentPreviewAttribute{}, false
	}
	values := occurrence.Values
	if len(values) == 0 {
		return EquipmentPreviewAttribute{}, false
	}
	format := "signed-number"
	if occurrence.Field.Preview != nil {
		format = occurrence.Field.Preview.Format
	}
	if format == "range" {
		if len(values) < 2 {
			addPreviewIssue(issues, text, occurrence.Start, "warning", label, "范围属性缺少最大值或最小值")
			return EquipmentPreviewAttribute{}, false
		}
		left, leftOK := parsePreviewNumber(values[0])
		right, rightOK := parsePreviewNumber(values[1])
		if !leftOK || !rightOK {
			addPreviewIssue(issues, text, occurrence.Start, "warning", label, "范围属性包含非法数值")
			return EquipmentPreviewAttribute{}, false
		}
		if left > right {
			left, right = right, left
		}
		negative := left < 0 || right < 0
		return EquipmentPreviewAttribute{Label: label, Value: signedNumber(left) + "-" + trimLeadingPlus(formatNumber(right)), Negative: negative}, true
	}

	number, ok := parsePreviewNumber(firstValue(values))
	if !ok {
		addPreviewIssue(issues, text, occurrence.Start, "warning", label, "属性值不是有效数值: "+firstValue(values))
		return EquipmentPreviewAttribute{}, false
	}
	value := signedNumber(number)
	if format == "percent" {
		value += "%"
	}
	return EquipmentPreviewAttribute{Label: label, Value: value, Negative: number < 0}, true
}

func formatMinimumLevel(occurrence annotationrules.PreviewFieldValue, text string, issues *[]PreviewIssue) string {
	raw := firstValue(occurrence.Values)
	level := strings.Fields(strings.TrimSpace(raw))
	if len(level) == 0 {
		return ""
	}
	if _, err := strconv.Atoi(strings.Trim(level[0], "()")); err != nil {
		addPreviewIssue(issues, text, occurrence.Start, "warning", "minimum level", "使用等级不是有效整数: "+raw)
		return ""
	}
	return "Lv" + strings.Trim(level[0], "()") + "以上可以使用"
}

func formatDurability(occurrence annotationrules.PreviewFieldValue, text string, issues *[]PreviewIssue) string {
	raw := firstValue(occurrence.Values)
	number, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 32)
	if err != nil || number < 0 {
		addPreviewIssue(issues, text, occurrence.Start, "warning", "durability", "耐久度不是有效整数: "+raw)
		return ""
	}
	return fmt.Sprintf("%d/%d", number, number)
}

func formatWeight(occurrence annotationrules.PreviewFieldValue, text string, issues *[]PreviewIssue) string {
	number, ok := parsePreviewNumber(firstValue(occurrence.Values))
	if !ok || number < 0 {
		addPreviewIssue(issues, text, occurrence.Start, "warning", "weight", "重量不是有效数值: "+firstValue(occurrence.Values))
		return ""
	}
	if number < 1000 {
		return formatNumber(number) + "g"
	}
	return formatNumber(number/1000) + "kg"
}

func formatPrice(occurrence annotationrules.PreviewFieldValue, text string, issues *[]PreviewIssue) string {
	number, ok := parsePreviewNumber(firstValue(occurrence.Values))
	if !ok {
		addPreviewIssue(issues, text, occurrence.Start, "warning", "value", "售价不是有效数值: "+firstValue(occurrence.Values))
		return ""
	}
	return formatNumber(math.Trunc(number / 5))
}

func parsePreviewNumber(value string) (float64, bool) {
	value = strings.TrimSpace(strings.TrimSuffix(value, "%"))
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
}

func signedNumber(value float64) string {
	formatted := formatNumber(value)
	if value >= 0 {
		return "+" + formatted
	}
	return formatted
}

func trimLeadingPlus(value string) string {
	return strings.TrimPrefix(value, "+")
}

func formatNumber(value float64) string {
	if math.Trunc(value) == value {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func joinFieldValues(values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, strings.TrimSpace(value))
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func joinFieldValuesPreserve(values []string) string {
	return strings.Join(values, " ")
}

func firstValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func normalizeExplain(value string) string {
	return strings.TrimSpace(normalizeDisplayText(strings.ReplaceAll(value, "%%", "%")))
}

func normalizeDisplayText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	// Some exported text stores line breaks as the two-character escape `\\n`.
	value = strings.ReplaceAll(value, `\r\n`, "\n")
	value = strings.ReplaceAll(value, `\r`, "\n")
	value = strings.ReplaceAll(value, `\n`, "\n")
	return value
}

func addPreviewIssue(issues *[]PreviewIssue, text string, offset int, severity, section, message string) {
	if issues == nil {
		return
	}
	*issues = append(*issues, PreviewIssue{
		Severity: severity,
		Line:     int32(lineAtUTF16Offset(text, offset)),
		Section:  section,
		Message:  message,
	})
}

func lineAtUTF16Offset(text string, target int) int {
	if target < 0 {
		return 1
	}
	line := 1
	offset := 0
	runes := []rune(text)
	for i, value := range runes {
		if offset >= target {
			return line
		}
		if value == '\n' {
			line++
		}
		if value == '\r' && i+1 < len(runes) && runes[i+1] == '\n' {
			continue
		}
		offset += len(utf16.Encode([]rune{value}))
	}
	return line
}

// ---------------------------------------------------------------------------
// 套装属性 & 词条效果（C2 — 2026-10-07）
// ---------------------------------------------------------------------------

// resolveSetBonusesLocked 从当前装备脚本的 [part set index] 出发，查
// etc/equipmentpartset.etc 找到套装 .equ 路径，再解析里面的 [piece set ability]
// 各件数属性加成。调用方须持有 c.mu。
func (s *PreviewService) resolveSetBonusesLocked(view pvf.ScriptView) []EquipmentSetBonus {
	if s == nil || s.c == nil || s.c.archive == nil {
		return nil
	}
	var partSetID string
	for _, elem := range view.Elements {
		if elem.Kind == "token" && strings.EqualFold(elem.Section, "part set index") && elem.Index == 0 {
			partSetID = strings.TrimSpace(elem.Value)
			break
		}
	}
	if partSetID == "" {
		return nil
	}
	setName, setPath := s.lookupPartSetLocked(partSetID)
	if setPath == "" {
		return nil
	}
	fullPath := "equipment/" + setPath
	idx, ok := s.c.archive.Find(fullPath)
	if !ok {
		return nil
	}
	setText, err := s.c.cachedDecodedText(idx, s.c.archive)
	if err != nil {
		return nil
	}
	resolvedName := resolvePreviewText(s.c.archive, setName)
	if resolvedName == "" {
		resolvedName = setName
	}
	pieces := parseSetBonusFile(setText)
	if len(pieces) == 0 {
		return nil
	}
	return []EquipmentSetBonus{{SetName: resolvedName, Pieces: pieces}}
}

// lookupPartSetLocked 在 etc/equipmentpartset.etc 里找 ID 匹配的
// [equipment part set] 段，返回套装显示名和套装 .equ 相对路径。
func (s *PreviewService) lookupPartSetLocked(setID string) (name, path string) {
	idx, ok := s.c.archive.Find("etc/equipmentpartset.etc")
	if !ok {
		return "", ""
	}
	text, err := s.c.cachedDecodedText(idx, s.c.archive)
	if err != nil {
		return "", ""
	}
	view := pvf.ParseScriptView(text)
	inMatching := false
	depth := 0
	for _, elem := range view.Elements {
		if elem.Kind == "section" {
			if strings.EqualFold(elem.Value, "[equipment part set]") {
				depth++
				continue
			}
			if elem.Value == "[/equipment part set]" && depth > 0 {
				depth--
				inMatching = false
				continue
			}
			continue
		}
		if depth <= 0 {
			continue
		}
		if strings.EqualFold(elem.Section, "equipment part set") && elem.Index == 0 {
			if strings.TrimSpace(elem.Value) == setID {
				inMatching = true
				continue
			}
		}
		if !inMatching {
			continue
		}
		if strings.EqualFold(elem.Section, "equipment part set") {
			switch elem.Index {
			case 1:
				path = strings.Trim(strings.TrimSpace(elem.Value), "`")
			case 2:
				name = strings.Trim(strings.TrimSpace(elem.Value), "`")
			}
		}
	}
	return name, path
}

// parseSetBonusFile 解析套装 .equ 文件里的全部 [piece set ability] 段，
// 每段第一 token 是件数门槛，后续嵌套段是属性名 + 属性值。
func parseSetBonusFile(text string) []EquipmentSetPieceBonus {
	view := pvf.ParseScriptView(text)
	var bonuses []EquipmentSetPieceBonus
	var currentAttrs []EquipmentPreviewAttribute
	currentPiece := int32(0)
	inPieceSet := false
	inNested := false

	for _, elem := range view.Elements {
		if elem.Kind == "section" {
			if strings.EqualFold(elem.Value, "[piece set ability]") && !inPieceSet {
				inPieceSet = true
				currentAttrs = nil
				currentPiece = 0
				continue
			}
			if elem.Value == "[/piece set ability]" && inPieceSet && !inNested {
				if currentPiece > 0 {
					bonuses = append(bonuses, EquipmentSetPieceBonus{
						PieceCount: currentPiece,
						Attributes: currentAttrs,
					})
				}
				inPieceSet = false
				continue
			}
			if inPieceSet && !strings.EqualFold(elem.Value, "[piece set ability]") && !strings.EqualFold(elem.Value, "[/piece set ability]") {
				if !inNested {
					inNested = true
				}
				continue
			}
			if inNested && strings.HasPrefix(elem.Value, "[/") {
				inNested = false
				continue
			}
			continue
		}
		if !inPieceSet {
			continue
		}
		if strings.EqualFold(elem.Section, "piece set ability") && elem.Index == 0 && currentPiece == 0 {
			if n, err := strconv.ParseInt(strings.TrimSpace(elem.Value), 10, 32); err == nil {
				currentPiece = int32(n)
			}
			continue
		}
		if inNested && elem.Kind == "token" && elem.Index == 0 {
			label := translateStatName(elem.Section)
			val, err := strconv.ParseFloat(strings.TrimSpace(elem.Value), 64)
			if err == nil && label != "" {
				currentAttrs = append(currentAttrs, EquipmentPreviewAttribute{
					Label:    label,
					Value:    signedNumber(val),
					Negative: val < 0,
				})
			}
		}
	}
	return bonuses
}

// resolveAppendageEffectLocked 从 [appendage] 的 ID 出发，查 list/appendage.lst
// 找到 .apd 路径，解析属性修正。调用方须持有 c.mu。
func (s *PreviewService) resolveAppendageEffectLocked(view pvf.ScriptView) *EquipmentAppendageEffect {
	if s == nil || s.c == nil || s.c.archive == nil {
		return nil
	}
	var appendageID string
	for _, elem := range view.Elements {
		if elem.Kind == "token" && strings.EqualFold(elem.Section, "appendage") && elem.Index == 0 {
			appendageID = strings.TrimSpace(elem.Value)
			break
		}
	}
	if appendageID == "" {
		return nil
	}
	apdPath := s.lookupAppendagePathLocked(appendageID)
	if apdPath == "" {
		return nil
	}
	idx, ok := s.c.archive.Find(apdPath)
	if !ok {
		return nil
	}
	apdText, err := s.c.cachedDecodedText(idx, s.c.archive)
	if err != nil {
		return nil
	}
	return parseAppendageFile(apdText, s.c.archive)
}

// lookupAppendagePathLocked 在 list/appendage.lst（flat 格式，2 token/记录）
// 里找 ID 匹配的记录，返回 .apd 文件的归档路径。
func (s *PreviewService) lookupAppendagePathLocked(id string) string {
	idx, ok := s.c.archive.FindList("list/appendage.lst")
	if !ok {
		idx, ok = s.c.archive.Find("list/appendage.lst")
		if !ok {
			return ""
		}
	}
	text, err := s.c.cachedDecodedText(idx, s.c.archive)
	if err != nil {
		return ""
	}
	view := pvf.ParseScriptView(text)
	tokens := make([]string, 0, len(view.Elements))
	for _, elem := range view.Elements {
		if elem.Kind == "token" {
			tokens = append(tokens, strings.TrimSpace(elem.Value))
		}
	}
	for i := 0; i+1 < len(tokens); i += 2 {
		if tokens[i] == id {
			return strings.Trim(tokens[i+1], "`")
		}
	}
	return ""
}

// parseAppendageFile 解析 .apd 文件，提取名称、类型与属性修正条目。
func parseAppendageFile(text string, archive *pvf.Archive) *EquipmentAppendageEffect {
	view := pvf.ParseScriptView(text)
	result := &EquipmentAppendageEffect{}
	var stringData, intDataStr, floatDataStr []string
	for _, elem := range view.Elements {
		if elem.Kind != "token" {
			continue
		}
		switch {
		case strings.EqualFold(elem.Section, "name") && elem.Index == 0:
			raw := strings.Trim(strings.TrimSpace(elem.Value), "`")
			if archive != nil {
				result.AppendageName = archive.ResolvePlaceholdersMarked(raw, untranslatedMark)
			} else {
				result.AppendageName = raw
			}
		case strings.EqualFold(elem.Section, "type") && elem.Index == 0:
			result.TypeName = translateAppendageType(strings.Trim(strings.TrimSpace(elem.Value), "`"))
		case strings.EqualFold(elem.Section, "buff") && elem.Index == 0:
			// buff=1 正面 / buff=0 中性或负面，暂不在 UI 上区分颜色。
		case strings.EqualFold(elem.Section, "string data"):
			stringData = append(stringData, strings.Trim(strings.TrimSpace(elem.Value), "`"))
		case strings.EqualFold(elem.Section, "int data"):
			intDataStr = append(intDataStr, strings.TrimSpace(elem.Value))
		case strings.EqualFold(elem.Section, "float data"):
			floatDataStr = append(floatDataStr, strings.TrimSpace(elem.Value))
		}
	}
	floatVals := make([]float64, len(floatDataStr))
	for i, s := range floatDataStr {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			floatVals[i] = v
		}
	}
	result.Entries = buildAppendageEntries(result.TypeName, stringData, intDataStr, floatVals)
	if result.AppendageName == "" && len(result.Entries) == 0 {
		return nil
	}
	return result
}

// buildAppendageEntries 按词条类型把 string/int/float 原始数据组合成可显示的属性条目。
func buildAppendageEntries(typeName string, stringData []string, intData []string, floatData []float64) []EquipmentAppendageEffectEntry {
	if len(stringData) == 0 {
		return nil
	}
	var entries []EquipmentAppendageEffectEntry
	switch typeName {
	case "状态变化", "attack type", "change basic attack type":
		for i, name := range stringData {
			if name == "" {
				continue
			}
			val := 0.0
			if i < len(floatData) {
				val = floatData[i]
			}
			entries = append(entries, EquipmentAppendageEffectEntry{
				StatName: translateStatName(name),
				Value:    val,
				Negative: val < 0,
			})
		}
	case "技能数据强化":
		for i := 0; i+6 < len(stringData); i += 7 {
			label := stringData[i+3]
			if label == "" {
				continue
			}
			val, _ := strconv.ParseFloat(stringData[i+6], 64)
			entries = append(entries, EquipmentAppendageEffectEntry{
				StatName: translateStatName(label),
				Value:    val,
				Negative: val < 0,
			})
		}
	default:
		for i, name := range stringData {
			if name == "" {
				continue
			}
			val := 0.0
			if i < len(floatData) {
				val = floatData[i]
			}
			entries = append(entries, EquipmentAppendageEffectEntry{
				StatName: translateStatName(name),
				Value:    val,
				Negative: val < 0,
			})
		}
	}
	return entries
}

// translateAppendageType 把 .apd 的 [type] 原始值译成中文显示名。
func translateAppendageType(raw string) string {
	m := map[string]string{
		"change status":            "状态变化",
		"attack type":              "攻击类型",
		"change basic attack type": "改变普攻类型",
		"skill data up":            "技能数据强化",
	}
	if cn, ok := m[strings.ToLower(strings.TrimSpace(raw))]; ok {
		return cn
	}
	return raw
}

// translateStatName 把 .apd / 套装里的英文属性名译成中文。
func translateStatName(raw string) string {
	m := map[string]string{
		"physical attack":                "物理攻击力",
		"magical attack":                 "魔法攻击力",
		"physical defense":               "物理防御力",
		"magical defense":                "魔法防御力",
		"independent attack":             "独立攻击力",
		"physical critical hit rate":     "物理暴击率",
		"magical critical hit rate":      "魔法暴击率",
		"HP MAX":                         "HP 上限",
		"MP MAX":                         "MP 上限",
		"HP regen speed":                 "HP 回复量",
		"MP regen speed":                 "MP 回复量",
		"move speed":                     "移动速度",
		"attack speed":                   "攻击速度",
		"cast speed":                     "施放速度",
		"stuck resistance":               "硬直",
		"jump force":                     "跳跃力",
		"all elemental resistance":       "全属性抗性",
		"all elemental attack":           "全属性强化",
		"fire elemental attack":          "火属性强化",
		"water elemental attack":         "水属性强化",
		"dark elemental attack":          "暗属性强化",
		"light elemental attack":         "光属性强化",
		"fire elemental resistance":      "火属性抗性",
		"water elemental resistance":     "水属性抗性",
		"dark elemental resistance":      "暗属性抗性",
		"light elemental resistance":     "光属性抗性",
		"stuck":                          "僵直",
		"inventory limit":                "负重上限",
		"dark element":                   "暗属性",
		"light element":                  "光属性",
		"fire element":                   "火属性",
		"water element":                  "水属性",
		"[cooltime]":                     "冷却时间",
		"[all]":                          "全部技能",
	}
	if cn, ok := m[raw]; ok {
		return cn
	}
	return raw
}

// ---------------------------------------------------------------------------
// 时装选择能力（C3 — 2026-10-07）
// ---------------------------------------------------------------------------

// extractAvatarSelectAbilities 从 ScriptView 里提取 [avatar select ability] 段
// 的全部条目。属性条目 3 token（key / operator / value），技能条目 4 token
// （[SKILL_LEVEL] / job / skillID / level）。
func extractAvatarSelectAbilities(view pvf.ScriptView) []EquipmentAvatarSelectAbility {
	tokens := make([]string, 0, len(view.Elements))
	for _, elem := range view.Elements {
		if elem.Kind == "token" && strings.EqualFold(elem.Section, "avatar select ability") {
			tokens = append(tokens, strings.TrimSpace(elem.Value))
		}
	}
	if len(tokens) == 0 {
		return nil
	}
	var abilities []EquipmentAvatarSelectAbility
	i := 0
	for i < len(tokens) {
		if strings.EqualFold(tokens[i], "[SKILL_LEVEL]") {
			if i+3 >= len(tokens) {
				break
			}
			level, _ := strconv.ParseInt(tokens[i+3], 10, 32)
			abilities = append(abilities, EquipmentAvatarSelectAbility{
				Key:        "SKILL_LEVEL",
				Label:      "技能等级",
				IsSkill:    true,
				SkillJob:   tokens[i+1],
				SkillID:    tokens[i+2],
				SkillLevel: int32(level),
			})
			i += 4
			continue
		}
		if i+2 >= len(tokens) {
			break
		}
		key := strings.Trim(tokens[i], "`")
		op := strings.Trim(tokens[i+1], "`")
		val, _ := strconv.ParseFloat(tokens[i+2], 64)
		abilities = append(abilities, EquipmentAvatarSelectAbility{
			Key:      key,
			Label:    translateAvatarAbilityKey(key),
			Operator: op,
			Value:    val,
		})
		i += 3
	}
	return abilities
}

// translateAvatarAbilityKey 把 [avatar select ability] 里的英文键译成中文。
func translateAvatarAbilityKey(key string) string {
	m := map[string]string{
		"HP_MAX":              "HP最大值",
		"MP_MAX":              "MP最大值",
		"HP_REGEN":            "HP回复量",
		"MP_REGEN":            "MP回复量",
		"MOVE_SPEED":          "移动速度",
		"ATTACK_SPEED":        "攻击速度",
		"CAST_SPEED":          "施放速度",
		"PHYSICAL_ATTACK":     "物理攻击力",
		"MAGICAL_ATTACK":      "魔法攻击力",
		"PHYSICAL_DEFENSE":    "物理防御力",
		"MAGICAL_DEFENSE":     "魔法防御力",
		"INDEPENDENT_ATTACK":  "独立攻击力",
		"PHYSICAL_CRITICAL":   "物理暴击率",
		"MAGICAL_CRITICAL":    "魔法暴击率",
		"STUCK_RESISTANCE":    "硬直",
		"JUMP_FORCE":          "跳跃力",
		"ALL_ELEMENTAL_ATTACK":  "全属性强化",
		"FIRE_ELEMENTAL_ATTACK":  "火属性强化",
		"WATER_ELEMENTAL_ATTACK": "水属性强化",
		"DARK_ELEMENTAL_ATTACK":  "暗属性强化",
		"LIGHT_ELEMENTAL_ATTACK": "光属性强化",
		"ALL_ELEMENTAL_RESISTANCE":  "全属性抗性",
		"FIRE_ELEMENTAL_RESISTANCE":  "火属性抗性",
		"WATER_ELEMENTAL_RESISTANCE": "水属性抗性",
		"DARK_ELEMENTAL_RESISTANCE":  "暗属性抗性",
		"LIGHT_ELEMENTAL_RESISTANCE": "光属性抗性",
		"INVENTORY_LIMIT":     "负重上限",
		"SKILL_LEVEL":         "技能等级",
	}
	if cn, ok := m[key]; ok {
		return cn
	}
	return key
}
