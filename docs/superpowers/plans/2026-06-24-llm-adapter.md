# OpenAI 兼容 LLM adapter 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `medagent/ai/openaicompat` 子包实现 `ai.LLMClient`，用强制 tool-use 把结构化输出接到 DeepSeek 与通义千问（OpenAI 兼容接口）。

**Architecture:** 单一 OpenAI 兼容 adapter，provider 仅是 `Config`（base URL / model / key）差异。请求构造与响应解析做成纯函数（无网络、可独立单测），`Complete` 只负责 HTTP 往返 + 错误映射。结构化输出靠 `tool_choice` 强制单一 schema 工具保证。

**Tech Stack:** Go 1.22 标准库（`net/http`、`encoding/json`、`httptest`），零外部依赖。

**关联 spec:** `docs/superpowers/specs/2026-06-24-llm-adapter-design.md`

## Global Constraints

- Go 1.22；**零外部依赖**，只用标准库。
- module 名 `medagent`；新包路径 `medagent/ai/openaicompat`，包名 `openaicompat`。
- 包导入方向只能 `openaicompat → ai`，`ai` 不得反向依赖（避免环）。
- 所有传输 / 协议层错误用 `fmt.Errorf("...: %w", ai.ErrLLM)` 包装，保证 `errors.Is(err, ai.ErrLLM)` 成立。
- adapter 不做 schema 语义校验、不做网络重试、不读环境变量。
- DeepSeek base URL：`https://api.deepseek.com/v1`；通义千问（DashScope 兼容模式）base URL：`https://dashscope.aliyuncs.com/compatible-mode/v1`。
- 结构化输出机制固定为强制 tool-use：`tool_choice` 锁定唯一工具 = `Schema.Name`。
- 请求侧必须合并连续同角色消息（content 以 `"\n\n"` 拼接）。

---

### Task 1: wire 结构体 + 同角色消息合并

**Files:**
- Create: `ai/openaicompat/wire.go`
- Create: `ai/openaicompat/messages.go`
- Test: `ai/openaicompat/messages_test.go`

**Interfaces:**
- Consumes: `ai.Message{Role, Content string}`（来自 `ai/llm.go`）。
- Produces:
  - wire 类型（供后续任务复用）：`chatRequest`、`wireMessage{Role, Content string}`、`tool{Type string; Function toolFunction}`、`toolFunction{Name, Description string; Parameters json.RawMessage}`、`toolChoice{Type string; Function toolChoiceFunction}`、`toolChoiceFunction{Name string}`；以及响应类型 `chatResponse`、`choice`、`respMessage`、`respToolCall`、`respToolCallFunction`。
  - `mergeMessages(msgs []ai.Message) []wireMessage`。

- [ ] **Step 1: 写 wire 结构体（无逻辑，先让包能编译）**

`ai/openaicompat/wire.go`：

```go
package openaicompat

import "encoding/json"

// ── 请求 wire 结构 ──

type chatRequest struct {
	Model      string        `json:"model"`
	Messages   []wireMessage `json:"messages"`
	Tools      []tool        `json:"tools"`
	ToolChoice toolChoice    `json:"tool_choice"`
}

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type tool struct {
	Type     string       `json:"type"` // 固定 "function"
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type toolChoice struct {
	Type     string             `json:"type"` // 固定 "function"
	Function toolChoiceFunction `json:"function"`
}

type toolChoiceFunction struct {
	Name string `json:"name"`
}

// ── 响应 wire 结构 ──

type chatResponse struct {
	Choices []choice `json:"choices"`
}

type choice struct {
	Message respMessage `json:"message"`
}

type respMessage struct {
	Content   string         `json:"content"`
	ToolCalls []respToolCall `json:"tool_calls"`
}

type respToolCall struct {
	Function respToolCallFunction `json:"function"`
}

type respToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
```

- [ ] **Step 2: 写失败测试（合并连续同角色）**

`ai/openaicompat/messages_test.go`：

```go
package openaicompat

import (
	"testing"

	"medagent/ai"
)

func TestMergeMessages_ConsecutiveSameRoleMerged(t *testing.T) {
	msgs := []ai.Message{
		{Role: "user", Content: "快照块"},
		{Role: "user", Content: "患者轮"},
		{Role: "assistant", Content: "医生回复"},
		{Role: "user", Content: "事件追加"},
	}
	got := mergeMessages(msgs)
	if len(got) != 3 {
		t.Fatalf("want 3 merged messages, got %d: %+v", len(got), got)
	}
	if got[0].Role != "user" || got[0].Content != "快照块\n\n患者轮" {
		t.Errorf("first merged = %+v", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content != "医生回复" {
		t.Errorf("second = %+v", got[1])
	}
	if got[2].Role != "user" || got[2].Content != "事件追加" {
		t.Errorf("third = %+v", got[2])
	}
}

func TestMergeMessages_AlternatingPreserved(t *testing.T) {
	msgs := []ai.Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
		{Role: "user", Content: "c"},
	}
	if got := mergeMessages(msgs); len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
}

func TestMergeMessages_Empty(t *testing.T) {
	if got := mergeMessages(nil); len(got) != 0 {
		t.Fatalf("want 0, got %d", len(got))
	}
}
```

- [ ] **Step 3: 运行测试，确认编译失败/未定义**

Run: `go test ./ai/openaicompat/ -run TestMergeMessages -v`
Expected: 编译失败，`undefined: mergeMessages`。

- [ ] **Step 4: 实现 mergeMessages**

`ai/openaicompat/messages.go`：

```go
package openaicompat

import "medagent/ai"

// mergeMessages 把 ai.Message 映射成 wire 消息，并合并连续同角色消息
// （content 以 "\n\n" 拼接）。Anthropic 风格 API 不接受连续同角色消息，
// 而 buildMessages 的快照块(user)+患者轮(user) 及 guardian 末尾追加(user)
// 都会产生连续 user——此处统一处理。
func mergeMessages(msgs []ai.Message) []wireMessage {
	out := make([]wireMessage, 0, len(msgs))
	for _, m := range msgs {
		if n := len(out); n > 0 && out[n-1].Role == m.Role {
			out[n-1].Content += "\n\n" + m.Content
			continue
		}
		out = append(out, wireMessage{Role: m.Role, Content: m.Content})
	}
	return out
}
```

- [ ] **Step 5: 运行测试，确认通过**

Run: `go test ./ai/openaicompat/ -run TestMergeMessages -v`
Expected: PASS（3 个测试）。

- [ ] **Step 6: 提交**

```bash
git add ai/openaicompat/wire.go ai/openaicompat/messages.go ai/openaicompat/messages_test.go
git commit -m "feat(openaicompat): wire 结构体与同角色消息合并"
```

---

### Task 2: buildRequest（请求构造，纯函数）

**Files:**
- Create: `ai/openaicompat/request.go`
- Test: `ai/openaicompat/request_test.go`

**Interfaces:**
- Consumes: `mergeMessages`、wire 类型（Task 1）；`ai.CompletionRequest{System string; Messages []ai.Message; Schema ai.OutputSchema}`、`ai.OutputSchema{Name string; JSON json.RawMessage}`。
- Produces: `buildRequest(req ai.CompletionRequest, model string) chatRequest`。

- [ ] **Step 1: 写失败测试**

`ai/openaicompat/request_test.go`：

```go
package openaicompat

import (
	"encoding/json"
	"testing"

	"medagent/ai"
)

func TestBuildRequest_SystemFirstAndForcedToolChoice(t *testing.T) {
	req := ai.CompletionRequest{
		System: "你是医生",
		Messages: []ai.Message{
			{Role: "user", Content: "快照"},
			{Role: "user", Content: "患者轮"},
		},
		Schema: ai.OutputSchema{
			Name: "triage_decision",
			JSON: json.RawMessage(`{"type":"object"}`),
		},
	}
	got := buildRequest(req, "deepseek-chat")

	if got.Model != "deepseek-chat" {
		t.Errorf("model = %q", got.Model)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("want system + 1 merged user = 2, got %d: %+v", len(got.Messages), got.Messages)
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "你是医生" {
		t.Errorf("system msg = %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "user" || got.Messages[1].Content != "快照\n\n患者轮" {
		t.Errorf("merged user msg = %+v", got.Messages[1])
	}
	if len(got.Tools) != 1 || got.Tools[0].Type != "function" || got.Tools[0].Function.Name != "triage_decision" {
		t.Fatalf("tools = %+v", got.Tools)
	}
	if string(got.Tools[0].Function.Parameters) != `{"type":"object"}` {
		t.Errorf("parameters = %s", got.Tools[0].Function.Parameters)
	}
	if got.ToolChoice.Type != "function" || got.ToolChoice.Function.Name != "triage_decision" {
		t.Errorf("tool_choice = %+v", got.ToolChoice)
	}
}

func TestBuildRequest_NoSystemOmitsSystemMessage(t *testing.T) {
	req := ai.CompletionRequest{
		Messages: []ai.Message{{Role: "user", Content: "hi"}},
		Schema:   ai.OutputSchema{Name: "x", JSON: json.RawMessage(`{}`)},
	}
	got := buildRequest(req, "m")
	if len(got.Messages) != 1 || got.Messages[0].Role != "user" {
		t.Fatalf("want single user msg, got %+v", got.Messages)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./ai/openaicompat/ -run TestBuildRequest -v`
Expected: 编译失败，`undefined: buildRequest`。

- [ ] **Step 3: 实现 buildRequest**

`ai/openaicompat/request.go`：

```go
package openaicompat

import "medagent/ai"

// toolDescription 是注入给强制工具的固定说明，引导模型把结构化结果作为入参返回。
const toolDescription = "Return the structured result as arguments to this function."

// buildRequest 把 provider 中立的 CompletionRequest 映射成 OpenAI 兼容请求体：
// System → 首条 system 消息；Messages 合并连续同角色；Schema → 唯一工具且强制 tool_choice。
func buildRequest(req ai.CompletionRequest, model string) chatRequest {
	msgs := make([]wireMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, wireMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, mergeMessages(req.Messages)...)

	return chatRequest{
		Model:    model,
		Messages: msgs,
		Tools: []tool{{
			Type: "function",
			Function: toolFunction{
				Name:        req.Schema.Name,
				Description: toolDescription,
				Parameters:  req.Schema.JSON,
			},
		}},
		ToolChoice: toolChoice{
			Type:     "function",
			Function: toolChoiceFunction{Name: req.Schema.Name},
		},
	}
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./ai/openaicompat/ -run TestBuildRequest -v`
Expected: PASS（2 个测试）。

- [ ] **Step 5: 提交**

```bash
git add ai/openaicompat/request.go ai/openaicompat/request_test.go
git commit -m "feat(openaicompat): buildRequest 映射 system/合并消息/强制 tool-use"
```

---

### Task 3: parseResult（响应解析，纯函数）

**Files:**
- Create: `ai/openaicompat/response.go`
- Test: `ai/openaicompat/response_test.go`

**Interfaces:**
- Consumes: 响应 wire 类型（Task 1）；`ai.CompletionResult{Structured json.RawMessage; Raw string}`、`ai.ErrLLM`。
- Produces: `parseResult(body []byte) (ai.CompletionResult, error)`。

- [ ] **Step 1: 写失败测试**

`ai/openaicompat/response_test.go`：

```go
package openaicompat

import (
	"errors"
	"testing"

	"medagent/ai"
)

func TestParseResult_ExtractsToolArguments(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"tool_calls":[{"function":{"name":"triage_decision","arguments":"{\"action\":\"treat\"}"}}]}}]}`)
	got, err := parseResult(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(got.Structured) != `{"action":"treat"}` {
		t.Errorf("Structured = %s", got.Structured)
	}
	if got.Raw != `{"action":"treat"}` {
		t.Errorf("Raw = %s", got.Raw)
	}
}

func TestParseResult_MissingToolCallsIsErrLLM(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"对不起"}}]}`)
	if _, err := parseResult(body); !errors.Is(err, ai.ErrLLM) {
		t.Fatalf("want ErrLLM, got %v", err)
	}
}

func TestParseResult_EmptyChoicesIsErrLLM(t *testing.T) {
	if _, err := parseResult([]byte(`{"choices":[]}`)); !errors.Is(err, ai.ErrLLM) {
		t.Fatalf("want ErrLLM, got %v", err)
	}
}

func TestParseResult_MalformedEnvelopeIsErrLLM(t *testing.T) {
	if _, err := parseResult([]byte(`not json`)); !errors.Is(err, ai.ErrLLM) {
		t.Fatalf("want ErrLLM, got %v", err)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./ai/openaicompat/ -run TestParseResult -v`
Expected: 编译失败，`undefined: parseResult`。

- [ ] **Step 3: 实现 parseResult**

`ai/openaicompat/response.go`：

```go
package openaicompat

import (
	"encoding/json"
	"fmt"

	"medagent/ai"
)

// parseResult 从 OpenAI 兼容响应里取出首个 tool_call 的 arguments 作为结构化输出。
// 不做 schema 语义校验。信封损坏 / 缺 choices / 缺 tool_calls 都映射为 ai.ErrLLM。
func parseResult(body []byte) (ai.CompletionResult, error) {
	var resp chatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 解析响应失败 (%v): %w", err, ai.ErrLLM)
	}
	if len(resp.Choices) == 0 || len(resp.Choices[0].Message.ToolCalls) == 0 {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 响应缺少 tool_calls: %w", ai.ErrLLM)
	}
	args := resp.Choices[0].Message.ToolCalls[0].Function.Arguments
	return ai.CompletionResult{
		Structured: json.RawMessage(args),
		Raw:        args,
	}, nil
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./ai/openaicompat/ -run TestParseResult -v`
Expected: PASS（4 个测试）。

- [ ] **Step 5: 提交**

```bash
git add ai/openaicompat/response.go ai/openaicompat/response_test.go
git commit -m "feat(openaicompat): parseResult 取 tool_call arguments，缺失映射 ErrLLM"
```

---

### Task 4: Client / 构造器 / Complete（HTTP 往返）

**Files:**
- Create: `ai/openaicompat/client.go`
- Test: `ai/openaicompat/client_test.go`

**Interfaces:**
- Consumes: `buildRequest`（Task 2）、`parseResult`（Task 3）、`chatRequest`（Task 1）；`ai.LLMClient`、`ai.CompletionRequest`、`ai.CompletionResult`、`ai.ErrLLM`。
- Produces:
  - `type Config struct { BaseURL, APIKey, Model string; Timeout time.Duration; HTTPClient *http.Client }`
  - `func New(cfg Config) *Client`、`func NewDeepSeek(apiKey, model string) *Client`、`func NewQwen(apiKey, model string) *Client`
  - `func (c *Client) Complete(ctx context.Context, req ai.CompletionRequest) (ai.CompletionResult, error)`
  - 编译期断言 `var _ ai.LLMClient = (*Client)(nil)`。

- [ ] **Step 1: 写失败测试（httptest 往返 + 错误路径 + 构造器）**

`ai/openaicompat/client_test.go`：

```go
package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

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
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./ai/openaicompat/ -run 'TestComplete|TestConstructors' -v`
Expected: 编译失败，`undefined: New` / `undefined: Config`。

- [ ] **Step 3: 实现 Client / 构造器 / Complete**

`ai/openaicompat/client.go`：

```go
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"medagent/ai"
)

const (
	deepSeekBaseURL = "https://api.deepseek.com/v1"
	qwenBaseURL     = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	defaultTimeout  = 60 * time.Second
)

// Config 是 adapter 的构造参数。BaseURL/APIKey/Model 必填；
// Timeout 为 0 时用默认 60s；HTTPClient 为 nil 时按 Timeout 内建（测试可注入 httptest）。
type Config struct {
	BaseURL    string
	APIKey     string
	Model      string
	Timeout    time.Duration
	HTTPClient *http.Client
}

// Client 是 OpenAI 兼容的 ai.LLMClient 实现。
type Client struct {
	cfg  Config
	http *http.Client
}

var _ ai.LLMClient = (*Client)(nil)

// New 按 Config 构造 Client。
func New(cfg Config) *Client {
	hc := cfg.HTTPClient
	if hc == nil {
		to := cfg.Timeout
		if to == 0 {
			to = defaultTimeout
		}
		hc = &http.Client{Timeout: to}
	}
	return &Client{cfg: cfg, http: hc}
}

// NewDeepSeek 预填 DeepSeek 的 base URL。
func NewDeepSeek(apiKey, model string) *Client {
	return New(Config{BaseURL: deepSeekBaseURL, APIKey: apiKey, Model: model})
}

// NewQwen 预填通义千问（DashScope 兼容模式）的 base URL。
func NewQwen(apiKey, model string) *Client {
	return New(Config{BaseURL: qwenBaseURL, APIKey: apiKey, Model: model})
}

// Complete 发起一次 chat-completions 调用，用强制 tool-use 拿结构化输出。
// 传输/非 2xx/协议异常全部包进 ai.ErrLLM；不做网络重试。
func (c *Client) Complete(ctx context.Context, req ai.CompletionRequest) (ai.CompletionResult, error) {
	if err := ctx.Err(); err != nil {
		return ai.CompletionResult{}, err
	}

	body, err := json.Marshal(buildRequest(req, c.cfg.Model))
	if err != nil {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 编码请求失败 (%v): %w", err, ai.ErrLLM)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 构造请求失败 (%v): %w", err, ai.ErrLLM)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 请求失败 (%v): %w", err, ai.ErrLLM)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 读取响应失败 (%v): %w", err, ai.ErrLLM)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 非 2xx 响应 %d: %s: %w", resp.StatusCode, snippet(respBody), ai.ErrLLM)
	}

	return parseResult(respBody)
}

// snippet 截断响应体，避免错误信息/日志爆量。
func snippet(b []byte) string {
	const max = 512
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./ai/openaicompat/ -run 'TestComplete|TestConstructors' -v`
Expected: PASS（4 个测试）。

- [ ] **Step 5: 跑全包测试 + vet**

Run: `go test ./ai/openaicompat/ -v && go vet ./ai/openaicompat/`
Expected: 全部 PASS，vet 无输出。

- [ ] **Step 6: 提交**

```bash
git add ai/openaicompat/client.go ai/openaicompat/client_test.go
git commit -m "feat(openaicompat): Client/构造器/Complete HTTP 往返与错误映射"
```

---

## 完成标准

- `go test ./ai/openaicompat/` 全绿；`go vet ./ai/openaicompat/` 无告警。
- `go build ./...` 通过，仓库仍零外部依赖（`go.sum` 仍为空）。
- `openaicompat.Client` 满足 `ai.LLMClient`（编译期断言存在）。
- DeepSeek / 通义千问可分别用 `NewDeepSeek` / `NewQwen` 构造。
