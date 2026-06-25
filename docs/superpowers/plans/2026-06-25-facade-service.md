# medagent 服务封装 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把决策层封装成最小暴露面的 HTTP 服务模块 `medagent`：内部固定编排（问诊→收敛→处置→购药→终决 + 并发急症守护），多轮会话按 ID 内存持有，底层包全部移入 `internal/`。

**Architecture:** 唯一公开包 `medagent`（根）暴露 `Service` + `Handler()`；`ai`/`openaicompat`/`consultlog` 移入 `internal/*`；退役 `harness.RunVisit`，编排收进 facade 的可恢复状态机；每个 session 持有 `ai.Snapshot` + 阶段 + 计数 + 可导出 `SessionRecord`。

**Tech Stack:** Go 1.22 标准库（`net/http` 增强路由、`encoding/json`、`httptest`、`sync`、`context`），零外部依赖。

**关联 spec:** `docs/superpowers/specs/2026-06-24-facade-service-design.md`

## Global Constraints

- Go 1.22；**零外部依赖**，仅标准库。
- module `medagent`；唯一公开包是根 `medagent`；`ai`/`openaicompat`/`consultlog` 移到 `medagent/internal/*`，外部 import 不到。
- 内部类型不外泄：公开 DTO 与内部 `ai` 类型同形但独立声明，边界处转换。
- 错误：内部 `ai.ErrLLM`→`ErrUpstream`；`*ai.SchemaError`→`ErrModelOutput`；ctx 取消/超时以原始 ctx 错误返回。
- 检验只血常规：triage 选 TEST 时 `TestItems` 恒为 `["血常规"]`。
- 购药闭环：MEDICATION→`PURCHASE`（`[]DrugOrder{Name,Quantity}`）→`SupplyPurchaseResult([]DrugPurchase{Name,Bought,Quantity})`→最终医嘱→`DONE`（不再循环购药）。
- 急症守护默认开；每推进轮并发跑，命中即取消主决策返回 `EMERGENCY`；守护错误 fail-open。`DisableGuardian` 关。
- 会话纪要 `SessionRecord` 带秒级时间戳（`time.Now().Truncate(time.Second)`）；`Export` 导出；`Start(profile, initial, prior)` 复诊回传 `prior` 渲染进 `snap.History`。
- 护栏：`maxInterviewTurns=20`、`maxTriageRounds=10`、`maxTreatmentRounds=5`，超限→`ErrUpstream`。
- 会话状态机内部常量与计数不提示给模型。

---

### Task 1: 重构——底层包移入 internal/

**Files:**
- Move: `ai/openaicompat` → `internal/openaicompat`；`ai/consultlog` → `internal/consultlog`；`ai/harness` → `internal/harness`；`ai/*.go` → `internal/ai/`
- Modify: 所有 `.go` 的 import 路径

**Interfaces:**
- Produces: 新 import 路径 `medagent/internal/ai`、`medagent/internal/openaicompat`、`medagent/internal/consultlog`、`medagent/internal/harness`（包名不变：`ai`/`openaicompat`/`consultlog`/`harness`）。

- [ ] **Step 1: 移动目录**

```bash
cd /Users/yym/GolandProjects/医疗agent
git mv ai internal/ai
git mv internal/ai/openaicompat internal/openaicompat
git mv internal/ai/consultlog internal/consultlog
git mv internal/ai/harness internal/harness
```

- [ ] **Step 2: 重写 import 路径（先长后短，避免误替换）**

```bash
cd /Users/yym/GolandProjects/医疗agent
grep -rl 'medagent/ai' --include='*.go' . | while read f; do
  sed -i '' \
    -e 's#medagent/ai/openaicompat#medagent/internal/openaicompat#g' \
    -e 's#medagent/ai/consultlog#medagent/internal/consultlog#g' \
    -e 's#medagent/ai/harness#medagent/internal/harness#g' \
    -e 's#medagent/ai#medagent/internal/ai#g' \
    "$f"
done
```

- [ ] **Step 3: 运行全套件验证（移动由现有测试守护）**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全部 PASS；包列表变为 `medagent/internal/ai`、`medagent/internal/openaicompat`、`medagent/internal/consultlog`、`medagent/internal/harness`、`medagent/cmd/smoke`、`medagent/cmd/consult`。

- [ ] **Step 4: 确认零依赖**

Run: `test ! -f go.sum && echo "zero-dep ok"`
Expected: `zero-dep ok`

- [ ] **Step 5: 提交**

```bash
git add -A
git commit -m "refactor: 底层包移入 internal/（ai/openaicompat/consultlog/harness）"
```

---

### Task 2: internal/ai 适配 facade 所需字段与提示

**Files:**
- Modify: `internal/ai/contract.go`（Snapshot 增 Profile/History）
- Modify: `internal/ai/snapshot.go`（renderSnapshotBlock 渲染两块）
- Modify: `internal/ai/contract.go`（Medication 增 Quantity）
- Modify: `internal/ai/prompts.go`（schemaTreatment quantity、promptTreatment、promptTriage CBC）
- Test: `internal/ai/snapshot_adapt_test.go`、`internal/ai/prompts_adapt_test.go`

**Interfaces:**
- Consumes: 现有 `Snapshot`、`Medication`、`renderSnapshotBlock`、`schemaTreatment`、`promptTriage`、`promptTreatment`。
- Produces: `Snapshot.Profile json.RawMessage`、`Snapshot.History string`、`Medication.Quantity int`。

- [ ] **Step 1: 写失败测试**

`internal/ai/snapshot_adapt_test.go`：

```go
package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderSnapshotIncludesProfileAndHistory(t *testing.T) {
	s := Snapshot{
		Profile: json.RawMessage(`{"年龄":30,"性别":"男"}`),
		History: "第1次(初诊): 急性咽炎，已开药。",
	}
	got := renderSnapshotBlock(s, nil)
	if !strings.Contains(got, "【患者资料】") || !strings.Contains(got, `"年龄"`) {
		t.Errorf("缺患者资料块：%s", got)
	}
	if !strings.Contains(got, "【历史就诊记录】") || !strings.Contains(got, "急性咽炎") {
		t.Errorf("缺历史就诊块：%s", got)
	}
}

func TestRenderSnapshotOmitsEmptyProfileHistory(t *testing.T) {
	got := renderSnapshotBlock(Snapshot{}, nil)
	if strings.Contains(got, "【患者资料】") || strings.Contains(got, "【历史就诊记录】") {
		t.Errorf("空时不应出现这两块：%s", got)
	}
}
```

`internal/ai/prompts_adapt_test.go`：

```go
package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMedicationCarriesQuantity(t *testing.T) {
	var m Medication
	if err := json.Unmarshal([]byte(`{"name":"阿莫西林","dosage":"0.5g","schedule":"每日3次","quantity":2}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.Quantity != 2 {
		t.Errorf("Quantity = %d, want 2", m.Quantity)
	}
}

func TestTreatmentSchemaHasQuantity(t *testing.T) {
	if !strings.Contains(string(schemaTreatment.JSON), `"quantity"`) {
		t.Errorf("schemaTreatment 缺 quantity：%s", schemaTreatment.JSON)
	}
}

func TestTriagePromptFixesBloodTest(t *testing.T) {
	if !strings.Contains(promptTriage, "血常规") {
		t.Errorf("promptTriage 未固定血常规")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/ai/ -run 'TestRenderSnapshotIncludes|TestMedicationCarriesQuantity|TestTreatmentSchemaHasQuantity|TestTriagePromptFixes' -v`
Expected: 编译失败（`Profile`/`History`/`Quantity` 未定义）或断言失败。

- [ ] **Step 3: 改 contract.go（Snapshot + Medication）**

`internal/ai/contract.go` 中 `Snapshot` 结构增两字段（放在 `Feedback` 之前）：

```go
	Profile json.RawMessage // 患者资料原样 JSON（facade 注入），可空
	History string          // 复诊历史，facade 预渲染，可空
```
并确保文件 import 了 `encoding/json`（若未导入则加）。

`internal/ai/contract.go` 中 `Medication`：

```go
type Medication struct {
	Name     string `json:"name"`
	Dosage   string `json:"dosage"`
	Schedule string `json:"schedule"`
	Quantity int    `json:"quantity,omitempty"`
}
```

- [ ] **Step 4: 改 snapshot.go（渲染两块）**

在 `renderSnapshotBlock` 内，紧接 `b.WriteString("【就诊快照】\n")` 之后插入：

```go
	if len(s.Profile) > 0 {
		fmt.Fprintf(&b, "【患者资料】%s\n", s.Profile)
	}
	if s.History != "" {
		fmt.Fprintf(&b, "【历史就诊记录】\n%s\n", s.History)
	}
```

- [ ] **Step 5: 改 prompts.go（schema quantity + 两处 prompt）**

`schemaTreatment` 的 medications item properties 增 `quantity`：把
`"medications": {"type": "array", "items": {"type": "object", "properties": {"name": {"type": "string"}, "dosage": {"type": "string"}, "schedule": {"type": "string"}}}},`
改为
`"medications": {"type": "array", "items": {"type": "object", "properties": {"name": {"type": "string"}, "dosage": {"type": "string"}, "schedule": {"type": "string"}, "quantity": {"type": "integer"}}}},`

`promptTreatment` 的 MEDICATION 行后补一句：
`- MEDICATION 用药：在 medications 给出 name、dosage、schedule，并给 quantity（建议购买份数，整数）。`（替换原 MEDICATION 行）

`promptTriage` 的 TEST 行改为固定血常规：
`- 主观信息已问尽仍无法确诊：decision=TEST，必须 subjective_exhausted=true，给出 reason；test_items 恒为 ["血常规"]（本系统只支持血常规）。`

- [ ] **Step 6: 运行测试，确认通过 + 全包回归**

Run: `go test ./internal/ai/ -v && go vet ./internal/ai/`
Expected: 全 PASS，vet 干净。

- [ ] **Step 7: 提交**

```bash
git add internal/ai/
git commit -m "feat(internal/ai): Snapshot 增 Profile/History、Medication 增 Quantity、检验固定血常规"
```

---

### Task 3: facade 公开 DTO、错误与转换

**Files:**
- Create: `types.go`、`errors.go`、`convert.go`
- Test: `convert_test.go`

**Interfaces:**
- Consumes: `internal/ai`（`ai.Diagnosis`/`ai.Medication`/`ai.TreatmentPlan`/`ai.TestResult` 等）。
- Produces（公开，`package medagent`）：`Config`、`Step`/`StepKind`(+常量)、`Result`、`Diagnosis`、`Medication`、`DrugOrder`、`DrugPurchase`、`TestResult`、`SessionRecord`、`RecordedTurn`；错误 `ErrSessionNotFound`/`ErrSessionClosed`/`ErrWrongStep`/`ErrUpstream`/`ErrModelOutput`；转换 `resultFromPlan(ai.TreatmentPlan, *ai.Diagnosis) Result`、`ordersFromMeds([]ai.Medication) []DrugOrder`、`testResultsToAI([]TestResult) []ai.TestResult`、`mapErr(error) error`。

- [ ] **Step 1: 写失败测试**

`convert_test.go`：

```go
package medagent

import (
	"errors"
	"testing"

	"medagent/internal/ai"
)

func TestResultFromPlan(t *testing.T) {
	dg := &ai.Diagnosis{Name: "急性咽炎", Basis: "症状", Confidence: 0.9}
	plan := ai.TreatmentPlan{
		Plan: ai.PlanMedication, Advice: "多休息",
		Medications: []ai.Medication{{Name: "对乙酰氨基酚", Dosage: "0.5g", Schedule: "每日3次", Quantity: 2}},
	}
	r := resultFromPlan(plan, dg)
	if r.Final != "ADVICE" || r.Plan != "MEDICATION" || r.Diagnosis.Name != "急性咽炎" {
		t.Fatalf("result 不符：%+v", r)
	}
	if len(r.Medications) != 1 || r.Medications[0].Quantity != 2 {
		t.Fatalf("medication 不符：%+v", r.Medications)
	}
}

func TestResultFinalReferral(t *testing.T) {
	r := resultFromPlan(ai.TreatmentPlan{Plan: ai.PlanReferral, Advice: "转诊", ReferralReason: "超能力"}, nil)
	if r.Final != "REFERRAL" {
		t.Fatalf("Final = %q, want REFERRAL", r.Final)
	}
}

func TestOrdersFromMeds(t *testing.T) {
	o := ordersFromMeds([]ai.Medication{{Name: "阿莫西林", Quantity: 3}, {Name: "布洛芬", Quantity: 1}})
	if len(o) != 2 || o[0].Name != "阿莫西林" || o[0].Quantity != 3 {
		t.Fatalf("orders 不符：%+v", o)
	}
}

func TestMapErr(t *testing.T) {
	if !errors.Is(mapErr(ai.ErrLLM), ErrUpstream) {
		t.Errorf("ErrLLM 应映射 ErrUpstream")
	}
	if !errors.Is(mapErr(&ai.SchemaError{Agent: "triage"}), ErrModelOutput) {
		t.Errorf("SchemaError 应映射 ErrModelOutput")
	}
	if mapErr(nil) != nil {
		t.Errorf("nil 应映射 nil")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test . -run 'TestResultFromPlan|TestResultFinalReferral|TestOrdersFromMeds|TestMapErr' -v`
Expected: 编译失败（types/errors/convert 未定义）。

- [ ] **Step 3: 实现 types.go**

```go
// Package medagent 是无人医院 AI 诊疗服务的唯一对外封装：以 HTTP/JSON 与外部组件通信，
// 内部按固定流程编排，多轮会话按 sessionID 内存持有。底层决策层在 internal/ 不外泄。
package medagent

import (
	"encoding/json"
	"time"
)

type Config struct {
	Provider        string
	APIKey          string
	Model           string
	BaseURL         string
	Caps            map[string]bool
	LogDir          string
	Timeout         time.Duration
	SessionTTL      time.Duration
	DisableGuardian bool
}

type StepKind string

const (
	StepAsk       StepKind = "ASK"
	StepNeedTests StepKind = "NEED_TESTS"
	StepPurchase  StepKind = "PURCHASE"
	StepEmergency StepKind = "EMERGENCY"
	StepDone      StepKind = "DONE"
	StepOK        StepKind = "OK"
)

type Step struct {
	Kind      StepKind    `json:"kind"`
	DoctorSay string      `json:"doctor_say,omitempty"`
	TestItems []string    `json:"test_items,omitempty"`
	Orders    []DrugOrder `json:"orders,omitempty"`
	Emergency string      `json:"emergency,omitempty"`
	Result    *Result     `json:"result,omitempty"`
}

type Result struct {
	Final       string       `json:"final"`
	Diagnosis   *Diagnosis   `json:"diagnosis,omitempty"`
	Plan        string       `json:"plan"`
	Medications []Medication `json:"medications,omitempty"`
	Advice      string       `json:"advice"`
}

type Diagnosis struct {
	Name       string  `json:"name"`
	Basis      string  `json:"basis"`
	Confidence float64 `json:"confidence"`
}
type Medication struct {
	Name     string `json:"name"`
	Dosage   string `json:"dosage"`
	Schedule string `json:"schedule"`
	Quantity int    `json:"quantity"`
}
type DrugOrder struct {
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
}
type DrugPurchase struct {
	Name     string `json:"name"`
	Bought   bool   `json:"bought"`
	Quantity int    `json:"quantity"`
}
type TestResult struct {
	Item  string `json:"item"`
	Value string `json:"value"`
}

type SessionRecord struct {
	SessionID string          `json:"session_id"`
	Initial   bool            `json:"initial"`
	StartedAt time.Time       `json:"started_at"`
	EndedAt   *time.Time      `json:"ended_at,omitempty"`
	Profile   json.RawMessage `json:"profile,omitempty"`
	Turns     []RecordedTurn  `json:"turns"`
	Outcome   *Result         `json:"outcome,omitempty"`
}
type RecordedTurn struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}
```

- [ ] **Step 4: 实现 errors.go**

```go
package medagent

import "errors"

var (
	ErrSessionNotFound = errors.New("medagent: session not found")
	ErrSessionClosed   = errors.New("medagent: session already completed")
	ErrWrongStep       = errors.New("medagent: call does not match current step")
	ErrUpstream        = errors.New("medagent: upstream LLM call failed")
	ErrModelOutput     = errors.New("medagent: model output invalid")
)
```

- [ ] **Step 5: 实现 convert.go**

```go
package medagent

import (
	"errors"
	"fmt"

	"medagent/internal/ai"
)

func resultFromPlan(p ai.TreatmentPlan, dg *ai.Diagnosis) Result {
	final := "ADVICE"
	if p.Plan == ai.PlanReferral {
		final = "REFERRAL"
	}
	r := Result{Final: final, Plan: string(p.Plan), Advice: p.Advice}
	if dg != nil {
		r.Diagnosis = &Diagnosis{Name: dg.Name, Basis: dg.Basis, Confidence: dg.Confidence}
	}
	for _, m := range p.Medications {
		r.Medications = append(r.Medications, Medication{Name: m.Name, Dosage: m.Dosage, Schedule: m.Schedule, Quantity: m.Quantity})
	}
	return r
}

func ordersFromMeds(meds []ai.Medication) []DrugOrder {
	out := make([]DrugOrder, 0, len(meds))
	for _, m := range meds {
		out = append(out, DrugOrder{Name: m.Name, Quantity: m.Quantity})
	}
	return out
}

func testResultsToAI(in []TestResult) []ai.TestResult {
	out := make([]ai.TestResult, 0, len(in))
	for _, t := range in {
		out = append(out, ai.TestResult{Item: t.Item, Value: t.Value})
	}
	return out
}

// mapErr 把内部错误归一为公开 sentinel；ctx 取消由调用处先行返回，不进这里。
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var se *ai.SchemaError
	if errors.As(err, &se) {
		return fmt.Errorf("%w: %v", ErrModelOutput, err)
	}
	if errors.Is(err, ai.ErrLLM) {
		return fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	return fmt.Errorf("%w: %v", ErrUpstream, err)
}
```

- [ ] **Step 6: 运行测试，确认通过**

Run: `go test . -run 'TestResultFromPlan|TestResultFinalReferral|TestOrdersFromMeds|TestMapErr' -v && go vet .`
Expected: 全 PASS。

- [ ] **Step 7: 提交**

```bash
git add types.go errors.go convert.go convert_test.go
git commit -m "feat(medagent): 公开 DTO、错误与内部类型转换"
```

---

### Task 4: Service 骨架——会话表、Start/End/Export、TTL、record

**Files:**
- Create: `service.go`、`record.go`
- Test: `service_test.go`

**Interfaces:**
- Consumes: Task 3 的 DTO/错误；`internal/ai`（`ai.DecisionLayer`、`ai.Guardian`、`ai.Snapshot`、`ai.NewDecisionLayer`）。
- Produces:
  - `type Service struct{…}`、`func newService(cfg Config, layer ai.DecisionLayer, guardian ai.Guardian) *Service`
  - `func (s *Service) Close() error`
  - `func (s *Service) Start(profile map[string]any, initial bool, prior []SessionRecord) (string, error)`
  - `func (s *Service) Export(sessionID string) (SessionRecord, error)`
  - `func (s *Service) End(sessionID string)`
  - 内部：`type phase int`（`phInterview/phTriage/phAwaitTests/phTreatment/phAwaitPurchase/phDone/phClosed`）、`type session struct{…}`、`func (s *Service) get(id string) (*session, error)`、`func renderHistory(prior []SessionRecord) string`、会话方法 `addTurn(kind, text string)`。

- [ ] **Step 1: 写失败测试**

`service_test.go`：

```go
package medagent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"medagent/internal/ai"
)

func testService(t *testing.T) *Service {
	t.Helper()
	fake := &ai.FakeLLM{On: func(ai.CompletionRequest) (ai.CompletionResult, error) {
		return ai.CompletionResult{}, nil
	}}
	return newService(Config{}, ai.NewDecisionLayer(fake), ai.NewGuardian(fake))
}

func TestStartExport(t *testing.T) {
	s := testService(t)
	defer s.Close()
	id, err := s.Start(map[string]any{"年龄": 30, "性别": "男"}, true, nil)
	if err != nil || id == "" {
		t.Fatalf("Start: id=%q err=%v", id, err)
	}
	rec, err := s.Export(id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.SessionID != id || !rec.Initial || rec.StartedAt.IsZero() {
		t.Fatalf("record 不符：%+v", rec)
	}
	if !strings.Contains(string(rec.Profile), "年龄") {
		t.Fatalf("profile 未存：%s", rec.Profile)
	}
}

func TestExportUnknownSession(t *testing.T) {
	s := testService(t)
	defer s.Close()
	if _, err := s.Export("nope"); err != ErrSessionNotFound {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}
}

func TestStartRendersPriorHistory(t *testing.T) {
	s := testService(t)
	defer s.Close()
	prior := []SessionRecord{{
		SessionID: "v0", Initial: true,
		Outcome: &Result{Diagnosis: &Diagnosis{Name: "急性咽炎"}, Advice: "多休息"},
		Turns:   []RecordedTurn{{At: time.Now(), Kind: "patient", Text: "嗓子疼"}},
	}}
	id, _ := s.Start(nil, false, prior)
	sess, _ := s.get(id)
	if !strings.Contains(sess.snap.History, "急性咽炎") {
		t.Fatalf("history 未渲染进 snapshot：%q", sess.snap.History)
	}
}

func TestTTLReaping(t *testing.T) {
	s := testService(t)
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	sess, _ := s.get(id)
	sess.lastActive = time.Now().Add(-time.Hour) // 强制过期
	s.reapOnce(time.Now())
	if _, err := s.Export(id); err != ErrSessionNotFound {
		t.Fatalf("过期会话应被回收，got %v", err)
	}
}

var _ = json.Marshal
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test . -run 'TestStartExport|TestExportUnknown|TestStartRendersPrior|TestTTLReaping' -v`
Expected: 编译失败（newService/Start/Export/get/reapOnce 未定义）。

- [ ] **Step 3: 实现 record.go**

```go
package medagent

import (
	"fmt"
	"strings"
	"time"
)

func nowSec() time.Time { return time.Now().Truncate(time.Second) }

// renderHistory 把先前会话纪要渲染成喂模型的历史文本。
func renderHistory(prior []SessionRecord) string {
	if len(prior) == 0 {
		return ""
	}
	var b strings.Builder
	for i, r := range prior {
		visit := "复诊"
		if r.Initial {
			visit = "初诊"
		}
		fmt.Fprintf(&b, "· 第%d次(%s, %s):\n", i+1, visit, r.StartedAt.Format("2006-01-02"))
		if r.Outcome != nil {
			if r.Outcome.Diagnosis != nil {
				fmt.Fprintf(&b, "    诊断: %s\n", r.Outcome.Diagnosis.Name)
			}
			for _, m := range r.Outcome.Medications {
				fmt.Fprintf(&b, "    处方: %s %s\n", m.Name, m.Dosage)
			}
			if r.Outcome.Advice != "" {
				fmt.Fprintf(&b, "    医嘱: %s\n", r.Outcome.Advice)
			}
		}
		for _, tn := range r.Turns {
			if tn.Kind == "patient" || tn.Kind == "doctor" {
				fmt.Fprintf(&b, "    [%s] %s: %s\n", tn.At.Format("15:04:05"), tn.Kind, tn.Text)
			}
		}
	}
	return b.String()
}
```

- [ ] **Step 4: 实现 service.go（会话表 + Start/End/Export + TTL）**

```go
package medagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"medagent/internal/ai"
)

type phase int

const (
	phInterview phase = iota
	phTriage
	phAwaitTests
	phTreatment
	phAwaitPurchase
	phDone
	phClosed
)

const (
	maxInterviewTurns  = 20
	maxTriageRounds    = 10
	maxTreatmentRounds = 5
)

type session struct {
	id     string
	snap   ai.Snapshot
	phase  phase
	iTurns, tRounds, pRounds int
	purchased bool // 已走过购药回报，处置重决策不再二次购药
	record SessionRecord
	lastActive time.Time
	mu     sync.Mutex
}

func (sess *session) addTurn(kind, text string) {
	sess.record.Turns = append(sess.record.Turns, RecordedTurn{At: nowSec(), Kind: kind, Text: text})
}

type Service struct {
	cfg      Config
	layer    ai.DecisionLayer
	guardian ai.Guardian
	ttl      time.Duration

	mu       sync.RWMutex
	sessions map[string]*session

	stop chan struct{}
	wg   sync.WaitGroup
}

func newService(cfg Config, layer ai.DecisionLayer, guardian ai.Guardian) *Service {
	ttl := cfg.SessionTTL
	if ttl == 0 {
		ttl = 30 * time.Minute
	}
	s := &Service{cfg: cfg, layer: layer, guardian: guardian, ttl: ttl,
		sessions: map[string]*session{}, stop: make(chan struct{})}
	s.wg.Add(1)
	go s.reaper()
	return s
}

func (s *Service) Close() error {
	close(s.stop)
	s.wg.Wait()
	return nil
}

func newSessionID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

func (s *Service) Start(profile map[string]any, initial bool, prior []SessionRecord) (string, error) {
	id := newSessionID()
	var prof json.RawMessage
	if profile != nil {
		if b, err := json.Marshal(profile); err == nil {
			prof = b
		}
	}
	sess := &session{
		id:    id,
		phase: phInterview,
		snap:  ai.Snapshot{Subjective: map[string]any{}, Profile: prof, History: renderHistory(prior)},
		record: SessionRecord{SessionID: id, Initial: initial, StartedAt: nowSec(), Profile: prof},
		lastActive: time.Now(),
	}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
	return id, nil
}

func (s *Service) get(id string) (*session, error) {
	s.mu.RLock()
	sess, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrSessionNotFound
	}
	return sess, nil
}

func (s *Service) Export(id string) (SessionRecord, error) {
	sess, err := s.get(id)
	if err != nil {
		return SessionRecord{}, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	cp := sess.record
	cp.Turns = append([]RecordedTurn(nil), sess.record.Turns...)
	return cp, nil
}

func (s *Service) End(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

func (s *Service) reaper() {
	defer s.wg.Done()
	tk := time.NewTicker(time.Minute)
	defer tk.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-tk.C:
			s.reapOnce(now)
		}
	}
}

func (s *Service) reapOnce(now time.Time) {
	s.mu.Lock()
	for id, sess := range s.sessions {
		if now.Sub(sess.lastActive) > s.ttl {
			delete(s.sessions, id)
		}
	}
	s.mu.Unlock()
}

// withVisit 在 ctx 上绑 sessionID 供日志归档（真实路径下 consultlog 用；FakeLLM 忽略）。
func withVisit(ctx context.Context, id string) context.Context {
	return ctx // Task 8 接真实日志时改为 consultlog.WithVisitID
}
```

- [ ] **Step 5: 运行测试，确认通过**

Run: `go test . -run 'TestStartExport|TestExportUnknown|TestStartRendersPrior|TestTTLReaping' -race -v && go vet .`
Expected: 全 PASS。

- [ ] **Step 6: 提交**

```bash
git add service.go record.go service_test.go
git commit -m "feat(medagent): Service 骨架——会话表/Start/Export/End/TTL/record"
```

---

### Task 5: advance 状态机（无守护）

**Files:**
- Create: `session.go`
- Test: `session_test.go`

**Interfaces:**
- Consumes: Task 4 的 `session`/`phase`/`Service`/`get`/`addTurn`/`withVisit`；Task 3 的转换与错误；`internal/ai`（`layer.Interview/Triage/Treatment`、intent 类型与常量、`ai.OrchestratorFeedback`、`ai.DialogTurn`、`ai.RefusalRecord`、`ai.TestResult`）。
- Produces:
  - `func (s *Service) PatientSay(ctx, id, message string) (Step, error)`
  - `func (s *Service) SupplyTestResults(ctx, id string, results []TestResult) (Step, error)`
  - `func (s *Service) SupplyPurchaseResult(ctx, id string, results []DrugPurchase) (Step, error)`
  - 内部 `func (s *Service) advance(ctx, sess) (Step, error)`（从当前 phase 推进到下一个需外部输入/终态）。

- [ ] **Step 1: 写失败测试**

`session_test.go`：

```go
package medagent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"medagent/internal/ai"
)

// scriptLLM 按 schema name + 计数返回脚本输出。
func scriptLLM(fn func(name string, n int) (any, error)) *ai.FakeLLM {
	counts := map[string]int{}
	return &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		counts[req.Schema.Name]++
		v, err := fn(req.Schema.Name, counts[req.Schema.Name])
		if err != nil {
			return ai.CompletionResult{}, err
		}
		return ai.StructuredOf(v), nil
	}}
}

func svcWith(t *testing.T, fake *ai.FakeLLM, caps map[string]bool) *Service {
	t.Helper()
	return newService(Config{Caps: caps, DisableGuardian: true},
		ai.NewDecisionLayer(fake), ai.NewGuardian(fake))
}

func TestFlowConfirmMedicationPurchaseDone(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			return ai.InterviewResult{Reply: "够了", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"主诉": "咽痛"}}}, nil
		case "triage_decide":
			return ai.TriageDecision{Decision: ai.TriageConfirm, Diagnosis: &ai.Diagnosis{Name: "急性咽炎", Basis: "症状", Confidence: 0.9}}, nil
		case "treatment_plan":
			if n == 1 {
				return ai.TreatmentPlan{Plan: ai.PlanMedication, Advice: "多休息",
					Medications: []ai.Medication{{Name: "对乙酰氨基酚", Dosage: "0.5g", Schedule: "每日3次", Quantity: 2}}}, nil
			}
			return ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "已购药，按医嘱服用，未购抗生素请补"}, nil
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
	if st.Kind != StepPurchase || len(st.Orders) != 1 || st.Orders[0].Quantity != 2 {
		t.Fatalf("应到 PURCHASE：%+v", st)
	}

	st, err = s.SupplyPurchaseResult(context.Background(), id, []DrugPurchase{{Name: "对乙酰氨基酚", Bought: true, Quantity: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepDone || st.Result == nil || st.Result.Final != "ADVICE" {
		t.Fatalf("应到 DONE：%+v", st)
	}
}

func TestFlowAskThenTest(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			if n == 1 {
				return ai.InterviewResult{Reply: "发烧几天了？"}, nil
			}
			return ai.InterviewResult{Reply: "好的", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"主诉": "发热"}}}, nil
		case "triage_decide":
			if n == 1 {
				return ai.TriageDecision{Decision: ai.TriageTest, SubjectiveExhausted: true, Reason: "需区分", TestItems: []string{"血常规"}}, nil
			}
			return ai.TriageDecision{Decision: ai.TriageConfirm, Diagnosis: &ai.Diagnosis{Name: "病毒感染", Basis: "血象", Confidence: 0.8}}, nil
		case "treatment_plan":
			return ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "多休息多饮水"}, nil
		}
		return nil, nil
	})
	s := svcWith(t, fake, nil)
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	st, _ := s.PatientSay(context.Background(), id, "发烧")
	if st.Kind != StepAsk || st.DoctorSay == "" {
		t.Fatalf("应先 ASK：%+v", st)
	}
	st, _ = s.PatientSay(context.Background(), id, "两天")
	if st.Kind != StepNeedTests || len(st.TestItems) != 1 || st.TestItems[0] != "血常规" {
		t.Fatalf("应 NEED_TESTS 血常规：%+v", st)
	}
	st, err := s.SupplyTestResults(context.Background(), id, []TestResult{{Item: "血常规", Value: "淋巴升高"}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepDone {
		t.Fatalf("应 DONE：%+v", st)
	}
}

func TestWrongStepAndClosed(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			return ai.InterviewResult{Reply: "x", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"a": "b"}}}, nil
		case "triage_decide":
			return ai.TriageDecision{Decision: ai.TriageConfirm, Diagnosis: &ai.Diagnosis{Name: "X", Basis: "y", Confidence: 0.9}}, nil
		case "treatment_plan":
			return ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "休息"}, nil
		}
		return nil, nil
	})
	s := svcWith(t, fake, nil)
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	if _, err := s.SupplyTestResults(context.Background(), id, nil); err != ErrWrongStep {
		t.Fatalf("非检验态应 ErrWrongStep，got %v", err)
	}
	st, _ := s.PatientSay(context.Background(), id, "hi")
	if st.Kind != StepDone {
		t.Fatalf("应 DONE：%+v", st)
	}
	if _, err := s.PatientSay(context.Background(), id, "again"); err != ErrSessionClosed {
		t.Fatalf("done 后应 ErrSessionClosed，got %v", err)
	}
}

func TestErrorMapping(t *testing.T) {
	fake := &ai.FakeLLM{On: func(ai.CompletionRequest) (ai.CompletionResult, error) {
		return ai.CompletionResult{}, ai.ErrLLM
	}}
	s := svcWith(t, fake, nil)
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	if _, err := s.PatientSay(context.Background(), id, "hi"); !errors.Is(err, ErrUpstream) {
		t.Fatalf("应 ErrUpstream，got %v", err)
	}
}

var _ = json.Marshal
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test . -run 'TestFlowConfirm|TestFlowAsk|TestWrongStep|TestErrorMapping' -v`
Expected: 编译失败（PatientSay 等未定义）。

- [ ] **Step 3: 实现 session.go（状态机）**

```go
package medagent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"medagent/internal/ai"
)

func (s *Service) PatientSay(ctx context.Context, id, message string) (Step, error) {
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
	if sess.phase != phInterview {
		return Step{}, ErrWrongStep
	}
	sess.snap.Interview = append(sess.snap.Interview, ai.DialogTurn{Role: "patient", Text: message})
	sess.addTurn("patient", message)
	return s.advance(ctx, sess)
}

func (s *Service) SupplyTestResults(ctx context.Context, id string, results []TestResult) (Step, error) {
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
	if sess.phase != phAwaitTests {
		return Step{}, ErrWrongStep
	}
	sess.snap.TestResults = append(sess.snap.TestResults, testResultsToAI(results)...)
	for _, r := range results {
		sess.addTurn("test_result", r.Item+": "+r.Value)
	}
	sess.phase = phTriage
	return s.advance(ctx, sess)
}

func (s *Service) SupplyPurchaseResult(ctx context.Context, id string, results []DrugPurchase) (Step, error) {
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
	if sess.phase != phAwaitPurchase {
		return Step{}, ErrWrongStep
	}
	bought := map[string]int{}
	for _, r := range results {
		if r.Bought {
			bought[r.Name] = r.Quantity
		} else {
			sess.snap.Refusals = append(sess.snap.Refusals, ai.RefusalRecord{What: "med_pay:" + r.Name})
		}
		sess.addTurn("purchase_result", fmt.Sprintf("%s 购买=%v 数量=%d", r.Name, r.Bought, r.Quantity))
	}
	if b, _ := json.Marshal(bought); b != nil {
		sess.snap.Subjective["购药结果"] = string(b)
	}
	sess.purchased = true
	sess.snap.Feedback = &ai.OrchestratorFeedback{NextExpected: "据购药结果给最终医嘱，勿重复开药"}
	sess.phase = phTreatment
	st, err := s.advance(ctx, sess)
	sess.snap.Feedback = nil
	return st, err
}

// advance 从当前 phase 推进到下一个需外部输入或终态。已持有 sess.mu。
func (s *Service) advance(ctx context.Context, sess *session) (Step, error) {
	cctx := withVisit(ctx, sess.id)
	for {
		switch sess.phase {
		case phInterview:
			sess.iTurns++
			if sess.iTurns > maxInterviewTurns {
				return Step{}, fmt.Errorf("%w: 问诊未收敛", ErrUpstream)
			}
			res, err := s.layer.Interview(cctx, sess.snap)
			if err != nil {
				return Step{}, ctxOrMap(cctx, err)
			}
			if res.Advance == nil {
				sess.snap.Interview = append(sess.snap.Interview, ai.DialogTurn{Role: "doctor", Text: res.Reply})
				sess.addTurn("doctor", res.Reply)
				return Step{Kind: StepAsk, DoctorSay: res.Reply}, nil
			}
			for k, v := range res.Advance.Subjective {
				sess.snap.Subjective[k] = v
			}
			sess.snap.Feedback = nil
			sess.phase = phTriage

		case phTriage:
			sess.tRounds++
			if sess.tRounds > maxTriageRounds {
				return Step{}, fmt.Errorf("%w: 收敛环未收敛", ErrUpstream)
			}
			td, err := s.layer.Triage(cctx, sess.snap)
			if err != nil {
				return Step{}, ctxOrMap(cctx, err)
			}
			switch td.Decision {
			case ai.TriageConfirm:
				sess.snap.Diagnosis = td.Diagnosis
				sess.addTurn("diagnosis", fmt.Sprintf("%s（%.2f）", td.Diagnosis.Name, td.Diagnosis.Confidence))
				sess.phase = phTreatment
			case ai.TriageInterview:
				sess.snap.Feedback = &ai.OrchestratorFeedback{MissingHint: td.MissingSubjective}
				sess.phase = phInterview
				// 立即再问一次拿追问句
				res, err := s.layer.Interview(cctx, sess.snap)
				if err != nil {
					return Step{}, ctxOrMap(cctx, err)
				}
				sess.snap.Feedback = nil
				if res.Advance != nil { // 模型直接补够：合并后继续收敛
					for k, v := range res.Advance.Subjective {
						sess.snap.Subjective[k] = v
					}
					sess.phase = phTriage
					continue
				}
				sess.snap.Interview = append(sess.snap.Interview, ai.DialogTurn{Role: "doctor", Text: res.Reply})
				sess.addTurn("doctor", res.Reply)
				return Step{Kind: StepAsk, DoctorSay: res.Reply}, nil
			case ai.TriageTest:
				sess.phase = phAwaitTests
				sess.addTurn("test_request", "血常规")
				return Step{Kind: StepNeedTests, TestItems: []string{"血常规"}}, nil
			default:
				return Step{}, fmt.Errorf("%w: 非法 triage %q", ErrModelOutput, td.Decision)
			}

		case phTreatment:
			sess.pRounds++
			if sess.pRounds > maxTreatmentRounds {
				return Step{}, fmt.Errorf("%w: 处置环未收敛", ErrUpstream)
			}
			tp, err := s.layer.Treatment(cctx, sess.snap)
			if err != nil {
				return Step{}, ctxOrMap(cctx, err)
			}
			if tp.Plan == ai.PlanTreatment && !s.cfg.Caps[tp.RequiredCapability] {
				sess.snap.Feedback = &ai.OrchestratorFeedback{LastReject: ai.RejectCapabilityMissing}
				continue
			}
			sess.snap.Feedback = nil
			if tp.Plan == ai.PlanMedication && !sess.purchased {
				sess.phase = phAwaitPurchase
				orders := ordersFromMeds(tp.Medications)
				sess.addTurn("purchase_request", fmt.Sprintf("%v", orders))
				return Step{Kind: StepPurchase, Orders: orders}, nil
			}
			// 已购药后重决策（含模型再返 MEDICATION）一律终决，不二次购药。
			return s.finish(sess, tp), nil

		default:
			return Step{}, ErrWrongStep
		}
	}
}

// finish 落终态：写 record.Outcome/EndedAt，phase=done。已持有 sess.mu。
func (s *Service) finish(sess *session, tp ai.TreatmentPlan) Step {
	r := resultFromPlan(tp, sess.snap.Diagnosis)
	sess.addTurn("advice", tp.Advice)
	sess.phase = phDone
	t := nowSec()
	sess.record.EndedAt = &t
	sess.record.Outcome = &r
	return Step{Kind: StepDone, Result: &r}
}

// ctxOrMap：ctx 取消优先以原始 ctx 错误返回，否则归一内部错误。
func ctxOrMap(ctx context.Context, err error) error {
	if ce := ctx.Err(); ce != nil {
		return ce
	}
	return mapErr(err)
}
```

说明：`purchased` 标志确保购药回报后的处置重决策（即使模型再返 MEDICATION）一律走 `finish` 终决，不再二次购药；它与能力缺失重试（也会 bump `pRounds`）互不干扰。`session` 的 `purchased` 字段已在 Task 4 结构体中定义。

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test . -run 'TestFlowConfirm|TestFlowAsk|TestWrongStep|TestErrorMapping' -race -v && go vet .`
Expected: 全 PASS。

- [ ] **Step 5: 提交**

```bash
git add session.go session_test.go service.go
git commit -m "feat(medagent): advance 状态机——问诊/收敛/处置/购药/终决"
```

---

### Task 6: 急症守护并发 + ReportVitals

**Files:**
- Create: `guardian.go`
- Modify: `session.go`（PatientSay/SupplyTestResults/SupplyPurchaseResult 包并发守护）
- Test: `guardian_test.go`

**Interfaces:**
- Consumes: Task 5 的推进方法、`Service`；`internal/ai`（`guardian.Assess`、`ai.Event`、`ai.EmergencyInterrupt`）。
- Produces:
  - `func (s *Service) ReportVitals(ctx, id string, vitals map[string]any) (Step, error)`
  - 内部 `func (s *Service) guarded(ctx, sess, ev ai.Event, main func(context.Context) (Step, error)) (Step, error)`

- [ ] **Step 1: 写失败测试**

`guardian_test.go`：

```go
package medagent

import (
	"context"
	"testing"

	"medagent/internal/ai"
)

func TestGuardianHitPreempts(t *testing.T) {
	fake := &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		if req.Schema.Name == "emergency_interrupt" {
			return ai.StructuredOf(map[string]any{"hit": true, "reason": "疑似心梗"}), nil
		}
		return ai.StructuredOf(ai.InterviewResult{Reply: "继续问"}), nil
	}}
	s := newService(Config{}, ai.NewDecisionLayer(fake), ai.NewGuardian(fake)) // 守护默认开
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	st, err := s.PatientSay(context.Background(), id, "胸口剧痛冒冷汗")
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != StepEmergency || st.Emergency == "" {
		t.Fatalf("应 EMERGENCY：%+v", st)
	}
	if _, err := s.PatientSay(context.Background(), id, "x"); err != ErrSessionClosed {
		t.Fatalf("急症后会话应 closed，got %v", err)
	}
}

func TestGuardianFailOpen(t *testing.T) {
	fake := &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		if req.Schema.Name == "emergency_interrupt" {
			return ai.CompletionResult{}, ai.ErrLLM // 守护出错
		}
		return ai.StructuredOf(ai.InterviewResult{Reply: "请问哪里不舒服？"}), nil
	}}
	s := newService(Config{}, ai.NewDecisionLayer(fake), ai.NewGuardian(fake))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	st, err := s.PatientSay(context.Background(), id, "有点不舒服")
	if err != nil {
		t.Fatalf("守护错误不应阻断：%v", err)
	}
	if st.Kind != StepAsk {
		t.Fatalf("应正常 ASK：%+v", st)
	}
}

func TestReportVitals(t *testing.T) {
	hit := false
	fake := &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		if req.Schema.Name == "emergency_interrupt" {
			return ai.StructuredOf(map[string]any{"hit": hit, "reason": "血压骤降"}), nil
		}
		return ai.StructuredOf(ai.InterviewResult{Reply: "x"}), nil
	}}
	s := newService(Config{}, ai.NewDecisionLayer(fake), ai.NewGuardian(fake))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	st, _ := s.ReportVitals(context.Background(), id, map[string]any{"收缩压": 70})
	if st.Kind != StepOK {
		t.Fatalf("未命中应 OK：%+v", st)
	}
	hit = true
	st, _ = s.ReportVitals(context.Background(), id, map[string]any{"收缩压": 50})
	if st.Kind != StepEmergency {
		t.Fatalf("命中应 EMERGENCY：%+v", st)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test . -run 'TestGuardianHit|TestGuardianFailOpen|TestReportVitals' -v`
Expected: 编译失败（ReportVitals/guarded 未定义）或 EMERGENCY 未实现而失败。

- [ ] **Step 3: 实现 guardian.go**

```go
package medagent

import (
	"context"

	"medagent/internal/ai"
)

type guardResult struct {
	ei  ai.EmergencyInterrupt
	hit bool
	err error
}

// guarded 并发跑守护与 main；守护命中即取消 main 返回 EMERGENCY，否则返回 main 结果。
// 守护错误 fail-open（忽略，等 main）。已持有 sess.mu。
func (s *Service) guarded(ctx context.Context, sess *session, ev ai.Event, main func(context.Context) (Step, error)) (Step, error) {
	if s.cfg.DisableGuardian {
		return main(ctx)
	}
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	gch := make(chan guardResult, 1)
	go func() {
		ei, hit, err := s.guardian.Assess(turnCtx, sess.snap, ev)
		gch <- guardResult{ei, hit, err}
	}()
	mch := make(chan struct {
		st  Step
		err error
	}, 1)
	go func() {
		st, err := main(turnCtx)
		mch <- struct {
			st  Step
			err error
		}{st, err}
	}()

	for {
		select {
		case g := <-gch:
			if g.err == nil && g.hit {
				cancel()
				<-mch // 排空
				return s.emergency(sess, g.ei.Reason), nil
			}
			m := <-mch // 守护放行或出错→等 main
			return m.st, m.err
		case m := <-mch:
			cancel()
			return m.st, m.err
		}
	}
}

func (s *Service) emergency(sess *session, reason string) Step {
	sess.addTurn("emergency", reason)
	sess.phase = phClosed
	t := nowSec()
	sess.record.EndedAt = &t
	return Step{Kind: StepEmergency, Emergency: reason}
}

func (s *Service) ReportVitals(ctx context.Context, id string, vitals map[string]any) (Step, error) {
	sess, err := s.get(id)
	if err != nil {
		return Step{}, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.lastActive = nowSecWall()
	if sess.phase == phDone || sess.phase == phClosed {
		return Step{}, ErrSessionClosed
	}
	if s.cfg.DisableGuardian {
		return Step{Kind: StepOK}, nil
	}
	ev := ai.Event{Kind: "vital", Data: vitals}
	ei, hit, gerr := s.guardian.Assess(withVisit(ctx, sess.id), sess.snap, ev)
	if gerr == nil && hit {
		return s.emergency(sess, ei.Reason), nil
	}
	return Step{Kind: StepOK}, nil
}
```

- [ ] **Step 4: 在 session.go 用 guarded 包裹推进**

把 `PatientSay` 末尾的 `return s.advance(ctx, sess)` 改为：
```go
	return s.guarded(ctx, sess, ai.Event{Kind: "dialog", Data: message}, func(c context.Context) (Step, error) {
		return s.advance(c, sess)
	})
```
把 `SupplyTestResults` 末尾的 `return s.advance(ctx, sess)` 改为：
```go
	return s.guarded(ctx, sess, ai.Event{Kind: "test_result", Data: results}, func(c context.Context) (Step, error) {
		return s.advance(c, sess)
	})
```
把 `SupplyPurchaseResult` 的 `st, err := s.advance(ctx, sess)` 改为：
```go
	st, err := s.guarded(ctx, sess, ai.Event{Kind: "purchase_result", Data: results}, func(c context.Context) (Step, error) {
		return s.advance(c, sess)
	})
```

- [ ] **Step 5: 运行测试，确认通过 + 全包回归**

Run: `go test . -race -v && go vet .`
Expected: 全 PASS（含 Task 5 用例，守护默认开但脚本里 emergency 未命中→正常流程）。

> 注：Task 5 的 `svcWith` 已设 `DisableGuardian: true`，不受守护影响；本任务用例显式开守护。

- [ ] **Step 6: 提交**

```bash
git add guardian.go session.go guardian_test.go
git commit -m "feat(medagent): 急症守护并发与 ReportVitals"
```

---

### Task 7: HTTP 端点

**Files:**
- Create: `httpapi.go`
- Test: `httpapi_test.go`

**Interfaces:**
- Consumes: 全部 Service 方法。
- Produces: `func (s *Service) Handler() http.Handler`。

- [ ] **Step 1: 写失败测试**

`httpapi_test.go`：

```go
package medagent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"medagent/internal/ai"
)

func httpSvc(t *testing.T) *httptest.Server {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			return ai.InterviewResult{Reply: "够了", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"a": "b"}}}, nil
		case "triage_decide":
			return ai.TriageDecision{Decision: ai.TriageConfirm, Diagnosis: &ai.Diagnosis{Name: "急性咽炎", Basis: "x", Confidence: 0.9}}, nil
		case "treatment_plan":
			return ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "多休息"}, nil
		case "emergency_interrupt":
			return map[string]any{"hit": false}, nil
		}
		return nil, nil
	})
	s := newService(Config{}, ai.NewDecisionLayer(fake), ai.NewGuardian(fake))
	t.Cleanup(func() { s.Close() })
	return httptest.NewServer(s.Handler())
}

func TestHTTPHappyPath(t *testing.T) {
	srv := httpSvc(t)
	defer srv.Close()

	var start struct {
		SessionID string `json:"session_id"`
	}
	postJSON(t, srv.URL+"/sessions", map[string]any{"initial": true, "profile": map[string]any{"年龄": 30}}, &start)
	if start.SessionID == "" {
		t.Fatal("无 session_id")
	}

	var step Step
	postJSON(t, srv.URL+"/sessions/"+start.SessionID+"/patient-say", map[string]any{"message": "嗓子疼"}, &step)
	if step.Kind != StepDone || step.Result == nil {
		t.Fatalf("应 DONE：%+v", step)
	}

	var rec SessionRecord
	getJSON(t, srv.URL+"/sessions/"+start.SessionID+"/record", &rec)
	if len(rec.Turns) == 0 || rec.Outcome == nil {
		t.Fatalf("record 不符：%+v", rec)
	}
}

func TestHTTPUnknownSession404(t *testing.T) {
	srv := httpSvc(t)
	defer srv.Close()
	resp, _ := http.Post(srv.URL+"/sessions/nope/patient-say", "application/json", bytes.NewReader([]byte(`{"message":"x"}`)))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func postJSON(t *testing.T, url string, body any, out any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("%s → %d", url, resp.StatusCode)
	}
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("%s → %d", url, resp.StatusCode)
	}
	json.NewDecoder(resp.Body).Decode(out)
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test . -run 'TestHTTPHappyPath|TestHTTPUnknown' -v`
Expected: 编译失败（Handler 未定义）。

- [ ] **Step 3: 实现 httpapi.go**

```go
package medagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions", s.hStart)
	mux.HandleFunc("POST /sessions/{id}/patient-say", s.hPatientSay)
	mux.HandleFunc("POST /sessions/{id}/test-results", s.hTestResults)
	mux.HandleFunc("POST /sessions/{id}/purchase-result", s.hPurchaseResult)
	mux.HandleFunc("POST /sessions/{id}/vitals", s.hVitals)
	mux.HandleFunc("GET /sessions/{id}/record", s.hRecord)
	mux.HandleFunc("DELETE /sessions/{id}", s.hEnd)
	return mux
}

func (s *Service) hStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Profile map[string]any  `json:"profile"`
		Initial bool            `json:"initial"`
		Prior   []SessionRecord `json:"prior"`
	}
	if !decode(w, r, &body) {
		return
	}
	id, err := s.Start(body.Profile, body.Initial, body.Prior)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"session_id": id})
}

func (s *Service) hPatientSay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message string `json:"message"`
	}
	if !decode(w, r, &body) {
		return
	}
	step, err := s.PatientSay(r.Context(), r.PathValue("id"), body.Message)
	respondStep(w, step, err)
}

func (s *Service) hTestResults(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Results []TestResult `json:"results"`
	}
	if !decode(w, r, &body) {
		return
	}
	step, err := s.SupplyTestResults(r.Context(), r.PathValue("id"), body.Results)
	respondStep(w, step, err)
}

func (s *Service) hPurchaseResult(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Results []DrugPurchase `json:"results"`
	}
	if !decode(w, r, &body) {
		return
	}
	step, err := s.SupplyPurchaseResult(r.Context(), r.PathValue("id"), body.Results)
	respondStep(w, step, err)
}

func (s *Service) hVitals(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Vitals map[string]any `json:"vitals"`
	}
	if !decode(w, r, &body) {
		return
	}
	step, err := s.ReportVitals(r.Context(), r.PathValue("id"), body.Vitals)
	respondStep(w, step, err)
}

func (s *Service) hRecord(w http.ResponseWriter, r *http.Request) {
	rec, err := s.Export(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, rec)
}

func (s *Service) hEnd(w http.ResponseWriter, r *http.Request) {
	s.End(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return false
	}
	return true
}

func respondStep(w http.ResponseWriter, step Step, err error) {
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, step)
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, ErrSessionNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrSessionClosed), errors.Is(err, ErrWrongStep):
		status = http.StatusConflict
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test . -run 'TestHTTPHappyPath|TestHTTPUnknown' -v && go vet .`
Expected: 全 PASS。

- [ ] **Step 5: 提交**

```bash
git add httpapi.go httpapi_test.go
git commit -m "feat(medagent): HTTP 端点（JSON，Go 1.22 路由）"
```

---

### Task 8: 真实接线 New + cmd/server

**Files:**
- Create: `new.go`、`cmd/server/main.go`
- Modify: `service.go`（`withVisit` 接真实 consultlog）
- Test: `new_test.go`

**Interfaces:**
- Consumes: `internal/openaicompat`、`internal/consultlog`、`internal/ai`；Task 4 的 `newService`。
- Produces: `func New(cfg Config) (*Service, error)`。

- [ ] **Step 1: 写失败测试**

`new_test.go`：

```go
package medagent

import "testing"

func TestNewValidates(t *testing.T) {
	if _, err := New(Config{Provider: "deepseek", Model: "deepseek-chat"}); err == nil {
		t.Error("缺 APIKey 应报错")
	}
	if _, err := New(Config{Provider: "无此", APIKey: "k", Model: "m"}); err == nil {
		t.Error("未知 provider 应报错")
	}
	s, err := New(Config{Provider: "deepseek", APIKey: "k", Model: "deepseek-chat"})
	if err != nil {
		t.Fatalf("合法配置应成功：%v", err)
	}
	s.Close()
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test . -run TestNewValidates -v`
Expected: 编译失败（New 未定义）。

- [ ] **Step 3: 实现 new.go**

```go
package medagent

import (
	"fmt"

	"medagent/internal/ai"
	"medagent/internal/consultlog"
	"medagent/internal/openaicompat"
)

func New(cfg Config) (*Service, error) {
	if cfg.APIKey == "" || cfg.Model == "" {
		return nil, fmt.Errorf("medagent: APIKey 与 Model 必填")
	}
	var llm ai.LLMClient
	switch {
	case cfg.BaseURL != "":
		llm = openaicompat.New(openaicompat.Config{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Model: cfg.Model, Timeout: cfg.Timeout})
	case cfg.Provider == "deepseek":
		llm = openaicompat.NewDeepSeek(cfg.APIKey, cfg.Model)
	case cfg.Provider == "qwen":
		llm = openaicompat.NewQwen(cfg.APIKey, cfg.Model)
	case cfg.Provider == "openai":
		llm = openaicompat.New(openaicompat.Config{BaseURL: "https://api.openai.com/v1", APIKey: cfg.APIKey, Model: cfg.Model, Timeout: cfg.Timeout})
	default:
		return nil, fmt.Errorf("medagent: 未知 provider %q（deepseek|qwen|openai 或设 BaseURL）", cfg.Provider)
	}
	if cfg.LogDir != "" {
		llm = consultlog.Wrap(llm, consultlog.NewFileLogger(cfg.LogDir))
	}
	return newService(cfg, ai.NewDecisionLayer(llm), ai.NewGuardian(llm)), nil
}
```

- [ ] **Step 4: 让日志按 sessionID 归档：改 service.go 的 withVisit**

把 `service.go` 中占位的 `withVisit` 替换为：
```go
func withVisit(ctx context.Context, id string) context.Context {
	return consultlog.WithVisitID(ctx, id)
}
```
并在 `service.go` import 增 `"medagent/internal/consultlog"`。

- [ ] **Step 5: 实现 cmd/server/main.go**

```go
// Command server 起 medagent HTTP 服务。
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"medagent"
)

func main() {
	addr := flag.String("addr", ":8080", "监听地址")
	provider := flag.String("provider", "deepseek", "provider: deepseek|qwen|openai")
	model := flag.String("model", "deepseek-chat", "模型名")
	baseURL := flag.String("base-url", "", "覆盖 base URL")
	logDir := flag.String("log-dir", "./logs", "诊疗日志目录")
	caps := flag.String("caps", "", "本院能力清单，逗号分隔")
	flag.Parse()

	keyEnv := map[string]string{"deepseek": "DEEPSEEK_API_KEY", "qwen": "DASHSCOPE_API_KEY", "openai": "OPENAI_API_KEY"}[*provider]
	key := os.Getenv(keyEnv)
	if key == "" {
		log.Fatalf("缺少环境变量 %s", keyEnv)
	}
	capSet := map[string]bool{}
	for _, c := range strings.Split(*caps, ",") {
		if c = strings.TrimSpace(c); c != "" {
			capSet[c] = true
		}
	}
	svc, err := medagent.New(medagent.Config{
		Provider: *provider, APIKey: key, Model: *model, BaseURL: *baseURL,
		LogDir: *logDir, Caps: capSet,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer svc.Close()
	log.Printf("medagent 服务监听 %s（provider=%s model=%s）", *addr, *provider, *model)
	log.Fatal(http.ListenAndServe(*addr, svc.Handler()))
}
```

- [ ] **Step 6: 运行测试 + 构建**

Run: `go test . -run TestNewValidates -v && go build ./... && go vet ./...`
Expected: 全 PASS，构建通过。

- [ ] **Step 7: 提交**

```bash
git add new.go cmd/server/ service.go new_test.go
git commit -m "feat(medagent): New 真实接线 + cmd/server HTTP 入口 + 日志按 sessionID 归档"
```

---

### Task 9: cmd/smoke 修 import + cmd/consult 重写驱动 facade

**Files:**
- Modify: `cmd/smoke/main.go`（import 改 internal/*，已在 Task 1 完成 sed，本步仅确认）
- Rewrite: `cmd/consult/main.go`（驱动 facade）

**Interfaces:**
- Consumes: 公开 `medagent` API。

- [ ] **Step 1: 重写 cmd/consult/main.go**

```go
// Command consult 用模拟患者驱动 medagent facade 跑一次完整诊疗，日志落 ./logs。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"medagent"
	"medagent/internal/ai"
	"medagent/internal/openaicompat"
)

const patientCaseSheet = `你在扮演一名前来就诊的成年患者。你的真实病情（按医生提问简洁如实回答，不确定就说不确定）：
- 发热约2天，最高38.7℃，嗓子中等程度疼，吞咽有点加重；扁桃体没仔细看不确定有没有脓点；有点轻微咳嗽、偶尔鼻塞；既往体健无过敏。
- 你想知道要不要吃消炎药/抗生素。
只用一两句口语化中文回答；首轮先说来意。只通过 reply 字段返回。`

func main() {
	provider := os.Getenv("PROVIDER")
	if provider == "" {
		provider = "deepseek"
	}
	keyEnv := map[string]string{"deepseek": "DEEPSEEK_API_KEY", "qwen": "DASHSCOPE_API_KEY", "openai": "OPENAI_API_KEY"}[provider]
	key := os.Getenv(keyEnv)
	if key == "" {
		fmt.Fprintf(os.Stderr, "缺少 %s\n", keyEnv)
		os.Exit(1)
	}
	model := os.Getenv("MODEL")
	baseURL := os.Getenv("BASE_URL")

	svc, err := medagent.New(medagent.Config{Provider: provider, APIKey: key, Model: model, BaseURL: baseURL, LogDir: "./logs"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer svc.Close()

	// 模拟患者也用一个直连 client（不进会话日志）。
	patient := patientClient(provider, baseURL, key, model)
	ctx := context.Background()

	id, _ := svc.Start(map[string]any{"年龄": 30, "性别": "男"}, true, nil)
	fmt.Printf("=== 诊疗开始 session=%s ===\n", id)

	msg := simulate(ctx, patient, "")
	for {
		fmt.Printf("👤 患者：%s\n", msg)
		st, err := svc.PatientSay(ctx, id, msg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "PatientSay: %v\n", err)
			os.Exit(1)
		}
		done := handle(ctx, svc, id, patient, st, &msg)
		if done {
			break
		}
	}
	rec, _ := svc.Export(id)
	b, _ := json.MarshalIndent(rec, "", "  ")
	fmt.Printf("📁 会话纪要:\n%s\n", b)
}

// handle 处理一个 Step；返回是否终结。需要患者再说话时把下一句写回 *msg。
func handle(ctx context.Context, svc *medagent.Service, id string, patient ai.LLMClient, st medagent.Step, msg *string) bool {
	switch st.Kind {
	case medagent.StepAsk:
		fmt.Printf("🩺 医生：%s\n", st.DoctorSay)
		*msg = simulate(ctx, patient, st.DoctorSay)
		return false
	case medagent.StepNeedTests:
		fmt.Printf("🧪 检验：%v → 回填\n", st.TestItems)
		next, _ := svc.SupplyTestResults(ctx, id, []medagent.TestResult{{Item: "血常规", Value: "WBC 13.5↑、中性粒↑，提示细菌"}})
		return handle(ctx, svc, id, patient, next, msg)
	case medagent.StepPurchase:
		fmt.Printf("💊 购药请求：%v → 全部购买\n", st.Orders)
		var res []medagent.DrugPurchase
		for _, o := range st.Orders {
			res = append(res, medagent.DrugPurchase{Name: o.Name, Bought: true, Quantity: o.Quantity})
		}
		next, _ := svc.SupplyPurchaseResult(ctx, id, res)
		return handle(ctx, svc, id, patient, next, msg)
	case medagent.StepEmergency:
		fmt.Printf("🚨 急症转急诊：%s\n", st.Emergency)
		return true
	case medagent.StepDone:
		r := st.Result
		if r.Diagnosis != nil {
			fmt.Printf("🏷️ 诊断：%s（%.2f）\n", r.Diagnosis.Name, r.Diagnosis.Confidence)
		}
		for _, m := range r.Medications {
			fmt.Printf("💊 处方：%s %s ×%d\n", m.Name, m.Dosage, m.Quantity)
		}
		fmt.Printf("📋 医嘱：%s\n✅ 终态：%s/%s\n", r.Advice, r.Final, r.Plan)
		return true
	}
	return true
}

func patientClient(provider, baseURL, key, model string) ai.LLMClient {
	if baseURL != "" {
		return openaicompat.New(openaicompat.Config{BaseURL: baseURL, APIKey: key, Model: model})
	}
	switch provider {
	case "qwen":
		return openaicompat.NewQwen(key, model)
	case "openai":
		return openaicompat.New(openaicompat.Config{BaseURL: "https://api.openai.com/v1", APIKey: key, Model: model})
	default:
		return openaicompat.NewDeepSeek(key, model)
	}
}

func simulate(ctx context.Context, c ai.LLMClient, doctor string) string {
	u := doctor
	if u == "" {
		u = "（请开始问诊）"
	}
	res, err := c.Complete(ctx, ai.CompletionRequest{
		System:   patientCaseSheet,
		Messages: []ai.Message{{Role: "user", Content: u}},
		Schema:   ai.OutputSchema{Name: "patient_reply", JSON: json.RawMessage(`{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"]}`)},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "模拟患者: %v\n", err)
		os.Exit(1)
	}
	var pr struct {
		Reply string `json:"reply"`
	}
	_ = json.Unmarshal(res.Structured, &pr)
	_ = time.Now
	return pr.Reply
}
```

删除 `_ = time.Now` 与未用 import（若 `time` 未用则移除其 import）。

- [ ] **Step 2: 构建 + vet + 离线全套件**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全 PASS（cmd/* 无测试文件）。

- [ ] **Step 3: 提交**

```bash
git add cmd/consult/main.go cmd/smoke/main.go
git commit -m "refactor(cmd): consult 重写为驱动 medagent facade；smoke 沿用 internal import"
```

---

### Task 10: 退役 harness，场景测试改写到 facade

**Files:**
- Delete: `internal/harness/`（整目录）
- Create: `walkthrough_test.go`（facade 包，移植急性咽炎与变体场景）、`realrun_test.go`（facade 包，门控真实-LLM）

**Interfaces:**
- Consumes: 公开 `medagent` API + `internal/ai`、`internal/openaicompat`。

- [ ] **Step 1: 删除 harness**

```bash
git rm -r internal/harness
```

- [ ] **Step 2: 写 facade 场景测试 walkthrough_test.go**

```go
package medagent

import (
	"context"
	"testing"

	"medagent/internal/ai"
)

// 急性咽炎主干：问诊→（无 advance 先 ASK）→advance→TEST→回填→CONFIRM→ADVICE_ONLY DONE。
func TestWalkthroughPharyngitis(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			if n == 1 {
				return ai.InterviewResult{Reply: "发烧多少度？有无咳嗽？"}, nil
			}
			return ai.InterviewResult{Reply: "信息够了", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"主诉": "咽痛发热", "体温": "38.5"}}}, nil
		case "triage_decide":
			if n == 1 {
				return ai.TriageDecision{Decision: ai.TriageTest, SubjectiveExhausted: true, Reason: "区分病毒细菌", TestItems: []string{"血常规"}}, nil
			}
			return ai.TriageDecision{Decision: ai.TriageConfirm, Diagnosis: &ai.Diagnosis{Name: "急性咽炎", Basis: "症状+血象", Confidence: 0.9}}, nil
		case "treatment_plan":
			return ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "多休息多饮水"}, nil
		case "emergency_interrupt":
			return map[string]any{"hit": false}, nil
		}
		return nil, nil
	})
	s := newService(Config{}, ai.NewDecisionLayer(fake), ai.NewGuardian(fake))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)

	st, _ := s.PatientSay(context.Background(), id, "嗓子痛发烧")
	if st.Kind != StepAsk {
		t.Fatalf("先 ASK：%+v", st)
	}
	st, _ = s.PatientSay(context.Background(), id, "38.5度，干咳")
	if st.Kind != StepNeedTests {
		t.Fatalf("应 NEED_TESTS：%+v", st)
	}
	st, _ = s.SupplyTestResults(context.Background(), id, []TestResult{{Item: "血常规", Value: "淋巴偏高"}})
	if st.Kind != StepDone || st.Result.Diagnosis.Name != "急性咽炎" {
		t.Fatalf("应 DONE 急性咽炎：%+v", st)
	}
}

// 能力缺失→转诊。
func TestWalkthroughCapabilityReferral(t *testing.T) {
	fake := scriptLLM(func(name string, n int) (any, error) {
		switch name {
		case "interview":
			return ai.InterviewResult{Reply: "x", Advance: &ai.AdvanceToTriage{Subjective: map[string]any{"a": "b"}}}, nil
		case "triage_decide":
			return ai.TriageDecision{Decision: ai.TriageConfirm, Diagnosis: &ai.Diagnosis{Name: "需手术病", Basis: "x", Confidence: 0.9}}, nil
		case "treatment_plan":
			if n == 1 {
				return ai.TreatmentPlan{Plan: ai.PlanTreatment, Advice: "需手术", RequiredCapability: "外科手术"}, nil
			}
			return ai.TreatmentPlan{Plan: ai.PlanReferral, Advice: "转上级医院", ReferralReason: "本院无外科手术能力"}, nil
		case "emergency_interrupt":
			return map[string]any{"hit": false}, nil
		}
		return nil, nil
	})
	s := newService(Config{Caps: map[string]bool{}}, ai.NewDecisionLayer(fake), ai.NewGuardian(fake))
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	st, _ := s.PatientSay(context.Background(), id, "hi")
	if st.Kind != StepDone || st.Result.Final != "REFERRAL" {
		t.Fatalf("应转诊 DONE：%+v", st)
	}
}
```

- [ ] **Step 3: 写门控真实-LLM 测试 realrun_test.go**

```go
package medagent

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"medagent/internal/ai"
	"medagent/internal/openaicompat"
)

func TestRealConsultFlow(t *testing.T) {
	if os.Getenv("MEDAGENT_REAL_LLM") == "" {
		t.Skip("未设 MEDAGENT_REAL_LLM=1，跳过真实验证")
	}
	provider := getenv("MEDAGENT_LLM_PROVIDER", "openai")
	keyEnv := map[string]string{"openai": "OPENAI_API_KEY", "deepseek": "DEEPSEEK_API_KEY", "qwen": "DASHSCOPE_API_KEY"}[provider]
	key := os.Getenv(keyEnv)
	if key == "" {
		t.Fatalf("缺 %s", keyEnv)
	}
	model := getenv("MEDAGENT_LLM_MODEL", "gpt-4o-mini")
	baseURL := os.Getenv("MEDAGENT_LLM_BASE_URL")

	svc, err := New(Config{Provider: provider, APIKey: key, Model: model, BaseURL: baseURL, LogDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	patient := patientClientReal(provider, baseURL, key, model)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	id, _ := svc.Start(map[string]any{"年龄": 30, "性别": "男"}, true, nil)
	msg := simReal(ctx, t, patient, "")
	for i := 0; i < 30; i++ {
		st, err := svc.PatientSay(ctx, id, msg)
		if err != nil {
			t.Fatalf("PatientSay: %v", err)
		}
		switch st.Kind {
		case StepAsk:
			msg = simReal(ctx, t, patient, st.DoctorSay)
		case StepNeedTests:
			st, _ = svc.SupplyTestResults(ctx, id, []TestResult{{Item: "血常规", Value: "WBC 13.5↑、中性粒↑"}})
			if !consumeTerminal(t, svc, id, patient, ctx, st, &msg) {
				continue
			}
			return
		case StepPurchase:
			var res []DrugPurchase
			for _, o := range st.Orders {
				res = append(res, DrugPurchase{Name: o.Name, Bought: true, Quantity: o.Quantity})
			}
			st, _ = svc.SupplyPurchaseResult(ctx, id, res)
			assertDone(t, st)
			return
		case StepEmergency:
			t.Logf("急症：%s", st.Emergency)
			return
		case StepDone:
			assertDone(t, st)
			return
		}
	}
	t.Fatal("未在 30 轮内收敛")
}

func consumeTerminal(t *testing.T, svc *Service, id string, patient ai.LLMClient, ctx context.Context, st Step, msg *string) bool {
	switch st.Kind {
	case StepPurchase:
		var res []DrugPurchase
		for _, o := range st.Orders {
			res = append(res, DrugPurchase{Name: o.Name, Bought: true, Quantity: o.Quantity})
		}
		st2, _ := svc.SupplyPurchaseResult(ctx, id, res)
		assertDone(t, st2)
		return true
	case StepDone:
		assertDone(t, st)
		return true
	case StepAsk:
		*msg = simReal(ctx, t, patient, st.DoctorSay)
		return false
	}
	return true
}

func assertDone(t *testing.T, st Step) {
	t.Helper()
	if st.Kind != StepDone || st.Result == nil || st.Result.Diagnosis == nil {
		t.Fatalf("应 DONE 且有诊断：%+v", st)
	}
	t.Logf("诊断=%s 处置=%s 医嘱=%s", st.Result.Diagnosis.Name, st.Result.Plan, st.Result.Advice)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func patientClientReal(provider, baseURL, key, model string) ai.LLMClient {
	if baseURL != "" {
		return openaicompat.New(openaicompat.Config{BaseURL: baseURL, APIKey: key, Model: model})
	}
	switch provider {
	case "deepseek":
		return openaicompat.NewDeepSeek(key, model)
	case "qwen":
		return openaicompat.NewQwen(key, model)
	default:
		return openaicompat.New(openaicompat.Config{BaseURL: "https://api.openai.com/v1", APIKey: key, Model: model})
	}
}

func simReal(ctx context.Context, t *testing.T, c ai.LLMClient, doctor string) string {
	u := doctor
	if u == "" {
		u = "（请开始问诊）"
	}
	res, err := c.Complete(ctx, ai.CompletionRequest{
		System:   "你扮演成年发热咽痛患者，按医生提问简洁如实回答，只用 reply 字段。最高39.4℃、扁桃体脓点、颈淋巴结肿大、无咳嗽、无过敏。",
		Messages: []ai.Message{{Role: "user", Content: u}},
		Schema:   ai.OutputSchema{Name: "patient_reply", JSON: json.RawMessage(`{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"]}`)},
	})
	if err != nil {
		t.Fatalf("模拟患者: %v", err)
	}
	var pr struct {
		Reply string `json:"reply"`
	}
	_ = json.Unmarshal(res.Structured, &pr)
	return pr.Reply
}
```

- [ ] **Step 4: 离线全套件 + 构建**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全 PASS；`medagent/internal/harness` 不再出现在包列表；`TestRealConsultFlow` SKIP。

- [ ] **Step 5: 提交**

```bash
git add -A
git commit -m "refactor: 退役 internal/harness，场景与真实验证测试移到 medagent 包"
```

---

### Task 11: 改写后端接入指南

**Files:**
- Rewrite: `docs/后端接入指南.md`

**Interfaces:**
- Consumes: 最终公开 API 与 HTTP 端点。

- [ ] **Step 1: 重写文档**

把 `docs/后端接入指南.md` 全文替换为面向**封装服务**的接入说明，须涵盖：
1. 概述：唯一公开包 `medagent`，HTTP/JSON 对外，内部固定编排、底层 internal 不外泄。
2. 起服务：`go run ./cmd/server -provider deepseek`（env 提供 key），或库内 `medagent.New(Config)` + `svc.Handler()` 自挂载。
3. HTTP 端点表（与 spec 一致）：`POST /sessions`(profile/initial/prior)、`/patient-say`、`/test-results`、`/purchase-result`、`/vitals`、`GET /sessions/{id}/record`、`DELETE /sessions/{id}`；`Step.kind` 取值与各字段；状态码映射。
4. 一次就诊时序（含 ASK 多轮、NEED_TESTS→test-results、PURCHASE→purchase-result、DONE/EMERGENCY）。
5. 患者资料 JSON、初诊/复诊（initial + prior 回传 Export 的纪要）、会话纪要导出与秒级时间戳。
6. Config 字段、错误语义、日志（LogDir，可选）、急症守护（默认开/关）。
7. 边界：后端负责初诊/复诊判定、检验与药品子系统、持久化（导出后端存）、鉴权网关。

文档须与代码一致（端点、字段名、StepKind 值、错误码）。

- [ ] **Step 2: 校验链接与一致性（人工通读）**

确认端点、`StepKind`、`Config` 字段、错误码与 `types.go`/`httpapi.go` 完全一致。

- [ ] **Step 3: 提交**

```bash
git add docs/后端接入指南.md
git commit -m "docs: 后端接入指南改写为 HTTP 服务封装版"
```

---

## 完成标准

- `go build ./...`、`go vet ./...`、`go test ./...` 全绿；仓库零外部依赖（无 `go.sum`）。
- 唯一公开包 `medagent`；`internal/*` 外部不可见；`internal/harness` 已删。
- HTTP 服务可起：`go run ./cmd/server`；`cmd/consult` 可驱动 facade 实跑（有 key 时）。
- 门控 `TestRealConsultFlow` 在 `MEDAGENT_REAL_LLM=1` + key 下跑通到 DONE。
