package openaicompat

import "medagent/internal/ai"

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
