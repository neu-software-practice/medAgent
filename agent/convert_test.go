package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"medagent/internal/ai"
)

func TestResultFromPlan(t *testing.T) {
	dg := &ai.Diagnosis{Name: "急性咽炎", Basis: "症状", Confidence: 0.9}
	plan := ai.TreatmentPlan{
		Plan: ai.PlanMedication, Advice: "多休息",
		Medications: []ai.Medication{{Name: "对乙酰氨基酚", Dosage: "0.5g", Schedule: "每日3次", Quantity: 2}},
	}
	r := resultFromPlan(plan, dg)
	if r.Final != "ADVICE" || r.Plan != "MEDICATION" || r.Diagnosis.Name != "急性咽炎" {
		t.Fatalf("result 不符：%+v", r)
	}
	if len(r.Medications) != 1 || r.Medications[0].Quantity != 2 {
		t.Fatalf("medication 不符：%+v", r.Medications)
	}
}

func TestResultFinalReferral(t *testing.T) {
	r := resultFromPlan(ai.TreatmentPlan{Plan: ai.PlanReferral, Advice: "转诊", ReferralReason: "超能力"}, nil)
	if r.Final != "REFERRAL" {
		t.Fatalf("Final = %q, want REFERRAL", r.Final)
	}
}

func TestOrdersFromAI(t *testing.T) {
	o := ordersFromAI([]ai.DrugOrder{{Name: "阿莫西林", Quantity: 3}, {Name: "布洛芬", Quantity: 1}})
	if len(o) != 2 || o[0].Name != "阿莫西林" || o[0].Quantity != 3 {
		t.Fatalf("orders 不符：%+v", o)
	}
}

func TestMapErr(t *testing.T) {
	if !errors.Is(mapErr(ai.ErrLLM), ErrUpstream) {
		t.Errorf("ErrLLM 应映射 ErrUpstream")
	}
	if !errors.Is(mapErr(&ai.SchemaError{Agent: "triage"}), ErrModelOutput) {
		t.Errorf("SchemaError 应映射 ErrModelOutput")
	}
	if mapErr(nil) != nil {
		t.Errorf("nil 应映射 nil")
	}
}

func TestMapErrTimeoutMapsToDeadline(t *testing.T) {
	// 链中含 context.DeadlineExceeded（如 LLM 客户端 http.Client.Timeout）→ 归一 ctx 超时（HTTP 504）
	chain := fmt.Errorf("%w: interview: %w", ai.ErrLLM,
		fmt.Errorf("openaicompat: 请求失败 (%w)", context.DeadlineExceeded))
	got := mapErr(chain)
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("超时应映射 context.DeadlineExceeded，got %v", got)
	}
	if errors.Is(got, ErrUpstream) {
		t.Fatalf("超时不应再归类 ErrUpstream")
	}
}

func TestMapErrNetTimeoutMapsToDeadline(t *testing.T) {
	chain := fmt.Errorf("%w: interview: %w", ai.ErrLLM, netTimeoutErr{})
	if !errors.Is(mapErr(chain), context.DeadlineExceeded) {
		t.Fatalf("net 超时应映射 context.DeadlineExceeded（HTTP 504）")
	}
}

// netTimeoutErr 实现 net.Error，Timeout()=true，模拟网络超时。
type netTimeoutErr struct{}

func (netTimeoutErr) Error() string   { return "i/o timeout" }
func (netTimeoutErr) Timeout() bool   { return true }
func (netTimeoutErr) Temporary() bool { return false }
