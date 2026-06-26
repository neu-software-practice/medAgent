# 购药前药品规格查询设计

- 日期：2026-06-26
- 范围：在处置阶段的「决定用药」与「购药」之间，插入一个**药品规格查询轮**。AI 决定要哪些药后，先发 `DRUG_QUERY`（仅药名），后端按药名返回每盒规格（片数/克数/液体体积，自由文本），AI 据规格把开药量定成**可计量的盒数**，再发 `PURCHASE`。避免出现「布洛芬 12 片」这种无法按盒计量的处方。
- 关联：`medagent`（types/session/httpapi）、`internal/ai`（promptTreatment）、`docs/后端接入指南.md`、`cmd/consult`、walkthrough/realrun 测试。

## 决策摘要

| 决策点 | 结论 |
| --- | --- |
| 轮次形态 | 新增 `DRUG_QUERY` 轮：MEDICATION→DRUG_QUERY→SupplyDrugInfo→PURCHASE |
| 查询负载 | 仅药名列表（用法/疗程 AI 自留，第二轮算盒数） |
| 规格形态 | 自由文本规格串 `DrugInfo{name, spec}`（如「每盒24粒×0.3g」「每瓶100ml」），AI 解析 |
| 谁定盒数 | AI——拿到规格后重跑 treatment，`medications.quantity` = 购买盒数 |
| 内部改动 | 仅 `promptTreatment`（quantity 语义改盒数、规格感知）；无新 Snapshot 字段（复用 Subjective） |
| 依赖 | 仍零外部依赖 |

## 公开 API 新增

```go
const StepDrugQuery StepKind = "DRUG_QUERY" // 需查询药品规格

type Step struct {
    // …现有字段…
    DrugNames []string `json:"drug_names,omitempty"` // DRUG_QUERY 时非空：待查规格的药名
}

type DrugInfo struct {
    Name string `json:"name"`
    Spec string `json:"spec"` // 自由文本：每盒规格，如「每盒24粒×0.3g」「每瓶100ml」
}

func (s *Service) SupplyDrugInfo(ctx context.Context, id string, infos []DrugInfo) (Step, error)
```

HTTP 端点：`POST /sessions/{id}/drug-info`，请求体 `{"infos":[{"name":"…","spec":"…"}]}`，响应 `Step`（通常 `PURCHASE`）。仅在收到 `kind=DRUG_QUERY` 后调用，否则 `409`（ErrWrongStep）。

`Result.Medications` 与 `DrugOrder` 的 `quantity` 含义统一为**购买盒数**；`dosage`/`schedule` 仍是用法。

## 状态机改动

新增阶段 `phAwaitDrugInfo`；新增会话标志 `drugInfoSupplied bool`。

`advance` 的 `phTreatment` 分支，`tp.Plan == ai.PlanMedication && !sess.purchased` 时：
- `!sess.drugInfoSupplied` → `sess.phase = phAwaitDrugInfo`；`names := drugNamesOf(tp.Medications)`；`addTurn("drug_query", names)`；返回 `Step{Kind: StepDrugQuery, DrugNames: names}`。
- `sess.drugInfoSupplied` → `sess.phase = phAwaitPurchase`；`orders := ordersFromMeds(tp.Medications)`；返回 `Step{Kind: StepPurchase, Orders: orders}`（既有逻辑）。
（第一轮 treatment 的 medications 只取药名；盒数由第二轮 treatment 据规格产出，无需在轮间持久化处方。）

新增 `drugNamesOf([]ai.Medication) []string`（取 name 列表）。

`SupplyDrugInfo(infos)`：
1. get → `sess.mu.Lock`/defer → `lastActive`；done/closed → `ErrSessionClosed`；`phase != phAwaitDrugInfo` → `ErrWrongStep`。
2. savepoint：`nTurns := len(record.Turns)`；`prevSpec, hadSpec := snap.Subjective["药品规格"]`；`prevSupplied := drugInfoSupplied`。
3. 把规格渲染成可读串写入 `snap.Subjective["药品规格"]`（如「布洛芬缓释胶囊：每盒24粒×0.3g；阿莫西林：每盒20粒×0.25g」）；逐条 `addTurn("drug_info", name+": "+spec)`。
4. `drugInfoSupplied = true`；`phase = phTreatment`。
5. `guarded(ctx, sess, ai.Event{Kind:"drug_info", Data:infos}, advance)`。
6. 出错且非终态：回滚 record.Turns、Subjective["药品规格"]（按 hadSpec 还原或删）、drugInfoSupplied、phase=phAwaitDrugInfo（与其他入口一致的事务回滚）。

## 内部 `ai` 改动

`promptTreatment` 的 MEDICATION 行改为：
> - MEDICATION 用药：在 medications 给出 name、dosage、schedule。`quantity` 指**购买盒数**（整数）：若【就诊快照】尚无「药品规格」，`quantity` 一律给 0（系统会先按药名查询规格）；若已提供「药品规格」（每盒片数/克数/液体体积），则**就规格中列出的药品开药**（即你上轮选定、已查得规格者，保持一致不要换药），按疗程总需求 ÷ 每盒规格**向上取整**给出 `quantity` 盒数。

无 schema 改动（quantity 已是 integer）；无新 Snapshot 字段（规格走 Subjective["药品规格"]，仅在处置后期注入，不影响 interview/triage）。

## 记录与日志

`RecordedTurn.Kind` 新增 `"drug_query"`、`"drug_info"`；其余沿用。这两轮的 LLM 调用照常进 consultlog。

## 连带改动

- `cmd/consult`：模拟驱动新增 `StepDrugQuery` 分支——对每个药名回填桩规格（如「每盒12片×0.3g」）调 `SupplyDrugInfo` 续跑。
- `walkthrough_test.go`：购药主干测试加 DRUG_QUERY→SupplyDrugInfo 一步；断言 PURCHASE 的 orders 盒数来自第二轮。
- `realrun_test.go`：门控真实测试驱动循环加 `StepDrugQuery` 分支（桩规格）。
- `httpapi.go`：注册 `POST /sessions/{id}/drug-info`。
- `docs/后端接入指南.md`：§2 端点表 + §3 新增 drug-info 端点详情 + §4 StepKind 增 DRUG_QUERY 行与 `drug_names` 字段 + §5 时序图插入 DRUG_QUERY 轮。

## 测试

- **离线单测（FakeLLM）**：treatment 第一轮 MEDICATION（quantity 0）→ `DRUG_QUERY{药名}`；`SupplyDrugInfo` 回填规格 → 第二轮 treatment（quantity=盒数）→ `PURCHASE{orders 盒数}` → purchase-result → DONE。错误步骤（非 awaitDrugInfo 调 SupplyDrugInfo → ErrWrongStep）；SupplyDrugInfo 中途 advance 出错的回滚恢复。
- **HTTP 单测**：`POST /drug-info` 形状与状态码。
- **门控真实-LLM**：跑通含 DRUG_QUERY 的整流程到 DONE，肉眼确认 orders 为盒数。

## 显式排除（YAGNI）

- 结构化规格字段（unit/per_box/strength）；库存/价格（后端管）；规格缓存；查询带用法/疗程；多检验项（仍只血常规）。
