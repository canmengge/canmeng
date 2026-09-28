package services

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"pvfine/internal/ai"
)

// newAITestService 构造一个「AI 已启用」的 AIService；writeProtection 由用例指定。
func newAITestService(t *testing.T, writeProtection bool) (*AIService, AIAssistantSettings) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	settings := newSettingsService(path)
	if err := settings.SaveSettings(func() AppSettings {
		base := DefaultAppSettings()
		base.AI = AIAssistantSettings{
			Enabled:         true,
			Provider:        AIProviderCustom,
			BaseURL:         "http://127.0.0.1:1/v1",
			Model:           "test-model",
			WriteProtection: writeProtection,
		}
		return base
	}()); err != nil {
		t.Fatalf("SaveSettings() error = %v", err)
	}
	return NewAIService(NewCore(), settings), DefaultAppSettings().AI
}

// 写保护开启时，写工具必须被拒绝且不能真正执行。
func TestAIRunToolRefusesWritesWhenProtected(t *testing.T) {
	svc, _ := newAITestService(t, true)
	registered := buildAITools(svc.c, svc.settings)
	byName := make(map[string]AITool, len(registered))
	for _, tool := range registered {
		byName[tool.Name] = tool
	}
	if len(byName) == 0 {
		t.Fatal("工具注册表为空")
	}
	for _, name := range []string{"edit_file", "save_archive"} {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("缺少写工具 %s", name)
		}
		if tool.ReadOnly {
			t.Fatalf("%s 不应标记为只读", name)
		}
		result := svc.runAITool(byName, AIAssistantSettings{WriteProtection: true},
			ai.ToolCall{ID: "x", Name: name, Arguments: "{}"})
		if !strings.Contains(result, "写保护已开启") {
			t.Fatalf("%s 在写保护下应被拒绝，实际: %q", name, result)
		}
	}
}

// 读工具不受写保护影响；写保护关闭时写工具会真正执行（此处以"归档未打开"报错证明执行到了工具本体）。
func TestAIRunToolGateAllowsReadsAndWritesWhenUnlocked(t *testing.T) {
	svc, _ := newAITestService(t, false)
	registered := buildAITools(svc.c, svc.settings)
	byName := make(map[string]AITool, len(registered))
	for _, tool := range registered {
		byName[tool.Name] = tool
	}

	if result := svc.runAITool(byName, AIAssistantSettings{WriteProtection: true},
		ai.ToolCall{ID: "x", Name: "search_files", Arguments: `{"query":"x"}`}); strings.Contains(result, "写保护已开启") {
		t.Fatalf("读工具不应被写保护拦截: %q", result)
	}

	result := svc.runAITool(byName, AIAssistantSettings{WriteProtection: false},
		ai.ToolCall{ID: "x", Name: "save_archive", Arguments: "{}"})
	if strings.Contains(result, "写保护已开启") {
		t.Fatalf("写保护关闭后写工具应真正执行: %q", result)
	}
	if !strings.Contains(result, "工具执行失败") || !strings.Contains(result, "归档") {
		t.Fatalf("无归档时 save_archive 应报归档错误: %q", result)
	}
}

// 未启用 AI 时 Chat 必须拒绝（避免静默放行未配置的调用）。
func TestAIChatFailsWhenDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	settings := newSettingsService(path)
	svc := NewAIService(NewCore(), settings)
	if _, err := svc.Chat(context.Background(), AIChatRequest{Messages: []AIMessage{{Role: "user", Content: "hi"}}}); err == nil {
		t.Fatal("AI 未启用时 Chat 应报错")
	} else if !strings.Contains(err.Error(), "未启用") {
		t.Fatalf("错误应说明未启用: %v", err)
	}
}

// 未知工具要有明确报错，而不是静默成功。
func TestAIRunToolUnknownTool(t *testing.T) {
	svc, _ := newAITestService(t, true)
	registered := buildAITools(svc.c, svc.settings)
	byName := make(map[string]AITool, len(registered))
	for _, tool := range registered {
		byName[tool.Name] = tool
	}
	result := svc.runAITool(byName, AIAssistantSettings{WriteProtection: false},
		ai.ToolCall{ID: "x", Name: "no_such_tool", Arguments: "{}"})
	if !strings.Contains(result, "未知工具") {
		t.Fatalf("应报未知工具: %q", result)
	}
}
