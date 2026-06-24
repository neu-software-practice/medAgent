# 决策层 × 真实 LLM 验证（发烧流程）设计

- 日期：2026-06-24
- 范围：用门控集成测试验证真实 `openaicompat` 客户端接入 `ai.DecisionLayer`，跑通一个发烧病人的问诊→收敛→处置全程，证明每个 agent **能正确发起工具调用、并正确消费工具返回的结构化反馈**。复用项目真实编排 `harness.RunVisit`，不新写 demo。
- 关联：`ai.NewDecisionLayer`、`ai/openaicompat`、`ai/internal/harness.RunVisit`、`docs/superpowers/specs/2026-06-24-smoke-cli-design.md`（复用其 env/provider 方案）。

## 决策摘要

| 决策点 | 结论 |
| --- | --- |
| 承载形式 | 门控真实-LLM 集成测试（非新 demo），有 pass/fail 断言 |
| 复用 | 真实 `RunVisit` 编排 + 真实 4-agent + 真实 adapter（验证生产代码路径，DRY） |
| 患者侧 | LLM 模拟（`{reply}` schema + 隐藏发烧病历卡），保证多轮收敛 |
| 门控 | 无 key → `t.Skip`，`go test ./...` 仍离线确定 |
| 观测处方 | 小改 `harness.Outcome` 增 `Medications`，让"开退烧药"可见可断言 |

## 为什么 RunVisit 成功 = 验证目标达成

`RunVisit` 只在每个 agent 的产出**反序列化进 typed intent 且通过 `Validate()`** 后才推进；否则 agent 返回 `SchemaError`、流程停住。因此一次成功的 `RunVisit` 直接证明：interview/triage/treatment 三个 agent 都成功对真实模型发起了**强制 tool-use** 调用，且工具返回被正确解析并消费。终态再校验语义合理性。

## 组件

### 1. `ai/internal/harness/harness.go` 改动（向后兼容）

- `Outcome` 增字段 `Medications []ai.Medication`。
- `treatmentPhase` 终态返回时填 `Medications: tp.Medications`。
- 现有 `harness_test.go` 不引用该字段，不受影响。

### 2. `ai/internal/harness/realrun_test.go`（新增，`package harness`）

`func TestRealFeverFlow(t *testing.T)`：

1. **显式开关门控**：未设 `MEDAGENT_REAL_LLM` → `t.Skip`。这是为了**避免环境里残留的 `OPENAI_API_KEY` 等误触发**（仅凭 key 是否存在来门控会让常见的 ambient key 触发真实网络调用，破坏离线套件）。
2. **读配置**（env）：`MEDAGENT_LLM_PROVIDER`（默认 `openai`）→ 对应 key 变量（`OPENAI_API_KEY`/`DEEPSEEK_API_KEY`/`DASHSCOPE_API_KEY`，开关已开但缺 key → `t.Fatalf`）；`MEDAGENT_LLM_BASE_URL`（可选，覆盖 base URL）；`MEDAGENT_LLM_MODEL`（可选，默认 openai→`gpt-4o-mini`、deepseek→`deepseek-chat`、qwen→`qwen-plus`）。
2. **构造**：`openaicompat` client（base-url 非空走 `New(Config{...})`，否则 deepseek/qwen 用预设）；`layer := ai.NewDecisionLayer(client)`。
3. **患者闭包** `func(lastDoctorReply string) string`：调 `client.Complete`，schema name `patient_reply`、JSON `{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"]}`；system = 隐藏发烧病历卡 + 指令（按医生提问简洁如实作答，`lastDoctorReply` 为空时先给开场主诉，不一次报全部）；user = `lastDoctorReply`（空时给"请开始问诊"）。解析 `{reply}` 返回；同时 `t.Logf` 打印 👤患者 / 🩺医生 对话。
4. **桩数据**：`Caps` 小 map；`TestResults` 对每个 item 返回病毒性发热的桩结果（如"WBC 偏低、淋巴细胞比例升高，提示病毒性"）。
5. **超时**：`context.WithTimeout(ctx, 4*time.Minute)` 防暴走/控成本。
6. **跑**：`out, err := RunVisit(ctx, deps)`。
7. **断言（验证集成正确性，不绑死临床选择）**：`err == nil`；`out.Final ∈ {ADVICE, REFERRAL}`；`out.Diagnosis != nil && Name != ""`（triage CONFIRM 成功）；`out.Advice != ""`（treatment 产出医嘱）；`Plan==MEDICATION` 时 `len(Medications)>0`。是否开药由真实模型自主决定，仅记录不强求。
8. **打印**：`t.Logf` 输出 trace、诊断、处方（药名/剂量/频次）、医嘱。

**病历卡**：成人，发热 1 天最高 38.9℃，伴乏力、轻微咽痛与干咳，无呼吸困难/皮疹/基础病/过敏——临床上明确需退烧对症，倾向 MEDICATION。

> **断言取舍（已据首跑放宽）**：首次实跑模型对 38.9℃ 轻症选了 `ADVICE_ONLY`（临床合理），故断言不再强求 `MEDICATION`——验证目标是「集成正确」，而非某个临床判断。同时把病历卡调强（39.4℃、明显不适、患者主动要求退烧）以更贴近"该开退烧药"的场景，争取真实跑出处方；即便仍是 advice-only，集成验证依然成立。

## 验证（实跑）

```
MEDAGENT_REAL_LLM=1 OPENAI_API_KEY=… MEDAGENT_LLM_BASE_URL=https://www.dogapi.cc/v1 \
  MEDAGENT_LLM_MODEL=gpt-5.4-mini \
  go test ./ai/internal/harness -run TestRealFeverFlow -v
```

`go test ./...`（无 env）仍全绿且离线。

## 显式排除（YAGNI）

- guardian 急症守护；复诊；可运行 cmd 入口；把 key 读进 adapter；CI 默认开启（保持 skip）。
