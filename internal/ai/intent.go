package ai

import "fmt"

// RejectReason 保留供 OrchestratorFeedback / guardian 快照渲染使用（编排语义校验回传原因）。
type RejectReason string

const (
	RejectNone                   RejectReason = ""
	RejectSchemaInvalid          RejectReason = "SCHEMA_INVALID"
	RejectIllegalTransition      RejectReason = "ILLEGAL_TRANSITION"
	RejectSubjectiveNotExhausted RejectReason = "SUBJECTIVE_NOT_EXHAUSTED"
	RejectRoundLimitFused        RejectReason = "ROUND_LIMIT_FUSED"
)

// PlanKind 是处置三选一（院内治疗执行难以闭环，无 TREATMENT）。
type PlanKind string

const (
	PlanMedication PlanKind = "MEDICATION"
	PlanAdviceOnly PlanKind = "ADVICE_ONLY"
	PlanReferral   PlanKind = "REFERRAL"
)

// TreatmentPlan 是处置方案：现由 finish/refer 工具入参解码而来，复用其结构校验。
type TreatmentPlan struct {
	Plan           PlanKind     `json:"plan"`
	Advice         string       `json:"advice"`
	Medications    []Medication `json:"medications,omitempty"`
	ReferralReason string       `json:"referral_reason,omitempty"`
}

// EmergencyInterrupt 是急症守护的产出。
type EmergencyInterrupt struct {
	Reason string `json:"reason"`
}

// Validate 做处置方案结构校验（advice 恒需非空 / enum 合法 / 自证字段存在）。
func (p TreatmentPlan) Validate() error {
	if p.Advice == "" {
		return fmt.Errorf("treatment: advice 恒需非空")
	}
	switch p.Plan {
	case PlanMedication:
		if len(p.Medications) == 0 {
			return fmt.Errorf("treatment MEDICATION: medications 为空")
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
