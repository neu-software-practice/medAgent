package ai

import (
	"context"
	"testing"
)

func TestLayerRoutesToAgents(t *testing.T) {
	llm := &FakeLLM{On: func(req CompletionRequest) (CompletionResult, error) {
		switch req.Schema.Name {
		case "interview":
			return StructuredOf(InterviewResult{Reply: "hi"}), nil
		case "triage_decide":
			return StructuredOf(TriageDecision{Decision: TriageInterview, MissingSubjective: []string{"体温"}}), nil
		case "treatment_plan":
			return StructuredOf(TreatmentPlan{Plan: PlanAdviceOnly, Advice: "观察"}), nil
		}
		t.Fatalf("未预期 schema %q", req.Schema.Name)
		return CompletionResult{}, nil
	}}
	l := NewDecisionLayer(llm)
	ctx := context.Background()
	if r, err := l.Interview(ctx, Snapshot{}); err != nil || r.Reply != "hi" {
		t.Fatalf("Interview 路由错误 r=%+v err=%v", r, err)
	}
	if d, err := l.Triage(ctx, Snapshot{}); err != nil || d.Decision != TriageInterview {
		t.Fatalf("Triage 路由错误 d=%+v err=%v", d, err)
	}
	if p, err := l.Treatment(ctx, Snapshot{}); err != nil || p.Plan != PlanAdviceOnly {
		t.Fatalf("Treatment 路由错误 p=%+v err=%v", p, err)
	}
}
