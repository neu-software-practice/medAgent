// Command consult 用模拟患者驱动 medagent facade 跑一次完整诊疗，日志落 ./logs。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"medagent"
	"medagent/internal/ai"
	"medagent/internal/openaicompat"
)

const patientCaseSheet = `你在扮演一名前来就诊的成年患者。你的真实病情（按医生提问简洁如实回答，不确定就说不确定）：
- 发热约2天，最高38.7℃，嗓子中等程度疼，吞咽有点加重；扁桃体没仔细看不确定有没有脓点；有点轻微咳嗽、偶尔鼻塞；既往体健无过敏。
- 你想知道要不要吃消炎药/抗生素。
只用一两句口语化中文回答；首轮先说来意。只通过 reply 字段返回。`

func main() {
	provider := os.Getenv("PROVIDER")
	if provider == "" {
		provider = "deepseek"
	}
	keyEnv := map[string]string{"deepseek": "DEEPSEEK_API_KEY", "qwen": "DASHSCOPE_API_KEY", "openai": "OPENAI_API_KEY"}[provider]
	key := os.Getenv(keyEnv)
	if key == "" {
		fmt.Fprintf(os.Stderr, "缺少 %s\n", keyEnv)
		os.Exit(1)
	}
	model := os.Getenv("MODEL")
	baseURL := os.Getenv("BASE_URL")

	svc, err := medagent.New(medagent.Config{Provider: provider, APIKey: key, Model: model, BaseURL: baseURL, LogDir: "./logs"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer svc.Close()

	// 模拟患者也用一个直连 client（不进会话日志）。
	patient := patientClient(provider, baseURL, key, model)
	ctx := context.Background()

	id, _ := svc.Start(map[string]any{"年龄": 30, "性别": "男"}, true, nil)
	fmt.Printf("=== 诊疗开始 session=%s ===\n", id)

	msg := simulate(ctx, patient, "")
	for {
		fmt.Printf("👤 患者：%s\n", msg)
		st, err := svc.PatientSay(ctx, id, msg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "PatientSay: %v\n", err)
			os.Exit(1)
		}
		done := handle(ctx, svc, id, patient, st, &msg)
		if done {
			break
		}
	}
	rec, _ := svc.Export(id)
	b, _ := json.MarshalIndent(rec, "", "  ")
	fmt.Printf("📁 会话纪要:\n%s\n", b)
}

// handle 处理一个 Step；返回是否终结。需要患者再说话时把下一句写回 *msg。
func handle(ctx context.Context, svc *medagent.Service, id string, patient ai.LLMClient, st medagent.Step, msg *string) bool {
	switch st.Kind {
	case medagent.StepAsk:
		fmt.Printf("🩺 医生：%s\n", st.DoctorSay)
		*msg = simulate(ctx, patient, st.DoctorSay)
		return false
	case medagent.StepNeedTests:
		fmt.Printf("🧪 检验：%v → 回填\n", st.TestItems)
		next, _ := svc.SupplyTestResults(ctx, id, []medagent.TestResult{{Item: "血常规", Value: "WBC 13.5↑、中性粒↑，提示细菌"}})
		return handle(ctx, svc, id, patient, next, msg)
	case medagent.StepPurchase:
		fmt.Printf("💊 购药请求：%v → 全部购买\n", st.Orders)
		var res []medagent.DrugPurchase
		for _, o := range st.Orders {
			res = append(res, medagent.DrugPurchase{Name: o.Name, Bought: true, Quantity: o.Quantity})
		}
		next, _ := svc.SupplyPurchaseResult(ctx, id, res)
		return handle(ctx, svc, id, patient, next, msg)
	case medagent.StepEmergency:
		fmt.Printf("🚨 急症转急诊：%s\n", st.Emergency)
		return true
	case medagent.StepDone:
		r := st.Result
		if r.Diagnosis != nil {
			fmt.Printf("🏷️ 诊断：%s（%.2f）\n", r.Diagnosis.Name, r.Diagnosis.Confidence)
		}
		for _, m := range r.Medications {
			fmt.Printf("💊 处方：%s %s ×%d\n", m.Name, m.Dosage, m.Quantity)
		}
		fmt.Printf("📋 医嘱：%s\n✅ 终态：%s/%s\n", r.Advice, r.Final, r.Plan)
		return true
	}
	return true
}

func patientClient(provider, baseURL, key, model string) ai.LLMClient {
	if baseURL != "" {
		return openaicompat.New(openaicompat.Config{BaseURL: baseURL, APIKey: key, Model: model})
	}
	switch provider {
	case "qwen":
		return openaicompat.NewQwen(key, model)
	case "openai":
		return openaicompat.New(openaicompat.Config{BaseURL: "https://api.openai.com/v1", APIKey: key, Model: model})
	default:
		return openaicompat.NewDeepSeek(key, model)
	}
}

func simulate(ctx context.Context, c ai.LLMClient, doctor string) string {
	u := doctor
	if u == "" {
		u = "（请开始问诊）"
	}
	res, err := c.Complete(ctx, ai.CompletionRequest{
		System:   patientCaseSheet,
		Messages: []ai.Message{{Role: "user", Content: u}},
		Schema:   ai.OutputSchema{Name: "patient_reply", JSON: json.RawMessage(`{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"]}`)},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "模拟患者: %v\n", err)
		os.Exit(1)
	}
	var pr struct {
		Reply string `json:"reply"`
	}
	_ = json.Unmarshal(res.Structured, &pr)
	return pr.Reply
}
