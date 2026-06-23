package ai

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

// InterviewResult 是问诊 agent 的产出：要么继续追问（Reply），要么 Advance。
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
