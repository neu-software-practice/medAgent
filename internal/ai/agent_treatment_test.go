package ai

import (
	"context"
	"strings"
	"testing"
)

func TestTreatmentFourPlansAlwaysAdvice(t *testing.T) {
	plans := []TreatmentPlan{
		{Plan: PlanMedication, Advice: "多休息", Medications: []Medication{{Name: "对乙酰氨基酚"}}},
		{Plan: PlanTreatment, Advice: "复查", RequiredCapability: "理疗"},
		{Plan: PlanAdviceOnly, Advice: "观察体温"},
		{Plan: PlanReferral, Advice: "尽快就医", ReferralReason: "本院无能力"},
	}
	for _, want := range plans {
		w := want
		llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
			return StructuredOf(w), nil
		}}
		got, err := (treatmentAgent{llm: llm}).decide(context.Background(), Snapshot{Diagnosis: &Diagnosis{Name: "x"}})
		if err != nil {
			t.Fatalf("%s: %v", w.Plan, err)
		}
		if got.Advice == "" {
			t.Fatalf("%s: advice 不应为空", w.Plan)
		}
	}
}

func TestTreatmentInjectsRefusals(t *testing.T) {
	var seen string
	llm := &FakeLLM{On: func(req CompletionRequest) (CompletionResult, error) {
		seen = req.Messages[0].Content
		return StructuredOf(TreatmentPlan{Plan: PlanAdviceOnly, Advice: "未购药，注意复测"}), nil
	}}
	s := Snapshot{Diagnosis: &Diagnosis{Name: "x"}, Refusals: []RefusalRecord{{What: "med_pay"}}}
	if _, err := (treatmentAgent{llm: llm}).decide(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen, "med_pay") {
		t.Fatalf("拒绝记录未注入上下文：%s", seen)
	}
}
