package ai

import "context"

// Layer 组装 4 个 agent，实现 DecisionLayer。
type Layer struct {
	interview interviewAgent
	triage    triageAgent
	treatment treatmentAgent
}

// NewDecisionLayer 用给定 LLMClient 构造决策层。
func NewDecisionLayer(llm LLMClient) *Layer {
	return &Layer{
		interview: interviewAgent{llm: llm},
		triage:    triageAgent{llm: llm},
		treatment: treatmentAgent{llm: llm},
	}
}

func (l *Layer) Interview(ctx context.Context, s Snapshot) (InterviewResult, error) {
	return l.interview.decide(ctx, s)
}
func (l *Layer) Triage(ctx context.Context, s Snapshot) (TriageDecision, error) {
	return l.triage.decide(ctx, s)
}
func (l *Layer) Treatment(ctx context.Context, s Snapshot) (TreatmentPlan, error) {
	return l.treatment.decide(ctx, s)
}

// NewGuardian 用给定 LLMClient 构造急症守护。
func NewGuardian(llm LLMClient) Guardian { return guardianAgent{llm: llm} }

var (
	_ DecisionLayer = (*Layer)(nil)
	_ Guardian      = guardianAgent{}
)
