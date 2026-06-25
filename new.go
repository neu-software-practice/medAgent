package medagent

import (
	"fmt"
	"os"

	"medagent/internal/ai"
	"medagent/internal/consultlog"
	"medagent/internal/openaicompat"
)

func New(cfg Config) (*Service, error) {
	if cfg.APIKey == "" || cfg.Model == "" {
		return nil, fmt.Errorf("medagent: APIKey 与 Model 必填")
	}
	var llm ai.LLMClient
	switch {
	case cfg.BaseURL != "":
		llm = openaicompat.New(openaicompat.Config{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Model: cfg.Model, Timeout: cfg.Timeout})
	case cfg.Provider == "deepseek":
		llm = openaicompat.NewDeepSeek(cfg.APIKey, cfg.Model)
	case cfg.Provider == "qwen":
		llm = openaicompat.NewQwen(cfg.APIKey, cfg.Model)
	case cfg.Provider == "openai":
		llm = openaicompat.New(openaicompat.Config{BaseURL: "https://api.openai.com/v1", APIKey: cfg.APIKey, Model: cfg.Model, Timeout: cfg.Timeout})
	default:
		return nil, fmt.Errorf("medagent: 未知 provider %q（deepseek|qwen|openai 或设 BaseURL）", cfg.Provider)
	}
	if cfg.LogDir != "" {
		if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
			return nil, fmt.Errorf("medagent: 创建日志目录 %s 失败: %w", cfg.LogDir, err)
		}
		llm = consultlog.Wrap(llm, consultlog.NewFileLogger(cfg.LogDir))
	}
	return newService(cfg, ai.NewDecisionLayer(llm), ai.NewGuardian(llm)), nil
}
