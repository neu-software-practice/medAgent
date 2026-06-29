package consultlog

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"medagent/internal/ai"
)

// bothClient 同时实现 Complete 与 Chat，供 Chat 装饰测试。
type bothClient struct {
	turn ai.AssistantTurn
	err  error
}

func (b *bothClient) Complete(context.Context, ai.CompletionRequest) (ai.CompletionResult, error) {
	return ai.CompletionResult{}, nil
}

func (b *bothClient) Chat(context.Context, ai.ChatRequest) (ai.AssistantTurn, error) {
	return b.turn, b.err
}

func TestWrapChatRecordsToolCall(t *testing.T) {
	inner := &bothClient{turn: ai.AssistantTurn{
		ToolCalls: []ai.ToolCall{{ID: "c1", Name: "order_test", Arguments: json.RawMessage(`{"items":["血常规"]}`)}},
	}}
	sink := &memSink{}
	c := Wrap(inner, sink)

	turn, err := c.Chat(WithVisitID(context.Background(), "v9"), ai.ChatRequest{
		System:   "sys",
		Messages: []ai.Message{{Role: "user", Content: "嗓子疼"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.ToolCalls) != 1 {
		t.Fatalf("turn 未透传：%+v", turn)
	}
	if len(sink.recs) != 1 {
		t.Fatalf("应记 1 条")
	}
	r := sink.recs[0]
	if r.VisitID != "v9" || r.Schema != "chat:order_test" {
		t.Errorf("记录 schema 错：%+v", r)
	}
	if string(r.Structured) != `{"items":["血常规"]}` {
		t.Errorf("structured 错：%s", r.Structured)
	}
}

func TestWrapChatErrorRecorded(t *testing.T) {
	boom := errors.New("boom")
	sink := &memSink{}
	c := Wrap(&bothClient{err: boom}, sink)
	if _, err := c.Chat(context.Background(), ai.ChatRequest{}); !errors.Is(err, boom) {
		t.Fatalf("应透传 error，得 %v", err)
	}
	if len(sink.recs) != 1 || sink.recs[0].Error == "" {
		t.Fatalf("错误未记录：%+v", sink.recs)
	}
}

func TestWrapChatUnsupportedInner(t *testing.T) {
	// inner 只实现 Complete（FakeLLM），Chat 应报"不支持"。
	c := Wrap(&ai.FakeLLM{On: func(ai.CompletionRequest) (ai.CompletionResult, error) {
		return ai.CompletionResult{}, nil
	}}, &memSink{})
	if _, err := c.Chat(context.Background(), ai.ChatRequest{}); err == nil {
		t.Fatal("inner 不支持 Chat 应报错")
	}
}
