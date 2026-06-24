package ai

import "context"

// triageAgent 是收敛环决策点：CONFIRM / INTERVIEW / TEST 三选一。
type triageAgent struct{ llm LLMClient }

func (a triageAgent) decide(ctx context.Context, s Snapshot) (TriageDecision, error) {
	return runStructured[TriageDecision](ctx, a.llm, "triage", promptTriage, schemaTriage, s)
}
