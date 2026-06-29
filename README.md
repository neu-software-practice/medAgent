# medagent · 无人医院患者端 AI 诊疗服务

把无人医院的 AI 诊疗决策封装成一个**最小暴露面的 HTTP 服务模块**：内部由**单 agent 工具循环**驱动一次就诊（模型自主调用 问诊 / 检验 / 查药规格 / 购药 / 转诊 / 收尾 等工具，并发急症守护、按 token 占用自动压缩上下文），外部后端只通过 HTTP/JSON 调用少数端点即可接入。

> **架构说明**：内部决策引擎已从「固定状态机 + 多 agent」改造为**单 agent 工具循环**（LLM 每步必选一个工具，`tool_choice=required`）；副作用（检验/购药）仍经 HTTP 让出给后端执行、回填后续跑，因此**外部端点与 `Step.kind` 契约完全不变**。

- **唯一公开包** `agent`（导入路径 `medagent/agent`，Go module 仍为 `medagent`），以 HTTP/JSON 对外；底层决策层在 `internal/`，外部不可见、不可直接调用。
- **Go 1.22，零外部依赖**（仅标准库）。
- 多轮会话按 `session_id` 内存持有（带 TTL 回收）；持久化由后端负责（`GET /record` 导出后存库）。

> 本仓库最初的完整系统设计见 `docs/files/无人医院_AI系统_实现规格.md`（方案 A）。该设计已**部分过时**——当前实现把编排收进了本模块、用 HTTP 替代了 MCP/卡片、未实现 Saga/总计时等。逐项对照见该文件顶部的「实现现状批注」。本 README 描述的是**当前真实形态**。

---

## 架构与目录

```
agent/                     # 唯一公开包（import "medagent/agent"）：facade（Service + HTTP Handler）
  service.go / session.go  #   会话状态机：Start/PatientSay/SupplyTestResults/
  guardian.go / record.go  #   SupplyDrugInfo/SupplyPurchaseResult/ReportVitals/Export/End
  types.go / errors.go     #   公开 DTO 与错误
  httpapi.go / new.go      #   HTTP 端点 + 真实 LLM 接线
internal/
  ai/                      # AI 决策层：单 agent 工具循环引擎(Engine, 6 工具) + 并发急症守护 + 上下文压缩
  openaicompat/            # LLM adapter（DeepSeek/通义千问/任意 OpenAI 兼容端点；多工具 chat + 守护用强制 tool-use）
  consultlog/              # 诊疗日志：按 sessionID 每诊一文件（JSONL 调用审计）
cmd/
  server/                  # 起 HTTP 服务（生产入口）
  consult/                 # 模拟患者驱动一次完整诊疗的 demo
  smoke/                   # 单次 LLM 调用烟雾测
docs/
  后端接入指南.md           # 后端接入文档（端点/字段/时序/错误/边界）—— 接入必读
  files/…实现规格.md        # 最初完整设计（方案 A）+ 实现现状批注
```

依赖方向：`agent → internal/{ai,openaicompat,consultlog}`，`internal` 外部不可见。

---

## 一次就诊的流程

服务返回的 `Step.kind` 告诉后端下一步做什么：

```
POST /sessions                      → {session_id}
  └─ 循环 POST /sessions/{id}/patient-say：
       ASK         医生追问        → 展示给患者，等下一句
       NEED_TESTS  需检验(恒血常规) → 后端检验 → POST /test-results
       DRUG_QUERY  需查药品规格     → 后端按药名查库 → POST /drug-info（返回每盒规格）
       PURCHASE    需购药(盒数)     → 后端购买 → POST /purchase-result
       EMERGENCY   急症打断         → 转急诊，会话关闭
       DONE        诊疗完成         → result 含诊断/处方/医嘱 → GET /record 导出 → DELETE 销毁
  （任意阶段）POST /sessions/{id}/vitals → 体征给急症守护，返回 OK 或 EMERGENCY
```

关键设计：
- **单 agent 工具循环**——模型每步必选一个工具（`tool_choice=required`）。问诊/检验/查规格/购药是"边界工具"，调用即让出给后端、回填 `tool_result` 后续跑；`finish`/`refer` 是终态工具。总步数由 step 预算护栏兜底，上下文占用超 60% 窗口时自动 LLM 压缩为摘要。
- **AI 只产出结构化决策，不执行副作用**——检验、购药、缴费由后端在收到对应 `Step` 后驱动，结果回填给服务续跑。
- **处置三选一**：`MEDICATION`（开药购买）/ `ADVICE_ONLY`（仅医嘱）/ `REFERRAL`（转诊）。本系统**不做院内治疗执行**（缴费/操作无法闭环），需院内操作/手术的情形 AI 直接转诊。
- **查药规格轮**：开药前先 `DRUG_QUERY` 拿到每盒规格（片数/克数/体积），AI 据此把处方量定成**可计量的盒数**（`quantity` 单位为盒，系统保证 **≥1**），避免"12 片"这种无法按盒发药的处方。
- **急症守护默认开**：每个推进轮与主决策并发运行，命中即返回 `EMERGENCY` 并关闭会话。

---

## 快速开始

**起服务**（API key 由环境变量提供）：

```bash
DEEPSEEK_API_KEY=<your_key> go run ./cmd/server -provider deepseek
# 第三方中转/自建网关：
OPENAI_API_KEY=<key> go run ./cmd/server -provider openai -base-url https://your-gateway/v1 -model <model>
```

**驱动一次诊疗**（curl 走 HTTP；患者发言由你的前端/后端转发）：

```bash
SID=$(curl -s -XPOST localhost:8080/sessions -d '{"initial":true,"profile":{"年龄":28,"性别":"男"}}' | jq -r .session_id)
curl -s -XPOST localhost:8080/sessions/$SID/patient-say -d '{"message":"嗓子疼、发烧两天"}'
# → 按返回的 kind 调 /test-results、/drug-info、/purchase-result，直到 DONE
curl -s localhost:8080/sessions/$SID/record   # 导出会话纪要（含秒级时间戳）
```

**库用法**（嵌入你的 Go 服务）：

```go
import "medagent/agent"

svc, _ := agent.New(agent.Config{Provider: "deepseek", APIKey: key, Model: "deepseek-chat", LogDir: "./logs"})
defer svc.Close()
http.Handle("/ai/", http.StripPrefix("/ai", svc.Handler()))
```

**demo / 自测**：

```bash
go run ./cmd/consult                                  # 模拟患者跑一次完整诊疗（需 key）
go test ./... -race                                   # 离线全套件（不触网）
MEDAGENT_REAL_LLM=1 OPENAI_API_KEY=… go test . -run TestRealConsultFlow -v   # 门控真实 LLM 端到端
```

完整端点、字段、错误码、时序、边界见 **`docs/后端接入指南.md`**。

---

## 公开能力一览

| 端点 | 作用 |
|---|---|
| `POST /sessions` | 开始会话（患者资料 JSON、初诊/复诊、复诊回传历史纪要） |
| `POST /sessions/{id}/patient-say` | 患者发言 → 下一步 `Step` |
| `POST /sessions/{id}/test-results` | 回填检验结果（响应 `NEED_TESTS` 后） |
| `POST /sessions/{id}/drug-info` | 回填药品规格（响应 `DRUG_QUERY` 后） |
| `POST /sessions/{id}/purchase-result` | 回报购药结果（响应 `PURCHASE` 后） |
| `POST /sessions/{id}/vitals` | 上报体征给急症守护 |
| `GET /sessions/{id}/record` | 导出会话纪要 `SessionRecord`（秒级时间戳） |
| `DELETE /sessions/{id}` | 销毁会话 |

`Step.kind`：`ASK` / `NEED_TESTS` / `DRUG_QUERY` / `PURCHASE` / `EMERGENCY` / `DONE` / `OK`。

---

## 已实现 vs 后端职责 vs 未实现

**本模块负责**：AI 问诊/收敛/处置/急症守护决策、单 agent 工具循环编排、多轮会话与 TTL、查药规格与购药闭环、初诊/复诊上下文、会话纪要导出、按 sessionID 的调用日志、step 预算护栏、上下文压缩。

**下放给后端**：检验子系统、药品库查询/药房/支付、初诊/复诊判定、卡片 UI 与"暂不决定"交互、会话持久化、鉴权网关、业务级超时/熔断。

**最初设计中尚未实现**（见规格批注）：MCP 接口层、副作用账本与 Saga 补偿（退款/留档）、总计时强制转诊、step 预算/内部纠正撞顶强制转诊（当前为返回错误并关闭会话）。

---

## 技术约束

- Go 1.22；零外部依赖（HTTP 用标准库 `net/http` 增强路由）。
- 选用的 LLM 必须支持多工具 function calling（agent 循环用 `tool_choice=required` 每步选一个工具；急症守护用单工具强制 tool-use）。
- **模型接入测试状态**：目前**仅 `openai` 接口（含 OpenAI 兼容中转，以 `gpt-5.5` / `gpt-5.4-mini` 实测）经过端到端测试**；`deepseek`、`qwen` 接入已实现但**未经测试**，使用前请自行验证。另：购药盒数计算依赖模型能力——`gpt-5.5` 能据规格正确算盒数，较弱模型可能返回 0（系统兜底为 1 盒并记 `warn`）。
- 日志含医患对话（医疗数据），落点与脱敏由后端决定；`./logs/` 已 gitignore。

更多见 `docs/后端接入指南.md`（接入）与 `docs/files/无人医院_AI系统_实现规格.md`（原设计 + 现状批注）。
