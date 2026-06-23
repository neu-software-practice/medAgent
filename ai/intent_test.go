package ai

import "testing"

func TestAdvanceToTriageValidate(t *testing.T) {
	if err := (AdvanceToTriage{Subjective: map[string]any{"主诉": "咽痛"}}).Validate(); err != nil {
		t.Fatalf("期望通过，得到 %v", err)
	}
	if err := (AdvanceToTriage{}).Validate(); err == nil {
		t.Fatal("空 subjective 应失败")
	}
}

func TestTriageDecisionValidate(t *testing.T) {
	cases := []struct {
		name string
		in   TriageDecision
		ok   bool
	}{
		{"confirm_ok", TriageDecision{Decision: TriageConfirm, Diagnosis: &Diagnosis{Name: "急性咽炎", Confidence: 0.9}}, true},
		{"confirm_no_diag", TriageDecision{Decision: TriageConfirm}, false},
		{"confirm_bad_conf", TriageDecision{Decision: TriageConfirm, Diagnosis: &Diagnosis{Name: "x", Confidence: 1.5}}, false},
		{"interview_ok", TriageDecision{Decision: TriageInterview, MissingSubjective: []string{"体温"}}, true},
		{"interview_empty", TriageDecision{Decision: TriageInterview}, false},
		{"test_ok", TriageDecision{Decision: TriageTest, SubjectiveExhausted: true, Reason: "区分感染", TestItems: []string{"血常规"}}, true},
		{"test_not_exhausted", TriageDecision{Decision: TriageTest, Reason: "x", TestItems: []string{"血常规"}}, false},
		{"test_no_items", TriageDecision{Decision: TriageTest, SubjectiveExhausted: true, Reason: "x"}, false},
		{"bad_decision", TriageDecision{Decision: "FOO"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.in.Validate()
			if c.ok && err != nil {
				t.Fatalf("期望通过，得到 %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("期望失败，却通过")
			}
		})
	}
}

func TestTreatmentPlanValidate(t *testing.T) {
	cases := []struct {
		name string
		in   TreatmentPlan
		ok   bool
	}{
		{"med_ok", TreatmentPlan{Plan: PlanMedication, Advice: "多休息", Medications: []Medication{{Name: "x"}}}, true},
		{"med_no_meds", TreatmentPlan{Plan: PlanMedication, Advice: "多休息"}, false},
		{"no_advice", TreatmentPlan{Plan: PlanAdviceOnly}, false},
		{"advice_only_ok", TreatmentPlan{Plan: PlanAdviceOnly, Advice: "观察"}, true},
		{"treat_ok", TreatmentPlan{Plan: PlanTreatment, Advice: "a", RequiredCapability: "理疗"}, true},
		{"treat_no_cap", TreatmentPlan{Plan: PlanTreatment, Advice: "a"}, false},
		{"referral_ok", TreatmentPlan{Plan: PlanReferral, Advice: "a", ReferralReason: "无能力"}, true},
		{"referral_no_reason", TreatmentPlan{Plan: PlanReferral, Advice: "a"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.in.Validate()
			if c.ok && err != nil {
				t.Fatalf("期望通过，得到 %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("期望失败，却通过")
			}
		})
	}
}

func TestInterviewResultValidate(t *testing.T) {
	if err := (InterviewResult{Reply: "请问体温多少？"}).Validate(); err != nil {
		t.Fatalf("纯追问应通过，得到 %v", err)
	}
	if err := (InterviewResult{Advance: &AdvanceToTriage{Subjective: map[string]any{"a": 1}}}).Validate(); err != nil {
		t.Fatalf("带 advance 应通过，得到 %v", err)
	}
	if err := (InterviewResult{}).Validate(); err == nil {
		t.Fatal("既无 reply 又无 advance 应失败")
	}
	if err := (InterviewResult{Advance: &AdvanceToTriage{}}).Validate(); err == nil {
		t.Fatal("advance.subjective 为空应失败")
	}
}

func TestEmergencyInterruptValidate(t *testing.T) {
	if err := (EmergencyInterrupt{Reason: "胸痛伴呼吸困难"}).Validate(); err != nil {
		t.Fatalf("期望通过，得到 %v", err)
	}
	if err := (EmergencyInterrupt{}).Validate(); err == nil {
		t.Fatal("空 reason 应失败")
	}
}
