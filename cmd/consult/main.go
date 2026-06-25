// Command consult 把真实 openaicompat 客户端 + ai.DecisionLayer + consultlog 接起来，
// 跑一次完整诊疗（咽痛发热、细菌/病毒难辨场景，LLM 模拟患者），逐步打印医患对话与处置，
// 并把这次诊疗的全部 LLM 调用审计流写到 {log-dir}/{visitID}.jsonl。API key 从环境变量读取。
//
// 预设病情刻意选了临床上需鉴别（细菌/病毒）且可能需开药的场景，给决策层 TEST（检验）
// 与 MEDICATION（开药）分支的判断机会；AI 仍自主决定。
//
// 用法：
//
//	go run ./cmd/consult -provider deepseek
//	go run ./cmd/consult -provider openai -base-url https://中转站/v1 -model gpt-5.4-mini
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"medagent/internal/ai"
	"medagent/internal/consultlog"
	"medagent/internal/harness"
	"medagent/internal/openaicompat"
)

var providerKeyEnv = map[string]struct{ keyVar, defModel string }{
	"openai":   {"OPENAI_API_KEY", "gpt-4o-mini"},
	"deepseek": {"DEEPSEEK_API_KEY", "deepseek-chat"},
	"qwen":     {"DASHSCOPE_API_KEY", "qwen-plus"},
}

const patientReplySchema = `{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"]}`

const patientCaseSheet = `你在扮演一名前来就诊的成年患者，正在和医生对话。你的真实病情（不要一次性全盘托出，只按医生的提问简洁如实回答；遇到自己不确定的就如实说"不太清楚/没注意"）：
- 发热约 2 天，最高 38.7℃，有点乏力。
- 嗓子中等程度疼，吞咽时有点加重，但还能正常吃喝。
- 你没仔细看过扁桃体，不确定上面有没有脓点或白苔（"没太看清"）。
- 有一点点轻微咳嗽，偶尔觉得鼻子有点塞，但都不严重。
- 颈部有没有淋巴结肿大你自己摸不太准，不确定。
- 既往体健，无慢性病，无药物过敏，未自行用药。
- 你想知道这到底是不是需要吃"消炎药/抗生素"，还是扛一扛就行。
要求：每次只用一两句口语化中文回答医生当前的问题；不确定的就说不确定；若医生尚未开口（首轮），先用一句话主动说明来意（嗓子疼加发烧，想看看要不要吃药）。只通过 reply 字段返回你要说的话。`

func main() {
	provider := flag.String("provider", "openai", "provider: openai | deepseek | qwen")
	model := flag.String("model", "", "模型名（留空用 provider 默认）")
	baseURL := flag.String("base-url", "", "覆盖 base URL（第三方中转/自建网关）")
	logDir := flag.String("log-dir", "./logs", "诊疗日志目录")
	timeout := flag.Duration("timeout", 4*time.Minute, "整次诊疗超时")
	flag.Parse()

	pk, ok := providerKeyEnv[*provider]
	if !ok {
		fmt.Fprintf(os.Stderr, "未知 provider %q（openai|deepseek|qwen）\n", *provider)
		os.Exit(2)
	}
	key := os.Getenv(pk.keyVar)
	if key == "" {
		fmt.Fprintf(os.Stderr, "缺少环境变量 %s\n", pk.keyVar)
		os.Exit(1)
	}
	m := *model
	if m == "" {
		m = pk.defModel
	}
	if err := os.MkdirAll(*logDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建日志目录失败：%v\n", err)
		os.Exit(1)
	}

	logged := consultlog.Wrap(newClient(*provider, *baseURL, key, m), consultlog.NewFileLogger(*logDir))
	visitID := consultlog.NewVisitID()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	ctx = consultlog.WithVisitID(ctx, visitID)

	fmt.Printf("=== 诊疗开始  visitID=%s  provider=%s model=%s ===\n", visitID, *provider, m)

	deps := harness.Deps{
		Layer: ai.NewDecisionLayer(logged),
		Caps:  map[string]bool{},
		Patient: func(lastDoctorReply string) string {
			if strings.TrimSpace(lastDoctorReply) != "" {
				fmt.Printf("🩺 医生：%s\n", lastDoctorReply)
			}
			reply := simulatePatient(ctx, logged, lastDoctorReply)
			fmt.Printf("👤 患者：%s\n", reply)
			return reply
		},
		TestResults: func(items []string) []ai.TestResult {
			fmt.Printf("🧪 检验回填：%v\n", items)
			out := make([]ai.TestResult, 0, len(items))
			for _, it := range items {
				out = append(out, ai.TestResult{Item: it, Value: bacterialTestValue(it)})
			}
			return out
		},
	}

	out, err := harness.RunVisit(ctx, deps)
	if err != nil {
		if errors.Is(err, ai.ErrLLM) {
			fmt.Fprintf(os.Stderr, "诊疗失败 (ErrLLM): %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "诊疗失败: %v\n", err)
		}
		os.Exit(1)
	}

	fmt.Println("--- 诊疗结果 ---")
	if out.Diagnosis != nil {
		fmt.Printf("🏷️ 诊断：%s（依据：%s，置信 %.2f）\n", out.Diagnosis.Name, out.Diagnosis.Basis, out.Diagnosis.Confidence)
	}
	for _, md := range out.Medications {
		fmt.Printf("💊 处方：%s %s %s\n", md.Name, md.Dosage, md.Schedule)
	}
	fmt.Printf("📋 医嘱：%s\n", out.Advice)
	fmt.Printf("⚖️ 轨迹：%s\n", strings.Join(out.Trace, " → "))
	fmt.Printf("✅ 终态：Final=%s Plan=%s\n", out.Final, out.Plan)
	fmt.Printf("📁 日志：%s\n", filepath.Join(*logDir, visitID+".jsonl"))
}

// newClient 按 provider/baseURL 构造真实 openaicompat 客户端。
func newClient(provider, baseURL, key, model string) ai.LLMClient {
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

// bacterialTestValue 按检验项返回支持「细菌性」的桩结果，让决策层在 TEST 后有据可
// 确诊细菌感染并据此开药。仅为本演示场景的桩数据。
func bacterialTestValue(item string) string {
	switch {
	case strings.Contains(item, "血常规") || strings.Contains(item, "血象"):
		return "WBC 13.5×10⁹/L 升高，中性粒细胞 82% 升高，提示细菌感染"
	case strings.Contains(item, "链球菌") || strings.Contains(item, "抗原") || strings.Contains(item, "拭子"):
		return "A 组 β 溶血性链球菌快速抗原阳性"
	case strings.Contains(item, "CRP") || strings.Contains(item, "C反应蛋白") || strings.Contains(item, "C 反应蛋白"):
		return "CRP 58 mg/L 明显升高"
	default:
		return "结果支持细菌性感染"
	}
}

// simulatePatient 用同一 client、按隐藏病历卡扮演患者，对医生上一句作答。
func simulatePatient(ctx context.Context, client ai.LLMClient, lastDoctorReply string) string {
	userMsg := lastDoctorReply
	if strings.TrimSpace(userMsg) == "" {
		userMsg = "（请开始问诊）"
	}
	res, err := client.Complete(ctx, ai.CompletionRequest{
		System:   patientCaseSheet,
		Messages: []ai.Message{{Role: "user", Content: userMsg}},
		Schema:   ai.OutputSchema{Name: "patient_reply", JSON: json.RawMessage(patientReplySchema)},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "模拟患者调用失败：%v\n", err)
		os.Exit(1)
	}
	var pr struct {
		Reply string `json:"reply"`
	}
	if err := json.Unmarshal(res.Structured, &pr); err != nil {
		fmt.Fprintf(os.Stderr, "解析患者回复失败：%v（raw=%s）\n", err, res.Raw)
		os.Exit(1)
	}
	return pr.Reply
}
