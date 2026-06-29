package ai

import (
	"context"
	"fmt"
	"strings"
)

// summarizePrompt 指导把较早的诊疗对话压成摘要，强约束保留临床要点。
const summarizePrompt = `你是无人医院诊疗系统的上下文压缩器。把以下较早的医患对话压缩成简洁摘要，务必无损保留以下临床要点：
- 主诉与关键症状、起病时间
- 阳性体征与检验发现（如血常规异常值）
- 已确认或疑似诊断
- 已开/已购药品、剂量与盒数
- 患者拒绝的事项（如拒绝购药/检验）
- 已给出的重要医嘱与转诊决定
用简体中文分条陈述，只输出摘要本身，不要寒暄。`

// Summarize 用一次无工具的纯文本对话，把给定消息压成摘要文本。
// 失败由调用方决定是否忽略（压缩失败不应中断诊疗）。
func (e *Engine) Summarize(ctx context.Context, msgs []Message) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	turn, err := e.chat.Chat(ctx, ChatRequest{
		System:   summarizePrompt,
		Messages: []Message{{Role: "user", Content: renderTranscript(msgs)}},
		// 不带工具、不强制 tool_choice → 返回纯文本摘要。
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("%w: summarize: %w", ErrLLM, err)
	}
	return strings.TrimSpace(turn.Text), nil
}

// renderTranscript 把工具循环 transcript 摊平成可读文本，供压缩输入。
func renderTranscript(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "user":
			fmt.Fprintf(&b, "患者/上下文: %s\n", m.Content)
		case "assistant":
			if m.Content != "" {
				fmt.Fprintf(&b, "医生: %s\n", m.Content)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "医生动作[%s]: %s\n", tc.Name, tc.Arguments)
			}
		case "tool":
			fmt.Fprintf(&b, "回填结果: %s\n", m.Content)
		}
	}
	return b.String()
}

// EstimateTokens 是零依赖的粗略 token 估算（CJK 友好、偏保守，宁可早压缩）：
// Content 与 tool_calls arguments 统一按 UTF-8 字节数估算，避免中文场景低估。
// 无 provider usage 时作回退信号。
func EstimateTokens(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		for _, tc := range m.ToolCalls {
			n += len(tc.Arguments)
		}
	}
	return n
}
