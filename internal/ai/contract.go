package ai

import (
	"context"
	"time"
)

// DialogTurn 是问诊对话中的一轮。
type DialogTurn struct {
	Role string // "patient" | "doctor"
	Text string
}

// Diagnosis 是确诊结果。
type Diagnosis struct {
	Name       string  `json:"name"`
	Basis      string  `json:"basis"`
	Confidence float64 `json:"confidence"`
}

// Medication 是一条用药明细。
type Medication struct {
	Name     string `json:"name"`
	Dosage   string `json:"dosage"`
	Schedule string `json:"schedule"`
}

// TestResult 是一条已回填的检验结果。
type TestResult struct {
	Item  string `json:"item"`
	Value string `json:"value"`
}

// VisitSummary 是复诊时携带的上次就诊摘要。
type VisitSummary struct {
	Diagnosis   *Diagnosis
	Medications []Medication
}

// RefusalRecord 是患者拒绝某项执行动作的记录。
type RefusalRecord struct {
	What string // "test_pay" | "med_pay" ...
	At   time.Time
}

// Event 是急症守护读取的事件。
type Event struct {
	Kind string // "dialog" | "vital" | "test_result"
	Data any
}

// OrchestratorFeedback 是编排层在 pull / 重调时回灌给 AI 的瞬时反馈。
type OrchestratorFeedback struct {
	LastReject   RejectReason
	NextExpected string
	CardDeferred bool
	MissingHint  []string
}

// Snapshot 是喂给 AI 的全量上下文。AI 无状态，每次从 Snapshot 重建。
// 注意：轮次计数不在此结构内——熔断由编排层强制，不提示 AI。
type Snapshot struct {
	Interview   []DialogTurn
	Subjective  map[string]any
	TestResults []TestResult
	Diagnosis   *Diagnosis
	PriorVisit  *VisitSummary
	Refusals    []RefusalRecord

	Feedback *OrchestratorFeedback
}

// DecisionLayer 是主决策层接口。全部无状态。
type DecisionLayer interface {
	Interview(ctx context.Context, s Snapshot) (InterviewResult, error)
	Triage(ctx context.Context, s Snapshot) (TriageDecision, error)
	Treatment(ctx context.Context, s Snapshot) (TreatmentPlan, error)
}

// Guardian 是急症守护接口，纯判断。并发/ticker 由编排层负责。
type Guardian interface {
	Assess(ctx context.Context, s Snapshot, ev Event) (EmergencyInterrupt, bool, error)
}
