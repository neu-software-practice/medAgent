package openaicompat

import (
	"testing"

	"medagent/internal/ai"
)

func TestMergeMessages_ConsecutiveSameRoleMerged(t *testing.T) {
	msgs := []ai.Message{
		{Role: "user", Content: "快照块"},
		{Role: "user", Content: "患者轮"},
		{Role: "assistant", Content: "医生回复"},
		{Role: "user", Content: "事件追加"},
	}
	got := mergeMessages(msgs)
	if len(got) != 3 {
		t.Fatalf("want 3 merged messages, got %d: %+v", len(got), got)
	}
	if got[0].Role != "user" || got[0].Content != "快照块\n\n患者轮" {
		t.Errorf("first merged = %+v", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content != "医生回复" {
		t.Errorf("second = %+v", got[1])
	}
	if got[2].Role != "user" || got[2].Content != "事件追加" {
		t.Errorf("third = %+v", got[2])
	}
}

func TestMergeMessages_AlternatingPreserved(t *testing.T) {
	msgs := []ai.Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
		{Role: "user", Content: "c"},
	}
	if got := mergeMessages(msgs); len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
}

func TestMergeMessages_Empty(t *testing.T) {
	if got := mergeMessages(nil); len(got) != 0 {
		t.Fatalf("want 0, got %d", len(got))
	}
}
