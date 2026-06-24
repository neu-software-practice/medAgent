package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"medagent/ai"
)

// scriptedLLM 按 schema name + 调用计数复刻急性咽炎场景。
func scriptedLLM(t *testing.T) *ai.FakeLLM {
	interviewN, triageN := 0, 0
	return &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		switch req.Schema.Name {
		case "interview":
			interviewN++
			if interviewN == 1 {
				return ai.StructuredOf(ai.InterviewResult{Reply: "发烧最高多少度？有没有咳嗽？"}), nil
			}
			return ai.StructuredOf(ai.InterviewResult{
				Reply: "信息够了，我来判断一下。",
				Advance: &ai.AdvanceToTriage{Subjective: map[string]any{
					"主诉": "咽痛发热", "体温": "38.5", "咳嗽": "干咳",
				}},
			}), nil
		case "triage_decide":
			triageN++
			if triageN == 1 {
				return ai.StructuredOf(ai.TriageDecision{
					Decision: ai.TriageTest, SubjectiveExhausted: true,
					Reason: "需区分细菌或病毒感染", TestItems: []string{"血常规"},
				}), nil
			}
			return ai.StructuredOf(ai.TriageDecision{
				Decision:  ai.TriageConfirm,
				Diagnosis: &ai.Diagnosis{Name: "急性咽炎", Basis: "症状+体温+血常规", Confidence: 0.9},
			}), nil
		case "treatment_plan":
			return ai.StructuredOf(ai.TreatmentPlan{
				Plan: ai.PlanMedication, Advice: "多休息、多饮水，观察体温",
				Medications: []ai.Medication{{Name: "对乙酰氨基酚", Dosage: "0.5g", Schedule: "每日3次"}},
			}), nil
		}
		t.Fatalf("未预期 schema %q", req.Schema.Name)
		return ai.CompletionResult{}, fmt.Errorf("unreachable")
	}}
}

func TestWalkthroughAcutePharyngitis(t *testing.T) {
	llm := scriptedLLM(t)
	answers := []string{"嗓子痛、有点发烧，从昨晚开始。", "38.5℃，有点干咳，呼吸正常。"}
	i := 0
	deps := Deps{
		Layer: ai.NewDecisionLayer(llm),
		Caps:  map[string]bool{},
		Patient: func(string) string {
			msg := answers[i]
			if i < len(answers)-1 {
				i++
			}
			return msg
		},
		TestResults: func([]string) []ai.TestResult {
			return []ai.TestResult{{Item: "血常规", Value: "淋巴细胞偏高，提示病毒"}}
		},
	}
	out, err := RunVisit(context.Background(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if out.Final != "ADVICE" || out.Plan != ai.PlanMedication {
		t.Fatalf("终态不符：%+v", out)
	}
	if out.Diagnosis == nil || out.Diagnosis.Name != "急性咽炎" {
		t.Fatalf("诊断不符：%+v", out.Diagnosis)
	}
	trace := strings.Join(out.Trace, ",")
	for _, want := range []string{"advance", "triage:TEST", "test_filled", "triage:CONFIRM", "treatment:MEDICATION"} {
		if !strings.Contains(trace, want) {
			t.Fatalf("轨迹缺少 %q：%s", want, trace)
		}
	}
}
