# 无人医院患者端 AI 系统 · 实现规格

> 概念性项目。本文档是面向实现的设计规格，供 Claude Code 据此分阶段落地。
> 配套图：`无人医院_患者端_交互原型.html`（页面流）、`unmanned_hospital_patient_flow_v2_no_registration.svg`（页面流程图）、`无人医院_AI架构_数据流.mermaid`（后端组件/数据流）。

---

> ## ⚠️ 实现现状批注（2026-06-26）
>
> **本文是最初的完整系统设计（方案 A），不再等于当前代码的真实形态。** 项目后来按需求转向了一个**最小暴露面的 `medagent` HTTP 服务**：把决策层 + 一套**固定、简化的编排**收进单一 Go 模块（唯一公开包 `medagent`，底层在 `internal/`），缴费/卡片/补偿等"执行与编排"职责下放给接它的后端或暂未实现。
>
> 当前项目说明见仓库根 **`README.md`**；后端接入见 **`docs/后端接入指南.md`**。
>
> 与本规格的逐项对照：
>
> | 规格章节 | 现状 |
> |---|---|
> | §2.2 收敛环三选一、主观优先、TEST 自证约束 | ✅ 忠实实现（`internal/ai`）|
> | §4.1 四 agent（问诊/triage/处置/急症守护） | ✅ 实现 |
> | §4.2 意图 schema + reject_reason | ✅ 类型一致；处置已改**三选一**（删 TREATMENT），reject 去掉 `CAPABILITY_MISSING` |
> | §4.3 上下文压缩、医嘱无条件生成 | ✅ 实现 |
> | §7 AI 经 **MCP** 提交意图 | ❌ 改为进程内 tool-use，无 MCP |
> | §2.3 两类**卡片**（可挂起/强制终结、defer） | ❌ 未实现，坍缩为 HTTP `Step` |
> | §0/§1 真相来源=**独立后端编排层** | ⚠️ 编排+会话状态搬进 `medagent` 模块内部（内存 session）|
> | §3 ~18 细粒度状态（缴费/预约/取号/取药/软结束） | ⚠️ 简化为 8 个 phase；执行环节下放后端（`NEED_TESTS`/`PURCHASE`）|
> | §6 副作用账本 + **Saga 补偿** | ❌ 未实现（购药仅记 Refusals，缴费/退款属后端）|
> | §5.1 急症抢占→走结算 Saga | ⚠️ 守护命中→`EMERGENCY`+关会话，但无结算 Saga |
> | §5.2 总计时 `VisitTimeout`→强制转诊 | ❌ 仅闲置 `SessionTTL` 回收 |
> | §2.2/§5.3 轮次撞顶→强制 `StReferral` | ⚠️ 撞顶返回 `ErrUpstream"未收敛"`+关会话，非强制转诊 |
> | §3.4 `StSoftEnd` 软挂起→复诊 | ⚠️ 改由后端 `Start(initial=false, prior=[…])` 注入历史 |
> | §8 配置（6/2/30min/置信阈值） | ⚠️ 实为 20/10/6 + `SessionTTL` 30min，无置信阈值 |
> | §3.3 TREATMENT 院内治疗 + 能力清单 | ➖ **已删除**（院内治疗执行难闭环）；处置三选一 MEDICATION/ADVICE_ONLY/REFERRAL，移除 `required_capability` 与 `Caps` |
> | —（规格未含）— | ➕ 新增**药品规格查询轮**（`DRUG_QUERY`）+ 处方量改"购买盒数"（盒数<1 兜底为 1 + warn）|

---

## 0. 设计前提（不可动摇的约束）

1. **概念性项目，假设 AI 医疗决策正确度不低于人类**，故诊断、处置、是否转诊等关键医疗决策完全交由 AI；本文档不引入任何硬编码的医学红旗规则。
2. **AI 只发起意图，不直接执行副作用**。AI 通过 MCP 提交带 schema 的 typed intent，后端校验后编排卡片数据下发前端，由用户在卡片上操作。
3. **就诊状态的唯一真相来源是后端编排层**，AI 无状态，每次调用从就诊快照重建上下文。
4. 三层职责严格分离：**信息层（医嘱）无条件生成；执行层（缴费/取药/治疗）以用户卡片操作为准；医疗判断本身不因患者态度而改变。**

> 注意：上述"关键决策交给 AI"仅指医疗判断。**设施性事实**（本院是否具备某治疗能力）与**流程性约束**（合法转移、轮次熔断、副作用结算）仍是确定性代码，这不与前提冲突——它们不是医学判断。

---

## 1. 系统分层

| 层 | 职责 | 是否含 AI | 是否确定性 |
|---|---|---|---|
| 前端 | 对话 UI、卡片渲染、采集用户卡片操作 | 否 | — |
| 编排层（核心） | 会话状态机、意图校验、卡片编排、副作用账本与补偿、总计时 | 否 | 是（真相来源） |
| AI 决策层 | 问诊对话、`triage_decide` 收敛判断、处置决策 | 是（无状态） | 否 |
| 急症守护 | 并行读取全量信息流，命中即高优先级打断 | 是（并行 agent） | 触发确定性 |
| 执行/能力层 | 缴费、预约/取号、药房/配送/提醒、退款 | 否 | 是（幂等+可补偿） |

### 主数据回路（正常前进）

```
用户消息/卡片操作 → 编排层(状态机)
  → 喂就诊快照, 召唤/接收 AI 意图
  → AI 提交 typed intent (选择 + 结构化参数)
  → 编排层校验①合法转移 ②设施能力 ③自证约束
  → 卡片编排器填充(金额/科室/排队人数/取药方式)
  → 下发前端渲染
  → 用户在卡片上操作(确认/拒绝/选择)
  → 编排层据卡片事件推进 + 触发执行层动作
  → 执行结果回填快照
```

### 横切回路（并行，独立于主流程 tick）

```
急症守护: 持续读事件流 → 命中 → 高优先级打断信号 → 抢占当前卡 → 走结算 → 终态[前往急诊]
总计时器: 独立 ticker → 超时 → 熔断升级 → 终态[转大医院]
```

---

## 2. 核心抽象

### 2.1 就诊会话与快照

AI 无状态，每次调用前由编排层从快照重建上下文。

```go
type VisitSession struct {
    ID           string
    State        VisitState         // 见 §3 状态枚举
    Snapshot     VisitSnapshot      // 喂给 AI 的全量上下文
    Ledger       SideEffectLedger   // 副作用账本，见 §6
    Counters     RoundCounters      // 轮次计数，熔断用
    PendingCard  *Card              // 当前阻塞卡（nil 表示无阻塞）
    CreatedAt    time.Time
    DeadlineAt   time.Time          // 总计时截止
}

type VisitSnapshot struct {
    Interview      []DialogTurn     // 问诊对话历史（可能被压缩，见 §5.3）
    Subjective     map[string]any   // 已采集的主观信息（主诉/病史/体征）
    TestResults    []TestResult     // 已回填的检验结果
    Diagnosis      *Diagnosis       // 确诊结果（出收敛环后填充）
    TreatmentPlan  *TreatmentPlan   // 处置决策
    PriorVisit     *VisitSummary    // 复诊时携带的上次就诊摘要（诊断+用药）
    Refusals       []RefusalRecord  // 患者拒绝记录（如拒绝缴费），进档
}

type RoundCounters struct {
    InterviewRounds int // 回问诊次数
    TestRounds      int // 开检验次数
}
```

### 2.2 收敛环不变式（系统中唯一的循环）

整个系统只有一个反馈循环：决策节点 `triage_decide`。它有**两条入边**（问诊回答、检验结果回填）和**三个出口**，反复执行直到吐出"确诊"。这是 AI 密集区的全部——一个 agent、一份三选一输出 schema、循环调用。

```
triage_decide(snapshot) -> 三选一:
  ┌─ CONFIRM   能确诊(置信度达标)        → 出环, 进入处置决策
  ├─ INTERVIEW 不能确诊 + 缺主观信息      → 回问诊(指明缺哪些)      ← 优先手段
  └─ TEST      不能确诊 + 主观已尽仍不确定 → 开检验(指定项目)        ← 最后手段
```

约束：
- **主观信息优先，检验是最后手段。** 主判断交给 AI（prompt 写明优先级）；后端用**自证约束**兜底：当输出为 `TEST` 时，schema 强制要求 `subjective_exhausted=true` 且附 `reason`，否则拒绝出检验卡（见 §5.2）。
- **轮次熔断（确定性，后端强制）：** 当 `TEST` 但 `TestRounds >= TestRoundsMax`，或 `INTERVIEW` 但 `InterviewRounds >= InterviewRoundsMax` 时，**后端覆盖 AI 输出，强制转大医院**（不提示 AI"该转了"，直接熔断）。出口复用转诊终态——"反复查/问仍不确定"本就是"本院无法闭环"的语义。

### 2.3 卡片的两种语义（必须在类型上区分）

```go
type CardKind int
const (
    CardDeferrable    CardKind = iota // 可挂起型：可"暂不决定"→解锁对话→稍后重来
    CardTerminating                   // 强制终结型：仅有限出口, 无 defer; 未操作则原地阻塞
)
```

- **可挂起型（如"是否检验"卡）：** 带"暂不决定"按钮。defer 后进入"自由对话但欠一张卡"的挂起态，AI 继续对话；准备好后重新下发。defer 期间 AI 可提交**覆盖**该 pendingCard 的新意图（如聊完不查了），不机械重弹。
- **强制终结型（缴费卡）：** **不带"暂不决定"**。只有两个出口（如"付款 / 拒绝"），两个出口都推进流程；**卡片事件未结束（既未付也未拒）则不推进，主流程原地阻塞。**
- **阻塞不冻结横切：** 主流程在强制终结型卡上阻塞期间，急症守护与总计时仍并行运行，可随时抢占/熔断。守护与计时必须用独立 ticker，**不得依赖主流程 tick**。

---

## 3. 状态机规格

### 3.1 状态枚举

```go
type VisitState int
const (
    StInterview      VisitState = iota // 问诊采集（收敛环入口）
    StTriage                           // triage_decide 决策点（瞬时态）
    StTestDecision                     // "是否检验"卡（可挂起型阻塞）
    StTestPay                          // 检验缴费（强制终结型阻塞）
    StTestBooking                      // 检验预约 科室/时间
    StTestQueue                        // 检验取号排队（到场）
    StDiagnosed                        // 确诊卡（出环）
    StTreatmentDecision                // AI 处置决策（pull 召唤）
    // —— 处置分支（线性，无 AI 往返）——
    StMedPay                           // 药品缴费（强制终结型阻塞）
    StMedPickup                        // 取药方式：到院自取/配送
    StMedQueue                         // 药房取号排队（仅自取）
    StTreatPay                         // 治疗缴费（强制终结型阻塞）
    StTreatConfirm                     // 治疗预约确认
    StAdvice                           // 医嘱卡（全路径共用收尾）
    StSoftEnd                          // 软结束（可被新输入唤醒 → 复诊）
    // —— 终态 ——
    StReferral                         // 转大医院（不具备/超时/熔断）
    StEmergency                        // 前往急诊（急症抢占）
)
```

### 3.2 收敛环与确诊段转移

| 当前状态 | 事件/意图 | 校验 | 下一状态 |
|---|---|---|---|
| StInterview | 用户消息 | — | StTriage |
| StTriage | intent=INTERVIEW | InterviewRounds<Max | StInterview（计数+1）|
| StTriage | intent=INTERVIEW | InterviewRounds>=Max | **StReferral（熔断）** |
| StTriage | intent=TEST | subjective_exhausted=true 且 TestRounds<Max | StTestDecision（计数+1）|
| StTriage | intent=TEST | TestRounds>=Max | **StReferral（熔断）** |
| StTriage | intent=CONFIRM | 置信度字段齐全 | StDiagnosed |
| StTestDecision | 卡:同意检验 | — | StTestPay |
| StTestDecision | 卡:不查 | — | StDiagnosed |
| StTestDecision | 卡:暂不决定(defer) | — | StInterview（挂起 pendingCard）|
| StTestPay | 卡:付款 | — | StTestBooking |
| StTestPay | 卡:拒绝 | — | StDiagnosed（跳过检验）|
| StTestBooking | 选定科室/时间 | — | StTestQueue |
| StTestQueue | 采样完成 → 结果回填 | — | **StTriage**（回到收敛环！）|
| StDiagnosed | 自动 | — | StTreatmentDecision |

> 关键：`StTestQueue` 结果回填后回到 `StTriage` 而非直接确诊——这实现了"多轮检验"。`triage_decide` 拿到新检验结果重新判断，可能再确诊、再问诊、或再开检验（受轮次熔断约束）。

### 3.3 处置决策与线性收尾

`StTreatmentDecision` 是 pull 式：编排层进入此态后回调处置 agent，等待 `submit_treatment_plan` 意图。

```
submit_treatment_plan -> 四选一:
  MEDICATION 用药   → StMedPay → StMedPickup → (自取:StMedQueue) → StAdvice
  TREATMENT  治疗   → 设施能力校验:
                        具备   → StTreatPay → StTreatConfirm → StAdvice
                        不具备 → StReferral（确定性, 见下）
  ADVICE_ONLY 仅医嘱 → StAdvice
  REFERRAL   转诊   → StReferral
```

| 当前状态 | 事件/意图 | 校验 | 下一状态 |
|---|---|---|---|
| StTreatmentDecision | plan=MEDICATION | schema 齐全 | StMedPay |
| StTreatmentDecision | plan=TREATMENT | **本院能力清单具备** | StTreatPay |
| StTreatmentDecision | plan=TREATMENT | **不具备** | StReferral |
| StTreatmentDecision | plan=ADVICE_ONLY | — | StAdvice |
| StTreatmentDecision | plan=REFERRAL | — | StReferral |
| StMedPay | 卡:付款 | — | StMedPickup |
| StMedPay | 卡:拒绝 | — | StAdvice（跳过取药）|
| StMedPickup | 到院自取 | — | StMedQueue |
| StMedPickup | 配送到家 | — | StAdvice |
| StMedQueue | 取药完成 | — | StAdvice |
| StTreatPay | 卡:付款 | — | StTreatConfirm |
| StTreatPay | 卡:拒绝 | — | StAdvice（跳过预约）|
| StTreatConfirm | 确认 | — | StAdvice |
| StAdvice | 自动 | — | StSoftEnd |

> **医嘱是全路径共用收尾。** 无论用药/治疗/仅医嘱，结束时都落一张 `StAdvice` 医嘱卡，且不因患者态度而改。处置分支只决定医嘱里写什么 + 是否挂执行动作。缴费卡的"拒绝"只跳过执行层动作（不买药/不预约），医嘱照出，并在医嘱中标注执行状态（已购药/未购药/已预约治疗/未治疗）+ 风险与注意事项。

### 3.4 软结束与复诊

- `StSoftEnd` 是**软挂起**而非硬终止：输入框仍可用。
- 收到新输入 → 唤醒 → **重入 `StInterview`**（不是回到"是否检验"），携带 `PriorVisit`（上次诊断+用药）作为快照上下文。
- 复诊先问主观信息；是否需要检验交由 `triage_decide` 按病情判断（同一条收敛环不变式）。

---

## 4. AI 决策层

### 4.1 Agent 角色

| Agent | 触发方式 | 输入 | 输出意图 |
|---|---|---|---|
| 问诊 agent | push（自发判断对话充分度后开口） | 快照 + 最新用户消息 | `advance_to_triage`（携带本轮采集到的主观信息）|
| triage agent | pull/push 混合（每条新信息后跑） | 快照 | `triage_decide`（CONFIRM/INTERVIEW/TEST）|
| 处置 agent | pull（进入 StTreatmentDecision 后回调） | 快照（含确诊） | `submit_treatment_plan` |
| 急症守护 | 并行常驻 | 全量事件流 | `emergency_interrupt`（见 §6.1）|

> 问诊 agent 与 triage agent 可由同一模型承载，区别在 prompt 与输出 schema。push/pull 在 MCP 接口层统一为"提交 typed intent"，触发时机差异由编排层管理。

### 4.2 意图 schema（MCP 工具集）

每个意图自带参数 schema 与返回值。被拒绝时返回结构化原因，AI 据此重试或改选。

```jsonc
// advance_to_triage —— 问诊充分, 请求进入决策
{ "subjective": { /* 本轮采集的主诉/病史/体征 */ } }

// triage_decide —— 收敛环三选一
{
  "decision": "CONFIRM | INTERVIEW | TEST",
  // decision=CONFIRM:
  "diagnosis": { "name": "...", "basis": "...", "confidence": 0.0-1.0 },
  // decision=INTERVIEW:
  "missing_subjective": ["还需采集的主观信息项"],
  // decision=TEST（自证约束）:
  "subjective_exhausted": true,
  "reason": "为何主观信息已问尽、必须借助检验",
  "test_items": ["血常规", ...]
}

// submit_treatment_plan —— 处置四选一
{
  "plan": "MEDICATION | TREATMENT | ADVICE_ONLY | REFERRAL",
  "advice": "无条件写入医嘱的内容（休息/饮水/观察/风险）",
  // plan=MEDICATION:
  "medications": [{ "name": "...", "dosage": "...", "schedule": "..." }],
  // plan=TREATMENT:
  "required_capability": "用于比对本院能力清单的能力标识",
  // plan=REFERRAL:
  "referral_reason": "..."
}
```

意图返回值（编排层 → AI）：

```jsonc
{
  "accepted": true | false,
  "reject_reason": "SCHEMA_INVALID | ILLEGAL_TRANSITION | CAPABILITY_MISSING | SUBJECTIVE_NOT_EXHAUSTED | ROUND_LIMIT_FUSED",
  "next_expected": "编排层期望 AI 下一步产出的意图类型（pull 场景）",
  "card_deferred": true | false // 上一张可挂起卡是否被 defer, AI 据此调整对话
}
```

### 4.3 上下文重建与压缩

- AI 每次调用从 `VisitSnapshot` 重建上下文；`Subjective`/`TestResults`/`Diagnosis` 为结构化字段，始终全量带入（信息密度高、体量小）。
- `Interview` 对话历史可能拉长，需压缩：保留最近 N 轮原文 + 早期对话的**结构化摘要**（已确认的主诉、体征、阴性发现）。**关键体征/阳性发现不得在压缩中丢失**——压缩目标是去冗余措辞，不是丢临床信息。
- 复诊时 `PriorVisit` 以摘要形式带入，不展开上次完整对话。

---

## 5. 横切机制

### 5.1 急症守护（并行 agent）

```go
// 独立 goroutine, 常驻, 不依赖主流程 tick
func (g *Guardian) Watch(ctx context.Context, sess *VisitSession, events <-chan Event) {
    for ev := range events {                 // 订阅: 对话/体征/检验结果
        if hit := g.assess(sess.Snapshot, ev); hit {
            sess.Interrupt(EmergencyInterrupt{ // 高优先级, 不可取消
                Reason: hit.Reason,
            })
            return
        }
    }
}
```

- 抢占语义：打断信号进来 → 抢占当前阻塞卡 → **走一遍退出结算 Saga**（结算未完成的副作用，见 §6）→ 落 `StEmergency`。
- 不可取消：与"主动退出"不同，急症打断不给用户取消机会。
- 复用结算：急症打断 ≠ 简单跳转，它复用退出结算的补偿逻辑，只是触发源不同。

### 5.2 总计时超时

- 整次就诊单一总计时（`DeadlineAt`），独立 ticker。
- 超时 → 熔断升级 → 走结算 → 落 `StReferral`（转大医院）。

### 5.3 轮次熔断

- 见 §2.2 / §3.2：`TestRounds`/`InterviewRounds` 撞顶 → 后端强制 `StReferral`。确定性，不交 AI 自觉。

---

## 6. 副作用与补偿（Saga）

### 6.1 副作用账本

每发生一个带副作用的动作即记账；结算时按账本逐项补偿。

```go
type SideEffectLedger struct {
    Entries []LedgerEntry
}
type LedgerEntry struct {
    Action   string    // "test_pay" | "test_done" | "med_pay" | "med_pickup" | "deliver" | "treat_pay" | "treat_done"
    Amount   int        // 涉及金额（分）
    Done     bool       // 是否已实际执行（区分"已缴未执行"与"已执行"）
    At       time.Time
}
```

### 6.2 补偿表（退出 / 急症 / 超时 三者共用）

| 已发生动作 | 结算处理 |
|---|---|
| 未缴费 / 未执行 | 直接结束或转移，无需结算 |
| 已缴费、未执行 | 原路退款 |
| 已执行检验 / 治疗 | 费用照收，结果留档 |
| 已取药 / 已配送 | 按退药政策处理 |

> 主动退出走相同补偿表，但允许用户取消退出（回到原阻塞卡）；急症/超时不可取消。

### 6.3 留档

- 风险告知、患者拒绝记录（`Refusals`）、未执行的处置建议，均写入快照并随就诊快照持久化。复诊时作为 `PriorVisit` 上下文的一部分。

---

## 7. MCP 接口清单（AI 侧可见的全部能力）

AI 仅能调用以下意图端点，不能直接触达执行层：

- `advance_to_triage`
- `triage_decide`
- `submit_treatment_plan`
- `emergency_interrupt`（仅守护 agent 可用）

所有缴费、预约、取号、退款、发药等动作**不是 MCP 工具**，而是编排层在用户卡片操作后调用的内部执行能力。

---

## 8. 配置项（待确认）

```go
const (
    InterviewRoundsMax = 6 // 问诊回合上限 —— 待确认，问诊成本低可宽松
    TestRoundsMax      = 2 // 检验回合上限 —— 待确认，每轮含缴费+到场排队, 偏保守
    VisitTimeout       = 30 * time.Minute // 总计时 —— 待确认
    ConfidenceThreshold = 0.0 // 确诊置信度阈值 —— 待确认（概念项目可由 AI 自判, 留作可选硬门槛）
)
```

> 这四个数请确认/调整后写死为常量或外置配置。轮次上限撞顶一律走转诊。

---

## 9. 建议实现阶段

- **Phase 1（核心闭环）：** 状态机 + 收敛环（problem→triage→确诊→处置→医嘱→软结束）+ 两类卡片 + typed intent 校验。先用 mock 执行层（缴费/检验直接置成功），跑通主干与复诊。
- **Phase 2（横切）：** 急症守护并行 agent + 总计时 + 轮次熔断 + 退出结算 Saga 与补偿表。
- **Phase 3（持久化与上下文）：** 就诊快照持久化、断点续跑（检验排队/治疗预约这几个等待点）、对话压缩。等待点确实需要断点续跑时再评估引入 Temporal；Phase 1/2 一个顺序执行器 + 状态持久化即可。

---

## 附：与页面流程图的对应关系

- 图中蓝色"是否检验?/缴费"阻塞卡 → §2.3 两类卡片；缴费卡为强制终结型。
- 图中紫色"AI 处置决策" → §3.3 `submit_treatment_plan`。
- 图中橙黄"检验取号排队/药房取号" → §3 到场执行节点（仅检验采样、到院自取需排队）。
- 图中橙红横切"急症/超时/退出" → §5、§6 横切机制与 Saga。
- 图中绿色"结束后继续输入=自动复诊"回边 → §3.4，落点由"是否检验"修正为"问诊"。
