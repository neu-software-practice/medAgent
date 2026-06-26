package agent

import (
	"errors"
	"fmt"

	"medagent/internal/ai"
)

func resultFromPlan(p ai.TreatmentPlan, dg *ai.Diagnosis) Result {
	final := "ADVICE"
	if p.Plan == ai.PlanReferral {
		final = "REFERRAL"
	}
	r := Result{Final: final, Plan: string(p.Plan), Advice: p.Advice}
	if dg != nil {
		r.Diagnosis = &Diagnosis{Name: dg.Name, Basis: dg.Basis, Confidence: dg.Confidence}
	}
	for _, m := range p.Medications {
		r.Medications = append(r.Medications, Medication{Name: m.Name, Dosage: m.Dosage, Schedule: m.Schedule, Quantity: m.Quantity})
	}
	return r
}

func ordersFromMeds(meds []ai.Medication) []DrugOrder {
	out := make([]DrugOrder, 0, len(meds))
	for _, m := range meds {
		out = append(out, DrugOrder{Name: m.Name, Quantity: m.Quantity})
	}
	return out
}

func testResultsToAI(in []TestResult) []ai.TestResult {
	out := make([]ai.TestResult, 0, len(in))
	for _, t := range in {
		out = append(out, ai.TestResult{Item: t.Item, Value: t.Value})
	}
	return out
}

// mapErr 把内部错误归一为公开 sentinel；ctx 取消由调用处先行返回，不进这里。
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var se *ai.SchemaError
	if errors.As(err, &se) {
		return fmt.Errorf("%w: %v", ErrModelOutput, err)
	}
	if errors.Is(err, ai.ErrLLM) {
		return fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	return fmt.Errorf("%w: %v", ErrUpstream, err)
}
