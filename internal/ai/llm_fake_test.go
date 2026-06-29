package ai

import (
	"context"
	"testing"
)

func TestFakeLLMReturnsScripted(t *testing.T) {
	f := &FakeLLM{On: func(req CompletionRequest) (CompletionResult, error) {
		if req.Schema.Name != "x" {
			t.Fatalf("未透传 schema name: %q", req.Schema.Name)
		}
		return StructuredOf(map[string]any{"ok": true}), nil
	}}
	res, err := f.Complete(context.Background(), CompletionRequest{Schema: OutputSchema{Name: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Structured) != `{"ok":true}` {
		t.Fatalf("结构化输出不符: %s", res.Structured)
	}
}

func TestFakeLLMHonorsCanceledContext(t *testing.T) {
	f := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		t.Fatal("ctx 已取消不应调用 On")
		return CompletionResult{}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Complete(ctx, CompletionRequest{}); err == nil {
		t.Fatal("期望 ctx.Err()")
	}
}

func TestFakeChatReturnsScriptedToolCall(t *testing.T) {
	fc := &FakeChat{OnChat: func(req ChatRequest) (AssistantTurn, error) {
		if req.ToolChoice != "required" {
			t.Fatalf("未透传 ToolChoice: %q", req.ToolChoice)
		}
		return ToolCallTurn("call_1", "finish", map[string]any{"plan": "ADVICE_ONLY", "advice": "多休息"}), nil
	}}
	turn, err := fc.Chat(context.Background(), ChatRequest{ToolChoice: "required"})
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.ToolCalls) != 1 || turn.ToolCalls[0].Name != "finish" {
		t.Fatalf("工具调用不符: %+v", turn.ToolCalls)
	}
	if string(turn.ToolCalls[0].Arguments) != `{"advice":"多休息","plan":"ADVICE_ONLY"}` {
		t.Fatalf("args 不符: %s", turn.ToolCalls[0].Arguments)
	}
}

func TestFakeChatHonorsCanceledContext(t *testing.T) {
	fc := &FakeChat{OnChat: func(ChatRequest) (AssistantTurn, error) {
		t.Fatal("ctx 已取消不应调用 OnChat")
		return AssistantTurn{}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fc.Chat(ctx, ChatRequest{}); err == nil {
		t.Fatal("期望 ctx.Err()")
	}
}
