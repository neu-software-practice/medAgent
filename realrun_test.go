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
	if keyEnv == "" {
		t.Fatalf("未知 MEDAGENT_LLM_PROVIDER %q（openai|deepseek|qwen）", provider)
	}
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
	for i := 0; i < 40; i++ {
		st, err := svc.PatientSay(ctx, id, msg)
		if err != nil {
			t.Fatalf("PatientSay: %v", err)
		}
	consume:
		switch st.Kind {
		case StepAsk:
			msg = simReal(ctx, t, patient, st.DoctorSay)
		case StepNeedTests:
			st, err = svc.SupplyTestResults(ctx, id, []TestResult{{Item: "血常规", Value: "WBC 13.5↑、中性粒↑"}})
			if err != nil {
				t.Fatalf("SupplyTestResults: %v", err)
			}
			goto consume
		case StepDrugQuery:
			var infos []DrugInfo
			for _, name := range st.DrugNames {
				infos = append(infos, DrugInfo{Name: name, Spec: "每盒12片×0.3g"})
			}
			st, err = svc.SupplyDrugInfo(ctx, id, infos)
			if err != nil {
				t.Fatalf("SupplyDrugInfo: %v", err)
			}
			goto consume
		case StepPurchase:
			var res []DrugPurchase
			for _, o := range st.Orders {
				res = append(res, DrugPurchase{Name: o.Name, Bought: true, Quantity: o.Quantity})
			}
			st, err = svc.SupplyPurchaseResult(ctx, id, res)
			if err != nil {
				t.Fatalf("SupplyPurchaseResult: %v", err)
			}
			goto consume
		case StepEmergency:
			t.Logf("急症转急诊：%s", st.Emergency)
			return
		case StepDone:
			assertDone(t, st)
			return
		default:
			t.Fatalf("未预期 Step.Kind=%s", st.Kind)
		}
	}
	t.Fatal("未在 40 轮内收敛")
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
	if err := json.Unmarshal(res.Structured, &pr); err != nil {
		t.Fatalf("解析患者回复失败: %v（raw=%s）", err, res.Raw)
	}
	return pr.Reply
}
