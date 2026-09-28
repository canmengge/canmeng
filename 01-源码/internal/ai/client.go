// Package ai 是「AI 助手」的模型接入抽象层（方案见 AI镶嵌.md §四.2）。
//
// 只讲 OpenAI 兼容的 /chat/completions 协议：DeepSeek、通义千问（兼容模式）、
// Kimi、Ollama、vLLM 以及各类自建网关都实现了这套协议，因此用户只要给
// baseURL + 模型名 + key（本地服务可空）就能接入，不预设、不锁死任何厂商。
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

// RedactError 把可能含 API Key / 地址的请求错误洗成可安全落盘的短串。
func RedactError(err error) string {
	if err == nil {
		return ""
	}
	return truncateRunes(err.Error(), 300)
}

// requestTimeout 是一次「模型生成」的等待上限（不是前端交互上限）。
//
// 为什么从 60s 提到 15min：旗舰模型 / 推理型模型（o 系、DeepSeek-reasoner、
// QwQ 等）单次生成普遍需要 1~5 分钟，60s 会在模型还在思考时就硬断回合，
// 表象是"AI 突然不说话了"。现在的取消入口只有一个 —— 用户点「停止思考」
// （前端 CancellablePromise.cancel），所以服务端不需要用短超时抢自己的活。
const requestTimeout = 15 * time.Minute

// chatHTTPClient 复用连接；整体超时交给 ctx，避免误伤长思考时间的模型。
var chatHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 2 * time.Second,
		WriteBufferSize:       64 << 10,
		ReadBufferSize:        64 << 10,
	},
}

// Config 描述一次对话使用的模型接入点。APIKey 只在本包内用于请求头，
// 绝不写入错误信息（避免随日志落盘）。
type Config struct {
	BaseURL string
	Model   string
	APIKey  string
}

// ChatMessage 是一条对话消息。工具轮次需要 Role=assistant 携带 ToolCalls、
// Role=tool 携带 ToolCallID，与 OpenAI 协议一致。
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// Tool 是暴露给模型的函数定义（JSON Schema）。
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ToolCall 是模型要求执行的一次工具调用。
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ---- OpenAI 兼容协议的线上结构 ----

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools,omitempty"`
	// Stream 用 omitempty：非流式请求保持与旧版完全一致的报文（不额外发 stream:false）。
	Stream bool `json:"stream,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"` // 固定 "function"
	Function chatToolFunc `json:"function"`
}

type chatToolFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// chatToolCallFunc 是模型发起的工具调用的 function 块（只有名字与参数串），
// 与工具定义的 chatToolFunc 字段不同，不能混用。
type chatToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatToolCallFunc `json:"function"`
	// Index 只在流式 delta 里出现：标记本次工具调用在工具列表里的序号，
	// 用于把跨帧打散的 id / name / arguments 片段拼回同一条 ToolCall。
	Index int `json:"index,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content   string         `json:"content"`
			ToolCalls []chatToolCall `json:"tool_calls"`
			// ReasoningContent 兼容「推理型」模型：部分厂商把正文放在这里而
			// content 为空/为 null，不兜一层就会出现"AI 什么都不说"。
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat 发送一轮对话。模型直接回答时返回 (reply, nil, nil)；要求调用工具时返回
// ("", calls, nil)，由调用方执行工具后把结果以 Role=tool 追加再发下一轮。
func Chat(ctx context.Context, cfg Config, messages []ChatMessage, tools []Tool) (string, []ToolCall, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return "", nil, fmt.Errorf("AI 接入地址为空，请先在设置里配置")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return "", nil, fmt.Errorf("AI 模型为空，请先在设置里配置")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	request := buildChatRequest(cfg.Model, messages, tools, false)
	payload, err := json.Marshal(request)
	if err != nil {
		return "", nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		chatEndpoint(cfg.BaseURL), bytes.NewReader(payload))
	if err != nil {
		return "", nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(cfg.APIKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.APIKey))
	}

	resp, err := chatHTTPClient.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("连接 AI 服务失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", nil, fmt.Errorf("读取 AI 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("AI 服务返回 %d: %s", resp.StatusCode, truncateRunes(string(body), 300))
	}
	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", nil, fmt.Errorf("解析 AI 响应失败: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", nil, fmt.Errorf("AI 服务报错: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", nil, fmt.Errorf("AI 服务没有返回任何结果")
	}
	choice := parsed.Choices[0]
	content := choice.Message.Content
	if strings.TrimSpace(content) == "" && strings.TrimSpace(choice.Message.ReasoningContent) != "" {
		// 推理型模型把答案写进 reasoning_content：直接采用，避免空回复。
		content = choice.Message.ReasoningContent
	}
	var calls []ToolCall
	for _, call := range choice.Message.ToolCalls {
		calls = append(calls, ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	if len(calls) > 0 {
		return content, calls, nil
	}
	if strings.TrimSpace(content) == "" {
		// 既没正文也没工具调用：把 finish_reason 带出来，用户/日志一眼能区分
		// 是"被截断""被过滤"还是厂商真的没给东西。
		return "", nil, fmt.Errorf("AI 服务返回了空内容（finish_reason=%s）", nonEmptyReason(choice.FinishReason))
	}
	return content, nil, nil
}

func nonEmptyReason(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "未知"
	}
	return reason
}

// ErrStreamUnsupported 表示接入点不接受 SSE 流式输出（返回了非事件流响应）。
// 服务层据此退回一次性请求，用户无感。
var ErrStreamUnsupported = errors.New("接入点不支持流式输出")

// ChatStream 走 SSE 流式接口：每拿到一小段正文就回调 onDelta，让界面边算边显示。
//
// 返回完整正文与工具调用；语义与非流式的 Chat 完全一致，因此可以互相兜底：
// 一次性请求的一切 PreCheck / 鉴权 / 容错规则这里都保留。
func ChatStream(
	ctx context.Context,
	cfg Config,
	messages []ChatMessage,
	tools []Tool,
	onDelta func(string),
) (string, []ToolCall, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return "", nil, fmt.Errorf("AI 接入地址为空，请先在设置里配置")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return "", nil, fmt.Errorf("AI 模型为空，请先在设置里配置")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	payload, err := json.Marshal(buildChatRequest(cfg.Model, messages, tools, true))
	if err != nil {
		return "", nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		chatEndpoint(cfg.BaseURL), bytes.NewReader(payload))
	if err != nil {
		return "", nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")
	if strings.TrimSpace(cfg.APIKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.APIKey))
	}

	resp, err := chatHTTPClient.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("连接 AI 服务失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		// 4xx 里绝大多数是接入点不认 stream 参数（或网关把它拦了）：让调用方退回一次性请求。
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return "", nil, ErrStreamUnsupported
		}
		return "", nil, fmt.Errorf("AI 服务返回 %d: %s", resp.StatusCode, truncateRunes(string(body), 300))
	}
	if contentType := strings.ToLower(resp.Header.Get("Content-Type")); !strings.Contains(contentType, "event-stream") {
		return "", nil, ErrStreamUnsupported
	}
	return readChatStream(resp.Body, onDelta)
}

// streamEnvelope 是 SSE 单帧的结构（一次性响应结构的一个超集）。
type streamEnvelope struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
			// ReasoningContent 兼容推理型模型把思考内容单独吐流的写法。
			ReasoningContent string         `json:"reasoning_content"`
			ToolCalls        []chatToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// readChatStream 解析 SSE：忽略注释行与心跳，按 [DONE] 收尾；工具调用按 index 累积
// （delta 只会给出 id/name 的第一帧，arguments 是逐字片段，必须自己拼）。
func readChatStream(body io.Reader, onDelta func(string)) (string, []ToolCall, error) {
	var content strings.Builder
	var reasoning strings.Builder
	callsByIndex := make(map[int]*ToolCall)

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		var env streamEnvelope
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			continue // 单帧脏数据不打断整条流
		}
		if env.Error != nil && env.Error.Message != "" {
			return "", nil, fmt.Errorf("AI 服务报错: %s", env.Error.Message)
		}
		if len(env.Choices) == 0 {
			continue
		}
		delta := env.Choices[0].Delta
		if delta.Content != "" {
			content.WriteString(delta.Content)
			if onDelta != nil {
				onDelta(delta.Content)
			}
		}
		if delta.ReasoningContent != "" {
			reasoning.WriteString(delta.ReasoningContent)
		}
		for _, piece := range delta.ToolCalls {
			current := callsByIndex[piece.Index]
			if current == nil {
				callsByIndex[piece.Index] = &ToolCall{
					ID:        piece.ID,
					Name:      piece.Function.Name,
					Arguments: piece.Function.Arguments,
				}
				continue
			}
			if current.ID == "" && piece.ID != "" {
				current.ID = piece.ID
			}
			if current.Name == "" && piece.Function.Name != "" {
				current.Name = piece.Function.Name
			}
			current.Arguments += piece.Function.Arguments
		}
	}
	if err := scanner.Err(); err != nil {
		return "", nil, fmt.Errorf("读取 AI 响应失败: %w", err)
	}

	calls := make([]ToolCall, 0, len(callsByIndex))
	indexes := make([]int, 0, len(callsByIndex))
	for index := range callsByIndex {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		calls = append(calls, *callsByIndex[index])
	}
	text := content.String()
	if strings.TrimSpace(text) == "" && strings.TrimSpace(reasoning.String()) != "" {
		text = reasoning.String()
	}
	return text, calls, nil
}

// buildChatRequest 组装 Chat Completions 请求体（流式与非流式共用）。
func buildChatRequest(model string, messages []ChatMessage, tools []Tool, stream bool) chatRequest {
	reqMessages := make([]chatMessage, 0, len(messages))
	for _, m := range messages {
		wire := chatMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, call := range m.ToolCalls {
			wire.ToolCalls = append(wire.ToolCalls, chatToolCall{
				ID:       call.ID,
				Type:     "function",
				Function: chatToolCallFunc{Name: call.Name, Arguments: call.Arguments},
			})
		}
		reqMessages = append(reqMessages, wire)
	}
	request := chatRequest{Model: model, Messages: reqMessages, Stream: stream}
	for _, tool := range tools {
		request.Tools = append(request.Tools, chatTool{
			Type: "function",
			Function: chatToolFunc{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	return request
}

// chatEndpoint 归一化接入地址：用户可能误粘贴完整 endpoint（含 /chat/completions），
// 去掉末尾斜杠与重复后缀后再拼，避免请求到 .../chat/completions/chat/completions 报 404。
func chatEndpoint(baseURL string) string {
	endpoint := strings.TrimRight(baseURL, "/")
	endpoint = strings.TrimSuffix(endpoint, "/chat/completions")
	return endpoint + "/chat/completions"
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
