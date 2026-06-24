package ai

import (
	"context"
	"strings"
	"testing"
)

func TestInterviewReplyOnly(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(InterviewResult{Reply: "发烧最高多少度？"}), nil
	}}
	a := interviewAgent{llm: llm}
	res, err := a.decide(context.Background(), Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Advance != nil || res.Reply == "" {
		t.Fatalf("期望纯追问，得到 %+v", res)
	}
}

func TestInterviewAdvance(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(InterviewResult{
			Reply:   "信息够了",
			Advance: &AdvanceToTriage{Subjective: map[string]any{"主诉": "咽痛"}},
		}), nil
	}}
	a := interviewAgent{llm: llm}
	res, err := a.decide(context.Background(), Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Advance == nil || res.Advance.Subjective["主诉"] != "咽痛" {
		t.Fatalf("期望 advance，得到 %+v", res)
	}
}

func TestInterviewInjectsMissingHint(t *testing.T) {
	var seen string
	llm := &FakeLLM{On: func(req CompletionRequest) (CompletionResult, error) {
		seen = req.Messages[0].Content
		return StructuredOf(InterviewResult{Reply: "好的"}), nil
	}}
	a := interviewAgent{llm: llm}
	s := Snapshot{Feedback: &OrchestratorFeedback{MissingHint: []string{"用药史"}}}
	if _, err := a.decide(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen, "用药史") {
		t.Fatalf("MissingHint 未注入上下文：%s", seen)
	}
}
