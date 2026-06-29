package ai

import (
	"context"
	"encoding/json"
)

// ToolSpec 描述一个可被模型调用的工具（function calling）。
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage // 入参的 JSON schema
}

// ToolCall 是模型发起的一次工具调用。
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage // 工具入参（模型生成的 JSON）
}

// AssistantTurn 是 Chat 的一次产出：模型的文本与/或工具调用。
// PromptTokens 取 provider usage 的真实输入 token 数（无则 0），供上下文压缩阈值判断。
type AssistantTurn struct {
	Text         string
	ToolCalls    []ToolCall
	PromptTokens int
}

// ChatRequest 是一次多工具对话调用的入参。
type ChatRequest struct {
	System     string
	Messages   []Message
	Tools      []ToolSpec
	ToolChoice string // "required" | "auto" | ""（provider 默认）
}

// ChatClient 是支持多工具对话（agent 循环）的 provider 中立接口。
// 与 LLMClient.Complete（单次强制结构化）并存：Complete 仍服务于急症守护。
type ChatClient interface {
	Chat(ctx context.Context, req ChatRequest) (AssistantTurn, error)
}
