package ai

import "testing"

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
		{"referral_ok", TreatmentPlan{Plan: PlanReferral, Advice: "a", ReferralReason: "无能力"}, true},
		{"referral_no_reason", TreatmentPlan{Plan: PlanReferral, Advice: "a"}, false},
		{"bad_plan", TreatmentPlan{Plan: "UNKNOWN", Advice: "a"}, false},
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

func TestEmergencyInterruptValidate(t *testing.T) {
	if err := (EmergencyInterrupt{Reason: "胸痛伴呼吸困难"}).Validate(); err != nil {
		t.Fatalf("期望通过，得到 %v", err)
	}
	if err := (EmergencyInterrupt{}).Validate(); err == nil {
		t.Fatal("空 reason 应失败")
	}
}
