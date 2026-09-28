// Package mcp 把 AI 助手的工具注册表按 MCP（Model Context Protocol）协议暴露。
//
// 状态：默认关闭，可通过环境变量按需开放。
//   - 默认不监听任何端口、不打开任何 I/O；
//   - 设置 PVFINE_MCP=1 后，main.go 会在 PVFINE_MCP_ADDR（默认 127.0.0.1:17650）
//     以 HTTP 方式提供 MCP 服务（POST 单条 JSON-RPC）；
//   - ⚠️ 只对外暴露只读工具：写能力（edit_file / apply_replace）一律不开放，
//     避免绕过「AI 写保护」门禁；
//   - 工具与内置 AI 助手共用同一张注册表（services.buildAITools），避免两套逻辑漂移。
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"pvfine/services"
)

// exposed 是否对外提供 MCP 服务。
// 默认关闭；设置环境变量 PVFINE_MCP=1 时开放（HTTP 传输，仅监听本机回环地址）。
var exposed = os.Getenv("PVFINE_MCP") == "1"

// Exposed 返回 MCP 服务是否已开放（由环境变量 PVFINE_MCP 控制）。
func Exposed() bool { return exposed }

// Addr 返回 HTTP MCP 服务的监听地址（环境变量 PVFINE_MCP_ADDR，默认 127.0.0.1:17650）。
func Addr() string {
	addr := strings.TrimSpace(os.Getenv("PVFINE_MCP_ADDR"))
	if addr == "" {
		return "127.0.0.1:17650"
	}
	return addr
}

// errDisabled 是 exposed=false 时 Start 返回的错误。
var errDisabled = fmt.Errorf("MCP 服务当前处于关闭状态（保留接口，未对外开放）")

// ToolSource 返回当前可用的 AI 工具注册表（与内置 AI 助手同源）。
type ToolSource func() []services.AITool

// Server 是一个基于 stdio 的极简 MCP 服务端（JSON-RPC 2.0，逐行分帧）。
type Server struct {
	source ToolSource
}

// New 创建 MCP 服务端。
func New(source ToolSource) *Server {
	return &Server{source: source}
}

// Start 以 stdio 传输启动 MCP 服务端。
// exposed=false 时直接返回错误，绝不阻塞、绝不读 stdin（保证"关闭"语义）。
func Start(source ToolSource) error {
	if !exposed {
		return errDisabled
	}
	return New(source).Serve(context.Background(), bufio.NewReader(os.Stdin), os.Stdout)
}

// Serve 在 r（请求）与 w（响应）之间按行处理 JSON-RPC 2.0。
// 公开方法：initialize / tools/list / tools/call / ping。
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			_ = s.writeError(w, nil, -32700, "解析错误")
			continue
		}
		switch request.Method {
		case "initialize":
			_ = s.writeResult(w, request.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]any{"name": "pvfine-mcp", "version": "0.1.0"},
			})
		case "notifications/initialized", "initialized":
			// 通知：无需应答。
		case "ping":
			_ = s.writeResult(w, request.ID, map[string]any{})
		case "tools/list":
			_ = s.writeResult(w, request.ID, map[string]any{"tools": s.toolDescriptors()})
		case "tools/call":
			result, callErr := s.callTool(request.Params.Name, request.Params.Arguments)
			if callErr != nil {
				_ = s.writeError(w, request.ID, -32000, callErr.Error())
				continue
			}
			_ = s.writeResult(w, request.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": result}},
			})
		default:
			_ = s.writeError(w, request.ID, -32601, "未知方法: "+request.Method)
		}
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return scanner.Err()
}

// ServeHTTP 处理一次 HTTP MCP 请求（POST 单条 JSON-RPC；仅用于对外开放模式）。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "MCP 仅接受 POST", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		http.Error(w, "读取请求失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	var out bytes.Buffer
	if err := s.Serve(r.Context(), bytes.NewReader(body), &out); err != nil {
		http.Error(w, "处理请求失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out.Bytes())
}

func (s *Server) toolDescriptors() []map[string]any {
	tools := s.source()
	descriptors := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		// 只对外暴露只读工具：MCP 不开放任何写能力。
		if !tool.ReadOnly {
			continue
		}
		descriptors = append(descriptors, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.Schema,
		})
	}
	return descriptors
}

func (s *Server) callTool(name string, arguments json.RawMessage) (string, error) {
	for _, tool := range s.source() {
		if tool.Name != name {
			continue
		}
		if !tool.ReadOnly {
			return "", fmt.Errorf("工具 %s 是写操作，MCP 不对外开放写能力", name)
		}
		return tool.Run(string(arguments))
	}
	return "", fmt.Errorf("未知工具: %s", name)
}

func (s *Server) writeResult(w io.Writer, id json.RawMessage, result any) error {
	return s.write(w, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *Server) writeError(w io.Writer, id json.RawMessage, code int, message string) error {
	return s.write(w, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
}

func (s *Server) write(w io.Writer, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}
