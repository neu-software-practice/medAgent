package consultlog

import (
	"encoding/json"
	"time"

	"medagent/internal/ai"
)

// Message 是记录里的一条对话消息。独立于 ai.Message 以固定 snake_case 的持久化键名。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// messagesOf 把 ai.Message 列表转成记录用的 Message 列表。
// assistant 轮的工具调用折进 content 以便审计（Complete 路径无 tool_calls，内容不变）。
func messagesOf(in []ai.Message) []Message {
	out := make([]Message, len(in))
	for i, m := range in {
		content := m.Content
		for _, tc := range m.ToolCalls {
			content += "\n[tool_call " + tc.Name + " " + string(tc.Arguments) + "]"
		}
		out[i] = Message{Role: m.Role, Content: content}
	}
	return out
}

// CallToolCall 是 Chat 路径中单个工具调用的审计记录。
type CallToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// CallRecord 是一次 LLM 调用的审计记录。成功失败都记（含 schema 重试/纠正轮）。
type CallRecord struct {
	VisitID    string          `json:"visit_id"`
	Time       time.Time       `json:"time"`
	Schema     string          `json:"schema"` // Complete 路径 = req.Schema.Name；Chat 路径 = "chat"
	System     string          `json:"system"`
	Messages   []Message       `json:"messages"`
	Structured json.RawMessage `json:"structured,omitempty"` // Complete 路径的结构化输出
	ToolCalls  []CallToolCall  `json:"tool_calls,omitempty"` // Chat 路径的全部工具调用
	Raw        string          `json:"raw,omitempty"`        // 模型文本输出
	LatencyMS  int64           `json:"latency_ms"`
	Error      string          `json:"error,omitempty"`
}

// Sink 是 CallRecord 的落地目标。这里只是为可测性留的最小 DI 缝，默认实现是 FileLogger。
type Sink interface {
	Write(rec CallRecord) error
}
