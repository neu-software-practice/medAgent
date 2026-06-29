package agent

import (
	"context"
	"testing"
)

// 急性咽炎主干：多轮问诊 → 检验 → 终决（仅医嘱）。
func TestWalkthroughPharyngitis(t *testing.T) {
	s := svcChat(chatScript(
		askT("发烧几天了？有无咳嗽？"),
		askT("扁桃体有没有脓点？"),
		orderTestT(),
		finishAdviceT("急性咽炎", "多休息多饮水"),
	))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	st, _ := s.PatientSay(context.Background(), id, "嗓子痛发烧")
	if st.Kind != StepAsk {
		t.Fatalf("先 ASK：%+v", st)
	}
	st, _ = s.PatientSay(context.Background(), id, "两天，没怎么咳嗽")
	if st.Kind != StepAsk {
		t.Fatalf("再 ASK：%+v", st)
	}
	st, _ = s.PatientSay(context.Background(), id, "好像有点白点")
	if st.Kind != StepNeedTests {
		t.Fatalf("应 NEED_TESTS：%+v", st)
	}
	st, _ = s.SupplyTestResults(context.Background(), id, []TestResult{{Item: "血常规", Value: "中性粒升高"}})
	if st.Kind != StepDone || st.Result.Diagnosis.Name != "急性咽炎" {
		t.Fatalf("应 DONE 急性咽炎：%+v", st)
	}
	if st.Result.Plan != "ADVICE_ONLY" {
		t.Fatalf("应 ADVICE_ONLY，得 %s", st.Result.Plan)
	}
}

// 购药主干含查规格轮：问诊 → 查规格 → 购药 → 终决。
func TestWalkthroughMedicationViaDrugQuery(t *testing.T) {
	s := svcChat(chatScript(
		askT("化脓多久了？"),
		queryDrugT("阿莫西林"),
		purchaseT(map[string]any{"name": "阿莫西林", "quantity": 1}),
		finishAdviceT("细菌性咽炎", "按医嘱服药"),
	))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	st, _ := s.PatientSay(context.Background(), id, "嗓子化脓")
	if st.Kind != StepAsk {
		t.Fatalf("应先 ASK：%+v", st)
	}
	st, _ = s.PatientSay(context.Background(), id, "三天了")
	if st.Kind != StepDrugQuery {
		t.Fatalf("应 DRUG_QUERY：%+v", st)
	}
	st, _ = s.SupplyDrugInfo(context.Background(), id, []DrugInfo{{Name: "阿莫西林", Spec: "每盒20粒×0.25g"}})
	if st.Kind != StepPurchase || st.Orders[0].Quantity != 1 {
		t.Fatalf("应 PURCHASE 盒数1：%+v", st)
	}
	st, _ = s.SupplyPurchaseResult(context.Background(), id, []DrugPurchase{{Name: "阿莫西林", Bought: true, Quantity: 1}})
	if st.Kind != StepDone {
		t.Fatalf("应 DONE：%+v", st)
	}
}

// 直接转诊（院内无法处理 → REFERRAL）。
func TestWalkthroughReferral(t *testing.T) {
	s := svcChat(chatScript(referT("本院无法开展，需上级医院", "尽快转上级医院")))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	st, _ := s.PatientSay(context.Background(), id, "需要手术的情况")
	if st.Kind != StepDone || st.Result.Final != "REFERRAL" {
		t.Fatalf("应转诊 DONE：%+v", st)
	}
}
