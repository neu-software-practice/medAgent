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
