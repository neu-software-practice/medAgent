package ai

import (
	"errors"
	"fmt"
)

// ErrLLM 包装 LLM 传输/超时/取消类错误。
var ErrLLM = errors.New("llm call failed")

// SchemaError 表示模型输出在 K 次重试后仍不合 schema。
type SchemaError struct {
	Agent    string // "interview" | "triage" | "treatment" | "guardian"
	Attempts int
	LastRaw  string
	Cause    error
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf("agent %s: 输出在 %d 次尝试后仍不合 schema: %v", e.Agent, e.Attempts, e.Cause)
}

func (e *SchemaError) Unwrap() error { return e.Cause }
