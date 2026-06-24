package ai

import "fmt"

// RejectReason 是编排层语义校验的回传原因（镜像系统规格 §4.2）。
type RejectReason string

const (
	RejectNone                   RejectReason = ""
	RejectSchemaInvalid          RejectReason = "SCHEMA_INVALID"
	RejectIllegalTransition      RejectReason = "ILLEGAL_TRANSITION"
	RejectCapabilityMissing      RejectReason = "CAPABILITY_MISSING"
	RejectSubjectiveNotExhausted RejectReason = "SUBJECTIVE_NOT_EXHAUSTED"
	RejectRoundLimitFused        RejectReason = "ROUND_LIMIT_FUSED"
)

// AdvanceToTriage：问诊充分，请求进入决策。
type AdvanceToTriage struct {
	Subjective map[string]any `json:"subjective"`
}

// InterviewResult 是问诊 agent 的产出：Reply 总是给患者的话；Advance 非空表示问诊充分，
// 此时 Reply 为过场告知（二者可共存）。仅 Reply 为空且无 Advance 时非法。
type InterviewResult struct {
	Reply   string           `json:"reply"`
	Advance *AdvanceToTriage `json:"advance,omitempty"`
}

// TriageChoice 是收敛环三选一。
type TriageChoice string

const (
	TriageConfirm   TriageChoice = "CONFIRM"
	TriageInterview TriageChoice = "INTERVIEW"
	TriageTest      TriageChoice = "TEST"
)

// TriageDecision 是 triage agent 的产出。
type TriageDecision struct {
	Decision            TriageChoice `json:"decision"`
	Diagnosis           *Diagnosis   `json:"diagnosis,omitempty"`
	MissingSubjective   []string     `json:"missing_subjective,omitempty"`
	SubjectiveExhausted bool         `json:"subjective_exhausted,omitempty"`
	Reason              string       `json:"reason,omitempty"`
	TestItems           []string     `json:"test_items,omitempty"`
}

// PlanKind 是处置四选一。
type PlanKind string

const (
	PlanMedication PlanKind = "MEDICATION"
	PlanTreatment  PlanKind = "TREATMENT"
	PlanAdviceOnly PlanKind = "ADVICE_ONLY"
	PlanReferral   PlanKind = "REFERRAL"
)

// TreatmentPlan 是处置 agent 的产出。
type TreatmentPlan struct {
	Plan               PlanKind     `json:"plan"`
	Advice             string       `json:"advice"`
	Medications        []Medication `json:"medications,omitempty"`
	RequiredCapability string       `json:"required_capability,omitempty"`
	ReferralReason     string       `json:"referral_reason,omitempty"`
}

// EmergencyInterrupt 是急症守护的产出。
type EmergencyInterrupt struct {
	Reason string `json:"reason"`
}

// Validate 做结构校验（字段齐全 / enum 合法 / 自证字段存在），对应 SCHEMA_INVALID。
func (a AdvanceToTriage) Validate() error {
	if len(a.Subjective) == 0 {
		return fmt.Errorf("advance_to_triage: subjective 为空")
	}
	return nil
}

func (r InterviewResult) Validate() error {
	if r.Advance != nil {
		return r.Advance.Validate()
	}
	if r.Reply == "" {
		return fmt.Errorf("interview: reply 为空且无 advance")
	}
	return nil
}

func (t TriageDecision) Validate() error {
	switch t.Decision {
	case TriageConfirm:
		if t.Diagnosis == nil {
			return fmt.Errorf("triage CONFIRM: diagnosis 缺失")
		}
		if t.Diagnosis.Name == "" {
			return fmt.Errorf("triage CONFIRM: diagnosis.name 为空")
		}
		if t.Diagnosis.Confidence < 0 || t.Diagnosis.Confidence > 1 {
			return fmt.Errorf("triage CONFIRM: confidence 越界 %g", t.Diagnosis.Confidence)
		}
	case TriageInterview:
		if len(t.MissingSubjective) == 0 {
			return fmt.Errorf("triage INTERVIEW: missing_subjective 为空")
		}
	case TriageTest:
		if !t.SubjectiveExhausted {
			return fmt.Errorf("triage TEST: 必须 subjective_exhausted=true")
		}
		if t.Reason == "" {
			return fmt.Errorf("triage TEST: reason 为空")
		}
		if len(t.TestItems) == 0 {
			return fmt.Errorf("triage TEST: test_items 为空")
		}
	default:
		return fmt.Errorf("triage: 非法 decision %q", t.Decision)
	}
	return nil
}

func (p TreatmentPlan) Validate() error {
	if p.Advice == "" {
		return fmt.Errorf("treatment: advice 恒需非空")
	}
	switch p.Plan {
	case PlanMedication:
		if len(p.Medications) == 0 {
			return fmt.Errorf("treatment MEDICATION: medications 为空")
		}
	case PlanTreatment:
		if p.RequiredCapability == "" {
			return fmt.Errorf("treatment TREATMENT: required_capability 为空")
		}
	case PlanAdviceOnly:
		// advice 已在上方校验
	case PlanReferral:
		if p.ReferralReason == "" {
			return fmt.Errorf("treatment REFERRAL: referral_reason 为空")
		}
	default:
		return fmt.Errorf("treatment: 非法 plan %q", p.Plan)
	}
	return nil
}

func (e EmergencyInterrupt) Validate() error {
	if e.Reason == "" {
		return fmt.Errorf("emergency_interrupt: reason 为空")
	}
	return nil
}
