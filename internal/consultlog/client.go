package consultlog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"medagent/internal/ai"
)

// Client 是 logging 装饰器：包住任意 ai.LLMClient，把每次 Complete 记进 sink。
// 日志失败绝不影响诊疗——sink 出错只交给 onErr（默认写 stderr），不传播。
type Client struct {
	inner ai.LLMClient
	sink  Sink
	onErr func(error)
}

var (
	_ ai.LLMClient  = (*Client)(nil)
	_ ai.ChatClient = (*Client)(nil)
)

// Wrap 用 sink 包装 inner。
func Wrap(inner ai.LLMClient, sink Sink) *Client {
	return &Client{
		inner: inner,
		sink:  sink,
		onErr: func(e error) { fmt.Fprintf(os.Stderr, "consultlog: 写日志失败：%v\n", e) },
	}
}

// Complete 调用 inner 并记录这次调用，原样返回 inner 的结果与错误。
func (c *Client) Complete(ctx context.Context, req ai.CompletionRequest) (ai.CompletionResult, error) {
	start := time.Now()
	res, err := c.inner.Complete(ctx, req)

	rec := CallRecord{
		VisitID:   VisitID(ctx),
		Time:      start,
		Schema:    req.Schema.Name,
		System:    req.System,
		Messages:  messagesOf(req.Messages),
		LatencyMS: time.Since(start).Milliseconds(),
	}
	if err != nil {
		rec.Error = err.Error()
	} else {
		rec.Structured = res.Structured
		rec.Raw = res.Raw
	}
	if werr := c.sink.Write(rec); werr != nil && c.onErr != nil {
		c.onErr(werr)
	}
	return res, err
}

// Chat 调用 inner 的 ChatClient 并记录这次调用（每个 agent 循环 step 一行）。
// inner 必须实现 ai.ChatClient（生产中即 openaicompat.Client）。
func (c *Client) Chat(ctx context.Context, req ai.ChatRequest) (ai.AssistantTurn, error) {
	cc, ok := c.inner.(ai.ChatClient)
	if !ok {
		return ai.AssistantTurn{}, fmt.Errorf("consultlog: inner 不支持 Chat")
	}
	start := time.Now()
	turn, err := cc.Chat(ctx, req)

	rec := CallRecord{
		VisitID:   VisitID(ctx),
		Time:      start,
		Schema:    "chat",
		System:    req.System,
		Messages:  messagesOf(req.Messages),
		LatencyMS: time.Since(start).Milliseconds(),
	}
	if err != nil {
		rec.Error = err.Error()
	} else {
		rec.Raw = turn.Text
		for _, tc := range turn.ToolCalls {
			rec.ToolCalls = append(rec.ToolCalls, CallToolCall{Name: tc.Name, Arguments: json.RawMessage(tc.Arguments)})
		}
	}
	if werr := c.sink.Write(rec); werr != nil && c.onErr != nil {
		c.onErr(werr)
	}
	return turn, err
}
