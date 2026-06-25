package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"medagent/internal/ai"
)

const (
	deepSeekBaseURL = "https://api.deepseek.com/v1"
	qwenBaseURL     = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	defaultTimeout  = 60 * time.Second
)

// Config 是 adapter 的构造参数。BaseURL/APIKey/Model 必填；
// Timeout 为 0 时用默认 60s；HTTPClient 为 nil 时按 Timeout 内建（测试可注入 httptest）。
type Config struct {
	BaseURL    string
	APIKey     string
	Model      string
	Timeout    time.Duration
	HTTPClient *http.Client
}

// Client 是 OpenAI 兼容的 ai.LLMClient 实现。
type Client struct {
	cfg  Config
	http *http.Client
}

var _ ai.LLMClient = (*Client)(nil)

// New 按 Config 构造 Client。
func New(cfg Config) *Client {
	hc := cfg.HTTPClient
	if hc == nil {
		to := cfg.Timeout
		if to == 0 {
			to = defaultTimeout
		}
		hc = &http.Client{Timeout: to}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{cfg: cfg, http: hc}
}

// NewDeepSeek 预填 DeepSeek 的 base URL。
func NewDeepSeek(apiKey, model string) *Client {
	return New(Config{BaseURL: deepSeekBaseURL, APIKey: apiKey, Model: model})
}

// NewQwen 预填通义千问（DashScope 兼容模式）的 base URL。
func NewQwen(apiKey, model string) *Client {
	return New(Config{BaseURL: qwenBaseURL, APIKey: apiKey, Model: model})
}

// Complete 发起一次 chat-completions 调用，用强制 tool-use 拿结构化输出。
// 传输/非 2xx/协议异常全部包进 ai.ErrLLM；不做网络重试。
func (c *Client) Complete(ctx context.Context, req ai.CompletionRequest) (ai.CompletionResult, error) {
	if err := ctx.Err(); err != nil {
		return ai.CompletionResult{}, err
	}

	body, err := json.Marshal(buildRequest(req, c.cfg.Model))
	if err != nil {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 编码请求失败 (%v): %w", err, ai.ErrLLM)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 构造请求失败 (%v): %w", err, ai.ErrLLM)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 请求失败 (%v): %w", err, ai.ErrLLM)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 读取响应失败 (%v): %w", err, ai.ErrLLM)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ai.CompletionResult{}, fmt.Errorf("openaicompat: 非 2xx 响应 %d: %s: %w", resp.StatusCode, snippet(respBody), ai.ErrLLM)
	}

	return parseResult(respBody)
}

// snippet 截断响应体，避免错误信息/日志爆量。
func snippet(b []byte) string {
	const max = 512
	if len(b) <= max {
		return string(b)
	}
	end := max
	for end > 0 && !utf8.RuneStart(b[end]) {
		end--
	}
	return string(b[:end]) + "…"
}
