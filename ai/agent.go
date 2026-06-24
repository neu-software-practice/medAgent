package ai

import (
	"context"
	"encoding/json"
	"fmt"
)

// validatable 是所有可结构校验的 intent 的约束。
type validatable interface {
	Validate() error
}

// runStructured 执行通用 6 步：构建上下文 → 调 LLM → 反序列化 → 结构校验 → 内部重试。
// schema-invalid 在包内自纠（≤SchemaRetryMax 次）；LLM 传输错误与 ctx 取消立即上抛。
func runStructured[T validatable](
	ctx context.Context, llm LLMClient, agentName, system string, schema OutputSchema, s Snapshot,
) (T, error) {
	var zero T
	msgs := buildMessages(s)
	var lastRaw string
	var lastErr error

	for attempt := 0; attempt <= SchemaRetryMax; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		res, err := llm.Complete(ctx, CompletionRequest{System: system, Messages: msgs, Schema: schema})
		if err != nil {
			return zero, fmt.Errorf("%w: %s: %v", ErrLLM, agentName, err)
		}
		lastRaw = res.Raw

		var out T
		if err := json.Unmarshal(res.Structured, &out); err != nil {
			lastErr = err
			msgs = appendCorrection(msgs, res.Raw, err)
			continue
		}
		if err := out.Validate(); err != nil {
			lastErr = err
			msgs = appendCorrection(msgs, res.Raw, err)
			continue
		}
		return out, nil
	}
	return zero, &SchemaError{Agent: agentName, Attempts: SchemaRetryMax + 1, LastRaw: lastRaw, Cause: lastErr}
}

func appendCorrection(msgs []Message, raw string, validationErr error) []Message {
	return append(msgs, Message{
		Role:    "user",
		Content: fmt.Sprintf("你上次的输出不符合要求：%v。原始输出：%s。请严格按 schema 重新输出。", validationErr, raw),
	})
}
