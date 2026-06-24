# medagent 服务封装设计（最小暴露面，固定流程）

- 日期：2026-06-24
- 范围：把现有决策层封装成一个**最小暴露面的服务模块**：内部按固定流程编排（问诊→收敛→处置 + 急症守护），外部后端只通过顶层 `medagent` 包的少数方法调用，多轮会话、会话状态由模块内部按 ID 持有。底层包全部移入 `internal/`。
- 关联：现有 `ai`/`ai/openaicompat`/`ai/consultlog`/`ai/harness`；`docs/后端接入指南.md`（将随之改写为面向 facade）。

## 目标与非目标

- 目标：外部 `import "medagent"` 即得一个 `Service`，用 `Start / PatientSay / SupplyTestResults / ReportVitals / End / Close` 跑完一次就诊；内部封装 LLM 接入、编排、急症守护、日志。
- 非目标：不暴露决策层积木给外部自行编排；不提供持久化（会话在内存，带 TTL 回收）；不做支付/卡片/saga（仍属上层后端，但本模块不涉及）。

## 决策摘要

| 决策点 | 结论 |
| --- | --- |
| 调用形态 | 多轮会话：每收到患者一句 `PatientSay` 推进一步 |
| 会话状态 | 模块内存会话表，按 sessionID；带 TTL 回收 |
| 流程范围 | 含急症守护（默认开，可关） |
| 封装 | `ai`/`openaicompat`/`consultlog` → `medagent/internal/*`；唯一公开包是根 `medagent` |
| 编排 | 退役 `harness.RunVisit`，编排收进 facade 的**可恢复状态机**，全项目单一编排 |
| 守护并发 | 每轮并发跑 guardian 与主决策，命中即取消主决策返回 EMERGENCY（约 2× LLM 调用/轮） |
| 依赖 | 仍零外部依赖 |

## 包布局

```
medagent/                 # 唯一公开包（package medagent）：facade
  service.go              #   Service / New / Close / 会话表 + TTL reaper
  session.go              #   会话状态机（PatientSay/SupplyTestResults/ReportVitals/Start/End）
  guardian.go             #   每轮并发守护的封装
  types.go                #   公开 DTO：Config / Step / StepKind / Result / Diagnosis / Medication / TestResult
  errors.go               #   公开错误：ErrSessionNotFound / ErrSessionClosed / ErrUpstream / ErrModelOutput
  *_test.go               #   FakeLLM 驱动的状态机/守护/TTL 离线单测；门控真实-LLM 集成测试
medagent/internal/
  ai/                     # 由 ai/ 移入（决策层契约 + 4 agent）
  openaicompat/           # 由 ai/openaicompat/ 移入
  consultlog/             # 由 ai/consultlog/ 移入
medagent/cmd/
  smoke/                  # 改 import 到 internal/*
  consult/                # 重写为驱动 facade（模拟患者循环 PatientSay），dogfood
```

- `ai/harness/` 删除：`harness.go`(RunVisit) 退役；其 `harness_test.go`/`variants_test.go`/`realrun_test.go` 的场景**改写为驱动 facade** 的测试，落在 `medagent` 包。
- internal 规则：外部模块 import 不到 `medagent/internal/*`；`medagent/cmd/*` 在同模块内仍可 import。

## 公开 API

```go
package medagent

type Config struct {
    Provider string // "deepseek" | "qwen" | "openai"；配合 BaseURL 可接任意 OpenAI 兼容端点
    APIKey   string
    Model    string
    BaseURL  string        // 可选：覆盖 base URL（第三方中转/自建网关）
    Caps     map[string]bool // 本院能力清单（缺能力→内部自动重决策，后端不感知）
    LogDir   string        // 诊疗日志目录；空=不落盘。visitID=sessionID
    Timeout  time.Duration // 单次 LLM 调用超时（0→默认 60s）
    SessionTTL time.Duration // 闲置会话回收（0→默认 30m）
    DisableGuardian bool    // 关闭急症守护
}

func New(cfg Config) (*Service, error) // Provider/APIKey/Model 必填（或 BaseURL+APIKey+Model）
func (s *Service) Close() error        // 停 TTL reaper，清理

// Start 开一次就诊，返回 sessionID（此刻无 LLM 调用）。
func (s *Service) Start() (sessionID string, err error)

// PatientSay 推进一步：患者说一句。返回下一步该做什么。
func (s *Service) PatientSay(ctx context.Context, sessionID, message string) (Step, error)

// SupplyTestResults 回填检验结果并续跑（仅当上一步 Kind==NEED_TESTS）。
func (s *Service) SupplyTestResults(ctx context.Context, sessionID string, results []TestResult) (Step, error)

// ReportVitals 把体征事件喂给急症守护（带外，不推进主流程）。命中→EMERGENCY，否则→OK。
func (s *Service) ReportVitals(ctx context.Context, sessionID string, vitals map[string]any) (Step, error)

// End 主动结束并清理会话（幂等）。
func (s *Service) End(sessionID string)
```

```go
type StepKind string
const (
    StepAsk       StepKind = "ASK"        // 医生追问，DoctorSay 给患者
    StepNeedTests StepKind = "NEED_TESTS" // 需检验，TestItems 列出项目；后端做完调 SupplyTestResults
    StepEmergency StepKind = "EMERGENCY"  // 急症守护命中，Emergency 给原因；会话终止
    StepDone      StepKind = "DONE"       // 诊疗完成，Result 为终态
    StepOK        StepKind = "OK"         // ReportVitals 未命中急症
)

type Step struct {
    Kind      StepKind
    DoctorSay string      // ASK
    TestItems []string    // NEED_TESTS
    Emergency string      // EMERGENCY
    Result    *Result     // DONE
}

type Result struct {
    Final       string       // "ADVICE" | "REFERRAL"
    Diagnosis   *Diagnosis
    Plan        string       // "MEDICATION" | "TREATMENT" | "ADVICE_ONLY" | "REFERRAL"
    Medications []Medication
    Advice      string
}

type Diagnosis  struct { Name, Basis string; Confidence float64 }
type Medication struct { Name, Dosage, Schedule string }
type TestResult struct { Item, Value string }
```

公开 DTO 与内部 `ai` 类型同形但独立声明，边界处转换，内部类型不外泄。

## 公开错误

```go
var (
    ErrSessionNotFound = errors.New("medagent: session not found")
    ErrSessionClosed   = errors.New("medagent: session already completed")
    ErrUpstream        = errors.New("medagent: upstream LLM call failed")   // 包装内部 ai.ErrLLM，可重试
    ErrModelOutput     = errors.New("medagent: model output invalid")        // 包装内部 *ai.SchemaError
)
```
- ctx 取消/超时：以原始 ctx 错误返回（`errors.Is(err, context.Canceled/DeadlineExceeded)`）。
- 内部 `ai.ErrLLM` → `ErrUpstream`；`*ai.SchemaError` → `ErrModelOutput`。其余封装异常归 `ErrUpstream`。

## 内部会话状态机

每个 session（按 sessionID 存内存表，`sync.Mutex` 保护单会话串行；表本身用 `sync.RWMutex`）持有：

```go
type session struct {
    id      string
    snap    ai.Snapshot          // 全量上下文
    phase   phase                // interview | triage | treatment | done | closed
    lastReply string             // 上一句医生话（ASK 用）
    iTurns, tRounds, pRounds int // 轮数计数（熔断）
    lastActive time.Time
    mu      sync.Mutex
}
```

护栏常量（内部）：`maxInterviewTurns=20`、`maxTriageRounds=10`、`maxTreatmentRounds=5`（沿用 harness 值），超限返回 `ErrUpstream`（标注"未收敛"）。

### 推进逻辑

`PatientSay(message)`：
1. 取会话（无→`ErrSessionNotFound`；已 done/closed→`ErrSessionClosed`）。
2. 追加 `DialogTurn{patient, message}`。
3. **守护并发**（见下）：事件 `{Kind:"dialog", Data:message}`。命中→标记会话 closed，返回 EMERGENCY。
4. 主推进 `advance()`：
   - phase=interview：`layer.Interview`。`advance==nil`→把医生 `reply` 追加为 doctor 轮、置 `lastReply`、返回 `Step{ASK, DoctorSay:reply}`；`advance!=nil`→合并 Subjective，phase=triage，落入收敛环。
   - phase=triage 收敛环（带计数）：`layer.Triage`：
     - CONFIRM→写 `snap.Diagnosis`，phase=treatment，落入处置。
     - INTERVIEW→`snap.Feedback={MissingHint}`，phase=interview，返回 `Step{ASK, DoctorSay: reply占位}`（实际再由下一句患者驱动；reply 取 triage 无 → 用一句通用过场或上次 interview reply）。**简化：INTERVIEW 时直接再跑一次 interview 以拿到给患者的追问句**，返回 ASK。
     - TEST→返回 `Step{NEED_TESTS, TestItems}`，phase 停在 triage 等回填。
   - phase=treatment 处置环（带计数）：`layer.Treatment`：TREATMENT 且 `!Caps[req]`→`Feedback={LastReject:CapabilityMissing}` 重决策；否则 phase=done，返回 `Step{DONE, Result}`。

`SupplyTestResults(results)`：取会话（须 phase=triage 且上步 NEED_TESTS，否则 `ErrSessionClosed`/状态错），`snap.TestResults+=results`，守护并发（事件 `{Kind:"test_result"}`），续跑收敛环。

`ReportVitals(vitals)`：仅守护，事件 `{Kind:"vital", Data:vitals}`；命中→closed+EMERGENCY，否则→OK（不动 snap/phase）。

> INTERVIEW 回退的 reply 来源：triage 选 INTERVIEW 后，状态机立即用更新后的 snap（带 MissingHint）再调一次 `layer.Interview` 拿到对患者的追问句作为 `DoctorSay`，置 `lastReply`，phase 回 interview。下一次 `PatientSay` 即患者对该追问的回答。

## 急症守护并发（默认开）

每个 `PatientSay`/`SupplyTestResults` 轮内：
```
turnCtx, cancel := WithCancel(ctx)
go guardian.Assess(turnCtx, snap, ev) → gch
go advanceMain(turnCtx)              → mch
select {
  case g := <-gch:
     if g.hit { cancel(); <-mch; return EMERGENCY }   // 守护命中→取消主决策
     m := <-mch; return m                              // 守护放行→等主决策
  case m := <-mch:
     cancel(); return m                                // 主决策先完成→取消守护
}
```
- 守护**错误不阻断诊疗**（fail-open，记日志）；命中才打断。
- `DisableGuardian=true` 时跳过并发，直接 `advanceMain`。

## 日志

- 内部用 `consultlog.Wrap(realClient, FileLogger(Config.LogDir))`，`ctx` 上 `WithVisitID(sessionID)`。该 session 所有 LLM 调用（含守护、含 schema 重试）落 `{LogDir}/{sessionID}.jsonl`。
- `LogDir==""`：不落盘（用 no-op，或直接不 Wrap）。

## 连带改动

- `cmd/smoke`：import 改 `medagent/internal/openaicompat` 等；逻辑不变。
- `cmd/consult`：重写为 `medagent.New` + 模拟患者循环 `PatientSay`/`SupplyTestResults`，打印每步；dogfood facade。
- `ai/harness` 删除；其 walkthrough/variants/realrun 场景改写为 `medagent` 包的测试（FakeLLM 驱动状态机 + 门控真实 LLM）。
- `docs/后端接入指南.md`：改写为面向 facade 的接入说明（小节精简到公开 API）。

## 测试

- **离线单测**（FakeLLM）：
  - ASK 路径（interview 未 advance）；advance→CONFIRM→DONE；triage INTERVIEW 回退→再 ASK；triage TEST→NEED_TESTS→SupplyTestResults→CONFIRM→DONE；treatment 能力缺失→内部重决策→REFERRAL。
  - 守护命中→EMERGENCY 且取消主决策；守护错误 fail-open；DisableGuardian 跳过。
  - 会话错误：未知 ID→ErrSessionNotFound；done 后再调→ErrSessionClosed；SupplyTestResults 时机不对→错误。
  - 错误映射：内部 ErrLLM→ErrUpstream；SchemaError→ErrModelOutput；ctx 取消透传。
  - 轮数护栏触发→未收敛错误。
  - TTL 回收：过期会话被 reaper 清理（用可注入的 clock 或短 TTL）。
  - 并发：`-race` 下多会话并发推进。
- **门控真实-LLM 集成**（沿用 `MEDAGENT_REAL_LLM` 开关）：用模拟患者驱动 facade 跑通整流程，断言走到 DONE 且日志文件生成。

## 显式排除（YAGNI）

- 会话持久化/可序列化状态/水平扩展；支付/卡片/saga；复诊入口（PriorVisit 可后续加进 Start 参数）；多 sink 日志后端；guardian 的 ticker 纯后台轮询（仅在有新事件的轮内并发跑）。
