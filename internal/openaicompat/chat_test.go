package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"medagent/internal/ai"
)

// 验证 Chat:多工具 + tool_choice=required 请求成形、工具协议消息(assistant tool_calls / tool 结果)
// 1:1 保留不合并、解析出 tool_call 与 usage.prompt_tokens —— 即"多工具对话 + tool_result 续跑"plumbing。
func TestChat_SendsMultiToolAndParsesToolCallAndUsage(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","function":{"name":"order_test","arguments":"{\"items\":[\"血常规\"]}"}}]}}],"usage":{"prompt_tokens":1234}}`)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "sk-test", Model: "gpt-x", HTTPClient: srv.Client()})
	turn, err := c.Chat(context.Background(), ai.ChatRequest{
		System: "你是医生",
		Messages: []ai.Message{
			{Role: "user", Content: "嗓子疼"},
			{Role: "assistant", ToolCalls: []ai.ToolCall{{ID: "call_0", Name: "ask_patient", Arguments: json.RawMessage(`{"question":"几天了"}`)}}},
			{Role: "tool", ToolCallID: "call_0", Content: "三天"},
		},
		Tools: []ai.ToolSpec{
			{Name: "ask_patient", Description: "追问", Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "order_test", Description: "开检验", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
		ToolChoice: "required",
	})
	if err != nil {
		t.Fatalf("Chat err: %v", err)
	}

	// 解析产出
	if len(turn.ToolCalls) != 1 || turn.ToolCalls[0].Name != "order_test" || turn.ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool call = %+v", turn.ToolCalls)
	}
	if string(turn.ToolCalls[0].Arguments) != `{"items":["血常规"]}` {
		t.Errorf("args = %s", turn.ToolCalls[0].Arguments)
	}
	if turn.PromptTokens != 1234 {
		t.Errorf("prompt_tokens = %d", turn.PromptTokens)
	}

	// 发出的请求体
	var sent chatLoopRequest
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("unmarshal sent: %v", err)
	}
	if sent.ToolChoice != "required" {
		t.Errorf("tool_choice = %q", sent.ToolChoice)
	}
	if len(sent.Tools) != 2 {
		t.Fatalf("tools = %+v", sent.Tools)
	}
	// system + user + assistant(tool_calls) + tool = 4 条，不合并
	if len(sent.Messages) != 4 || sent.Messages[0].Role != "system" {
		t.Fatalf("messages = %d: %+v", len(sent.Messages), sent.Messages)
	}
	a := sent.Messages[2]
	if a.Role != "assistant" || len(a.ToolCalls) != 1 || a.ToolCalls[0].ID != "call_0" || a.ToolCalls[0].Type != "function" {
		t.Errorf("assistant tool_call msg = %+v", a)
	}
	if a.ToolCalls[0].Function.Name != "ask_patient" || a.ToolCalls[0].Function.Arguments != `{"question":"几天了"}` {
		t.Errorf("echoed tool_call = %+v", a.ToolCalls[0].Function)
	}
	tr := sent.Messages[3]
	if tr.Role != "tool" || tr.ToolCallID != "call_0" || tr.Content != "三天" {
		t.Errorf("tool result msg = %+v", tr)
	}
}

func TestChat_Non2xxIsErrLLM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"bad"}`)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "k", Model: "m", HTTPClient: srv.Client()})
	if _, err := c.Chat(context.Background(), ai.ChatRequest{ToolChoice: "required"}); err == nil {
		t.Fatal("应报错")
	}
}
