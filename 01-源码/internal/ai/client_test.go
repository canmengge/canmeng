package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatParsesPlainTextAndSendsKey(t *testing.T) {
	var gotAuth, gotModel, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var request chatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		gotModel = request.Model
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"你好"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	reply, calls, err := Chat(context.Background(),
		Config{BaseURL: server.URL + "/v1", Model: "test-model", APIKey: "sk-secret"},
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply != "你好" || calls != nil {
		t.Fatalf("reply=%q calls=%v", reply, calls)
	}
	if gotAuth != "Bearer sk-secret" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotModel != "test-model" {
		t.Fatalf("model = %q", gotModel)
	}
}

func TestChatExtractsToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[
			{"id":"call-1","type":"function","function":{"name":"search_files","arguments":"{\"query\":\"belt\"}"}}]}}]}`))
	}))
	defer server.Close()

	_, calls, err := Chat(context.Background(),
		Config{BaseURL: server.URL, Model: "m"},
		[]ChatMessage{{Role: "user", Content: "搜 belt"}}, nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(calls) != 1 || calls[0].Name != "search_files" || calls[0].ID != "call-1" {
		t.Fatalf("calls = %#v", calls)
	}
	if !strings.Contains(calls[0].Arguments, "belt") {
		t.Fatalf("arguments = %q", calls[0].Arguments)
	}
}

func TestChatReportsHTTPErrorWithoutLeakingKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer server.Close()

	_, _, err := Chat(context.Background(),
		Config{BaseURL: server.URL, Model: "m", APIKey: "sk-secret"},
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "sk-secret") {
		t.Fatalf("错误信息泄漏了 API key: %v", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("错误未包含状态码: %v", err)
	}
}

func TestChatRequiresBaseURLAndModel(t *testing.T) {
	if _, _, err := Chat(context.Background(), Config{Model: "m"}, nil, nil); err == nil {
		t.Fatal("空 baseURL 应报错")
	}
	if _, _, err := Chat(context.Background(), Config{BaseURL: "http://x"}, nil, nil); err == nil {
		t.Fatal("空 model 应报错")
	}
}
