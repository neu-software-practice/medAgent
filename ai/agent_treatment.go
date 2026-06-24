package ai

import "context"

// treatmentAgent 是处置决策器：确诊后四选一，并无条件写入医嘱。
type treatmentAgent struct{ llm LLMClient }

func (a treatmentAgent) decide(ctx context.Context, s Snapshot) (TreatmentPlan, error) {
	return runStructured[TreatmentPlan](ctx, a.llm, "treatment", promptTreatment, schemaTreatment, s)
}
