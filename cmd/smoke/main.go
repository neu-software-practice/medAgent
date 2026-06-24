// Command smoke 对真实 OpenAI 兼容端点发一次 tool-use 调用，验证 openaicompat
// adapter 的端到端结构化输出。API key 从环境变量读取（调用方职责，不进 adapter）。
//
// 用法：
//
//	go run ./cmd/smoke -provider openai
//	go run ./cmd/smoke -provider deepseek -model deepseek-chat -prompt "..."
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"medagent/ai"
	"medagent/ai/openaicompat"
)

// providerSpec 描述一个 provider 的接线参数。
type providerSpec struct {
	envVar       string // 读 key 的环境变量名
	baseURL      string // 仅 openai 直连用；deepseek/qwen 走预设构造器
	defaultModel string
}

var providers = map[string]providerSpec{
	"openai":   {envVar: "OPENAI_API_KEY", baseURL: "https://api.openai.com/v1", defaultModel: "gpt-4o-mini"},
	"deepseek": {envVar: "DEEPSEEK_API_KEY", defaultModel: "deepseek-chat"},
	"qwen":     {envVar: "DASHSCOPE_API_KEY", defaultModel: "qwen-plus"},
}

// answerSchema 是中性极简的结构化输出 schema，只为验证 tool-use 路径。
const answerSchema = `{"type":"object","properties":{"answer":{"type":"string"},"confidence":{"type":"number"}},"required":["answer"]}`

func main() {
	provider := flag.String("provider", "openai", "provider: openai | deepseek | qwen")
	model := flag.String("model", "", "模型名（留空用 provider 默认）")
	prompt := flag.String("prompt", "用一句话解释什么是布洛芬。", "发给模型的问题")
	flag.Parse()

	spec, ok := providers[*provider]
	if !ok {
		fmt.Fprintf(os.Stderr, "未知 provider %q（可选 openai|deepseek|qwen）\n", *provider)
		os.Exit(2)
	}

	key := os.Getenv(spec.envVar)
	if key == "" {
		fmt.Fprintf(os.Stderr, "缺少环境变量 %s\n", spec.envVar)
		os.Exit(1)
	}

	m := *model
	if m == "" {
		m = spec.defaultModel
	}

	var client ai.LLMClient
	switch *provider {
	case "openai":
		client = openaicompat.New(openaicompat.Config{BaseURL: spec.baseURL, APIKey: key, Model: m})
	case "deepseek":
		client = openaicompat.NewDeepSeek(key, m)
	case "qwen":
		client = openaicompat.NewQwen(key, m)
	}

	req := ai.CompletionRequest{
		System:   "你是一个简洁的助手，请用 answer 工具返回结果。",
		Messages: []ai.Message{{Role: "user", Content: *prompt}},
		Schema:   ai.OutputSchema{Name: "answer", JSON: json.RawMessage(answerSchema)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := client.Complete(ctx, req)
	if err != nil {
		if errors.Is(err, ai.ErrLLM) {
			fmt.Fprintf(os.Stderr, "调用失败 (ErrLLM): %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "调用失败: %v\n", err)
		}
		os.Exit(1)
	}

	fmt.Printf("provider=%s model=%s\n", *provider, m)
	fmt.Printf("Structured: %s\n", res.Structured)
	fmt.Printf("Raw: %s\n", res.Raw)
}
