package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"medagent/ai"
)

func TestComplete_SendsForcedToolUseRequest(t *testing.T) {
	var gotBody []byte
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"tool_calls":[{"function":{"name":"triage_decision","arguments":"{\"action\":\"treat\"}"}}]}}]}`)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "sk-test", Model: "deepseek-chat", HTTPClient: srv.Client()})
	res, err := c.Complete(context.Background(), ai.CompletionRequest{
		System:   "你是医生",
		Messages: []ai.Message{{Role: "user", Content: "快照"}, {Role: "user", Content: "患者轮"}},
		Schema:   ai.OutputSchema{Name: "triage_decision", JSON: json.RawMessage(`{"type":"object"}`)},
	})
	if err != nil {
		t.Fatalf("Complete err: %v", err)
	}
	if string(res.Structured) != `{"action":"treat"}` {
		t.Errorf("Structured = %s", res.Structured)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth = %q", gotAuth)
	}

	var sent chatRequest
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("unmarshal sent body: %v", err)
	}
	if sent.Model != "deepseek-chat" {
		t.Errorf("model = %q", sent.Model)
	}
	if len(sent.Messages) != 2 || sent.Messages[0].Role != "system" || sent.Messages[1].Content != "快照\n\n患者轮" {
		t.Errorf("messages = %+v", sent.Messages)
	}
	if sent.ToolChoice.Function.Name != "triage_decision" {
		t.Errorf("tool_choice = %+v", sent.ToolChoice)
	}
}

func TestComplete_Non2xxIsErrLLM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":"rate limited"}`)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "k", Model: "m", HTTPClient: srv.Client()})
	_, err := c.Complete(context.Background(), ai.CompletionRequest{Schema: ai.OutputSchema{Name: "x", JSON: json.RawMessage(`{}`)}})
	if !errors.Is(err, ai.ErrLLM) {
		t.Fatalf("want ErrLLM, got %v", err)
	}
}

func TestComplete_CanceledContext(t *testing.T) {
	c := New(Config{BaseURL: "http://example.invalid", APIKey: "k", Model: "m"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Complete(ctx, ai.CompletionRequest{Schema: ai.OutputSchema{Name: "x", JSON: json.RawMessage(`{}`)}}); err == nil {
		t.Fatal("want error for canceled context")
	}
}

func TestConstructors_SetBaseURL(t *testing.T) {
	if c := NewDeepSeek("k", "deepseek-chat"); c.cfg.BaseURL != "https://api.deepseek.com/v1" {
		t.Errorf("deepseek base = %q", c.cfg.BaseURL)
	}
	if c := NewQwen("k", "qwen-plus"); c.cfg.BaseURL != "https://dashscope.aliyuncs.com/compatible-mode/v1" {
		t.Errorf("qwen base = %q", c.cfg.BaseURL)
	}
}

func TestSnippet_TruncatesOnRuneBoundary(t *testing.T) {
	// Build a byte slice with many multi-byte UTF-8 runes (Chinese chars) longer than 512 bytes
	var longStr string
	for i := 0; i < 200; i++ {
		longStr += "医"
	}
	b := []byte(longStr)

	result := snippet(b)

	// The result (minus the trailing "…") should be valid UTF-8
	if !utf8.ValidString(strings.TrimSuffix(result, "…")) {
		t.Errorf("snippet result is not valid UTF-8: %q", result)
	}

	// The length should not exceed 512 bytes (plus the "…")
	if len(result) > 512+3 { // "…" is 3 bytes in UTF-8
		t.Errorf("snippet result exceeds expected length: %d bytes", len(result))
	}
}

func TestNew_TrimsTrailingSlashBaseURL(t *testing.T) {
	c := New(Config{BaseURL: "https://host/v1/", APIKey: "k", Model: "m"})
	if c.cfg.BaseURL != "https://host/v1" {
		t.Errorf("BaseURL = %q, want %q", c.cfg.BaseURL, "https://host/v1")
	}
}
