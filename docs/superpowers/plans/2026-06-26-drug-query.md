# 购药前药品规格查询 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在处置阶段 MEDICATION 与 PURCHASE 之间插入 `DRUG_QUERY` 轮：AI 先发药名，后端返回每盒规格（自由文本），AI 据规格把开药量定成可计量的盒数再购药。

**Architecture:** 新增 `StepDrugQuery`/`Step.DrugNames`/`DrugInfo`/`SupplyDrugInfo` 与 `POST /drug-info`；状态机加 `phAwaitDrugInfo` 阶段 + `drugInfoSupplied` 标志；`promptTreatment` 把 `quantity` 语义改为购买盒数（规格未知给 0、规格已知向上取整）。

**Tech Stack:** Go 1.22 标准库，零外部依赖。

**关联 spec:** `docs/superpowers/specs/2026-06-26-drug-query-design.md`

## Global Constraints

- Go 1.22；零外部依赖；唯一公开包 `medagent`；底层在 `internal/*`。
- 公开 DTO 与内部 `ai` 类型同形但独立声明；内部类型不外泄。
- `quantity`（`Medication`/`DrugOrder`/`Result.Medications`）语义统一为**购买盒数**。
- DRUG_QUERY 查询只带药名（`Step.DrugNames []string`）；规格 `DrugInfo{name, spec}` spec 为自由文本。
- 状态机入口出错事务回滚（与现有 PatientSay/SupplyTestResults/SupplyPurchaseResult 一致）；轮数熔断置 `phClosed`。
- 检验仍只血常规（不在本次范围内改动）。

---

### Task 1: 公开类型——StepDrugQuery / Step.DrugNames / DrugInfo

**Files:**
- Modify: `types.go`
- Test: `types_drug_test.go`（新建）

**Interfaces:**
- Produces: `StepDrugQuery StepKind = "DRUG_QUERY"`；`Step.DrugNames []string`（json `drug_names,omitempty`）；`type DrugInfo struct{ Name, Spec string }`（json `name`/`spec`）。

- [ ] **Step 1: 写失败测试**

`types_drug_test.go`：

```go
package medagent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStepDrugQueryMarshals(t *testing.T) {
	b, err := json.Marshal(Step{Kind: StepDrugQuery, DrugNames: []string{"布洛芬", "阿莫西林"}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"kind":"DRUG_QUERY"`) || !strings.Contains(s, `"drug_names":["布洛芬","阿莫西林"]`) {
		t.Fatalf("Step 序列化不符：%s", s)
	}
}

func TestDrugInfoMarshals(t *testing.T) {
	b, _ := json.Marshal(DrugInfo{Name: "布洛芬缓释胶囊", Spec: "每盒24粒×0.3g"})
	if string(b) != `{"name":"布洛芬缓释胶囊","spec":"每盒24粒×0.3g"}` {
		t.Fatalf("DrugInfo 序列化不符：%s", b)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test . -run 'TestStepDrugQueryMarshals|TestDrugInfoMarshals' -v`
Expected: 编译失败（`StepDrugQuery`/`DrugNames`/`DrugInfo` 未定义）。

- [ ] **Step 3: 改 types.go**

在 `StepKind` 常量块内（与 `StepPurchase` 同组）增：
```go
	StepDrugQuery StepKind = "DRUG_QUERY"
```

在 `Step` 结构体内（`TestItems` 之后、`Orders` 之前或任意位置）增字段：
```go
	DrugNames []string `json:"drug_names,omitempty"`
```

在 `TestResult` 类型附近增：
```go
type DrugInfo struct {
	Name string `json:"name"`
	Spec string `json:"spec"`
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test . -run 'TestStepDrugQueryMarshals|TestDrugInfoMarshals' -v && go vet .`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add types.go types_drug_test.go
git commit -m "feat(medagent): 公开类型 StepDrugQuery/Step.DrugNames/DrugInfo"
```

---

### Task 2: promptTreatment——quantity 语义改购买盒数

**Files:**
- Modify: `internal/ai/prompts.go`
- Test: `internal/ai/prompts_drug_test.go`（新建）

**Interfaces:**
- Consumes: 现有 `promptTreatment`。
- Produces: `promptTreatment` 含「盒数」「药品规格」字样。

- [ ] **Step 1: 写失败测试**

`internal/ai/prompts_drug_test.go`：

```go
package ai

import (
	"strings"
	"testing"
)

func TestTreatmentPromptUsesBoxQuantity(t *testing.T) {
	for _, want := range []string{"盒数", "药品规格"} {
		if !strings.Contains(promptTreatment, want) {
			t.Errorf("promptTreatment 缺 %q", want)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/ai/ -run TestTreatmentPromptUsesBoxQuantity -v`
Expected: FAIL（缺「盒数」）。

- [ ] **Step 3: 改 prompts.go**

把 `promptTreatment` 里的 MEDICATION 行
`- MEDICATION 用药：在 medications 给出 name、dosage、schedule，并给 quantity（建议购买份数，整数）。`
替换为：
`- MEDICATION 用药：在 medications 给出 name、dosage、schedule。quantity 指购买盒数（整数）：若【就诊快照】尚无「药品规格」，quantity 一律给 0（系统会先按药名查询规格）；若已提供「药品规格」（每盒片数/克数/液体体积），则就规格中列出的药品开药（即上轮已选定、已查得规格者，保持一致不要换药），按疗程总需求 ÷ 每盒规格向上取整给出 quantity 盒数。`

- [ ] **Step 4: 运行确认通过 + 全包回归**

Run: `go test ./internal/ai/ -v && go vet ./internal/ai/`
Expected: 全 PASS（现有 ai 测试不受影响——它们不断言此行原文）。

- [ ] **Step 5: 提交**

```bash
git add internal/ai/prompts.go internal/ai/prompts_drug_test.go
git commit -m "feat(internal/ai): treatment quantity 语义改为购买盒数、规格感知"
```

---

### Task 3: 状态机——phAwaitDrugInfo / drugInfoSupplied / SupplyDrugInfo / advance 分支

**Files:**
- Modify: `service.go`（phase 常量 + session 字段）
- Modify: `session.go`（advance 分支、drugNamesOf、SupplyDrugInfo）
- Modify: `session_test.go`（更新 TestFlowConfirmMedicationPurchaseDone 走 DRUG_QUERY；加 wrong-step）

**Interfaces:**
- Consumes: Task 1 的 `StepDrugQuery`/`Step.DrugNames`/`DrugInfo`；现有 `session`/`phase`/`advance`/`guarded`/`ordersFromMeds`/`addTurn`/`ctxOrMap`/`finish`/护栏常量；`internal/ai`（`ai.Medication`/`ai.Event`/`ai.PlanMedication`）。
- Produces: `phAwaitDrugInfo phase`；`session.drugInfoSupplied bool`；`drugNamesOf([]ai.Medication) []string`；`func (s *Service) SupplyDrugInfo(ctx, id string, infos []DrugInfo) (Step, error)`。

- [ ] **Step 1: 写/改失败测试**

把 `session_test.go` 里现有的 `TestFlowConfirmMedicationPurchaseDone` 整体替换为下面版本（新增 DRUG_QUERY 一步），并新增 `TestSupplyDrugInfoWrongStep`：

```go
func TestFlowConfirmMedicationPurchaseDone(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			return ai.InterviewResult{Reply: "够了", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"主诉": "咽痛"}}}, nil
		case "triage_decide":
			return ai.TriageDecision{Decision: ai.TriageConfirm, Diagnosis: &ai.Diagnosis{Name: "急性咽炎", Basis: "症状", Confidence: 0.9}}, nil
		case "treatment_plan":
			switch n {
			case 1: // 规格未知：quantity 0，触发 DRUG_QUERY
				return ai.TreatmentPlan{Plan: ai.PlanMedication, Advice: "多休息",
					Medications: []ai.Medication{{Name: "对乙酰氨基酚", Dosage: "0.5g", Schedule: "每日3次", Quantity: 0}}}, nil
			case 2: // 规格已知：quantity=盒数
				return ai.TreatmentPlan{Plan: ai.PlanMedication, Advice: "多休息",
					Medications: []ai.Medication{{Name: "对乙酰氨基酚", Dosage: "0.5g", Schedule: "每日3次", Quantity: 2}}}, nil
			default: // 购药后终决
				return ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "已购药，按医嘱服用"}, nil
			}
		}
		return nil, nil
	})
	s := svcWith(t, fake, nil)
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	st, err := s.PatientSay(context.Background(), id, "嗓子疼")
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepDrugQuery || len(st.DrugNames) != 1 || st.DrugNames[0] != "对乙酰氨基酚" {
		t.Fatalf("应到 DRUG_QUERY：%+v", st)
	}

	st, err = s.SupplyDrugInfo(context.Background(), id, []DrugInfo{{Name: "对乙酰氨基酚", Spec: "每盒12片×0.5g"}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepPurchase || len(st.Orders) != 1 || st.Orders[0].Quantity != 2 {
		t.Fatalf("应到 PURCHASE 且盒数=2：%+v", st)
	}

	st, err = s.SupplyPurchaseResult(context.Background(), id, []DrugPurchase{{Name: "对乙酰氨基酚", Bought: true, Quantity: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepDone || st.Result == nil || st.Result.Final != "ADVICE" {
		t.Fatalf("应到 DONE：%+v", st)
	}
}

func TestSupplyDrugInfoWrongStep(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			return ai.InterviewResult{Reply: "发烧几天？"}, nil // 停在问诊
		}
		return nil, nil
	})
	s := svcWith(t, fake, nil)
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	if _, err := s.PatientSay(context.Background(), id, "发烧"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SupplyDrugInfo(context.Background(), id, nil); err != ErrWrongStep {
		t.Fatalf("非 awaitDrugInfo 调 SupplyDrugInfo 应 ErrWrongStep，得 %v", err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test . -run 'TestFlowConfirmMedicationPurchaseDone|TestSupplyDrugInfoWrongStep' -v`
Expected: 编译失败（`StepDrugQuery` 已有但 `SupplyDrugInfo`/`phAwaitDrugInfo` 未定义）或断言失败。

- [ ] **Step 3: 改 service.go——phase 常量 + session 字段**

phase 常量块（`service.go`）在 `phTreatment` 之后、`phAwaitPurchase` 之前插入 `phAwaitDrugInfo`：
```go
const (
	phInterview phase = iota
	phTriage
	phAwaitTests
	phTreatment
	phAwaitDrugInfo
	phAwaitPurchase
	phDone
	phClosed
)
```
`session` 结构体增字段（与 `purchased` 相邻）：
```go
	drugInfoSupplied bool // 已回填药品规格，处置据规格定盒数
```

- [ ] **Step 4: 改 session.go——advance 分支 + drugNamesOf**

把 `advance` 的 `phTreatment` 分支里
```go
			if tp.Plan == ai.PlanMedication && !sess.purchased {
				sess.phase = phAwaitPurchase
				orders := ordersFromMeds(tp.Medications)
				sess.addTurn("purchase_request", fmt.Sprintf("%v", orders))
				return Step{Kind: StepPurchase, Orders: orders}, nil
			}
```
替换为：
```go
			if tp.Plan == ai.PlanMedication && !sess.purchased {
				if !sess.drugInfoSupplied {
					names := drugNamesOf(tp.Medications)
					sess.phase = phAwaitDrugInfo
					sess.addTurn("drug_query", fmt.Sprintf("%v", names))
					return Step{Kind: StepDrugQuery, DrugNames: names}, nil
				}
				sess.phase = phAwaitPurchase
				orders := ordersFromMeds(tp.Medications)
				sess.addTurn("purchase_request", fmt.Sprintf("%v", orders))
				return Step{Kind: StepPurchase, Orders: orders}, nil
			}
```
在 `session.go` 末尾（或 ctxOrMap 附近）增 helper：
```go
func drugNamesOf(meds []ai.Medication) []string {
	out := make([]string, 0, len(meds))
	for _, m := range meds {
		out = append(out, m.Name)
	}
	return out
}
```

- [ ] **Step 5: 改 session.go——SupplyDrugInfo 方法**

在 `SupplyPurchaseResult` 之后增（`session.go` import 增 `"strings"`）：
```go
func (s *Service) SupplyDrugInfo(ctx context.Context, id string, infos []DrugInfo) (Step, error) {
	sess, err := s.get(id)
	if err != nil {
		return Step{}, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.lastActive = time.Now()
	if sess.phase == phDone || sess.phase == phClosed {
		return Step{}, ErrSessionClosed
	}
	if sess.phase != phAwaitDrugInfo {
		return Step{}, ErrWrongStep
	}
	nTurns := len(sess.record.Turns)
	prevSpec, hadSpec := sess.snap.Subjective["药品规格"]
	var b strings.Builder
	for _, di := range infos {
		fmt.Fprintf(&b, "%s：%s；", di.Name, di.Spec)
		sess.addTurn("drug_info", di.Name+": "+di.Spec)
	}
	sess.snap.Subjective["药品规格"] = b.String()
	sess.drugInfoSupplied = true
	sess.phase = phTreatment
	st, err := s.guarded(ctx, sess, ai.Event{Kind: "drug_info", Data: infos}, func(c context.Context) (Step, error) {
		return s.advance(c, sess)
	})
	if err != nil && sess.phase != phDone && sess.phase != phClosed {
		sess.record.Turns = sess.record.Turns[:nTurns]
		if hadSpec {
			sess.snap.Subjective["药品规格"] = prevSpec
		} else {
			delete(sess.snap.Subjective, "药品规格")
		}
		sess.drugInfoSupplied = false
		sess.phase = phAwaitDrugInfo
	}
	return st, err
}
```

- [ ] **Step 6: 运行确认通过 + 全包 -race**

Run: `go test . -race -v 2>&1 | tail -25 && go vet .`
Expected: 全 PASS（含更新后的 medication 流、wrong-step、及其余既有用例）。

- [ ] **Step 7: 提交**

```bash
git add service.go session.go session_test.go
git commit -m "feat(medagent): DRUG_QUERY 轮——phAwaitDrugInfo/SupplyDrugInfo 据规格定盒数"
```

---

### Task 4: HTTP 端点 POST /sessions/{id}/drug-info

**Files:**
- Modify: `httpapi.go`
- Test: `httpapi_drug_test.go`（新建）

**Interfaces:**
- Consumes: Task 3 的 `SupplyDrugInfo`；现有 `decode`/`respondStep`/`writeJSON` 等。
- Produces: 路由 `POST /sessions/{id}/drug-info` → `hDrugInfo`。

- [ ] **Step 1: 写失败测试**

`httpapi_drug_test.go`：

```go
package medagent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"medagent/internal/ai"
)

func TestHTTPDrugInfoFlow(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			return ai.InterviewResult{Reply: "够了", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"a": "b"}}}, nil
		case "triage_decide":
			return ai.TriageDecision{Decision: ai.TriageConfirm, Diagnosis: &ai.Diagnosis{Name: "急性咽炎", Basis: "x", Confidence: 0.9}}, nil
		case "treatment_plan":
			if n == 1 {
				return ai.TreatmentPlan{Plan: ai.PlanMedication, Advice: "x", Medications: []ai.Medication{{Name: "对乙酰氨基酚", Quantity: 0}}}, nil
			}
			return ai.TreatmentPlan{Plan: ai.PlanMedication, Advice: "x", Medications: []ai.Medication{{Name: "对乙酰氨基酚", Quantity: 2}}}, nil
		case "emergency_interrupt":
			return map[string]any{"hit": false}, nil
		}
		return nil, nil
	})
	s := newService(Config{}, ai.NewDecisionLayer(fake), ai.NewGuardian(fake))
	t.Cleanup(func() { s.Close() })
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	var start struct {
		SessionID string `json:"session_id"`
	}
	postJSON(t, srv.URL+"/sessions", map[string]any{"initial": true}, &start)

	var step Step
	postJSON(t, srv.URL+"/sessions/"+start.SessionID+"/patient-say", map[string]any{"message": "嗓子疼"}, &step)
	if step.Kind != StepDrugQuery || len(step.DrugNames) != 1 {
		t.Fatalf("应 DRUG_QUERY：%+v", step)
	}

	var step2 Step
	postJSON(t, srv.URL+"/sessions/"+start.SessionID+"/drug-info",
		map[string]any{"infos": []map[string]any{{"name": "对乙酰氨基酚", "spec": "每盒12片×0.5g"}}}, &step2)
	if step2.Kind != StepPurchase || step2.Orders[0].Quantity != 2 {
		t.Fatalf("应 PURCHASE 盒数2：%+v", step2)
	}
}

func TestHTTPDrugInfoWrongStep409(t *testing.T) {
	srv := httpSvc(t) // 复用 httpapi_test.go 的 helper（ADVICE_ONLY 流，不进 DRUG_QUERY）
	defer srv.Close()
	var start struct {
		SessionID string `json:"session_id"`
	}
	postJSON(t, srv.URL+"/sessions", map[string]any{"initial": true}, &start)
	resp, _ := http.Post(srv.URL+"/sessions/"+start.SessionID+"/drug-info", "application/json",
		strings.NewReader(`{"infos":[]}`))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("非 DRUG_QUERY 态调 drug-info 应 409，得 %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test . -run 'TestHTTPDrugInfo' -v`
Expected: 失败（`/drug-info` 路由未注册 → 404，断言不符）。

- [ ] **Step 3: 改 httpapi.go**

在 `Handler()` 的 mux 注册里（`purchase-result` 之后）增：
```go
	mux.HandleFunc("POST /sessions/{id}/drug-info", s.hDrugInfo)
```
增 handler（与 `hPurchaseResult` 同构）：
```go
func (s *Service) hDrugInfo(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Infos []DrugInfo `json:"infos"`
	}
	if !decode(w, r, &body) {
		return
	}
	step, err := s.SupplyDrugInfo(r.Context(), r.PathValue("id"), body.Infos)
	respondStep(w, step, err)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test . -run 'TestHTTPDrugInfo' -v && go test . -race 2>&1 | tail -5 && go vet .`
Expected: 全 PASS。

- [ ] **Step 5: 提交**

```bash
git add httpapi.go httpapi_drug_test.go
git commit -m "feat(medagent): HTTP POST /sessions/{id}/drug-info"
```

---

### Task 5: cmd/consult 与门控/场景测试处理 DRUG_QUERY

**Files:**
- Modify: `cmd/consult/main.go`
- Modify: `realrun_test.go`
- Modify: `walkthrough_test.go`

**Interfaces:**
- Consumes: `StepDrugQuery`/`DrugInfo`/`SupplyDrugInfo`。

- [ ] **Step 1: 改 cmd/consult/main.go——handle 加 DRUG_QUERY 分支**

在 `handle` 的 switch 中（`StepNeedTests` 同款递归风格）增：
```go
	case medagent.StepDrugQuery:
		fmt.Printf("💊 查询药品规格：%v → 回填\n", st.DrugNames)
		var infos []medagent.DrugInfo
		for _, name := range st.DrugNames {
			infos = append(infos, medagent.DrugInfo{Name: name, Spec: "每盒12片×0.3g"})
		}
		next, _ := svc.SupplyDrugInfo(ctx, id, infos)
		return handle(ctx, svc, id, patient, next, msg)
```

- [ ] **Step 2: 改 realrun_test.go——驱动循环加 DRUG_QUERY 分支**

在 `TestRealConsultFlow` 的 `switch st.Kind` 里（`StepNeedTests` 旁）增：
```go
		case StepDrugQuery:
			var infos []DrugInfo
			for _, name := range st.DrugNames {
				infos = append(infos, DrugInfo{Name: name, Spec: "每盒12片×0.3g"})
			}
			st, err = svc.SupplyDrugInfo(ctx, id, infos)
			if err != nil {
				t.Fatalf("SupplyDrugInfo: %v", err)
			}
			goto consume
```
（若 realrun 用的是内层 for 而非 `goto consume`，则按等价方式：回填后继续消费下一个 Step，不要静默退出。）

- [ ] **Step 3: 改 walkthrough_test.go——加一个含 DRUG_QUERY 的购药主干测试**

在 `walkthrough_test.go` 增：
```go
func TestWalkthroughMedicationViaDrugQuery(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			return ai.InterviewResult{Reply: "够了", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"a": "b"}}}, nil
		case "triage_decide":
			return ai.TriageDecision{Decision: ai.TriageConfirm, Diagnosis: &ai.Diagnosis{Name: "细菌性咽炎", Basis: "x", Confidence: 0.9}}, nil
		case "treatment_plan":
			if n == 1 {
				return ai.TreatmentPlan{Plan: ai.PlanMedication, Advice: "x", Medications: []ai.Medication{{Name: "阿莫西林", Quantity: 0}}}, nil
			}
			if n == 2 {
				return ai.TreatmentPlan{Plan: ai.PlanMedication, Advice: "x", Medications: []ai.Medication{{Name: "阿莫西林", Quantity: 1}}}, nil
			}
			return ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "按医嘱服药"}, nil
		case "emergency_interrupt":
			return map[string]any{"hit": false}, nil
		}
		return nil, nil
	})
	s := newService(Config{}, ai.NewDecisionLayer(fake), ai.NewGuardian(fake))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	st, _ := s.PatientSay(context.Background(), id, "嗓子化脓")
	if st.Kind != StepDrugQuery {
		t.Fatalf("应 DRUG_QUERY：%+v", st)
	}
	st, _ = s.SupplyDrugInfo(context.Background(), id, []DrugInfo{{Name: "阿莫西林", Spec: "每盒20粒×0.25g"}})
	if st.Kind != StepPurchase || st.Orders[0].Quantity != 1 {
		t.Fatalf("应 PURCHASE 盒数1：%+v", st)
	}
	st, _ = s.SupplyPurchaseResult(context.Background(), id, []DrugPurchase{{Name: "阿莫西林", Bought: true, Quantity: 1}})
	if st.Kind != StepDone {
		t.Fatalf("应 DONE：%+v", st)
	}
}
```

- [ ] **Step 4: 构建 + 全套件**

Run: `go build ./... && go vet ./... && go test . -race 2>&1 | tail -8`
Expected: 构建通过，`TestWalkthroughMedicationViaDrugQuery` 等全 PASS，`TestRealConsultFlow` 仍 SKIP（未设 env）。

- [ ] **Step 5: 提交**

```bash
git add cmd/consult/main.go realrun_test.go walkthrough_test.go
git commit -m "feat: cmd/consult 与场景/门控测试处理 DRUG_QUERY 轮"
```

---

### Task 6: 接入指南补 DRUG_QUERY 轮

**Files:**
- Modify: `docs/后端接入指南.md`

- [ ] **Step 1: 改文档**

读 `docs/后端接入指南.md`，按现行公开 API 一致地补充 DRUG_QUERY 轮：
1. §2 端点总表：在 `purchase-result` 行之前/之后增一行 `POST /sessions/{id}/drug-info` —— 回填药品规格 → AI 下一步，`200`。
2. §3 端点详情：新增「POST /sessions/{id}/drug-info — 回填药品规格」小节，请求体 `{"infos":[{"name":"布洛芬缓释胶囊","spec":"每盒24粒×0.3g"}]}`，说明仅在收到 `kind=DRUG_QUERY` 后调用、否则 409，响应通常 `PURCHASE`。
3. §4 Step：字段表增 `DrugNames`（json `drug_names`，`DRUG_QUERY` 时非空：待查规格的药名）；StepKind 取值表增 `DRUG_QUERY` 行——「需查询药品规格：用 `drug_names` 逐一查库返回每盒规格 → `/drug-info`」；并说明 `quantity`（DrugOrder/Medication）为**购买盒数**。
4. §5 时序：在 `PURCHASE` 之前插入 DRUG_QUERY 一步（`patient-say → DRUG_QUERY: [药名] → POST /drug-info → PURCHASE: [{name,quantity=盒数}] → /purchase-result → DONE`）；更新「典型多轮问诊示例」。

须与 `types.go`/`httpapi.go` 实际字段、路径、StepKind 值一致。

- [ ] **Step 2: 通读核对一致性**

确认端点路径、`drug_names`/`DrugInfo` 字段、`DRUG_QUERY` 值、盒数说明与代码一致。

- [ ] **Step 3: 提交**

```bash
git add docs/后端接入指南.md
git commit -m "docs: 接入指南补购药前 DRUG_QUERY 规格查询轮"
```

---

## 完成标准

- `go build ./...`、`go vet ./...`、`go test ./... -race` 全绿；零依赖（无 go.sum）。
- 公开面新增 `DRUG_QUERY`/`Step.DrugNames`/`DrugInfo`/`SupplyDrugInfo`/`POST /drug-info`；`quantity` 语义为盒数。
- 医疗主干：MEDICATION→DRUG_QUERY→drug-info→PURCHASE→purchase-result→DONE 离线与门控均跑通。
