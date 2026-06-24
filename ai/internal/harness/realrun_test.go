package harness

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"medagent/ai"
	"medagent/ai/openaicompat"
)

// providerKeyEnv 把 provider 映射到其标准 key 环境变量与默认模型（与 cmd/smoke 一致）。
var providerKeyEnv = map[string]struct{ keyVar, defModel string }{
	"openai":   {"OPENAI_API_KEY", "gpt-4o-mini"},
	"deepseek": {"DEEPSEEK_API_KEY", "deepseek-chat"},
	"qwen":     {"DASHSCOPE_API_KEY", "qwen-plus"},
}

const patientReplySchema = `{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"]}`

const feverCaseSheet = `你在扮演一名前来就诊的成年患者，正在和医生对话。你的真实病情（不要一次性全盘托出，只按医生的提问简洁如实回答）：
- 发热约 1 天，最高 39.4℃，自觉明显发热不适、乏力、畏寒，伴轻度头痛和全身酸痛。
- 伴轻微咽痛和偶有干咳，无明显鼻塞流涕。
- 无呼吸困难、无胸痛、无皮疹、无腹泻呕吐。
- 既往体健，无慢性病，无药物过敏，未自行用药。
- 你比较难受，希望能尽快把烧退下去、缓解不适。
要求：每次只用一两句口语化中文回答医生当前的问题；若医生尚未开口（首轮），先用一句话主动说明来意（发烧很不舒服、想退烧）。只通过 reply 字段返回你要说的话。`

// TestRealFeverFlow 用真实 LLM 驱动 ai.DecisionLayer 跑一遍发烧问诊→收敛→处置全程，
// 验证 interview/triage/treatment 各 agent 能正确发起强制 tool 调用、并正确消费工具
// 返回的结构化反馈。需配置 env（见 spec），否则 t.Skip——故 go test ./... 仍离线确定。
//
// RunVisit 能成功本身即证明三个 agent 的产出都被反序列化进 typed intent 且通过
// Validate（否则 agent 返回 SchemaError、流程停住）。终态再校验语义合理性。
func TestRealFeverFlow(t *testing.T) {
	// 显式开关门控：未开则 skip，避免环境里残留的 OPENAI_API_KEY 等误触发，
	// 保证 go test ./... 始终离线确定。
	if os.Getenv("MEDAGENT_REAL_LLM") == "" {
		t.Skip("未设 MEDAGENT_REAL_LLM=1，跳过真实 LLM 验证")
	}
	provider := getenvOr("MEDAGENT_LLM_PROVIDER", "openai")
	pk, ok := providerKeyEnv[provider]
	if !ok {
		t.Fatalf("未知 MEDAGENT_LLM_PROVIDER %q（openai|deepseek|qwen）", provider)
	}
	key := os.Getenv(pk.keyVar)
	if key == "" {
		t.Fatalf("MEDAGENT_REAL_LLM 已开但未设 %s", pk.keyVar)
	}
	model := getenvOr("MEDAGENT_LLM_MODEL", pk.defModel)
	baseURL := os.Getenv("MEDAGENT_LLM_BASE_URL")

	client := newRealClient(provider, baseURL, key, model)
	layer := ai.NewDecisionLayer(client)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	deps := Deps{
		Layer: layer,
		Caps:  map[string]bool{},
		Patient: func(lastDoctorReply string) string {
			if strings.TrimSpace(lastDoctorReply) != "" {
				t.Logf("🩺 医生：%s", lastDoctorReply)
			}
			reply := simulatePatient(ctx, t, client, lastDoctorReply)
			t.Logf("👤 患者：%s", reply)
			return reply
		},
		TestResults: func(items []string) []ai.TestResult {
			t.Logf("🧪 检验回填：%v", items)
			out := make([]ai.TestResult, 0, len(items))
			for _, it := range items {
				out = append(out, ai.TestResult{Item: it, Value: "WBC 正常偏低，淋巴细胞比例升高，提示病毒性感染"})
			}
			return out
		},
	}

	out, err := RunVisit(ctx, deps)
	if err != nil {
		t.Fatalf("RunVisit 失败：%v", err)
	}

	t.Logf("⚖️ 轨迹：%s", strings.Join(out.Trace, " → "))
	if out.Diagnosis != nil {
		t.Logf("🏷️ 诊断：%s（依据：%s，置信 %.2f）", out.Diagnosis.Name, out.Diagnosis.Basis, out.Diagnosis.Confidence)
	}
	for _, m := range out.Medications {
		t.Logf("💊 处方：%s %s %s", m.Name, m.Dosage, m.Schedule)
	}
	t.Logf("📋 医嘱：%s", out.Advice)
	t.Logf("✅ 终态：Final=%s Plan=%s", out.Final, out.Plan)

	// 验证目标是「集成正确」：interview/triage/treatment 都成功发起 tool 调用、
	// 消费了结构化反馈，流程走到合法终态并给出诊断与医嘱。具体临床选择
	// （开药 vs 仅医嘱）由真实模型自主决定，不强求——否则会把验证绑死在一个
	// 非确定的临床判断上。
	if out.Final != "ADVICE" && out.Final != "REFERRAL" {
		t.Fatalf("非法终态 Final=%q", out.Final)
	}
	if out.Diagnosis == nil || out.Diagnosis.Name == "" {
		t.Fatalf("triage 未给出有效诊断：%+v", out.Diagnosis)
	}
	if strings.TrimSpace(out.Advice) == "" {
		t.Fatalf("treatment 未给出医嘱")
	}
	if out.Plan == ai.PlanMedication && len(out.Medications) == 0 {
		t.Fatalf("Plan=MEDICATION 但无处方药")
	}
	if len(out.Medications) == 0 {
		t.Logf("ℹ️ 模型本次选择 %s（未开药）——集成验证不依赖具体临床选择", out.Plan)
	}
}

// newRealClient 按 provider/baseURL 构造真实 openaicompat 客户端。
// baseURL 非空时一律用 New（支持第三方中转），否则 deepseek/qwen 用预设。
func newRealClient(provider, baseURL, key, model string) ai.LLMClient {
	if baseURL != "" {
		return openaicompat.New(openaicompat.Config{BaseURL: baseURL, APIKey: key, Model: model})
	}
	switch provider {
	case "deepseek":
		return openaicompat.NewDeepSeek(key, model)
	case "qwen":
		return openaicompat.NewQwen(key, model)
	default: // openai
		return openaicompat.New(openaicompat.Config{BaseURL: "https://api.openai.com/v1", APIKey: key, Model: model})
	}
}

// simulatePatient 用同一 client、按隐藏病历卡扮演患者，对医生上一句作答。
func simulatePatient(ctx context.Context, t *testing.T, client ai.LLMClient, lastDoctorReply string) string {
	userMsg := lastDoctorReply
	if strings.TrimSpace(userMsg) == "" {
		userMsg = "（请开始问诊）"
	}
	res, err := client.Complete(ctx, ai.CompletionRequest{
		System:   feverCaseSheet,
		Messages: []ai.Message{{Role: "user", Content: userMsg}},
		Schema:   ai.OutputSchema{Name: "patient_reply", JSON: json.RawMessage(patientReplySchema)},
	})
	if err != nil {
		t.Fatalf("模拟患者调用失败：%v", err)
	}
	var pr struct {
		Reply string `json:"reply"`
	}
	if err := json.Unmarshal(res.Structured, &pr); err != nil {
		t.Fatalf("解析患者 reply 失败：%v（raw=%s）", err, res.Raw)
	}
	return pr.Reply
}

func getenvOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
