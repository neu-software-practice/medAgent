# 诊疗日志系统设计（LLM 调用审计流，每诊一文件）

- 日期：2026-06-24
- 范围：新包 `ai/consultlog`——一个 logging `ai.LLMClient` **装饰器**，按 visitID 把每次诊疗经过的所有 LLM 调用记成完整审计流，每次诊疗写一个 JSONL 文件，按 visitID 直接可寻。决策层零侵入。
- 关联：`ai.LLMClient`/`ai.Message`/`ai.CompletionRequest`、`ai/openaicompat`、`ai/internal/harness`（门控验证测试将包上 logger）。

## 决策摘要

| 决策点 | 结论 |
| --- | --- |
| 记录粒度 | 底层调用审计流：拦截每次 `Complete`，成功失败都记（含 schema 重试/纠正轮） |
| 持久化 | 每次诊疗一个文件 `{dir}/{visitID}.jsonl`，按 visitID 可寻 |
| 包 | `ai/consultlog`（import `ai`，无环） |
| 集成测试日志 | 写 `t.TempDir()`，断言后自动清理 |
| 日志失败 | **绝不影响诊疗**：sink 出错只写 stderr、吞掉 |
| 依赖 | 仍零外部依赖，标准库 only |

## 为什么装饰 LLMClient 就够

每个 agent（interview/triage/treatment/guardian）都经 `runStructured → llm.Complete`，schema 重试也走同一路径。包一层 `Complete` 即可捕获一次诊疗里**全部**模型交互（含每轮纠正、含患者模拟调用），且决策层无需改动、保持纯净。`req.Schema.Name` 标识是哪个 agent。

## 文件结构

- `ai/consultlog/context.go` — visitID 的 context 注入与生成
- `ai/consultlog/record.go` — `CallRecord` 类型 + 最小 `Sink` 接口（仅为可测性的 DI 缝，非可插拔 sink 系统）
- `ai/consultlog/file.go` — `FileLogger`：每诊一文件
- `ai/consultlog/client.go` — `Wrap` 装饰器
- 各 `*_test.go` — 离线单测（FakeLLM + memory sink / temp dir）

## API

```go
package consultlog

// —— context.go ——
func WithVisitID(ctx context.Context, id string) context.Context
func VisitID(ctx context.Context) string // 不存在返回 ""
func NewVisitID() string                  // 形如 20260624-153012-a1b2c3（time + crypto/rand hex）

// —— record.go ——
type CallRecord struct {
    VisitID    string          `json:"visit_id"`
    Time       time.Time       `json:"time"`
    Schema     string          `json:"schema"`     // = req.Schema.Name，标识 agent
    System     string          `json:"system"`
    Messages   []ai.Message    `json:"messages"`
    Structured json.RawMessage `json:"structured,omitempty"`
    Raw        string          `json:"raw,omitempty"`
    LatencyMS  int64           `json:"latency_ms"`
    Error      string          `json:"error,omitempty"`
}
type Sink interface{ Write(rec CallRecord) error }

// —— file.go ——
type FileLogger struct { /* dir + sync.Mutex */ }
func NewFileLogger(dir string) *FileLogger
func (f *FileLogger) Write(rec CallRecord) error // 追加一行 JSON 到 {dir}/{visitID}.jsonl；visitID 为空用 "unknown"

// —— client.go ——
type Client struct { /* inner + sink + onErr */ }
func Wrap(inner ai.LLMClient, sink Sink) *Client
func (c *Client) Complete(ctx, req) (ai.CompletionResult, error) // 实现 ai.LLMClient
```

## 数据流与行为

1. 调用方：`real := openaicompat.New(...)` → `logged := consultlog.Wrap(real, consultlog.NewFileLogger(dir))` → `ai.NewDecisionLayer(logged)`。
2. 一次诊疗开始：`ctx = consultlog.WithVisitID(ctx, consultlog.NewVisitID())`。
3. 每次 `Complete`：计时 → 调 inner → 组 `CallRecord`（VisitID 取自 ctx；inner 出错则填 `Error`，成功填 `Structured`/`Raw`）→ `sink.Write` → **原样返回 inner 的结果/错误**。
4. `sink.Write` 出错：调 `onErr`（默认写 stderr），不传播、不影响诊疗。
5. 产物：`{dir}/{visitID}.jsonl`，一次诊疗一份，每行一条调用记录，文件追加顺序即时间顺序。

## 测试

- **离线单测**（无网络）：
  - `Wrap` 包 `FakeLLM` + memory sink：断言 `CallRecord` 字段（VisitID 来自 ctx、Schema、Messages、Structured、`LatencyMS>=0`）。
  - 错误路径：inner 返回 error → 记录里 `Error` 非空、`Complete` 仍把原 error 上抛。
  - sink 失败：sink.Write 返回 error → `Complete` 仍正常返回 inner 结果（日志失败不破坏诊疗）。
  - `FileLogger`：同一 visitID 多条 → 一个文件多行且可逐行反序列化；不同 visitID → 不同文件；空 visitID → `unknown.jsonl`。
  - 并发安全：`go test -race` 下并发 Write 同一 visit。
  - `context`：`WithVisitID`/`VisitID` 往返；`NewVisitID` 唯一性（多次不重复、格式合规）。
- **门控集成**（复用 `TestRealFeverFlow`，可选增强或新增）：把 client 包上 `FileLogger(t.TempDir())` + 注入 visitID，跑完读回 `{tmp}/{visitID}.jsonl`，断言含 `interview`/`triage_decide`/`treatment_plan` 等 schema 的记录。

## 边界与排除（YAGNI）

- 这是决策层 infra（装饰 LLMClient）；生产里日志最终落 DB/ELK/对象存储是**调用方**的事，本包只交付装饰器 + context 管线 + 可用的文件 sink。
- 记录**不含 API key**（key 在 HTTP 层、不进 `CompletionRequest`），但含医患对话与诊断——这正是诊疗记录本身。
- 不做：可插拔多 sink 系统、高层语义病历渲染、PII 脱敏、日志轮转/清理、检索/索引工具、把 visitID 生成塞进真实编排（编排不在本范围）。
