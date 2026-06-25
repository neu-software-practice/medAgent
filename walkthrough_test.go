package medagent

import (
	"context"
	"testing"

	"medagent/internal/ai"
)

// 急性咽炎主干：问诊→（无 advance 先 ASK）→advance→TEST→回填→CONFIRM→ADVICE_ONLY DONE。
func TestWalkthroughPharyngitis(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			if n == 1 {
				return ai.InterviewResult{Reply: "发烧多少度？有无咳嗽？"}, nil
			}
			return ai.InterviewResult{Reply: "信息够了", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"主诉": "咽痛发热", "体温": "38.5"}}}, nil
		case "triage_decide":
			if n == 1 {
				return ai.TriageDecision{Decision: ai.TriageTest, SubjectiveExhausted: true, Reason: "区分病毒细菌", TestItems: []string{"血常规"}}, nil
			}
			return ai.TriageDecision{Decision: ai.TriageConfirm, Diagnosis: &ai.Diagnosis{Name: "急性咽炎", Basis: "症状+血象", Confidence: 0.9}}, nil
		case "treatment_plan":
			return ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "多休息多饮水"}, nil
		case "emergency_interrupt":
			return map[string]any{"hit": false}, nil
		}
		return nil, nil
	})
	s := newService(Config{}, ai.NewDecisionLayer(fake), ai.NewGuardian(fake))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	st, _ := s.PatientSay(context.Background(), id, "嗓子痛发烧")
	if st.Kind != StepAsk {
		t.Fatalf("先 ASK：%+v", st)
	}
	st, _ = s.PatientSay(context.Background(), id, "38.5度，干咳")
	if st.Kind != StepNeedTests {
		t.Fatalf("应 NEED_TESTS：%+v", st)
	}
	st, _ = s.SupplyTestResults(context.Background(), id, []TestResult{{Item: "血常规", Value: "淋巴偏高"}})
	if st.Kind != StepDone || st.Result.Diagnosis.Name != "急性咽炎" {
		t.Fatalf("应 DONE 急性咽炎：%+v", st)
	}
	if st.Result.Plan != string(ai.PlanAdviceOnly) {
		t.Fatalf("应 PlanAdviceOnly，得 %s", st.Result.Plan)
	}
}

// 能力缺失→转诊。
func TestWalkthroughCapabilityReferral(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			return ai.InterviewResult{Reply: "x", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"a": "b"}}}, nil
		case "triage_decide":
			return ai.TriageDecision{Decision: ai.TriageConfirm, Diagnosis: &ai.Diagnosis{Name: "需手术病", Basis: "x", Confidence: 0.9}}, nil
		case "treatment_plan":
			if n == 1 {
				return ai.TreatmentPlan{Plan: ai.PlanTreatment, Advice: "需手术", RequiredCapability: "外科手术"}, nil
			}
			return ai.TreatmentPlan{Plan: ai.PlanReferral, Advice: "转上级医院", ReferralReason: "本院无外科手术能力"}, nil
		case "emergency_interrupt":
			return map[string]any{"hit": false}, nil
		}
		return nil, nil
	})
	s := newService(Config{Caps: map[string]bool{}}, ai.NewDecisionLayer(fake), ai.NewGuardian(fake))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	st, _ := s.PatientSay(context.Background(), id, "hi")
	if st.Kind != StepDone || st.Result.Final != "REFERRAL" {
		t.Fatalf("应转诊 DONE：%+v", st)
	}
}
