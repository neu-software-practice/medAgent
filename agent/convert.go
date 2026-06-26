package agent

import (
	"context"
	"errors"
	"fmt"
	"net"

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

// mapErr 把内部错误归一为公开 sentinel。请求 ctx 取消由 ctxOrMap 先行返回；
// 但 LLM 客户端自身的 http.Client.Timeout 触发时请求 ctx 未取消，超时埋在错误链里，
// 这里据链中的 context.DeadlineExceeded / net 超时归一为 ctx 错误，使 HTTP 映射 504（文档 §8）。
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || isNetTimeout(err) {
		return fmt.Errorf("%w: %v", context.DeadlineExceeded, err)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %v", context.Canceled, err)
	}
	var se *ai.SchemaError
	if errors.As(err, &se) {
		return fmt.Errorf("%w: %v", ErrModelOutput, err)
	}
	return fmt.Errorf("%w: %v", ErrUpstream, err)
}

// isNetTimeout 识别链中的网络超时（如 *url.Error / *net.OpError 的 Timeout()）。
func isNetTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
