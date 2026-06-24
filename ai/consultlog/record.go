package consultlog

import (
	"encoding/json"
	"time"

	"medagent/ai"
)

// Message 是记录里的一条对话消息。独立于 ai.Message 以固定 snake_case 的持久化键名。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// messagesOf 把 ai.Message 列表转成记录用的 Message 列表。
func messagesOf(in []ai.Message) []Message {
	out := make([]Message, len(in))
	for i, m := range in {
		out[i] = Message{Role: m.Role, Content: m.Content}
	}
	return out
}

// CallRecord 是一次 LLM 调用的审计记录。成功失败都记（含 schema 重试/纠正轮）。
type CallRecord struct {
	VisitID    string          `json:"visit_id"`
	Time       time.Time       `json:"time"`
	Schema     string          `json:"schema"` // = req.Schema.Name，标识是哪个 agent
	System     string          `json:"system"`
	Messages   []Message       `json:"messages"`
	Structured json.RawMessage `json:"structured,omitempty"`
	Raw        string          `json:"raw,omitempty"`
	LatencyMS  int64           `json:"latency_ms"`
	Error      string          `json:"error,omitempty"`
}

// Sink 是 CallRecord 的落地目标。这里只是为可测性留的最小 DI 缝，默认实现是 FileLogger。
type Sink interface {
	Write(rec CallRecord) error
}
