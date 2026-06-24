# 无人医院患者端 · AI 决策层实现设计

> 本文档是 AI 决策层（`ai` 包）的实现设计规格。上游需求来自
> `docs/files/无人医院_AI系统_实现规格.md`（下称"系统规格"）。
> 本设计只覆盖 **AI 相关内容**，不含前端、编排层真身、执行层与持久化。

- 日期：2026-06-23
- 范围所有者：AI 决策层
- 选定方案：**方案 A —— 无状态单次决策 agent + 编排层驱动的"拒绝→重决策"外环**

---

## 1. 范围

### 1.1 我交付的（in scope）

一个 Go 包 `ai`（建议 module `medagent`，import 路径 `medagent/ai`），包含：

- **4 个 agent**：问诊（interview）、收敛判断（triage）、处置（treatment）、急症守护（guardian）。
- **typed intent 契约**：`AdvanceToTriage` / `TriageDecision` / `TreatmentPlan` / `EmergencyInterrupt`，及 `InterviewResult` 包装。
- **结构校验** `Validate()`（字段齐全 / enum 合法 / 自证字段存在）。
- **LLMClient 抽象** + 可编程 **fake LLM**（推迟真实 provider 选型）。
- **上下文构建与压缩**（从 Snapshot 重建 LLM 上下文；Interview 历史压缩）。
- **prompts**（4 个 agent 的 system prompt，简体中文）。
- **mock 编排 harness**（`internal/harness`）+ **急性咽炎 walkthrough**（端到端测试兼可运行 demo）。

### 1.2 不归我的（out of scope）

- 会话状态机真身、卡片编排、副作用账本与补偿（Saga）、总计时、轮次熔断的**执行**。
- intent 的**语义/状态校验**（合法转移 / 能力清单 / 自证是否当真成立 / 熔断）。
- 缴费 / 预约取号 / 药房配送 / 退款等执行层动作。
- 前端 UI、卡片渲染、流程引导语等 chrome。

### 1.3 产出归属（谁生成什么文本）

- **AI 层产出**：问诊追问对话文本、triage 三选一决策、确诊 name/basis/confidence、处置四选一、医嘱 advice / 用药明细、急症打断判断与理由。
- **编排层产出**：状态机推进、语义校验、卡片金额/排队人数等 chrome、缴费引导语等流程模板。

---

## 2. 设计前提（继承系统规格 §0，不可动摇）

1. 概念性项目，假设 AI 医疗决策正确度不低于人类；诊断/处置/转诊交给 AI；**不引入任何硬编码医学红旗规则**（含 guardian——急症判断也是 AI 判断，非规则）。
2. **AI 只发起意图，不直接执行副作用**。agent 产出 typed intent，编排层校验后才落地。
3. **就诊状态唯一真相在编排层**；**AI 无状态**，每次调用从 Snapshot 重建上下文。
4. 信息层（医嘱）无条件生成；执行层以用户卡片操作为准；**医疗判断不因患者态度改变**。

> 确定性约束（能力清单、合法转移、轮次熔断、副作用结算）不是医学判断，留在编排层，与"决策交给 AI"不冲突。

---

## 3. 包结构与对外接口

```
医疗agent/                       module medagent
├── go.mod
└── ai/                          ← 交付的全部
    ├── contract.go              Snapshot / Event / 各 Intent / 接口
    ├── intent.go                intent + 结构校验 Validate()
    ├── snapshot.go              Snapshot/Event 类型 + 上下文构建与压缩
    ├── llm.go                   LLMClient 接口 + 请求/响应类型
    ├── llm_fake.go              测试用可编程 fake LLM
    ├── prompts.go               各 agent system prompt 模板
    ├── agent_interview.go       问诊 agent
    ├── agent_triage.go          triage agent
    ├── agent_treatment.go       处置 agent
    ├── agent_guardian.go        急症守护 agent
    ├── layer.go                 DecisionLayer 实现（组装 4 agent）
    ├── *_test.go
    └── internal/harness/        mock 编排层 + 急性咽炎 walkthrough
```

### 3.1 对外接口

```go
// 主决策层：编排层按当前状态调用对应方法。全部无状态——每次只吃 Snapshot。
type DecisionLayer interface {
    // 问诊：吃快照(含最新用户消息)，要么继续追问，要么判定充分→请求进入 triage
    Interview(ctx context.Context, s Snapshot) (InterviewResult, error)
    // 收敛环三选一：CONFIRM / INTERVIEW / TEST
    Triage(ctx context.Context, s Snapshot) (TriageDecision, error)
    // 处置四选一：MEDICATION / TREATMENT / ADVICE_ONLY / REFERRAL
    Treatment(ctx context.Context, s Snapshot) (TreatmentPlan, error)
}

// 急症守护：独立接口，纯判断。并发/ticker 由编排层负责。
type Guardian interface {
    Assess(ctx context.Context, s Snapshot, ev Event) (EmergencyInterrupt, bool, error)
}
```

### 3.2 校验分工

- **结构校验（我）**：必填字段齐全、enum 合法、TEST 必带 `subjective_exhausted`+`reason`。对应 `SCHEMA_INVALID`。
- **语义/状态校验（编排层）**：合法转移、能力具备、自证是否当真成立、轮次熔断。对应其余 `RejectReason`。
- 编排层拒绝后，把 `RejectReason` 放进下一次 Snapshot 的 `Feedback`，agent 据此重新决策（方案 A 外环）。

---

## 4. 数据契约

### 4.1 输入：Snapshot

```go
type Snapshot struct {
    // —— 持久就诊状态 ——
    Interview   []DialogTurn    // 问诊对话全量历史（压缩在包内部做）
    Subjective  map[string]any  // 已采集主观信息（主诉/病史/体征）
    TestResults []TestResult    // 已回填检验结果
    Diagnosis   *Diagnosis      // 确诊结果（出环后才非空）
    PriorVisit  *VisitSummary   // 复诊携带的上次摘要（诊断+用药）
    Refusals    []RefusalRecord // 患者拒绝记录（进档，影响医嘱措辞）

    // —— 本次调用的瞬时编排反馈（pull / 重调时填充）——
    Feedback *OrchestratorFeedback
}

type OrchestratorFeedback struct {
    LastReject   RejectReason // 上次 intent 被拒原因（驱动重决策）
    NextExpected string       // 编排层期望的 intent 类型（pull 场景）
    CardDeferred bool         // 上一张可挂起卡是否被 defer
    MissingHint  []string     // 上一次 triage=INTERVIEW 指明的待补项
}
```

**两个有意取舍：**

1. **不把轮次计数喂给 AI**。系统规格 §2.2 明确"熔断不提示 AI，直接熔断"，故 `RoundCounters` 留在编排层，不进 Snapshot，避免 AI 自我审查。
2. **对话压缩在包内部做**。编排层传全量 `Interview`，包在构建 prompt 时压缩：结构化字段始终全量带入；Interview 保留最近 N 轮原文 + 早期转结构化摘要，**阳性发现/关键体征不丢**。摘要 v1 用规则抽取，留 LLM summarizer 接口缝。

### 4.2 输出：4 个 Intent

```go
type InterviewResult struct {            // 问诊 agent
    Reply   string                       // 对患者说的话（追问 / 过场）
    Advance *AdvanceToTriage             // 非 nil = 问诊充分，请求进 triage
}
type AdvanceToTriage struct{ Subjective map[string]any }

type TriageChoice string
const (
    TriageConfirm   TriageChoice = "CONFIRM"
    TriageInterview TriageChoice = "INTERVIEW"
    TriageTest      TriageChoice = "TEST"
)
type TriageDecision struct {             // 收敛环三选一
    Decision            TriageChoice
    Diagnosis           *Diagnosis       // CONFIRM: name/basis/confidence
    MissingSubjective   []string         // INTERVIEW: 待采集项
    SubjectiveExhausted bool             // TEST: 自证约束
    Reason              string           // TEST: 为何必须检验
    TestItems           []string         // TEST: 检验项目
}

type PlanKind string
const (
    PlanMedication PlanKind = "MEDICATION"
    PlanTreatment  PlanKind = "TREATMENT"
    PlanAdviceOnly PlanKind = "ADVICE_ONLY"
    PlanReferral   PlanKind = "REFERRAL"
)
type TreatmentPlan struct {              // 处置四选一
    Plan               PlanKind
    Advice             string            // 无条件写入医嘱
    Medications        []Medication      // MEDICATION
    RequiredCapability string            // TREATMENT: 比对能力清单
    ReferralReason     string            // REFERRAL
}

type EmergencyInterrupt struct{ Reason string } // 急症守护
```

辅助类型：

```go
type DialogTurn    struct{ Role, Text string }              // Role: "patient" | "doctor"
type Diagnosis     struct{ Name, Basis string; Confidence float64 }
type Medication    struct{ Name, Dosage, Schedule string }
type TestResult    struct{ Item, Value string }
type VisitSummary  struct{ Diagnosis *Diagnosis; Medications []Medication }
type RefusalRecord struct{ What string; At time.Time }       // What: "test_pay" | "med_pay" ...
type Event         struct{ Kind string; Data any }           // Kind: "dialog" | "vital" | "test_result"
```

### 4.3 结构校验 `Validate()`

- `AdvanceToTriage`：`Subjective` 非空。
- `TriageDecision`：CONFIRM→Diagnosis 齐全且 `Confidence∈[0,1]`、Name 非空；INTERVIEW→`MissingSubjective` 非空；TEST→`SubjectiveExhausted==true` 且 `Reason`、`TestItems` 非空。
- `TreatmentPlan`：`Advice` 恒非空；MEDICATION→`Medications` 非空；TREATMENT→`RequiredCapability` 非空；REFERRAL→`ReferralReason` 非空。
- `EmergencyInterrupt`：命中时 `Reason` 非空。

### 4.4 RejectReason（编排层语义校验回传，镜像系统规格 §4.2）

```go
type RejectReason string
const (
    RejectNone                   RejectReason = ""
    RejectSchemaInvalid          RejectReason = "SCHEMA_INVALID"
    RejectIllegalTransition      RejectReason = "ILLEGAL_TRANSITION"
    RejectCapabilityMissing      RejectReason = "CAPABILITY_MISSING"
    RejectSubjectiveNotExhausted RejectReason = "SUBJECTIVE_NOT_EXHAUSTED"
    RejectRoundLimitFused        RejectReason = "ROUND_LIMIT_FUSED"
)
```

> `ROUND_LIMIT_FUSED` 是终态（编排层强制转诊、不再回调 AI），定义出来仅为完备。

---

## 5. LLMClient 抽象与 agent 执行骨架

### 5.1 LLMClient 接口（provider 中立、schema 驱动结构化输出）

```go
type LLMClient interface {
    // 给定 system + 对话消息 + 期望输出 JSON schema，返回符合该 schema 的结构化 JSON。
    // 只保证“结构化”，不做任何语义校验。
    Complete(ctx context.Context, req CompletionRequest) (CompletionResult, error)
}

type CompletionRequest struct {
    System   string          // 角色与约束（来自 prompts.go）
    Messages []Message       // 已压缩拼好的对话上下文
    Schema   OutputSchema    // 期望输出的 JSON schema
}
type Message      struct{ Role, Content string }          // Role: "user" | "assistant"
type OutputSchema struct{ Name string; JSON json.RawMessage }
type CompletionResult struct {
    Structured json.RawMessage // 符合 Schema 的 JSON → agent 反序列化进 intent
    Raw        string          // 原始文本，调试/日志用
}
```

将来真实实现的映射：Claude → 强制 tool-use（tool 参数即 schema，返回 tool input）；JSON-mode → `response_format`；fake → 直接吐预设 `Structured`。**推迟选型不影响 agent 逻辑**。

> **接入真实 provider 时的已知约束（来自最终审查）：** `buildMessages` 产出的首条"快照块"是 `user` 角色，紧随其后保留的最近对话若以患者轮（同样映射为 `user`）开头，会出现**连续两条 user 消息**；guardian 把事件作为 `user` 追加在末尾也是同理。fake 忽略角色故单测无碍，但 Anthropic 风格 API 不接受连续同角色消息。**真实 `LLMClient` adapter 必须合并连续同角色消息**（或把快照块并入首条 user 轮）。此外，schema-invalid 重试的纠正消息是把上次原始输出作为 user 文本回灌（provider 中立），而非作为 assistant/tool 轮重插——adapter 作者需知晓这一点。

### 5.2 agent 执行骨架（6 步，统一）

```
1. msgs := buildMessages(snapshot)        // 压缩 Interview + 注入结构化字段/Feedback
2. 选本 agent 的 system prompt + OutputSchema
3. res := llm.Complete(ctx, {System, msgs, Schema})
4. 反序列化 res.Structured → 具体 intent 结构体
5. intent.Validate()  // 结构校验
   └─ 不过 → 把校验错误作为纠正消息追加，内部 bounded 重试（≤K 次）
6. 返回 intent（K 次仍失败 → 返回 SchemaError）
```

两层 retry 边界：第 5 步 schema-invalid 是 **LLM 级**，agent 内部自纠（不出包）；编排层语义拒绝是**外环**，经下次 Snapshot 的 `Feedback.LastReject` 回传重决策。

### 5.3 上下文构建 `buildMessages`（`snapshot.go`）

- 首条 user 消息 = 紧凑的"就诊快照块"：Subjective / TestResults / Diagnosis / PriorVisit / Refusals / Feedback。
- 追加压缩后的 Interview：最近 N 轮原文作为 message；更早折叠成 digest 行并入快照块。
- digest v1 用规则抽取（结构化字段已覆盖大部分，digest 只保留尚未提升为 Subjective 的阴/阳性发现），留 LLM summarizer 接口缝。

---

## 6. 各 agent 职责与 prompt 要点（`prompts.go`，简体中文，不写硬编码红旗规则）

| Agent | 负责环节 | system prompt 要点 |
|---|---|---|
| 问诊 | 采集主观信息、对话追问（`StInterview`） | 一次只问最具鉴别力的 1 个问题；不下诊断；`MissingHint` 非空时优先补；判定充分则 `Advance` 附本轮结构化 subjective；口语化、患者可懂 |
| triage | **收敛环决策点（`StTriage`）** | **主观优先、检验是最后手段**；能确诊→CONFIRM(name/basis/confidence)；缺主观→INTERVIEW(指明缺项)；主观问尽仍不定→TEST(`subjective_exhausted=true`+reason+items)；`LastReject` 驱动改选；置信度自评 |
| 处置 | 确诊后定方案（`StTreatmentDecision`） | 四选一 + **advice 恒输出且不因患者态度改变**；用药给 name/dosage/schedule；治疗给 required_capability；转诊给 reason；`Refusals` 非空时 advice 注明执行状态与风险 |
| 急症守护 | 并行读全量信息流 | 读快照+最新 Event，按临床危急程度判断是否打断转急诊（AI 判断，非规则）；输出 hit+reason；命中即不可取消 |

**收敛环不变式**（系统中唯一循环）：triage 有两条入边（问诊 Advance、检验结果回填）、三个出口（CONFIRM 出环 / INTERVIEW 回问诊 / TEST 开检验）。检验回填后**回到 triage 而非直接确诊**，实现多轮检验。

---

## 7. 数据流

### 7.1 主回路（★ = 对本包的调用）

```
患者发消息
   │  StInterview
 ★ Interview(snapshot)
   ├─ Reply（继续追问）── 编排层下发 → 等下条消息 ↺
   └─ Advance(subjective) ── 编排层并入快照
                                   │  StTriage
                              ★ Triage(snapshot) ◀──────────────┐
              ┌────────────────────┼───────────────────────┐   │
          INTERVIEW                TEST                  CONFIRM │
        带 MissingHint        编排层语义校验(自证/熔断)     出环  │
        回 StInterview ↺      通过→检验卡→缴费→预约→取号   StDiagnosed
                              →采样→结果回填 ────────────────┘(回 Triage)
                                                              │ StTreatmentDecision
                                                         ★ Treatment(snapshot)
                                                       MED/TREAT/ADVICE/REFERRAL
                                                              │
                                                  线性收尾(编排层卡片)→医嘱→软结束
```

### 7.2 "拒绝→重决策"外环（方案 A 核心，每次 ★ 调用后套一圈）

```
 ★ agent.Decide(snapshot) ─→ intent
        │  编排层: Validate() 结构校验 + 语义校验
   ┌────┴─────┐
 通过        拒绝 → 写 snapshot.Feedback.LastReject → ★ 重调同一 agent（据 Feedback 改选）↺
 推进流程
```

例：Treatment 返回 `TREATMENT` 但本院无此能力 → `CAPABILITY_MISSING` → 重调 → 改 `REFERRAL`。AI 始终无状态。

### 7.3 横切回路（并行，独立于主 tick）

```
编排层独立 goroutine:  for ev := range events {
                          ★ hit, ok := Guardian.Assess(snapshot, ev)
                          if ok { 编排层抢占当前卡 → 结算 → StEmergency }
                       }
总计时 / 轮次熔断:  纯编排层，与本包无关
```

本包不碰并发，只保证 `Assess` 是纯函数、可被并发安全调用。

---

## 8. 错误处理

| 类别 | 来源 | 处理位置 | 是否浮出本包 |
|---|---|---|---|
| ① LLM 传输错误 | 网络/超时/限流/取消 | LLMClient 实现退避；agent 包装上抛 | 是，`ErrLLM` |
| ② schema-invalid 输出 | 反序列化失败 / `Validate()` 不过 | agent 内部 bounded 重试（≤K 次）；仍失败才上抛 | 仅 K 次耗尽后，`SchemaError` |
| ③ 语义拒绝 | 编排层校验 | 经下次 Snapshot 的 `Feedback.LastReject` 回灌重决策 | 否，从不是 error |

```go
var ErrLLM = errors.New("llm call failed")

type SchemaError struct {
    Agent    string  // "interview" | "triage" | "treatment" | "guardian"
    Attempts int
    LastRaw  string
    Cause    error
}
```

**关键策略：**

- **ctx 贯穿所有 LLM 调用**：急症抢占 / 超时 cancel ctx，agent 立即中止（含中止内部重试），返回 `ctx.Err()`。这是横切机制能打断慢 LLM 调用的前提。
- **错误后兜底是编排层的事**：`Decide` 返回 error 时本包不替编排层决定转诊/重试，只给清晰 error；编排层按规格处置（持续失败可视作"本院无法闭环"→转诊终态）。
- **置信度阈值不在 `Validate()` 里**：`Validate()` 只查 `Confidence∈[0,1]`。是否对低置信度 CONFIRM 设硬门槛（系统规格 §8 `ConfidenceThreshold`，默认 0.0=关）属语义校验，归编排层。
- **guardian 失败方向**：`Assess` 出错返回 `(_, false, err)`，**不臆造打断**（命中是医疗判断，基础设施故障不是）。文档建议：编排层应把"guardian 持续不可用"当作降级安全状况处理（如升级转诊）——此安全策略归运营/编排，本包不硬编码。

**边界情况：**

- 问诊 `Advance` 但 `Subjective` 为空 → `Validate` 不过 → 内部重试（②）。
- triage TEST 缺 `subjective_exhausted`（②，字段没填）vs 编排层 `SUBJECTIVE_NOT_EXHAUSTED`（③，填了但不认）是两件事。
- fake LLM 脚本化、确定性，单测稳定无抖动。

---

## 9. 测试策略

### 9.1 可编程 fake LLM（`llm_fake.go`）

```go
type FakeLLM struct {
    On func(req CompletionRequest) (CompletionResult, error)
}
func StructuredOf(v any) CompletionResult // 把 intent 结构体 marshal 成 Structured
```

每条用例可：① 断言 `req.System`/`Messages`/`Schema`（如"counters 未出现""MissingHint 已注入"）；② 返回构造输出驱动分支。确定性、不打真实 API。

### 9.2 表驱动单测（每个 agent）

- **triage**：fake 返回三选一 → 断言 intent 正确且 `Validate` 通过；返回非法 → 断言内部重试 K 次后 `SchemaError`；"先错后对" → K 次内自纠成功。
- **interview**：Reply vs Advance；`Feedback.MissingHint` 非空时断言注入 messages。
- **treatment**：四种 plan；断言 `Advice` 恒非空；`Refusals` 非空时 advice 反映执行状态。
- **guardian**：命中/不命中；出错返回 `(_, false, err)`。
- **ctx 取消**：模拟超时/抢占 → 断言中止内部重试、立即返回 `ctx.Err()`。

### 9.3 mock 编排 harness + 急性咽炎 walkthrough（`internal/harness`）

- harness = 极简确定性状态机，实现 §3 够走通主干的转移，调用 `DecisionLayer`/`Guardian`，做结构校验 + 桩化语义校验 + 拒绝→重决策外环；执行层桩成功。**非真实编排层**（无卡片/saga/持久化），兼作可运行 demo。
- **主 walkthrough** 复刻原型：嗓子痛发烧 → 问诊追问 → Advance → triage=TEST(血常规) → 桩化回填 → triage=CONFIRM(急性咽炎) → treatment=MEDICATION → 落医嘱/软结束，逐步断言。
- **变体**：① 双轮检验（验回到 triage）；② guardian 中途命中→StEmergency；③ TREATMENT 能力不具备→CAPABILITY_MISSING 重决策→REFERRAL；④ defer 挂起后重弹；⑤ 复诊（带 PriorVisit 重入 StInterview）。

### 9.4 显式覆盖不变式

| 不变式 | 怎么测 |
|---|---|
| 检验回填回到 triage（多轮检验） | walkthrough 变体①，断言 Triage 二次调用 |
| advice 恒输出、不因态度改变 | treatment 四路全断言 Advice 非空 |
| AI 无状态 | 同一 agent 连续两次不同 snapshot，断言无状态泄漏 |
| counters 不喂 AI | 断言 `buildMessages` 输出不含轮次信息 |
| 自证约束结构强制 | triage TEST 缺字段 → 内部重试 |

### 9.5 诚实边界

fake 脚本化，**测不了 prompt 临床质量**（主观优先 / 不堆砌问题 / 危急识别准不准）。这类行为/质量由**可选真实 LLM 集成测试**覆盖（env 开关 `MEDAGENT_LLM_E2E=1`，默认跳过，待选定 provider 后启用）。确定性单测只保证结构、控制流、错误处理、契约正确。

---

## 10. 配置项

本包自身（建议常量或注入）：

```go
const (
    InterviewRawTurns = 6 // buildMessages 保留的最近原文轮数（更早转 digest）
    SchemaRetryMax    = 2 // schema-invalid 内部重试次数 K
)
```

属编排层、本包不持有：`InterviewRoundsMax` / `TestRoundsMax` / `VisitTimeout` / `ConfidenceThreshold`（系统规格 §8）。

---

## 11. 实现顺序建议

1. **契约与类型**：`contract.go` / `intent.go`（含 `Validate()`）/ `snapshot.go` 骨架。先 TDD `Validate()`。
2. **LLM 抽象 + fake**：`llm.go` / `llm_fake.go`。
3. **上下文构建**：`buildMessages` + 压缩，TDD（含"counters 不喂"断言）。
4. **4 个 agent + prompts**：逐个 TDD（控制流 / 内部重试 / ctx 取消）。
5. **mock harness + walkthrough**：端到端打通主干与变体。
6. **可选真实 LLM 集成测试**：选定 provider 后接入（env 开关）。

---

## 附：与系统规格的对应

- 系统规格 §2.2 收敛环 → 本设计 §6 triage + §7.1。
- 系统规格 §4.2 意图 schema / 返回值 → 本设计 §4.2 / §4.4。
- 系统规格 §4.3 上下文重建与压缩 → 本设计 §4.1 取舍 2 + §5.3。
- 系统规格 §5 横切机制 → 本设计 §7.3（guardian 纯函数部分归本包）。
- 系统规格 §7 MCP 接口清单 → 本设计 §3.1 接口（方案 A 下 MCP 框架退化为 Go 接口契约）。
