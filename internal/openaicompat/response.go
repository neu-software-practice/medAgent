package openaicompat

import (
	"encoding/json"
	"fmt"

	"medagent/internal/ai"
)

// parseResult 从 OpenAI 兼容响应里取出首个 tool_call 的 arguments 作为结构化输出。
// 不做 schema 语义校验。信封损坏 / 缺 choices / 缺 tool_calls 都映射为 ai.ErrLLM。
func parseResult(body []byte) (ai.CompletionResult, error) {
	var resp chatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 解析响应失败 (%v): %w", err, ai.ErrLLM)
	}
	if len(resp.Choices) == 0 {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 响应缺少 choices: %w", ai.ErrLLM)
	}
	if len(resp.Choices[0].Message.ToolCalls) == 0 {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 响应缺少 tool_calls: %w", ai.ErrLLM)
	}
	args := resp.Choices[0].Message.ToolCalls[0].Function.Arguments
	return ai.CompletionResult{
		Structured: json.RawMessage(args),
		Raw:        args,
	}, nil
}

// parseAssistantTurn 从多工具响应里取出 assistant 的文本与全部 tool_call，并读 usage.prompt_tokens。
// 与 parseResult 不同：不强求存在 tool_call（由 agent 循环按 tool_choice 语义裁决）。
func parseAssistantTurn(body []byte) (ai.AssistantTurn, error) {
	var resp chatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ai.AssistantTurn{}, fmt.Errorf("openaicompat: 解析响应失败 (%v): %w", err, ai.ErrLLM)
	}
	if len(resp.Choices) == 0 {
		return ai.AssistantTurn{}, fmt.Errorf("openaicompat: 响应缺少 choices: %w", ai.ErrLLM)
	}
	msg := resp.Choices[0].Message
	turn := ai.AssistantTurn{Text: msg.Content, PromptTokens: resp.Usage.PromptTokens}
	for _, tc := range msg.ToolCalls {
		turn.ToolCalls = append(turn.ToolCalls, ai.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}
	return turn, nil
}
