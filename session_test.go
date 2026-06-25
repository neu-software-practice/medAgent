package medagent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"medagent/internal/ai"
)

// scriptLLM 按 schema name + 计数返回脚本输出。
func scriptLLM(fn func(name string, n int) (any, error)) *ai.FakeLLM {
	counts := map[string]int{}
	var mu sync.Mutex
	return &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		mu.Lock()
		counts[req.Schema.Name]++
		n := counts[req.Schema.Name]
		mu.Unlock()
		v, err := fn(req.Schema.Name, n)
		if err != nil {
			return ai.CompletionResult{}, err
		}
		return ai.StructuredOf(v), nil
	}}
}

func svcWith(t *testing.T, fake *ai.FakeLLM, caps map[string]bool) *Service {
	t.Helper()
	return newService(Config{Caps: caps, DisableGuardian: true},
		ai.NewDecisionLayer(fake), ai.NewGuardian(fake))
}

func TestFlowConfirmMedicationPurchaseDone(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			return ai.InterviewResult{Reply: "够了", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"主诉": "咽痛"}}}, nil
		case "triage_decide":
			return ai.TriageDecision{Decision: ai.TriageConfirm, Diagnosis: &ai.Diagnosis{Name: "急性咽炎", Basis: "症状", Confidence: 0.9}}, nil
		case "treatment_plan":
			if n == 1 {
				return ai.TreatmentPlan{Plan: ai.PlanMedication, Advice: "多休息",
					Medications: []ai.Medication{{Name: "对乙酰氨基酚", Dosage: "0.5g", Schedule: "每日3次", Quantity: 2}}}, nil
			}
			return ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "已购药，按医嘱服用，未购抗生素请补"}, nil
		}
		return nil, nil
	})
	s := svcWith(t, fake, nil)
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	st, err := s.PatientSay(context.Background(), id, "嗓子疼")
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepPurchase || len(st.Orders) != 1 || st.Orders[0].Quantity != 2 {
		t.Fatalf("应到 PURCHASE：%+v", st)
	}

	st, err = s.SupplyPurchaseResult(context.Background(), id, []DrugPurchase{{Name: "对乙酰氨基酚", Bought: true, Quantity: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepDone || st.Result == nil || st.Result.Final != "ADVICE" {
		t.Fatalf("应到 DONE：%+v", st)
	}
}

func TestFlowAskThenTest(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			if n == 1 {
				return ai.InterviewResult{Reply: "发烧几天了？"}, nil
			}
			return ai.InterviewResult{Reply: "好的", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"主诉": "发热"}}}, nil
		case "triage_decide":
			if n == 1 {
				return ai.TriageDecision{Decision: ai.TriageTest, SubjectiveExhausted: true, Reason: "需区分", TestItems: []string{"血常规"}}, nil
			}
			return ai.TriageDecision{Decision: ai.TriageConfirm, Diagnosis: &ai.Diagnosis{Name: "病毒感染", Basis: "血象", Confidence: 0.8}}, nil
		case "treatment_plan":
			return ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "多休息多饮水"}, nil
		}
		return nil, nil
	})
	s := svcWith(t, fake, nil)
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	st, _ := s.PatientSay(context.Background(), id, "发烧")
	if st.Kind != StepAsk || st.DoctorSay == "" {
		t.Fatalf("应先 ASK：%+v", st)
	}
	st, _ = s.PatientSay(context.Background(), id, "两天")
	if st.Kind != StepNeedTests || len(st.TestItems) != 1 || st.TestItems[0] != "血常规" {
		t.Fatalf("应 NEED_TESTS 血常规：%+v", st)
	}
	st, err := s.SupplyTestResults(context.Background(), id, []TestResult{{Item: "血常规", Value: "淋巴升高"}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepDone {
		t.Fatalf("应 DONE：%+v", st)
	}
}

func TestWrongStepAndClosed(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			return ai.InterviewResult{Reply: "x", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"a": "b"}}}, nil
		case "triage_decide":
			return ai.TriageDecision{Decision: ai.TriageConfirm, Diagnosis: &ai.Diagnosis{Name: "X", Basis: "y", Confidence: 0.9}}, nil
		case "treatment_plan":
			return ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "休息"}, nil
		}
		return nil, nil
	})
	s := svcWith(t, fake, nil)
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	if _, err := s.SupplyTestResults(context.Background(), id, nil); err != ErrWrongStep {
		t.Fatalf("非检验态应 ErrWrongStep，got %v", err)
	}
	st, _ := s.PatientSay(context.Background(), id, "hi")
	if st.Kind != StepDone {
		t.Fatalf("应 DONE：%+v", st)
	}
	if _, err := s.PatientSay(context.Background(), id, "again"); err != ErrSessionClosed {
		t.Fatalf("done 后应 ErrSessionClosed，got %v", err)
	}
}

func TestErrorMapping(t *testing.T) {
	fake := &ai.FakeLLM{On: func(ai.CompletionRequest) (ai.CompletionResult, error) {
		return ai.CompletionResult{}, ai.ErrLLM
	}}
	s := svcWith(t, fake, nil)
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	if _, err := s.PatientSay(context.Background(), id, "hi"); !errors.Is(err, ErrUpstream) {
		t.Fatalf("应 ErrUpstream，got %v", err)
	}
}

// TestTransientErrorRecovery 验证瞬时错误后会话不卡死：
// advance 在 triage 第一次失败时，PatientSay 回滚 Interview/Turns/phase，
// 使客户端可用同一接口重试直到成功。
func TestTransientErrorRecovery(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			// 每次都直接 advance（收集够主诉）
			return ai.InterviewResult{Reply: "好的", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"主诉": "头疼"}}}, nil
		case "triage_decide":
			if n == 1 {
				return nil, ai.ErrLLM // 第一次瞬时错误
			}
			return ai.TriageDecision{
				Decision:  ai.TriageConfirm,
				Diagnosis: &ai.Diagnosis{Name: "偏头痛", Basis: "症状", Confidence: 0.85},
			}, nil
		case "treatment_plan":
			return ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "多休息"}, nil
		}
		return nil, nil
	})
	s := svcWith(t, fake, nil)
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	// 第一次 PatientSay：interview 成功，但 triage 瞬时失败 → ErrUpstream
	_, err := s.PatientSay(context.Background(), id, "头疼")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("第一次应 ErrUpstream，got %v", err)
	}

	// 会话未卡死：事务已回滚
	sess, _ := s.get(id)
	sess.mu.Lock()
	ph := sess.phase
	nInterview := len(sess.snap.Interview)
	nTurns := len(sess.record.Turns)
	sess.mu.Unlock()
	if ph != phInterview {
		t.Fatalf("回滚后 phase 应为 phInterview，got %v", ph)
	}
	if nInterview != 0 || nTurns != 0 {
		t.Fatalf("回滚后 snap.Interview/record.Turns 应被截断，got Interview=%d Turns=%d", nInterview, nTurns)
	}

	// 第二次 PatientSay：triage 成功 → 最终 DONE
	st, err := s.PatientSay(context.Background(), id, "头疼")
	if err != nil {
		t.Fatalf("第二次 PatientSay 应成功，got %v", err)
	}
	if st.Kind != StepDone {
		t.Fatalf("第二次应 DONE，got %+v", st)
	}
}

var _ = json.Marshal
