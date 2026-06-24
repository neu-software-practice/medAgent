package ai

import (
	"context"
	"encoding/json"
)

// Message 是一条对话消息。
type Message struct {
	Role    string // "user" | "assistant"
	Content string
}

// OutputSchema 描述期望输出的 JSON schema；Name 供 tool-use 命名用。
type OutputSchema struct {
	Name string
	JSON json.RawMessage
}

// CompletionRequest 是一次 LLM 调用的入参。
type CompletionRequest struct {
	System   string
	Messages []Message
	Schema   OutputSchema
}

// CompletionResult 是一次 LLM 调用的结构化产出。
type CompletionResult struct {
	Structured json.RawMessage // 符合 Schema 的 JSON
	Raw        string          // 原始文本，调试/日志用
}

// LLMClient 是 provider 中立的结构化输出接口。
// 只保证"结构化"，不做语义校验。真实实现把 Schema 映射为 tool-use 或 response_format。
type LLMClient interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResult, error)
}
