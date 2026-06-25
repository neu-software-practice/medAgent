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
