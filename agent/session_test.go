package agent

import (
	"context"
	"errors"
	"testing"

	"medagent/internal/ai"
)

func TestFlowConfirmMedicationPurchaseDone(t *testing.T) {
	// 开药 → 查规格 → 购药 → 终决：每一步一个工具调用。
	s := svcChat(chatScript(
		queryDrugT("对乙酰氨基酚"),
		purchaseT(map[string]any{"name": "对乙酰氨基酚", "quantity": 2}),
		finishAdviceT("急性咽炎", "已购药，按医嘱服用"),
	))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	st, err := s.PatientSay(context.Background(), id, "嗓子疼")
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepDrugQuery || len(st.DrugNames) != 1 || st.DrugNames[0] != "对乙酰氨基酚" {
		t.Fatalf("应到 DRUG_QUERY：%+v", st)
	}

	st, err = s.SupplyDrugInfo(context.Background(), id, []DrugInfo{{Name: "对乙酰氨基酚", Spec: "每盒12片×0.5g"}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepPurchase || len(st.Orders) != 1 || st.Orders[0].Quantity != 2 {
		t.Fatalf("应到 PURCHASE 且盒数=2：%+v", st)
	}

	st, err = s.SupplyPurchaseResult(context.Background(), id, []DrugPurchase{{Name: "对乙酰氨基酚", Bought: true, Quantity: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepDone || st.Result == nil || st.Result.Final != "ADVICE" {
		t.Fatalf("应到 DONE：%+v", st)
	}
}

func TestSupplyDrugInfoWrongStep(t *testing.T) {
	s := svcChat(chatScript(askT("发烧几天？"))) // 停在问诊
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	if _, err := s.PatientSay(context.Background(), id, "发烧"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SupplyDrugInfo(context.Background(), id, nil); err != ErrWrongStep {
		t.Fatalf("非 DRUG_QUERY 态调 SupplyDrugInfo 应 ErrWrongStep，得 %v", err)
	}
}

func TestFlowAskThenTest(t *testing.T) {
	s := svcChat(chatScript(
		askT("发烧几天了？"),
		orderTestT(),
		finishAdviceT("病毒感染", "多休息多饮水"),
	))
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
	s := svcChat(chatScript(finishAdviceT("X", "休息")))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	if _, err := s.SupplyTestResults(context.Background(), id, nil); err != ErrWrongStep {
		t.Fatalf("无 pending 时应 ErrWrongStep，got %v", err)
	}
	st, _ := s.PatientSay(context.Background(), id, "hi")
	if st.Kind != StepDone {
		t.Fatalf("应 DONE：%+v", st)
	}
	if _, err := s.PatientSay(context.Background(), id, "again"); err != ErrSessionClosed {
		t.Fatalf("done 后应 ErrSessionClosed，got %v", err)
	}
}

func TestReferralTerminal(t *testing.T) {
	s := svcChat(chatScript(referT("本院无法开展，需上级医院", "尽快转诊")))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	st, _ := s.PatientSay(context.Background(), id, "需要手术")
	if st.Kind != StepDone || st.Result == nil || st.Result.Final != "REFERRAL" {
		t.Fatalf("应转诊 DONE：%+v", st)
	}
}

func TestErrorMapping(t *testing.T) {
	s := svcChat(chatFn(func(int) (ai.AssistantTurn, error) {
		return ai.AssistantTurn{}, ai.ErrLLM
	}))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	if _, err := s.PatientSay(context.Background(), id, "hi"); !errors.Is(err, ErrUpstream) {
		t.Fatalf("应 ErrUpstream，got %v", err)
	}
}

// TestTransientErrorRecovery 验证瞬时错误后会话不卡死：drive 失败时 PatientSay 回滚
// transcript/record/pending，客户端可用同一接口重试直到成功。
func TestTransientErrorRecovery(t *testing.T) {
	s := svcChat(chatFn(func(n int) (ai.AssistantTurn, error) {
		if n == 1 {
			return ai.AssistantTurn{}, ai.ErrLLM // 第一次瞬时错误
		}
		return finishAdviceT("偏头痛", "多休息"), nil
	}))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	if _, err := s.PatientSay(context.Background(), id, "头疼"); !errors.Is(err, ErrUpstream) {
		t.Fatalf("第一次应 ErrUpstream，got %v", err)
	}

	// 事务已回滚：会话仍 active，transcript/record 截断，pending 复位。
	sess, _ := s.get(id)
	sess.mu.Lock()
	active := sess.status == stActive
	nTrans, nTurns := len(sess.transcript), len(sess.record.Turns)
	pendingNil := sess.pending == nil
	sess.mu.Unlock()
	if !active || !pendingNil {
		t.Fatalf("回滚后应 active 且 pending=nil（active=%v pending=nil? %v）", active, pendingNil)
	}
	if nTrans != 0 || nTurns != 0 {
		t.Fatalf("回滚后 transcript/record.Turns 应截断，got transcript=%d turns=%d", nTrans, nTurns)
	}

	st, err := s.PatientSay(context.Background(), id, "头疼")
	if err != nil {
		t.Fatalf("第二次 PatientSay 应成功，got %v", err)
	}
	if st.Kind != StepDone {
		t.Fatalf("第二次应 DONE，got %+v", st)
	}
}

func TestPurchaseZeroQuantityWarns(t *testing.T) {
	s := svcChat(chatScript(
		queryDrugT("某药"),
		purchaseT(map[string]any{"name": "某药", "quantity": 0}), // 异常盒数
	))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	st, _ := s.PatientSay(context.Background(), id, "x")
	if st.Kind != StepDrugQuery {
		t.Fatalf("应 DRUG_QUERY：%+v", st)
	}
	st, _ = s.SupplyDrugInfo(context.Background(), id, []DrugInfo{{Name: "某药", Spec: "每盒10片"}})
	if st.Kind != StepPurchase || st.Orders[0].Quantity != 1 {
		t.Fatalf("应 PURCHASE 且盒数兜底为 1：%+v", st)
	}
	rec, _ := s.Export(id)
	found := false
	for _, tn := range rec.Turns {
		if tn.Kind == "warn" {
			found = true
		}
	}
	if !found {
		t.Fatalf("盒数0 应产生 warn turn：%+v", rec.Turns)
	}
}
