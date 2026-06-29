# medagent · 项目规格

本文档定义 medagent 的核心概念、术语与设计约束，是理解整个系统的入口。
接入细节见 [后端接入指南.md](后端接入指南.md)。

---

## 1. 项目定位

medagent 是一个**无人医院患者端 AI 诊疗服务**——把 AI 医疗决策封装为最小暴露面的 HTTP 模块。

核心假设：**概念性项目，AI 医疗决策正确度不低于人类**。诊断、处置、是否转诊等关键医疗判断完全交由 AI；系统不引入硬编码的医学红旗规则。

系统边界：**AI 只产出结构化决策，不执行副作用**。检验、购药、缴费等实际动作由接入方后端在收到对应指令后驱动，结果回填给服务续跑。

---

## 2. 核心概念

### 2.1 会话（Session）

一次就诊对应一个 Session，由 `session_id` 唯一标识。Session 在内存中按 ID 路由，带 TTL 自动回收（默认 30 分钟无活动）。持久化由后端负责。

复诊通过 `initial=false` + 回传历史 `SessionRecord` 数组实现——服务将历次摘要渲染后喂给 AI 以了解病史。

### 2.2 单 Agent 工具循环（Tool Loop）

系统的决策引擎是一个**单 agent 工具循环**：LLM 每步必选恰好一个工具（`tool_choice=required`），循环执行直至命中终态工具。

六个工具构成完整的就诊能力集：

| 工具 | 作用 | 类型 |
|---|---|---|
| `ask_patient` | 继续问诊，追问一个问题 | 边界工具（让出给后端） |
| `order_test` | 开检验（仅支持血常规） | 边界工具 |
| `query_drug_spec` | 查药品每盒规格 | 边界工具 |
| `purchase_drug` | 下购药单（盒数） | 边界工具 |
| `finish` | 就诊收尾（诊断 + 处置 + 医嘱） | 终态工具 |
| `refer` | 转诊到上级医院 | 终态工具 |

**边界工具**调用即让出控制权给后端执行，后端回填 `tool_result` 后循环续跑。**终态工具**触发即结束循环。

### 2.3 Step 与 StepKind

Step 是服务对外输出的统一指令结构，`kind` 字段告知后端下一步做什么：

| `kind` | 语义 | 对应工具 |
|---|---|---|
| `ASK` | 医生追问，展示给患者 | `ask_patient` |
| `NEED_TESTS` | 需要检验 | `order_test` |
| `DRUG_QUERY` | 需查药品规格 | `query_drug_spec` |
| `PURCHASE` | 需购药（含盒数） | `purchase_drug` |
| `DONE` | 就诊完成，含完整结果 | `finish` |
| `EMERGENCY` | 急症打断，会话关闭 | 急症守护触发 |
| `OK` | 体征正常（仅 `/vitals` 返回） | — |

### 2.4 处置三选一（Plan）

AI 的处置决策有三种出口，不含院内治疗执行（缴费/操作无法闭环）：

| Plan | 含义 |
|---|---|
| `MEDICATION` | 开药购买 |
| `ADVICE_ONLY` | 仅医嘱 |
| `REFERRAL` | 转诊到上级医院 |

### 2.5 就诊终态（Final）

`Result.final` 表示就诊最终归宿：

- `ADVICE`：给出医嘱/用药即结束（涵盖 MEDICATION 和 ADVICE_ONLY）
- `REFERRAL`：需转诊

### 2.6 急症守护（Guardian）

并发运行的独立判断通道。每次推进轮（patient-say / test-results / drug-info / purchase-result）与主决策并发执行；命中即返回 `EMERGENCY` 并关闭会话。

守护错误 fail-open：守护自身 LLM 调用出错时不打断主流程，诊疗照常继续。

`/vitals` 端点用于主动上报体征（如监护仪数据），独立于主流程。

---

## 3. 就诊流程模型

```
POST /sessions                      → {session_id}
  └─ 循环 POST /patient-say：
       ASK         → 展示给患者，等下一句
       NEED_TESTS  → 后端检验 → POST /test-results
       DRUG_QUERY  → 后端查库 → POST /drug-info
       PURCHASE    → 后端购买 → POST /purchase-result
       EMERGENCY   → 转急诊，会话关闭
       DONE        → 诊疗完成 → GET /record 导出 → DELETE 销毁
```

关键路径：**问诊采集 → 收敛诊断 → 处置决策 → 开药流程 → 收尾**。

开药流程的固定序列：`DRUG_QUERY`（查规格） → `PURCHASE`（按盒下单） → `DONE`（据购买结果出最终医嘱）。这确保处方量是可计量的盒数而非抽象片数。

---

## 4. 问诊与收敛策略

AI 在工具循环中自主驱动问诊与收敛。核心守则：

1. **主观信息优先，检验是最后手段**——先通过问诊穷尽症状/病史/体征，再考虑开检验。
2. **一次只问一个问题**——选鉴别诊断价值最高的问题，口语化、患者能听懂。
3. **检验仅支持血常规**——需其他检查或本院无法开展的操作/手术，直接转诊。
4. **医嘱无条件给出**——不因患者态度改变（拒绝购药/检验时在医嘱注明风险）。

收敛由 AI 在循环中自判，系统以 step 预算护栏兜底（每会话 agent 决策步数 ≤40，单次推进内部纠正 ≤8）。

---

## 5. 上下文管理

### 5.1 快照驱动

AI 无状态。每次调用时，编排层将当前会话状态构造为 Snapshot 传入——包含对话历史、患者资料、检验结果、复诊历史等全量上下文。

### 5.2 自动压缩

上下文 token 占用超过模型窗口的指定比例（默认 60%）时，自动触发 LLM 压缩：将历史对话凝缩为结构化摘要，保留关键体征和阳性发现。

### 5.3 复诊上下文

复诊时历史 `SessionRecord` 被预渲染为摘要文本注入 Snapshot，使 AI 了解既往诊断与用药而不展开完整原始对话。

---

## 6. 架构分层

```
agent/                  唯一公开包 · facade（Service + HTTP Handler）
  ├── 会话管理          Session 生命周期、TTL 回收
  ├── HTTP 端点         8 个 REST 端点
  ├── 急症守护桥接      并发调度 Guardian
  └── 会话纪要导出      SessionRecord

internal/
  ├── ai/               AI 决策层 · 单 agent 工具循环引擎 + Guardian + 上下文压缩
  ├── openaicompat/     LLM adapter · OpenAI 兼容协议（DeepSeek/通义千问/任意兼容端点）
  ├── consultlog/       诊疗日志 · 按 sessionID 的 JSONL 调用审计
  └── envfile/          .env 文件加载

cmd/
  ├── server/           生产 HTTP 服务入口
  ├── consult/          模拟患者 demo
  └── smoke/            LLM 烟雾测试
```

依赖方向单向：`agent → internal/{ai, openaicompat, consultlog}`。`internal/` 外部不可见、不可直接调用。

---

## 7. 设计约束

1. **Go 1.22，零外部依赖**——仅标准库（HTTP 用 `net/http` 1.22 增强路由）。
2. **LLM 必须支持多工具 function calling**——agent 循环用 `tool_choice=required`；急症守护用单工具强制 tool-use。
3. **唯一公开包 `agent`**——所有对外交互通过 HTTP/JSON 或 Go 库调用；决策内部细节封装在 `internal/`。
4. **会话内存持有**——无内置持久化；重启丢失，持久化由后端通过 `/record` 导出后自行负责。
5. **日志含医疗数据**——落点权限与脱敏由后端决定。

---

## 8. 职责划分

### 本模块负责

AI 问诊/收敛/处置/急症守护决策、单 agent 工具循环编排、多轮会话与 TTL、查药规格与购药闭环、初诊/复诊上下文、会话纪要导出、调用日志审计、step 预算护栏、上下文压缩。

### 后端负责

检验子系统、药品库查询/药房/支付、初诊/复诊判定、UI 与交互、会话持久化、鉴权网关、业务级超时/熔断。

---

## 9. 术语表

| 术语 | 定义 |
|---|---|
| **Session** | 一次就诊的完整生命周期，由 `session_id` 标识 |
| **Step** | 服务返回的结构化指令，`kind` 字段决定后端动作 |
| **StepKind** | Step 的类型枚举：ASK / NEED_TESTS / DRUG_QUERY / PURCHASE / DONE / EMERGENCY / OK |
| **Tool Loop** | 单 agent 工具循环——LLM 每步选一个工具，循环至终态 |
| **边界工具** | 调用后让出控制权给后端（ask_patient / order_test / query_drug_spec / purchase_drug） |
| **终态工具** | 调用后结束循环（finish / refer） |
| **Guardian** | 急症守护——与主流程并发运行的独立急症判断通道 |
| **Snapshot** | 喂给 AI 的全量上下文快照，每次调用从中重建 |
| **SessionRecord** | 会话纪要——含对话回合、时间戳、诊疗结果的导出结构 |
| **Plan** | 处置类型：MEDICATION / ADVICE_ONLY / REFERRAL |
| **Final** | 就诊终态：ADVICE（含用药和仅医嘱）/ REFERRAL |
| **DrugOrder** | 购药指令，`quantity` 为盒数（≥1） |
| **Compact** | 上下文压缩——token 占用过高时自动将历史凝缩为摘要 |
