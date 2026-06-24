# env 读 key 接线 + 真实调用烟雾工具设计

- 日期：2026-06-24
- 范围：给 medagent 加第一个可执行入口 `cmd/smoke`，从环境变量读 key、构造 `openaicompat` client，对真实 OpenAI 兼容端点发一次 tool-use 调用，验证端到端结构化输出。今天用 `OPENAI_API_KEY` 跑通；后续设 DeepSeek/Qwen 的 key 即可复用同一工具。
- 关联：`ai/openaicompat`（adapter）、`docs/superpowers/specs/2026-06-24-llm-adapter-design.md`（其 §2 明确"密钥读取是调用方职责"）。

## 决策摘要

| 决策点 | 结论 |
| --- | --- |
| 真实调用 key | 用现有 `OPENAI_API_KEY` 打 OpenAI 真实端点烟雾测（OpenAI 兼容、tool-use 路径一致） |
| 交付形态 | 仅 `cmd/smoke`（手动运行），不加自动集成测试 |
| 示例 schema | 中性极简 `{answer:string, confidence:number}`，不牵扯医疗逻辑 |
| env 读取位置 | 只在 `cmd/smoke`，不进 `openaicompat`（守边界） |
| 依赖 | 仍零外部依赖，标准库 only |

## 组件：`cmd/smoke/main.go`（package main）

flags：
- `-provider`：`openai` | `deepseek` | `qwen`，默认 `openai`。
- `-model`：默认按 provider 取（见下表），可覆盖。
- `-prompt`：默认 `用一句话解释什么是布洛芬。`，可覆盖。
- `-base-url`：覆盖 base URL（第三方中转 / 自建网关用；填到 `/chat/completions` 之前的路径，通常以 `/v1` 结尾）。留空走 provider 默认。设置时一律经 `openaicompat.New(Config{BaseURL:override,...})` 构造，绕开 `NewDeepSeek`/`NewQwen` 预设。

provider 映射：

| provider | env 变量 | base URL | 默认 model | 构造 |
| --- | --- | --- | --- | --- |
| openai | `OPENAI_API_KEY` | `https://api.openai.com/v1` | `gpt-4o-mini` | `openaicompat.New(Config{BaseURL,APIKey,Model})` |
| deepseek | `DEEPSEEK_API_KEY` | （预设）| `deepseek-chat` | `openaicompat.NewDeepSeek(key, model)` |
| qwen | `DASHSCOPE_API_KEY` | （预设）| `qwen-plus` | `openaicompat.NewQwen(key, model)` |

流程：
1. 解析 flags；未知 provider → stderr 报错 + `os.Exit(2)`。
2. 从对应 env 读 key；为空 → stderr 打印"缺少环境变量 X"（**不打印 key 值**）+ `os.Exit(1)`。
3. 构造 client。
4. 组 `ai.CompletionRequest`：
   - `System`：`你是一个简洁的助手，请用 answer 工具返回结果。`
   - `Messages`：`[{Role:"user", Content: prompt}]`
   - `Schema`：name `answer`，JSON =
     `{"type":"object","properties":{"answer":{"type":"string"},"confidence":{"type":"number"}},"required":["answer"]}`
5. `context.WithTimeout(30s)` 调 `Complete`。
6. 成功：打印 provider、model、`res.Structured`、`res.Raw`。失败：stderr 打印 error（`errors.Is(err, ai.ErrLLM)` 时标注），`os.Exit(1)`。

## 验证

- `go build ./...`、`go vet ./...` 通过；仓库仍零外部依赖。
- 实跑 `OPENAI_API_KEY=… go run ./cmd/smoke -provider openai`，确认返回符合 schema 的结构化 JSON（`answer` 字段存在）。
- `go test ./...` 仍全绿且不打真实网络（cmd/smoke 不含测试）。

## 显式排除（YAGNI）

- 自动 / CI 集成测试；网络重试；流式；把 key 读进 `openaicompat`；新增 provider 预设构造器；除 OpenAI 外的真实调用（缺 key，后续按需）。
