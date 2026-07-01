package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"medagent/internal/ai"
)

// 全局 step 预算撞顶：模型永远只追问，达 maxSteps 后强制关闭会话。
func TestStepBudgetCloses(t *testing.T) {
	s := svcChat(chatFn(func(int) (ai.AssistantTurn, error) { return askT("再说说？"), nil }))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	var err error
	for i := 0; i < maxSteps+1; i++ {
		var st Step
		st, err = s.PatientSay(context.Background(), id, "嗯")
		if err != nil {
			break
		}
		if st.Kind != StepAsk {
			t.Fatalf("第 %d 轮应 ASK：%+v", i, st)
		}
	}
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("撞顶应 ErrUpstream，got %v", err)
	}
	if _, err := s.PatientSay(context.Background(), id, "x"); err != ErrSessionClosed {
		t.Fatalf("撞顶后应 closed，got %v", err)
	}
}

// 购药幂等：已购药后模型再次请求购药 → 被拦截并要求收尾，最终正常 DONE，且记 warn。
func TestPurchaseIdempotencyRecovers(t *testing.T) {
	s := svcChat(chatScript(
		queryDrugT("阿莫西林"),
		purchaseT(map[string]any{"name": "阿莫西林", "quantity": 1}),
		purchaseT(map[string]any{"name": "阿莫西林", "quantity": 1}), // 冗余购药，应被拦截
		finishAdviceT("细菌性咽炎", "按医嘱服药"),
	))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	s.PatientSay(context.Background(), id, "嗓子化脓")
	s.SupplyDrugInfo(context.Background(), id, []DrugInfo{{Name: "阿莫西林", Spec: "每盒20粒"}})
	st, err := s.SupplyPurchaseResult(context.Background(), id, []DrugPurchase{{Name: "阿莫西林", Bought: true, Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepDone {
		t.Fatalf("冗余购药应被拦截并最终 DONE：%+v", st)
	}
	rec, _ := s.Export(id)
	warned := false
	for _, tn := range rec.Turns {
		if tn.Kind == "warn" && strings.Contains(tn.Text, "再次请求购药") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("应记录拦截 warn：%+v", rec.Turns)
	}
}

// 检验幂等：已开检验后模型再次请求 order_test → 被拦截并要求继续，最终正常结束。
func TestTestOrderIdempotencyRecovers(t *testing.T) {
	s := svcChat(chatScript(
		orderTestT(),
		orderTestT(), // 冗余开检验，应被拦截
		finishAdviceT("上呼吸道感染", "多喝水休息"),
	))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	st, err := s.PatientSay(context.Background(), id, "发烧")
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepNeedTests {
		t.Fatalf("第一步应为 NEED_TESTS: %+v", st)
	}

	st, err = s.SupplyTestResults(context.Background(), id, []TestResult{{Item: "血常规-白细胞", Value: "11.2"}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepDone {
		t.Fatalf("冗余开检验应被拦截并最终 DONE: %+v", st)
	}

	rec, _ := s.Export(id)
	warned := false
	for _, tn := range rec.Turns {
		if tn.Kind == "warn" && strings.Contains(tn.Text, "再次请求检验") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("应记录拦截 warn: %+v", rec.Turns)
	}
}

// 内部纠正预算：模型购药后一直重复请求购药 → 达 maxInternalSteps 后关闭会话。
func TestInternalCorrectionBudget(t *testing.T) {
	n := 0
	s := svcChat(&ai.FakeChat{OnChat: func(ai.ChatRequest) (ai.AssistantTurn, error) {
		n++
		switch n {
		case 1:
			return queryDrugT("药"), nil
		case 2:
			return purchaseT(map[string]any{"name": "药", "quantity": 1}), nil
		default:
			return purchaseT(map[string]any{"name": "药", "quantity": 1}), nil // 永远重复购药
		}
	}})
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	s.PatientSay(context.Background(), id, "x")
	s.SupplyDrugInfo(context.Background(), id, []DrugInfo{{Name: "药", Spec: "每盒10片"}})
	_, err := s.SupplyPurchaseResult(context.Background(), id, []DrugPurchase{{Name: "药", Bought: true, Quantity: 1}})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("内部纠正不收敛应 ErrUpstream，got %v", err)
	}
	if _, err := s.PatientSay(context.Background(), id, "x"); err != ErrSessionClosed {
		t.Fatalf("应已关闭，got %v", err)
	}
}

// 上下文压缩：占用超 60% 阈值时触发一次 LLM 摘要，较早原文压成摘要、最近若干轮保留。
func TestContextCompaction(t *testing.T) {
	chat := &ai.FakeChat{OnChat: func(req ai.ChatRequest) (ai.AssistantTurn, error) {
		if len(req.Tools) == 0 { // 压缩摘要调用（无工具）
			return ai.AssistantTurn{Text: "发热咽痛，已问诊数轮"}, nil
		}
		turn := askT("继续说说？")
		turn.PromptTokens = 700 // 高占用，超过 1000*0.6=600 阈值
		return turn, nil
	}}
	// 小窗口让阈值易触发。
	s := newService(Config{DisableGuardian: true, ContextTokens: 1000},
		ai.NewEngine(chat), ai.NewGuardian(noGuardian()))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	// 多轮问诊把 transcript 撑过 compactKeepRecent+1，并积累高 lastPromptTokens。
	for i := 0; i < 5; i++ {
		if _, err := s.PatientSay(context.Background(), id, "嗯"); err != nil {
			t.Fatalf("第 %d 轮：%v", i, err)
		}
	}

	rec, _ := s.Export(id)
	compacted := false
	for _, tn := range rec.Turns {
		if tn.Kind == "compact" {
			compacted = true
		}
	}
	if !compacted {
		t.Fatalf("应触发一次上下文压缩，纪要：%+v", rec.Turns)
	}

	sess, _ := s.get(id)
	sess.mu.Lock()
	first := sess.transcript[0]
	sess.mu.Unlock()
	if first.Role != "user" || !strings.Contains(first.Content, "既往对话摘要") {
		t.Fatalf("压缩后首条应为摘要：%+v", first)
	}
}
