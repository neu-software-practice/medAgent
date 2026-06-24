package ai

import (
	"context"
	"errors"
	"testing"
)

func TestTriageThreeWay(t *testing.T) {
	cases := []TriageDecision{
		{Decision: TriageConfirm, Diagnosis: &Diagnosis{Name: "急性咽炎", Confidence: 0.9}},
		{Decision: TriageInterview, MissingSubjective: []string{"体温"}},
		{Decision: TriageTest, SubjectiveExhausted: true, Reason: "区分感染", TestItems: []string{"血常规"}},
	}
	for _, want := range cases {
		w := want
		llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
			return StructuredOf(w), nil
		}}
		got, err := (triageAgent{llm: llm}).decide(context.Background(), Snapshot{})
		if err != nil {
			t.Fatalf("%s: %v", w.Decision, err)
		}
		if got.Decision != w.Decision {
			t.Fatalf("期望 %s 得到 %s", w.Decision, got.Decision)
		}
	}
}

func TestTriageTestMissingSelfProofExhaustsRetries(t *testing.T) {
	// TEST 但缺 subjective_exhausted —— 结构校验失败，内部重试耗尽
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(TriageDecision{Decision: TriageTest, Reason: "x", TestItems: []string{"血常规"}}), nil
	}}
	_, err := (triageAgent{llm: llm}).decide(context.Background(), Snapshot{})
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("期望 SchemaError，得到 %v", err)
	}
}
