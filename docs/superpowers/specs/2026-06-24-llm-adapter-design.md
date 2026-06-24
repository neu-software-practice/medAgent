# OpenAI 兼容 LLM adapter 设计（DeepSeek + 通义千问，tool-use 结构化输出）

- 日期：2026-06-24
- 范围：实现 `ai.LLMClient` 的真实 provider adapter，仅 adapter 本身；不改 agent / 编排，不写 main 接线。
- 关联：
  - `ai/llm.go`（`LLMClient` / `CompletionRequest` / `CompletionResult` / `OutputSchema` 契约）
  - `ai/errors.go`（`ErrLLM`）
  - `docs/superpowers/specs/2026-06-23-ai-decision-layer-design.md` 第 234 / 236 行（tool-use 映射与同角色消息合并约束）

## 1. 决策摘要

| 决策点 | 结论 | 理由 |
| --- | --- | --- |
| adapter 形态 | **单一 OpenAI 兼容 adapter**，DeepSeek / 通义千问只是不同 config | 两家均提供 OpenAI 兼容 chat-completions，tool-use 请求/响应格式一致；新增第三家 provider 只改配置 |
| HTTP 层 | **手写 `net/http` + `encoding/json`**，维持仓库零依赖 | 需要的接口面很窄（单次 chat-completions + tool-use），完全可控 tool_choice / 消息合并 / 错误映射，不被 SDK 对 DashScope 兼容模式的假设绑架 |
| 结构化输出机制 | **强制 tool-use（function calling）** | 用户已定；`tool_choice` 强制单一工具，保证模型必产出结构化 JSON |
| 网络层重试 | **单次调用，不重试** | 职责单一、最易测；退避重试以后用薄 wrapper 包，符合 YAGNI |
| 缺 `tool_calls` 时 | **映射为 `ai.ErrLLM`** | 强制 tool_choice 下没有 tool_call 属 provider 异常，非"模型输出不合 schema"，不应触发 agent 的 schema 重试 |

## 2. 架构与包结构

新增子包 `medagent/ai/openaicompat`（包名 `openaicompat`），实现 `ai.LLMClient`。

- 隔离理由：让 `ai` 保持纯契约 + agent 逻辑，adapter 的 HTTP / JSON 细节与独立单测留在子包。
- 依赖方向：`openaicompat → ai`（引用 `ai.CompletionRequest` 等类型）；`ai` 不反向依赖，无环。

对外 API：

```go
package openaicompat

type Config struct {
    BaseURL    string        // 形如 https://api.deepseek.com/v1
    APIKey     string
    Model      string
    Timeout    time.Duration // 0 → 默认 60s
    HTTPClient *http.Client  // 可选；nil 用内建（便于测试注入 httptest）
}

type Client struct { /* cfg + http client */ }

func New(cfg Config) *Client                  // 实现 ai.LLMClient
func NewDeepSeek(apiKey, model string) *Client // 预填 BaseURL = https://api.deepseek.com/v1
func NewQwen(apiKey, model string) *Client     // 预填 BaseURL = https://dashscope.aliyuncs.com/compatible-mode/v1

func (c *Client) Complete(ctx context.Context, req ai.CompletionRequest) (ai.CompletionResult, error)
```

读环境变量取 API key 是调用方（main）的职责，不进 adapter。

预设 BaseURL：
- DeepSeek：`https://api.deepseek.com/v1`
- 通义千问（DashScope 兼容模式）：`https://dashscope.aliyuncs.com/compatible-mode/v1`

## 3. 请求映射（核心：tool-use 强制）

`ai.CompletionRequest` → `POST {BaseURL}/chat/completions`，`Authorization: Bearer {APIKey}`。

请求体（内部 wire 结构）要点：

- `model` = `Config.Model`。
- `messages`：
  - 若 `req.System` 非空 → 首条 `{role:"system", content:req.System}`。
  - `req.Messages` 依次映射（`Role` 直接用 `"user"` / `"assistant"`），**合并连续同角色消息**：相邻同 role 的 content 以 `"\n\n"` 拼接成一条。
    - 这直接满足 2026-06-23 spec 第 236 行约束：`buildMessages` 的首条快照块是 user、紧随的患者轮也是 user 会连续两条 user；guardian 把事件作为 user 追加在末尾同理。system 是独立 role，不参与与 user/assistant 的合并。
- `tools`：单一工具
  ```json
  [{"type":"function","function":{
      "name": "<Schema.Name>",
      "description": "Return the structured result as arguments to this function.",
      "parameters": <Schema.JSON>
  }}]
  ```
- `tool_choice`：`{"type":"function","function":{"name":"<Schema.Name>"}}` —— **强制**调用该工具。
- 不设 temperature 等额外参数（v1 保持最小面；后续需要再加进 `Config`）。

## 4. 响应映射

解析响应 `choices[0].message.tool_calls[0].function.arguments`（OpenAI 兼容格式下 arguments 是 JSON 字符串）：

- `CompletionResult.Structured = json.RawMessage([]byte(arguments))`
- `CompletionResult.Raw = arguments`

adapter **只保证"拿到结构化 JSON"，不做 schema 语义校验**。schema 校验仍由 agent 的 `intent.Validate()` + `SchemaRetryMax` 负责，与现有契约不变。

## 5. 错误处理

全部传输 / 协议层错误包进 `ai.ErrLLM`（`fmt.Errorf("...: %w", ai.ErrLLM)`），保持 provider 中立：

- 先 `ctx.Err()` 检查（取消 / 超时）。
- `http.Client.Do` 失败、超时 → `ErrLLM`。
- 非 2xx 响应 → `ErrLLM`，错误信息带 status code 与 body 截断片段（便于诊断，避免日志爆量）。
- 响应体 JSON 解析失败 → `ErrLLM`。
- `choices` 为空、或 `tool_calls` 缺失 / 为空（强制 tool_choice 下属 provider 异常）→ `ErrLLM`（**不**触发 agent schema 重试）。

不做网络层重试。

### 已知 provider caveat（记录，不在 v1 处理）

- 通义千问部分推理型模型（如带 thinking 的 qwen3）非流式调用可能需要 `enable_thinking:false`；选用具备 function-calling 能力的模型即可规避。
- DashScope 兼容模式与 DeepSeek 对 `tool_choice` 强制 function 的支持已知可用；若个别模型不支持强制，再按需在 `Config` 暴露开关。

## 6. 测试（零真实网络）

用 `httptest.NewServer` 并通过 `Config.HTTPClient` 注入：

- **请求断言**：
  - `system` 消息在首位、content 正确。
  - 消息已合并：响应给定连续同角色输入后，实际请求体中无相邻同 role 消息，且合并内容以 `\n\n` 拼接。
  - `tools` 含正确 `name` 与 `parameters`（= `Schema.JSON`）。
  - `tool_choice` 强制到该 function name。
  - `Authorization` 头与 `model` 正确。
- **响应解析**：从 canned `tool_calls` 取出 arguments → `Structured` / `Raw` 一致。
- **错误路径**：非 2xx → `errors.Is(err, ai.ErrLLM)`；缺 `tool_calls` → `ErrLLM`；`ctx` 取消 → 返回错误。
- **预设构造器**：`NewDeepSeek` / `NewQwen` 的 BaseURL 正确。

## 7. 显式排除（YAGNI）

- 流式（`Complete` 为单次）。
- 网络层重试 / 退避。
- 多工具 / 并行 tool_calls。
- temperature / top_p 等采样参数（需要时再进 `Config`）。
- 从环境变量读密钥（调用方职责）。
- agent / 编排 / main 接线改动。
