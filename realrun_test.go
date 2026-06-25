package medagent

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"medagent/internal/ai"
	"medagent/internal/openaicompat"
)

func TestRealConsultFlow(t *testing.T) {
	if os.Getenv("MEDAGENT_REAL_LLM") == "" {
		t.Skip("未设 MEDAGENT_REAL_LLM=1，跳过真实验证")
	}
	provider := getenv("MEDAGENT_LLM_PROVIDER", "openai")
	keyEnv := map[string]string{"openai": "OPENAI_API_KEY", "deepseek": "DEEPSEEK_API_KEY", "qwen": "DASHSCOPE_API_KEY"}[provider]
	key := os.Getenv(keyEnv)
	if key == "" {
		t.Fatalf("缺 %s", keyEnv)
	}
	model := getenv("MEDAGENT_LLM_MODEL", "gpt-4o-mini")
	baseURL := os.Getenv("MEDAGENT_LLM_BASE_URL")

	svc, err := New(Config{Provider: provider, APIKey: key, Model: model, BaseURL: baseURL, LogDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	patient := patientClientReal(provider, baseURL, key, model)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	id, _ := svc.Start(map[string]any{"年龄": 30, "性别": "男"}, true, nil)
	msg := simReal(ctx, t, patient, "")
	for i := 0; i < 30; i++ {
		st, err := svc.PatientSay(ctx, id, msg)
		if err != nil {
			t.Fatalf("PatientSay: %v", err)
		}
		switch st.Kind {
		case StepAsk:
			msg = simReal(ctx, t, patient, st.DoctorSay)
		case StepNeedTests:
			st, _ = svc.SupplyTestResults(ctx, id, []TestResult{{Item: "血常规", Value: "WBC 13.5↑、中性粒↑"}})
			if !consumeTerminal(t, svc, id, patient, ctx, st, &msg) {
				continue
			}
			return
		case StepPurchase:
			var res []DrugPurchase
			for _, o := range st.Orders {
				res = append(res, DrugPurchase{Name: o.Name, Bought: true, Quantity: o.Quantity})
			}
			st, _ = svc.SupplyPurchaseResult(ctx, id, res)
			assertDone(t, st)
			return
		case StepEmergency:
			t.Logf("急症：%s", st.Emergency)
			return
		case StepDone:
			assertDone(t, st)
			return
		}
	}
	t.Fatal("未在 30 轮内收敛")
}

func consumeTerminal(t *testing.T, svc *Service, id string, patient ai.LLMClient, ctx context.Context, st Step, msg *string) bool {
	switch st.Kind {
	case StepPurchase:
		var res []DrugPurchase
		for _, o := range st.Orders {
			res = append(res, DrugPurchase{Name: o.Name, Bought: true, Quantity: o.Quantity})
		}
		st2, _ := svc.SupplyPurchaseResult(ctx, id, res)
		assertDone(t, st2)
		return true
	case StepDone:
		assertDone(t, st)
		return true
	case StepAsk:
		*msg = simReal(ctx, t, patient, st.DoctorSay)
		return false
	}
	return true
}

func assertDone(t *testing.T, st Step) {
	t.Helper()
	if st.Kind != StepDone || st.Result == nil || st.Result.Diagnosis == nil {
		t.Fatalf("应 DONE 且有诊断：%+v", st)
	}
	t.Logf("诊断=%s 处置=%s 医嘱=%s", st.Result.Diagnosis.Name, st.Result.Plan, st.Result.Advice)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func patientClientReal(provider, baseURL, key, model string) ai.LLMClient {
	if baseURL != "" {
		return openaicompat.New(openaicompat.Config{BaseURL: baseURL, APIKey: key, Model: model})
	}
	switch provider {
	case "deepseek":
		return openaicompat.NewDeepSeek(key, model)
	case "qwen":
		return openaicompat.NewQwen(key, model)
	default:
		return openaicompat.New(openaicompat.Config{BaseURL: "https://api.openai.com/v1", APIKey: key, Model: model})
	}
}

func simReal(ctx context.Context, t *testing.T, c ai.LLMClient, doctor string) string {
	u := doctor
	if u == "" {
		u = "（请开始问诊）"
	}
	res, err := c.Complete(ctx, ai.CompletionRequest{
		System:   "你扮演成年发热咽痛患者，按医生提问简洁如实回答，只用 reply 字段。最高39.4℃、扁桃体脓点、颈淋巴结肿大、无咳嗽、无过敏。",
		Messages: []ai.Message{{Role: "user", Content: u}},
		Schema:   ai.OutputSchema{Name: "patient_reply", JSON: json.RawMessage(`{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"]}`)},
	})
	if err != nil {
		t.Fatalf("模拟患者: %v", err)
	}
	var pr struct {
		Reply string `json:"reply"`
	}
	_ = json.Unmarshal(res.Structured, &pr)
	return pr.Reply
}
