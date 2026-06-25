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
