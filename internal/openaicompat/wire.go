package openaicompat

import "encoding/json"

// ── 请求 wire 结构 ──

type chatRequest struct {
	Model      string        `json:"model"`
	Messages   []wireMessage `json:"messages"`
	Tools      []tool        `json:"tools"`
	ToolChoice toolChoice    `json:"tool_choice"`
}

// chatLoopRequest 是 agent 多工具循环的请求体：开放多个工具，tool_choice 用字符串
// （本设计取 "required"，每步必选一个工具）。与单工具强制的 chatRequest 分开，
// 互不影响其各自的 wire 形态与既有测试。
type chatLoopRequest struct {
	Model      string        `json:"model"`
	Messages   []wireMessage `json:"messages"`
	Tools      []tool        `json:"tools,omitempty"` // 为空则省略 → 纯文本对话（压缩摘要用）
	ToolChoice string        `json:"tool_choice,omitempty"`
}

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`

	// agent 循环用：assistant 轮携带的工具调用 / tool 角色回填结果的关联 ID。
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// wireToolCall 是请求里回灌的 assistant 工具调用（与响应中的 tool_call 同形）。
type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // 固定 "function"
	Function wireToolCallFunc `json:"function"`
}

type wireToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON 串（OpenAI 约定 arguments 为字符串）
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
	Usage   usage    `json:"usage"`
}

type usage struct {
	PromptTokens int `json:"prompt_tokens"`
}

type choice struct {
	Message respMessage `json:"message"`
}

type respMessage struct {
	Content   string         `json:"content"`
	ToolCalls []respToolCall `json:"tool_calls"`
}

type respToolCall struct {
	ID       string               `json:"id"`
	Function respToolCallFunction `json:"function"`
}

type respToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
