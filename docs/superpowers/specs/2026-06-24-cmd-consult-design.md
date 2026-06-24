# cmd/consult 可运行诊疗入口设计

- 日期：2026-06-24
- 范围：新增可运行入口 `cmd/consult`，把真实 `openaicompat` + `ai.DecisionLayer` + `consultlog` 接起来跑一次完整诊疗，日志默认落 `./logs/{visitID}.jsonl`。为复用编排，把 `ai/internal/harness` 移出 internal。
- 关联：`ai`、`ai/openaicompat`、`ai/consultlog`、`ai/internal/harness`（`RunVisit`/`Deps`/`Outcome`）、`cmd/smoke`（env/provider 方案）。

## 决策摘要

| 决策点 | 结论 |
| --- | --- |
| 编排来源 | 复用现有 `RunVisit`；`git mv ai/internal/harness → ai/harness`（保留包名 `harness`、`RunVisit`），仅移出 internal 让 cmd 可 import |
| 患者侧 | cmd 内联 LLM 模拟患者（发烧病历卡），打印医患对话 |
| 日志 | `consultlog.Wrap` + `FileLogger`，默认 `./logs`，每诊一文件 |
| 配置 | flags + env（复用 smoke：provider→key 变量、`-base-url` 覆盖） |
| 依赖 | 仍零外部依赖 |

## 编排复用：移出 internal

`git mv ai/internal/harness ai/harness`：包名仍 `harness`、函数仍 `RunVisit`，仅 import 路径由 `medagent/ai/internal/harness` 变 `medagent/ai/harness`。当前无任何外部 importer（仅其自身测试文件，随包同移），零风险。包文档已写明"用于端到端测试与 demo"——`cmd/consult` 正是 demo，契合其定位。其测试（walkthrough/variants/realrun）随包同移，继续覆盖编排正确性。

## 组件：`cmd/consult/main.go`（package main）

flags：
- `-provider`：`openai`|`deepseek`|`qwen`，默认 `openai`。
- `-model`：默认按 provider（openai→`gpt-4o-mini`、deepseek→`deepseek-chat`、qwen→`qwen-plus`）。
- `-base-url`：覆盖 base URL（第三方中转/自建网关）。
- `-log-dir`：日志目录，默认 `./logs`。
- `-timeout`：整次诊疗超时，默认 `4m`。

流程：
1. 解析 flags；未知 provider → stderr + `os.Exit(2)`。
2. 从 provider 对应 env 读 key；为空 → stderr「缺少环境变量 X」+ `os.Exit(1)`（不打印 key）。
3. `os.MkdirAll(logDir, 0o755)`。
4. `real := openaicompat.New/NewDeepSeek/NewQwen`（`-base-url` 非空走 `New`）。
5. `logged := consultlog.Wrap(real, consultlog.NewFileLogger(logDir))`。
6. `visitID := consultlog.NewVisitID()`；`ctx, cancel := context.WithTimeout(bg, timeout)`；`ctx = consultlog.WithVisitID(ctx, visitID)`。
7. `layer := ai.NewDecisionLayer(logged)`。
8. `deps := harness.Deps{ Layer: layer, Caps: {}, Patient: 本地 LLM 模拟患者（发烧病历卡；打印 👤/🩺 对话）, TestResults: 桩（病毒性发热回填） }`。
9. `out, err := harness.RunVisit(ctx, deps)`；err → stderr（`errors.Is(err, ai.ErrLLM)` 标注）+ `os.Exit(1)`。
10. 打印诊断 / 处方（药名·剂量·频次）/ 医嘱 / 轨迹 / 终态；末行打印 `日志: {logDir}/{visitID}.jsonl`。

模拟患者：用 `logged` client（调用也进日志）+ schema `{"reply":string}` + 发烧病历卡 system；`func(lastDoctorReply string) string`，出错则 stderr + `os.Exit(1)`。

## .gitignore

新增/追加忽略 `/logs/`（真实诊疗日志不入库）。

## 验证

- `go build ./...`、`go vet ./...`、`go test ./...` 全绿（harness 移位后路径变更，确认无残留 `internal/harness` 引用）；零依赖保持。
- 实跑：`go run ./cmd/consult -base-url https://www.dogapi.cc/v1 -model gpt-5.4-mini`（env 提供 key），确认生成 `./logs/{visitID}.jsonl` 且内容为本次诊疗的完整调用审计流。

## 显式排除（YAGNI）

- 交互式 stdin 真人患者；自动化测试（命中真实网络）；把患者模拟抽成公共 helper（cmd 内联）；guardian 急症守护；复诊；多场景 case 选择（默认发烧）。
