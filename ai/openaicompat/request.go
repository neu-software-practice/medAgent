package openaicompat

import "medagent/ai"

// toolDescription 是注入给强制工具的固定说明，引导模型把结构化结果作为入参返回。
const toolDescription = "Return the structured result as arguments to this function."

// buildRequest 把 provider 中立的 CompletionRequest 映射成 OpenAI 兼容请求体：
// System → 首条 system 消息；Messages 合并连续同角色；Schema → 唯一工具且强制 tool_choice。
func buildRequest(req ai.CompletionRequest, model string) chatRequest {
	msgs := make([]wireMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, wireMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, mergeMessages(req.Messages)...)

	return chatRequest{
		Model:    model,
		Messages: msgs,
		Tools: []tool{{
			Type: "function",
			Function: toolFunction{
				Name:        req.Schema.Name,
				Description: toolDescription,
				Parameters:  req.Schema.JSON,
			},
		}},
		ToolChoice: toolChoice{
			Type:     "function",
			Function: toolChoiceFunction{Name: req.Schema.Name},
		},
	}
}
