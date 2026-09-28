package services

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"pvfine/internal/ai"
	"pvfine/internal/logging"
)

// AIService 是「AI 助手」的对话入口（方案见 AI镶嵌.md §四.4）。
// 只依赖 core 与设置：模型接入点由用户在设置里配置（OpenAI 兼容），
// 不内置、不锁死任何厂商；写保护默认拦截全部写工具。
type AIService struct {
	c        *core
	settings *SettingsService
}

func NewAIService(c *core, settings *SettingsService) *AIService {
	return &AIService{c: c, settings: settings}
}

// AIMessage 是前端传来的会话消息（不含 system，由后端统一拼装）。
type AIMessage struct {
	Role    string `json:"role"` // user | assistant | tool
	Content string `json:"content"`
	// ToolCallID 在 Role=tool 时必填：对应上一轮模型发起的工具调用 ID。
	ToolCallID string `json:"toolCallId,omitempty"`
}

// EditorContext 是前端随每次提问传来的编辑器当前状态，
// 让模型「看见」用户正在编辑的文件、选区与光标，从而给出上下文相关建议。
type EditorContext struct {
	ActivePath  string   `json:"activePath"`  // 当前活动文件路径
	ActiveText  string   `json:"activeText"`  // 当前文件内容（前端已截断）
	Selection   string   `json:"selection"`   // 选中的文本
	CursorLine  int      `json:"cursorLine"`  // 光标行号（1 起；0 表示未知）
	CursorCol   int      `json:"cursorCol"`   // 光标列号（1 起；0 表示未知）
	RecentPaths []string `json:"recentPaths"` // 最近打开的文件路径
}

type AIChatRequest struct {
	Messages []AIMessage   `json:"messages"`
	Context  EditorContext `json:"context"`
}

// AIToolRun 回报本次实际执行过的工具（供面板回显，只含名称与单行摘要）。
type AIToolRun struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// AIRef 是 AI 回复里的一处可定位引用（与编辑器注释引擎同源的「语义坐标」思路：
// 让回答与文本位置建立可跳转的关联）。
// Kind 取值：file（文件，可带行号）/ dir（目录）/ object（对象，如「装备 10018」）。
type AIRef struct {
	Path  string `json:"path,omitempty"`
	Line  int    `json:"line,omitempty"`
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
}

type AIChatResponse struct {
	Reply string      `json:"reply"`
	Runs  []AIToolRun `json:"runs"`
	Refs  []AIRef     `json:"refs,omitempty"`
}

// aiMaxToolRounds 限制一次提问内的工具调用轮数，防止模型无限循环调用。
// P-2026.09：4 → 8，配合 search_files 相关性排序与知识库工具，减少"轮次不够被迫拆问"。
const aiMaxToolRounds = 8

const (
	// aiMaxToolResultRunes 是单条工具结果回填给模型的字符上限。工具原始回显上限是
	// 20000，但结果会**永久留在历史里**并在接下来每一轮原样重传 —— 8 轮下来能把请求
	// 撑到几十万字符，每轮首字延迟随之线性变长。这里只在写入历史时压一道，
	// 面板里的工具回显仍用完整 result，不影响用户查看。
	aiMaxToolResultRunes = 8000
	// aiMaxHistoryRunes 是整段对话（含 system）发给模型的字符预算，超出后从最早的
	// 工具结果开始瘦身，保证长对话不会越聊越慢。
	aiMaxHistoryRunes = 120000
)

// Chat 处理一轮对话：内部执行「模型 → 工具 → 回填 → 再问」循环后返回最终回复。
// ctx 由 Wails 注入：前端取消（CancellablePromise.cancel）会取消 ctx，立即中断当前生成。
func (s *AIService) Chat(ctx context.Context, req AIChatRequest) (AIChatResponse, error) {
	cfg, aiCfg, err := s.aiConfig()
	if err != nil {
		return AIChatResponse{}, err
	}

	registered := buildAITools(s.c, s.settings)
	byName := make(map[string]AITool, len(registered))
	definitions := make([]ai.Tool, 0, len(registered))
	for _, tool := range registered {
		byName[tool.Name] = tool
		definitions = append(definitions, ai.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Schema,
		})
	}

	messages := make([]ai.ChatMessage, 0, len(req.Messages)+1)
	messages = append(messages, ai.ChatMessage{Role: "system", Content: s.systemPrompt(req.Context)})
	for _, m := range req.Messages {
		switch m.Role {
		case "tool":
			messages = append(messages, ai.ChatMessage{Role: "tool", Content: m.Content, ToolCallID: m.ToolCallID})
		case "assistant":
			messages = append(messages, ai.ChatMessage{Role: "assistant", Content: m.Content})
		default:
			messages = append(messages, ai.ChatMessage{Role: "user", Content: m.Content})
		}
	}

	var runs []AIToolRun
	refs := make([]AIRef, 0, 8)
	refSeen := make(map[string]struct{})
	appendRef := func(ref AIRef) {
		key := ref.Kind + "|" + ref.Path + "|" + strconv.Itoa(ref.Line) + "|" + ref.Label
		if _, exists := refSeen[key]; exists || len(refSeen) >= aiMaxRefs {
			return
		}
		refSeen[key] = struct{}{}
		refs = append(refs, ref)
	}
	reply := ""
	aiLog := logging.For("ai")
	chatStartedAt := time.Now()
	for round := 0; round < aiMaxToolRounds; round++ {
		// 前端点「停止思考」会取消 ctx：立即中止本轮及后续轮次。
		if err := ctx.Err(); err != nil {
			aiLog.Warn("AI 对话被取消", "轮次", round+1, "耗时", logging.FormatDuration(time.Since(chatStartedAt)))
			return AIChatResponse{}, err
		}
		prompt := trimAIHistory(messages)
		roundStartedAt := time.Now()
		text, calls, err := s.chatRound(ctx, cfg, prompt, definitions)
		roundCost := time.Since(roundStartedAt)
		if err != nil {
			// 失真/超时/取消都要留下可定位的现场：轮次、耗时、提示词规模。
			aiLog.Error("AI 单轮请求失败",
				"轮次", round+1, "耗时", logging.FormatDuration(roundCost),
				"提示词字符", aiMessageRunes(prompt), "错误", ai.RedactError(err))
			return AIChatResponse{}, err
		}
		aiLog.Info("AI 单轮完成",
			"轮次", round+1, "耗时", logging.FormatDuration(roundCost),
			"提示词字符", aiMessageRunes(prompt), "回复字符", utf8.RuneCountInString(text),
			"工具调用", len(calls))
		if strings.TrimSpace(text) != "" {
			reply = text
		}
		if len(calls) == 0 {
			if strings.TrimSpace(reply) == "" {
				reply = "（模型没有返回内容）"
			}
			for _, ref := range aiRefsFromReply(reply) {
				appendRef(ref)
			}
			return AIChatResponse{Reply: reply, Runs: runs, Refs: refs}, nil
		}
		messages = append(messages, ai.ChatMessage{Role: "assistant", ToolCalls: calls})
		for _, call := range calls {
			emitEvent("ai:tool", map[string]any{"name": call.Name, "phase": "start"})
			result := s.runAITool(byName, aiCfg, call)
			emitEvent("ai:tool", map[string]any{
				"name": call.Name, "phase": "done", "summary": aiSummarize(result)})
			for _, ref := range aiRefsFromTool(call.Name, call.Arguments) {
				appendRef(ref)
			}
			// 工具结果写入历史时压到预算内（原样 result 仅用于上面的界面回显）。
			messages = append(messages, ai.ChatMessage{
				Role: "tool", Content: aiTruncate(result, aiMaxToolResultRunes), ToolCallID: call.ID})
		}
	}
	if strings.TrimSpace(reply) == "" {
		reply = "工具调用轮次过多，已停止执行；请把问题拆小后再试。"
	}
	for _, ref := range aiRefsFromReply(reply) {
		appendRef(ref)
	}
	return AIChatResponse{Reply: reply, Runs: runs, Refs: refs}, nil
}

// aiMaxRefs 限制单次回复携带的引用条数，避免界面被链接淹没。
const aiMaxRefs = 20

// aiRefPathLinePattern 匹配回复中的「归档路径:行号」（如 vest_owool.equ:7）。
var aiRefPathLinePattern = regexp.MustCompile(`([A-Za-z0-9_][A-Za-z0-9_./\-\[\]]*\.(?:equ|stk|lst|etc|shp|dgn|map|mob|obj|npc|qst|cre|apd|twn|rgn|wdm|aic|chr|vm|evt|tbl|str|po|co|key|ani|act|ai|lay|xui|xml)):(\d+)`)

// aiRefsFromTool 把工具调用折算成可定位引用（记录 AI 访问过的文件/目录/对象）。
func aiRefsFromTool(name, args string) []AIRef {
	parsed, err := aiParseArgs(args)
	if err != nil {
		return nil
	}
	path := strings.Trim(strings.TrimSpace(aiStringArg(parsed, "path")), "/")
	switch name {
	case "read_file", "edit_file":
		if path != "" {
			return []AIRef{{Path: path, Kind: "file"}}
		}
	case "list_directory":
		return []AIRef{{Path: path, Kind: "dir"}}
	case "object_view":
		objectType := strings.TrimSpace(aiStringArg(parsed, "objectType"))
		objectID := strings.TrimSpace(aiStringArg(parsed, "objectID"))
		if objectType != "" && objectID != "" {
			return []AIRef{{Kind: "object", Label: objectType + " " + objectID}}
		}
	}
	return nil
}

// aiRefsFromReply 从回复文本中解析「归档路径:行号」，得到精确到行的引用。
func aiRefsFromReply(reply string) []AIRef {
	matches := aiRefPathLinePattern.FindAllStringSubmatch(reply, -1)
	refs := make([]AIRef, 0, len(matches))
	for _, match := range matches {
		line, err := strconv.Atoi(match[2])
		if err != nil || line <= 0 {
			continue
		}
		refs = append(refs, AIRef{Path: match[1], Line: line, Kind: "file"})
	}
	return refs
}

// TestConnection 用当前配置发一次最小对话，验证接入点与 key 可用。
func (s *AIService) TestConnection() error {
	cfg, _, err := s.aiConfig()
	if err != nil {
		return err
	}
	_, _, err = ai.Chat(context.Background(), cfg,
		[]ai.ChatMessage{{Role: "user", Content: "请只回复两个字母：OK"}}, nil)
	return err
}

// runAITool 执行一个工具调用；「AI 写保护」开启时拒绝所有写工具（需求 1）。
func (s *AIService) runAITool(byName map[string]AITool, aiCfg AIAssistantSettings, call ai.ToolCall) (result string) {
	tool, ok := byName[call.Name]
	if !ok {
		return fmt.Sprintf("未知工具: %s", call.Name)
	}
	if !tool.ReadOnly && aiCfg.WriteProtection {
		return "写保护已开启：AI 无法修改文件。请用户在 AI 面板或设置里关闭「AI 写保护」后再重试。"
	}
	defer func() {
		if r := recover(); r != nil {
			result = fmt.Sprintf("工具执行异常: %v", r)
		}
	}()
	output, err := tool.Run(call.Arguments)
	if err != nil {
		return "工具执行失败: " + err.Error()
	}
	if strings.TrimSpace(output) == "" {
		return "(无输出)"
	}
	return output
}

// aiConfig 读取并补全模型接入配置：预设服务商允许只填一半，空缺用预设补齐。
func (s *AIService) aiConfig() (ai.Config, AIAssistantSettings, error) {
	if s.settings == nil {
		return ai.Config{}, AIAssistantSettings{}, fmt.Errorf("设置服务不可用")
	}
	settings, err := s.settings.GetSettings()
	if err != nil {
		return ai.Config{}, AIAssistantSettings{}, err
	}
	aiCfg := settings.AI
	if !aiCfg.Enabled {
		return ai.Config{}, aiCfg, fmt.Errorf("AI 助手未启用：请先到「设置 → AI 助手」开启并配置模型")
	}
	if preset, ok := ai.LookupProvider(aiCfg.Provider); ok {
		if strings.TrimSpace(aiCfg.BaseURL) == "" {
			aiCfg.BaseURL = preset.BaseURL
		}
		if strings.TrimSpace(aiCfg.Model) == "" {
			aiCfg.Model = preset.DefaultModel
		}
	}
	if strings.TrimSpace(aiCfg.BaseURL) == "" || strings.TrimSpace(aiCfg.Model) == "" {
		return ai.Config{}, aiCfg, fmt.Errorf("AI 接入地址或模型为空：请到「设置 → AI 助手」补全")
	}
	return ai.Config{BaseURL: aiCfg.BaseURL, Model: aiCfg.Model, APIKey: aiCfg.APIKey}, aiCfg, nil
}

// chatRound 执行一轮模型请求：**优先 SSE 流式**，边生成边向前端推增量文本；
// 接入点不支持流式（4xx / 非事件流响应）时自动退回一次性请求，用户无感。
//
// 为什么流式：旗舰模型的等待感几乎全部来自"看不见它在动"。流式把首字延迟从
// "整段生成完"缩短到几百毫秒，配合面板里的工具调用进度，用户始终知道 AI 在做什么。
func (s *AIService) chatRound(
	ctx context.Context,
	cfg ai.Config,
	prompt []ai.ChatMessage,
	definitions []ai.Tool,
) (string, []ai.ToolCall, error) {
	var pending strings.Builder
	lastFlush := time.Now()
	flush := func() {
		if pending.Len() == 0 {
			return
		}
		emitEvent("ai:delta", map[string]any{"text": pending.String()})
		pending.Reset()
		lastFlush = time.Now()
	}
	text, calls, err := ai.ChatStream(ctx, cfg, prompt, definitions, func(chunk string) {
		pending.WriteString(chunk)
		// 攒够一小段或超过 80ms 才推一次：逐 token 推事件会把 IPC 打满。
		if pending.Len() >= 160 || time.Since(lastFlush) >= 80*time.Millisecond {
			flush()
		}
	})
	flush()
	if errors.Is(err, ai.ErrStreamUnsupported) {
		return ai.Chat(ctx, cfg, prompt, definitions)
	}
	return text, calls, err
}

// aiMessageRunes 统计整段对话的字符数（日志用，避免不能用字节估算模型输入规模）。
func aiMessageRunes(messages []ai.ChatMessage) int {
	total := 0
	for _, message := range messages {
		total += utf8.RuneCountInString(message.Content)
	}
	return total
}

// trimAIHistory 把对话历史压到字符预算内：多轮工具调用会在历史里留下大量结果，
// 而每一轮都要整段重传给模型 —— 不瘦身的话输入越长、单轮越慢，表现为"越聊越卡"。
// 策略：保留 system 与全部非工具消息，只把**最早**的工具结果逐个削到摘要，
// 最新一轮的上下文始终完整。
func trimAIHistory(messages []ai.ChatMessage) []ai.ChatMessage {
	if aiMessageRunes(messages) <= aiMaxHistoryRunes {
		return messages
	}
	trimmed := make([]ai.ChatMessage, len(messages))
	copy(trimmed, messages)
	for i := 1; i < len(trimmed) && aiMessageRunes(trimmed) > aiMaxHistoryRunes; i++ {
		message := &trimmed[i]
		if message.Role != "tool" {
			continue
		}
		runes := []rune(message.Content)
		if len(runes) <= 400 {
			continue
		}
		message.Content = string(runes[:400]) + "\n…（较早的工具结果已省略，需要时用工具重新获取）"
	}
	return trimmed
}

// systemPrompt 组装系统提示：角色 + 工具用法 + 安全边界 + 注释数据概况 + 编辑器上下文。
func (s *AIService) systemPrompt(ctx EditorContext) string {
	var sb strings.Builder
	sb.WriteString("你是 DNF（地下城与勇士）PVF 归档编辑器「PVF工坊」内置的 AI 助手。\n")
	sb.WriteString("用简体中文、面向普通玩家回答；涉及修改时先用一句话说明要改什么，再调用工具。\n\n")
	sb.WriteString("可用工具：search_files（搜文件，结果已按名称相关性排序）、read_file（读文件）、list_directory（列目录，可 offset 翻页）、")
	sb.WriteString("object_view（对象聚合视图）、field_semantics（查字段语义）、resolve_string（查字符串表译文）、")
	sb.WriteString("search_knowledge（查内置知识库）、read_knowledge（读知识库文件）、")
	sb.WriteString("diagnose（归档体检）、diff_file（查看改动）、describe_batch_ops（查批量能力）、")
	sb.WriteString("preview_replace（批量替换预览，只读）、apply_replace（应用替换计划，写操作）、")
	sb.WriteString("edit_file（写操作，可能被写保护拦截；save_archive 已禁用，归档保存只由人类执行）。\n\n")
	sb.WriteString("高效搜索策略（务必遵守，能一次调用完成就不要多轮试错）：\n")
	sb.WriteString("- 归档内已有同名/语义名的对象（NPC、怪物、装备、商店）直接以其名字或中文名作 query，一次 search_files 即可，返回值已按相关性排序，不要反复换同义词重搜；\n")
	sb.WriteString("- 已知大致目录范围时传 scope 限定（如 stackable/shop、equipment/character/mage），既准又快；\n")
	sb.WriteString("- 目录结构不确定时先 list_directory 浏览，再带 scope 精准搜索；目录超过 50 条用 offset 翻页；\n")
	sb.WriteString("- PVF 语法、字段含义、脚本写法类问题：先用 search_knowledge 查知识库，再结合 field_semantics 与 read_file。\n\n")
	sb.WriteString("批量修改流程（务必遵守）：先用 search_files 定位文件 → 用 preview_replace 预览改动并展示给用户 → ")
	sb.WriteString("等用户确认后，才用 apply_replace 应用；应用只写内存覆盖层，改完提示用户手动保存归档。\n")
	sb.WriteString("导入/导出大批文件请在界面使用「导入文件…」「导出文件」功能，AI 更适合精确定位与小范围修改。\n\n")
	sb.WriteString("引用位置规范：read_file 返回的每一行都带「行号:」前缀（行号与编辑器一致）。")
	sb.WriteString("回答中凡涉及具体位置，必须写成「归档路径:行号」，例如 equipment/character/common/jacket/cloth/vest_owool.equ:7；")
	sb.WriteString("界面会把它渲染成可点击的定位链接，用户一点即可跳转到该文件该行。\n\n")
	sb.WriteString("安全边界：\n")
	sb.WriteString("- 未经用户明确要求，不要调用写工具；\n")
	sb.WriteString("- 归档保存属于人类操作，AI 禁止自动归档：绝不调用 save_archive；")
	sb.WriteString("edit_file 只写内存覆盖层，改完后明确提示用户手动保存归档；\n")
	sb.WriteString("- 不覆盖用户的源 PVF：保存只走应用内既有流程；\n")
	sb.WriteString("- 字符串表等敏感写入由应用自身的写保护兜底，被拒时向用户解释原因。\n\n")
	if s.c != nil && s.c.annotationEngine != nil {
		document := s.c.annotationEngine.Document()
		fmt.Fprintf(&sb, "当前注释数据概况：字段说明 %d 条、规则 %d 条、引用关系 %d 组；字段语义可用 field_semantics 查询。\n\n",
			len(document.Fields), len(document.Rules), len(document.Relations))
	}
	if summary := knowledgeSummary(); summary != "" {
		fmt.Fprintf(&sb, "%s\n\n", summary)
	}
	s.writeContextSection(&sb, ctx)
	sb.WriteString("归档未打开时，归档类工具会报错；此时请提示用户先打开 PVF 归档。")
	return sb.String()
}

// writeContextSection 把前端传来的编辑器当前状态写进系统提示，
// 让模型能「看见」用户正在编辑的文件与选区（上下文感知）。
func (s *AIService) writeContextSection(sb *strings.Builder, ctx EditorContext) {
	if strings.TrimSpace(ctx.ActivePath) == "" {
		return
	}
	sb.WriteString("当前编辑器上下文：\n")
	fmt.Fprintf(sb, "- 活动文件：%s", ctx.ActivePath)
	if ctx.CursorLine > 0 {
		fmt.Fprintf(sb, "（光标第 %d 行", ctx.CursorLine)
		if ctx.CursorCol > 0 {
			fmt.Fprintf(sb, "第 %d 列", ctx.CursorCol)
		}
		sb.WriteString("）")
	}
	sb.WriteString("\n")
	if sel := strings.TrimSpace(ctx.Selection); sel != "" {
		fmt.Fprintf(sb, "- 选中文本：%s\n", aiTruncate(sel, 2000))
	}
	if text := strings.TrimSpace(ctx.ActiveText); text != "" {
		fmt.Fprintf(sb, "- 该文件内容（截断）：\n%s\n", aiTruncate(text, 3000))
	}
	if len(ctx.RecentPaths) > 0 {
		recent := make([]string, 0, len(ctx.RecentPaths))
		for _, path := range ctx.RecentPaths {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			recent = append(recent, path)
			if len(recent) >= 8 {
				break
			}
		}
		if len(recent) > 0 {
			sb.WriteString("- 最近打开的文件：")
			sb.WriteString(strings.Join(recent, "、"))
			sb.WriteString("\n")
		}
	}
	sb.WriteString("回答请优先围绕上述文件与选中内容；需要完整内容时用 read_file 读取。\n\n")
}

// aiSummarize 把工具结果压成单行短摘要，供面板回显（不把大段输出塞进 UI）。
func aiSummarize(result string) string {
	oneLine := strings.Join(strings.Fields(result), " ")
	runes := []rune(oneLine)
	if len(runes) > 120 {
		return string(runes[:120]) + "…"
	}
	return oneLine
}
