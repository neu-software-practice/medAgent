# medagent 服务封装设计（最小暴露面 · HTTP 服务 · 固定流程）

- 日期：2026-06-24（修订 2026-06-25：HTTP 服务、患者资料 JSON、检验收束血常规、购药闭环）
- 范围：把决策层封装成**最小暴露面的服务模块**，对外以 **HTTP（JSON）** 通信：内部按固定流程编排（问诊→收敛→处置→购药→终决，并行急症守护），多轮会话状态由模块内存按 ID 持有。底层包全部移入 `internal/`。
- 关联：现有 `ai`/`ai/openaicompat`/`ai/consultlog`/`ai/harness`；`docs/后端接入指南.md`（随之改写）。

## 决策摘要

| 决策点 | 结论 |
| --- | --- |
| 对外形态 | **内置 HTTP 服务**（net/http，JSON DTO）；内部保留 Go `Service` 核心可单测 |
| 调用形态 | 多轮会话；每收到患者一句推进一步 |
| 会话状态 | 模块内存会话表，按 sessionID，带 TTL 回收 |
| 患者资料 | `Start` 接自由 `map[string]any`（年龄/性别/病史/过敏/备注…，可有可无），原样以 JSON 注入模型上下文，AI 与外部解耦 |
| 检验 | **只有血常规**：triage 选 TEST 时项目恒为「血常规」 |
| 处置/购药 | MEDICATION 时输出购药请求 `[]{药名,数量}`（AI 定数量，可多药）→ 后端购买回报 `[]{药名,是否购买,数量}` → AI 据此出最终医嘱 → DONE |
| 急症守护 | 含，默认开；每轮并发，命中即取消主决策返回 EMERGENCY（约 2× LLM/轮） |
| 封装 | `ai`/`openaicompat`/`consultlog` → `medagent/internal/*`；唯一公开包是根 `medagent` |
| 编排 | 退役 `harness.RunVisit`，编排收进 facade 可恢复状态机，全项目单一编排 |
| 依赖 | 仍零外部依赖（HTTP 用 Go 1.22 标准库 `net/http` 增强路由） |

## 包布局

```
medagent/                 # 唯一公开包（package medagent）
  service.go              #   Service / New / Close / 会话表 + TTL reaper
  session.go              #   会话状态机：Start/PatientSay/SupplyTestResults/SupplyPurchaseResult/ReportVitals/End
  guardian.go             #   每轮并发守护封装
  types.go                #   公开 DTO：Config / Step / StepKind / Result / Diagnosis / Medication / DrugOrder / DrugPurchase / TestResult
  errors.go               #   公开错误
  httpapi.go              #   (s *Service) Handler() http.Handler —— JSON 端点
  *_test.go               #   FakeLLM 驱动的状态机/守护/TTL/HTTP 离线单测；门控真实-LLM 集成
medagent/internal/
  ai/                     # 由 ai/ 移入（+ Snapshot.Profile、Medication.Quantity、triage 检验固定血常规）
  openaicompat/           # 由 ai/openaicompat/ 移入
  consultlog/             # 由 ai/consultlog/ 移入
medagent/cmd/
  smoke/                  # 改 import 到 internal/*
  consult/                # 重写为驱动 facade（模拟患者循环），dogfood
  server/                 # 新增：起 HTTP 服务（medagent.New + Handler + ListenAndServe）
```

`ai/harness/` 删除：`RunVisit` 退役；其 walkthrough/variants/realrun 场景改写为 `medagent` 包测试。`medagent/cmd/*` 在同模块内仍可 import `internal/*`；外部模块 import 不到。

## 公开 Go API

```go
package medagent

type Config struct {
    Provider string // "deepseek" | "qwen" | "openai"；配合 BaseURL 接任意 OpenAI 兼容端点
    APIKey   string
    Model    string
    BaseURL  string          // 可选：覆盖 base URL（第三方中转/自建网关）
    Caps     map[string]bool // 本院能力清单（缺能力→内部自动重决策，后端不感知）
    LogDir   string          // 诊疗日志目录；空=不落盘。visitID=sessionID
    Timeout  time.Duration   // 单次 LLM 超时（0→默认 60s）
    SessionTTL time.Duration // 闲置会话回收（0→默认 30m）
    DisableGuardian bool     // 关闭急症守护
}

func New(cfg Config) (*Service, error)
func (s *Service) Close() error
func (s *Service) Handler() http.Handler // HTTP 端点（见下）

func (s *Service) Start(profile map[string]any) (sessionID string, err error) // profile 可为 nil
func (s *Service) PatientSay(ctx context.Context, sessionID, message string) (Step, error)
func (s *Service) SupplyTestResults(ctx context.Context, sessionID string, results []TestResult) (Step, error)
func (s *Service) SupplyPurchaseResult(ctx context.Context, sessionID string, results []DrugPurchase) (Step, error)
func (s *Service) ReportVitals(ctx context.Context, sessionID string, vitals map[string]any) (Step, error)
func (s *Service) End(sessionID string)
```

```go
type StepKind string
const (
    StepAsk       StepKind = "ASK"        // 医生追问，DoctorSay 给患者
    StepNeedTests StepKind = "NEED_TESTS" // 需检验，TestItems（恒为 ["血常规"]）；后端做完调 SupplyTestResults
    StepPurchase  StepKind = "PURCHASE"   // 需购药，Orders 列出药名+数量；后端处理后调 SupplyPurchaseResult
    StepEmergency StepKind = "EMERGENCY"  // 急症守护命中，Emergency 给原因；会话终止
    StepDone      StepKind = "DONE"       // 诊疗完成，Result 为终态
    StepOK        StepKind = "OK"         // ReportVitals 未命中急症
)

type Step struct {
    Kind      StepKind
    DoctorSay string       // ASK
    TestItems []string     // NEED_TESTS
    Orders    []DrugOrder  // PURCHASE
    Emergency string       // EMERGENCY
    Result    *Result      // DONE
}

type Result struct {
    Final       string       // "ADVICE" | "REFERRAL"
    Diagnosis   *Diagnosis
    Plan        string       // "MEDICATION" | "TREATMENT" | "ADVICE_ONLY" | "REFERRAL"
    Medications []Medication
    Advice      string
}

type Diagnosis    struct { Name, Basis string; Confidence float64 }
type Medication   struct { Name, Dosage, Schedule string; Quantity int }
type DrugOrder    struct { Name string; Quantity int }          // AI → 后端：要买什么、几份
type DrugPurchase struct { Name string; Bought bool; Quantity int } // 后端 → AI：买没买、几份
type TestResult   struct { Item, Value string }
```

公开 DTO 与内部 `ai` 类型同形但独立声明，边界处转换；内部类型不外泄。

## 公开错误

```go
var (
    ErrSessionNotFound = errors.New("medagent: session not found")
    ErrSessionClosed   = errors.New("medagent: session already completed")
    ErrWrongStep       = errors.New("medagent: call does not match current step") // 如未处于 NEED_TESTS 却 SupplyTestResults
    ErrUpstream        = errors.New("medagent: upstream LLM call failed")          // 包装内部 ai.ErrLLM，可重试
    ErrModelOutput     = errors.New("medagent: model output invalid")               // 包装内部 *ai.SchemaError
)
```
ctx 取消/超时以原始 ctx 错误返回。

## HTTP 端点（`Handler()`，全 JSON，Go 1.22 增强路由）

| 方法+路径 | 请求体 | 成功响应 |
| --- | --- | --- |
| `POST /sessions` | `{"profile": {…}}`（profile 可省） | `200 {"session_id": "..."}` |
| `POST /sessions/{id}/patient-say` | `{"message": "..."}` | `200 <Step>` |
| `POST /sessions/{id}/test-results` | `{"results":[{"item","value"}]}` | `200 <Step>` |
| `POST /sessions/{id}/purchase-result` | `{"results":[{"name","bought","quantity"}]}` | `200 <Step>` |
| `POST /sessions/{id}/vitals` | `{"vitals": {…}}` | `200 <Step>` |
| `DELETE /sessions/{id}` | — | `204` |

`<Step>` JSON：`{"kind","doctor_say?","test_items?","orders?","emergency?","result?"}`（按 kind 出现对应字段）。

错误→状态码：`ErrSessionNotFound`→404，`ErrSessionClosed`/`ErrWrongStep`→409，请求体非法→400，`ErrUpstream`/`ErrModelOutput`→502，ctx 超时→504。错误体 `{"error":"..."}`。

`cmd/server` 从 env/flags 读 Config，`http.ListenAndServe(addr, svc.Handler())`。

## 内部会话状态机

每个 session（内存表按 ID，`sync.RWMutex` 护表、`sync.Mutex` 护单会话串行）持有：

```go
type session struct {
    id     string
    snap   ai.Snapshot
    phase  phase // interview | triage | awaitTests | treatment | awaitPurchase | done | closed
    iTurns, tRounds, pRounds int // 熔断计数
    lastActive time.Time
    mu sync.Mutex
}
```

护栏：`maxInterviewTurns=20`、`maxTriageRounds=10`、`maxTreatmentRounds=5`，超限→`ErrUpstream`（"未收敛"）。

### 推进逻辑

`Start(profile)`：建会话；`snap.Profile = json(profile)`（nil 则空）。

`PatientSay(message)`：取会话（无→`ErrSessionNotFound`；done/closed→`ErrSessionClosed`）。追加患者轮 → 并发守护（事件 dialog）→ `advance()`：
- interview：`layer.Interview`。未 advance → 追加医生轮、`Step{ASK}`；advance → 合并 Subjective、phase=triage，进收敛环。
- triage 收敛环：`layer.Triage`：
  - CONFIRM → 写 Diagnosis，phase=treatment，进处置。
  - INTERVIEW → `snap.Feedback={MissingHint}`，**立即再跑一次 `layer.Interview`** 取追问句作 DoctorSay，phase=interview，`Step{ASK}`。
  - TEST → phase=awaitTests，`Step{NEED_TESTS, TestItems:["血常规"]}`（项目固定）。
- treatment 处置环：`layer.Treatment`：
  - TREATMENT 且 `!Caps[req]` → `Feedback={LastReject:CapabilityMissing}` 重决策。
  - MEDICATION → phase=awaitPurchase，`Step{PURCHASE, Orders: 由 medications 的 name+quantity 构成}`。
  - ADVICE_ONLY / REFERRAL → phase=done，`Step{DONE, Result}`。

`SupplyTestResults(results)`：须 phase=awaitTests（否则 `ErrWrongStep`）。`snap.TestResults += results`、phase=triage、并发守护（事件 test_result）、续跑收敛环。

`SupplyPurchaseResult(results)`：须 phase=awaitPurchase（否则 `ErrWrongStep`）。据 results：未购买项 → `snap.Refusals += {What:"med_pay", note 药名}`；已购买项及数量 → 记入 `snap.Subjective["购药结果"]`（JSON 串）。phase=treatmentFinal，再跑一次 `layer.Treatment`（带 `Feedback{NextExpected:"据购药结果出最终医嘱，勿重复开药"}`）取**最终医嘱**；其结果即终态（不再进购药）→ phase=done，`Step{DONE, Result}`。

`ReportVitals(vitals)`：仅并发守护（事件 vital）；命中→closed+EMERGENCY，否则→`Step{OK}`（不动 snap/phase）。

> 一次 `PatientSay`/`SupplyTestResults` 会尽量往前推，直到需患者补问（ASK）/需检验（NEED_TESTS）/需购药（PURCHASE）/完成（DONE）。

## 急症守护并发（默认开）

每个推进轮（PatientSay/SupplyTestResults/SupplyPurchaseResult）内：并发跑 `guardian.Assess(turnCtx, snap, ev)` 与 `advanceMain(turnCtx)`，`select`：守护命中→`cancel()` 主决策、会话 closed、返回 EMERGENCY；主决策先完成→`cancel()` 守护、返回其 Step。守护**错误不阻断诊疗**（fail-open，记日志）。`DisableGuardian` 时跳过并发。`ReportVitals` 仅跑守护。

## 内部 `ai` 改动（随迁移一并做）

1. `Snapshot` 增 `Profile json.RawMessage`；`renderSnapshotBlock` 增【患者资料】块（profile 非空时输出其 JSON）。
2. `Medication` 增 `Quantity int`；`schemaTreatment` 的 medications item 增 `quantity`(integer)；`promptTreatment` 说明 MEDICATION 须给每药 `quantity`（购买份数）。
3. `promptTriage` 限定：选 TEST 时 `test_items` 恒为 `["血常规"]`。

## 日志

内部 `consultlog.Wrap(real, FileLogger(Config.LogDir))`，ctx `WithVisitID(sessionID)`；该 session 所有 LLM 调用落 `{LogDir}/{sessionID}.jsonl`。`LogDir==""` 不落盘。

## 连带改动

- `cmd/smoke`：import 改 internal/*。
- `cmd/consult`：重写为 `medagent.New` + 模拟患者循环（含模拟检验回填血常规、模拟购药回报），dogfood，日志 ./logs。
- `cmd/server`：新增 HTTP 服务入口。
- `ai/harness` 删除；场景测试改写到 `medagent`。
- `docs/后端接入指南.md`：改写为面向 HTTP 服务 + Go API 的接入说明。

## 测试

- **离线单测（FakeLLM）**：ASK / advance→CONFIRM→（ADVICE_ONLY）DONE / triage INTERVIEW 回退→ASK / TEST→NEED_TESTS→SupplyTestResults→CONFIRM / treatment MEDICATION→PURCHASE→SupplyPurchaseResult→DONE（含未购买→Refusal→最终医嘱）/ 能力缺失→内部重决策→REFERRAL；守护命中→EMERGENCY 且取消主决策、fail-open、DisableGuardian；会话错误（NotFound/Closed/WrongStep）；错误映射（ErrUpstream/ErrModelOutput/ctx 透传）；护栏未收敛；TTL 回收；`-race` 多会话并发。
- **HTTP 单测**：用 `httptest` 打各端点，断言 JSON 形状与状态码映射。
- **门控真实-LLM 集成**（`MEDAGENT_REAL_LLM`）：模拟患者驱动 facade 跑通整流程到 DONE，断言日志文件生成。

## 显式排除（YAGNI）

- 会话持久化/可序列化状态/水平扩展；鉴权/限流/TLS（后端在前置网关处理）；支付/卡片/saga；复诊入口（PriorVisit 后续可加进 Start）；多 sink 日志后端；guardian 纯后台 ticker（仅在有新事件轮内并发）。
