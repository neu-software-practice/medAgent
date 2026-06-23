# AI 决策层 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 `medagent/ai` 包——无人医院患者端 AI 决策层的 4 个无状态 agent（问诊 / triage / 处置 / 急症守护）、typed intent 契约、LLM 抽象与可编程 fake、上下文构建/压缩，及 mock 编排 harness 与急性咽炎 walkthrough。

**Architecture:** 方案 A——无状态单次决策 agent；编排层（本计划用测试 harness 代替）驱动"拒绝→重决策"外环。所有 agent 共用一个 schema 驱动的 6 步执行骨架；LLMClient 抽象出来，单测用 fake，真实 provider 推迟选型。

**Tech Stack:** Go 1.22；仅标准库（`encoding/json` / `context` / `errors` / `time` / `sort` / `strings`）；无外部依赖。真实 LLM 集成测试（`MEDAGENT_LLM_E2E`）待选定 provider 后另行加入，不在本计划内。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `go.mod` | module `medagent`，go 1.22 |
| `ai/doc.go` | 包声明与说明 |
| `ai/contract.go` | Snapshot / Event / 反馈 / 辅助类型 / DecisionLayer & Guardian 接口 |
| `ai/intent.go` | 4 个 intent 结构体 + enum + RejectReason + `Validate()` |
| `ai/config.go` | `InterviewRawTurns` / `SchemaRetryMax` 常量 |
| `ai/llm.go` | LLMClient 接口 + 请求/响应类型 |
| `ai/llm_fake.go` | 可编程 FakeLLM + `StructuredOf` |
| `ai/snapshot.go` | `buildMessages` 上下文构建与压缩 |
| `ai/prompts.go` | 4 个 system prompt + 4 个 OutputSchema |
| `ai/errors.go` | `ErrLLM` + `SchemaError` |
| `ai/agent.go` | `validatable` 接口 + 通用 `runStructured` 6 步骨架 |
| `ai/agent_interview.go` | 问诊 agent |
| `ai/agent_triage.go` | triage agent |
| `ai/agent_treatment.go` | 处置 agent |
| `ai/agent_guardian.go` | 急症守护 agent |
| `ai/layer.go` | `Layer`（DecisionLayer 实现）+ 构造函数 |
| `ai/internal/harness/harness.go` | mock 编排驱动 `RunVisit` |

---

## Task 0: 项目脚手架

**Files:**
- Create: `go.mod`
- Create: `ai/doc.go`

- [ ] **Step 1: 创建 go.mod**

```
module medagent

go 1.22
```

- [ ] **Step 2: 创建 ai/doc.go**

```go
// Package ai 实现无人医院患者端的 AI 决策层：问诊 / triage / 处置 / 急症守护
// 四个无状态 agent，及其 typed intent 契约、LLM 抽象与上下文构建。
// AI 无状态，每次调用从 Snapshot 重建上下文；语义校验与编排归调用方。
package ai
```

- [ ] **Step 3: 验证编译**

Run: `go build ./...`
Expected: 无输出、退出码 0

- [ ] **Step 4: Commit**

```bash
git add go.mod ai/doc.go
git commit -m "chore: 初始化 medagent 模块与 ai 包"
```

---

## Task 1: 契约与 intent 类型定义

**Files:**
- Create: `ai/contract.go`
- Create: `ai/intent.go`

- [ ] **Step 1: 创建 ai/contract.go**

```go
package ai

import (
	"context"
	"time"
)

// DialogTurn 是问诊对话中的一轮。
type DialogTurn struct {
	Role string // "patient" | "doctor"
	Text string
}

// Diagnosis 是确诊结果。
type Diagnosis struct {
	Name       string  `json:"name"`
	Basis      string  `json:"basis"`
	Confidence float64 `json:"confidence"`
}

// Medication 是一条用药明细。
type Medication struct {
	Name     string `json:"name"`
	Dosage   string `json:"dosage"`
	Schedule string `json:"schedule"`
}

// TestResult 是一条已回填的检验结果。
type TestResult struct {
	Item  string `json:"item"`
	Value string `json:"value"`
}

// VisitSummary 是复诊时携带的上次就诊摘要。
type VisitSummary struct {
	Diagnosis   *Diagnosis
	Medications []Medication
}

// RefusalRecord 是患者拒绝某项执行动作的记录。
type RefusalRecord struct {
	What string // "test_pay" | "med_pay" ...
	At   time.Time
}

// Event 是急症守护读取的事件。
type Event struct {
	Kind string // "dialog" | "vital" | "test_result"
	Data any
}

// OrchestratorFeedback 是编排层在 pull / 重调时回灌给 AI 的瞬时反馈。
type OrchestratorFeedback struct {
	LastReject   RejectReason
	NextExpected string
	CardDeferred bool
	MissingHint  []string
}

// Snapshot 是喂给 AI 的全量上下文。AI 无状态，每次从 Snapshot 重建。
// 注意：轮次计数不在此结构内——熔断由编排层强制，不提示 AI。
type Snapshot struct {
	Interview   []DialogTurn
	Subjective  map[string]any
	TestResults []TestResult
	Diagnosis   *Diagnosis
	PriorVisit  *VisitSummary
	Refusals    []RefusalRecord

	Feedback *OrchestratorFeedback
}

// DecisionLayer 是主决策层接口。全部无状态。
type DecisionLayer interface {
	Interview(ctx context.Context, s Snapshot) (InterviewResult, error)
	Triage(ctx context.Context, s Snapshot) (TriageDecision, error)
	Treatment(ctx context.Context, s Snapshot) (TreatmentPlan, error)
}

// Guardian 是急症守护接口，纯判断。并发/ticker 由编排层负责。
type Guardian interface {
	Assess(ctx context.Context, s Snapshot, ev Event) (EmergencyInterrupt, bool, error)
}
```

- [ ] **Step 2: 创建 ai/intent.go（仅类型，Validate 在 Task 2）**

```go
package ai

// RejectReason 是编排层语义校验的回传原因（镜像系统规格 §4.2）。
type RejectReason string

const (
	RejectNone                   RejectReason = ""
	RejectSchemaInvalid          RejectReason = "SCHEMA_INVALID"
	RejectIllegalTransition      RejectReason = "ILLEGAL_TRANSITION"
	RejectCapabilityMissing      RejectReason = "CAPABILITY_MISSING"
	RejectSubjectiveNotExhausted RejectReason = "SUBJECTIVE_NOT_EXHAUSTED"
	RejectRoundLimitFused        RejectReason = "ROUND_LIMIT_FUSED"
)

// AdvanceToTriage：问诊充分，请求进入决策。
type AdvanceToTriage struct {
	Subjective map[string]any `json:"subjective"`
}

// InterviewResult 是问诊 agent 的产出：要么继续追问（Reply），要么 Advance。
type InterviewResult struct {
	Reply   string           `json:"reply"`
	Advance *AdvanceToTriage `json:"advance,omitempty"`
}

// TriageChoice 是收敛环三选一。
type TriageChoice string

const (
	TriageConfirm   TriageChoice = "CONFIRM"
	TriageInterview TriageChoice = "INTERVIEW"
	TriageTest      TriageChoice = "TEST"
)

// TriageDecision 是 triage agent 的产出。
type TriageDecision struct {
	Decision            TriageChoice `json:"decision"`
	Diagnosis           *Diagnosis   `json:"diagnosis,omitempty"`
	MissingSubjective   []string     `json:"missing_subjective,omitempty"`
	SubjectiveExhausted bool         `json:"subjective_exhausted,omitempty"`
	Reason              string       `json:"reason,omitempty"`
	TestItems           []string     `json:"test_items,omitempty"`
}

// PlanKind 是处置四选一。
type PlanKind string

const (
	PlanMedication PlanKind = "MEDICATION"
	PlanTreatment  PlanKind = "TREATMENT"
	PlanAdviceOnly PlanKind = "ADVICE_ONLY"
	PlanReferral   PlanKind = "REFERRAL"
)

// TreatmentPlan 是处置 agent 的产出。
type TreatmentPlan struct {
	Plan               PlanKind     `json:"plan"`
	Advice             string       `json:"advice"`
	Medications        []Medication `json:"medications,omitempty"`
	RequiredCapability string       `json:"required_capability,omitempty"`
	ReferralReason     string       `json:"referral_reason,omitempty"`
}

// EmergencyInterrupt 是急症守护的产出。
type EmergencyInterrupt struct {
	Reason string `json:"reason"`
}
```

- [ ] **Step 3: 验证编译**

Run: `go build ./...`
Expected: 退出码 0

- [ ] **Step 4: Commit**

```bash
git add ai/contract.go ai/intent.go
git commit -m "feat(ai): 定义 Snapshot / Event / intent 契约类型"
```

---

## Task 2: intent 结构校验 Validate()

**Files:**
- Modify: `ai/intent.go`（追加 `import "fmt"` 与各 `Validate` 方法）
- Test: `ai/intent_test.go`

- [ ] **Step 1: 写失败测试 ai/intent_test.go**

```go
package ai

import "testing"

func TestAdvanceToTriageValidate(t *testing.T) {
	if err := (AdvanceToTriage{Subjective: map[string]any{"主诉": "咽痛"}}).Validate(); err != nil {
		t.Fatalf("期望通过，得到 %v", err)
	}
	if err := (AdvanceToTriage{}).Validate(); err == nil {
		t.Fatal("空 subjective 应失败")
	}
}

func TestTriageDecisionValidate(t *testing.T) {
	cases := []struct {
		name string
		in   TriageDecision
		ok   bool
	}{
		{"confirm_ok", TriageDecision{Decision: TriageConfirm, Diagnosis: &Diagnosis{Name: "急性咽炎", Confidence: 0.9}}, true},
		{"confirm_no_diag", TriageDecision{Decision: TriageConfirm}, false},
		{"confirm_bad_conf", TriageDecision{Decision: TriageConfirm, Diagnosis: &Diagnosis{Name: "x", Confidence: 1.5}}, false},
		{"interview_ok", TriageDecision{Decision: TriageInterview, MissingSubjective: []string{"体温"}}, true},
		{"interview_empty", TriageDecision{Decision: TriageInterview}, false},
		{"test_ok", TriageDecision{Decision: TriageTest, SubjectiveExhausted: true, Reason: "区分感染", TestItems: []string{"血常规"}}, true},
		{"test_not_exhausted", TriageDecision{Decision: TriageTest, Reason: "x", TestItems: []string{"血常规"}}, false},
		{"test_no_items", TriageDecision{Decision: TriageTest, SubjectiveExhausted: true, Reason: "x"}, false},
		{"bad_decision", TriageDecision{Decision: "FOO"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.in.Validate()
			if c.ok && err != nil {
				t.Fatalf("期望通过，得到 %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("期望失败，却通过")
			}
		})
	}
}

func TestTreatmentPlanValidate(t *testing.T) {
	cases := []struct {
		name string
		in   TreatmentPlan
		ok   bool
	}{
		{"med_ok", TreatmentPlan{Plan: PlanMedication, Advice: "多休息", Medications: []Medication{{Name: "x"}}}, true},
		{"med_no_meds", TreatmentPlan{Plan: PlanMedication, Advice: "多休息"}, false},
		{"no_advice", TreatmentPlan{Plan: PlanAdviceOnly}, false},
		{"advice_only_ok", TreatmentPlan{Plan: PlanAdviceOnly, Advice: "观察"}, true},
		{"treat_ok", TreatmentPlan{Plan: PlanTreatment, Advice: "a", RequiredCapability: "理疗"}, true},
		{"treat_no_cap", TreatmentPlan{Plan: PlanTreatment, Advice: "a"}, false},
		{"referral_ok", TreatmentPlan{Plan: PlanReferral, Advice: "a", ReferralReason: "无能力"}, true},
		{"referral_no_reason", TreatmentPlan{Plan: PlanReferral, Advice: "a"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.in.Validate()
			if c.ok && err != nil {
				t.Fatalf("期望通过，得到 %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("期望失败，却通过")
			}
		})
	}
}

func TestInterviewResultValidate(t *testing.T) {
	if err := (InterviewResult{Reply: "请问体温多少？"}).Validate(); err != nil {
		t.Fatalf("纯追问应通过，得到 %v", err)
	}
	if err := (InterviewResult{Advance: &AdvanceToTriage{Subjective: map[string]any{"a": 1}}}).Validate(); err != nil {
		t.Fatalf("带 advance 应通过，得到 %v", err)
	}
	if err := (InterviewResult{}).Validate(); err == nil {
		t.Fatal("既无 reply 又无 advance 应失败")
	}
	if err := (InterviewResult{Advance: &AdvanceToTriage{}}).Validate(); err == nil {
		t.Fatal("advance.subjective 为空应失败")
	}
}

func TestEmergencyInterruptValidate(t *testing.T) {
	if err := (EmergencyInterrupt{Reason: "胸痛伴呼吸困难"}).Validate(); err != nil {
		t.Fatalf("期望通过，得到 %v", err)
	}
	if err := (EmergencyInterrupt{}).Validate(); err == nil {
		t.Fatal("空 reason 应失败")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./ai/ -run Validate -v`
Expected: 编译失败（`Validate` 未定义）

- [ ] **Step 3: 在 ai/intent.go 追加 Validate 实现**

在文件顶部把 `package ai` 改为带 import：

```go
package ai

import "fmt"
```

在文件末尾追加：

```go
// Validate 做结构校验（字段齐全 / enum 合法 / 自证字段存在），对应 SCHEMA_INVALID。
func (a AdvanceToTriage) Validate() error {
	if len(a.Subjective) == 0 {
		return fmt.Errorf("advance_to_triage: subjective 为空")
	}
	return nil
}

func (r InterviewResult) Validate() error {
	if r.Advance != nil {
		return r.Advance.Validate()
	}
	if r.Reply == "" {
		return fmt.Errorf("interview: reply 为空且无 advance")
	}
	return nil
}

func (t TriageDecision) Validate() error {
	switch t.Decision {
	case TriageConfirm:
		if t.Diagnosis == nil {
			return fmt.Errorf("triage CONFIRM: diagnosis 缺失")
		}
		if t.Diagnosis.Name == "" {
			return fmt.Errorf("triage CONFIRM: diagnosis.name 为空")
		}
		if t.Diagnosis.Confidence < 0 || t.Diagnosis.Confidence > 1 {
			return fmt.Errorf("triage CONFIRM: confidence 越界 %v", t.Diagnosis.Confidence)
		}
	case TriageInterview:
		if len(t.MissingSubjective) == 0 {
			return fmt.Errorf("triage INTERVIEW: missing_subjective 为空")
		}
	case TriageTest:
		if !t.SubjectiveExhausted {
			return fmt.Errorf("triage TEST: 必须 subjective_exhausted=true")
		}
		if t.Reason == "" {
			return fmt.Errorf("triage TEST: reason 为空")
		}
		if len(t.TestItems) == 0 {
			return fmt.Errorf("triage TEST: test_items 为空")
		}
	default:
		return fmt.Errorf("triage: 非法 decision %q", t.Decision)
	}
	return nil
}

func (p TreatmentPlan) Validate() error {
	if p.Advice == "" {
		return fmt.Errorf("treatment: advice 恒需非空")
	}
	switch p.Plan {
	case PlanMedication:
		if len(p.Medications) == 0 {
			return fmt.Errorf("treatment MEDICATION: medications 为空")
		}
	case PlanTreatment:
		if p.RequiredCapability == "" {
			return fmt.Errorf("treatment TREATMENT: required_capability 为空")
		}
	case PlanAdviceOnly:
		// advice 已在上方校验
	case PlanReferral:
		if p.ReferralReason == "" {
			return fmt.Errorf("treatment REFERRAL: referral_reason 为空")
		}
	default:
		return fmt.Errorf("treatment: 非法 plan %q", p.Plan)
	}
	return nil
}

func (e EmergencyInterrupt) Validate() error {
	if e.Reason == "" {
		return fmt.Errorf("emergency_interrupt: reason 为空")
	}
	return nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./ai/ -run Validate -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add ai/intent.go ai/intent_test.go
git commit -m "feat(ai): intent 结构校验 Validate()"
```

---

## Task 3: LLMClient 接口与请求/响应类型

**Files:**
- Create: `ai/llm.go`

- [ ] **Step 1: 创建 ai/llm.go**

```go
package ai

import (
	"context"
	"encoding/json"
)

// Message 是一条对话消息。
type Message struct {
	Role    string // "user" | "assistant"
	Content string
}

// OutputSchema 描述期望输出的 JSON schema；Name 供 tool-use 命名用。
type OutputSchema struct {
	Name string
	JSON json.RawMessage
}

// CompletionRequest 是一次 LLM 调用的入参。
type CompletionRequest struct {
	System   string
	Messages []Message
	Schema   OutputSchema
}

// CompletionResult 是一次 LLM 调用的结构化产出。
type CompletionResult struct {
	Structured json.RawMessage // 符合 Schema 的 JSON
	Raw        string          // 原始文本，调试/日志用
}

// LLMClient 是 provider 中立的结构化输出接口。
// 只保证“结构化”，不做语义校验。真实实现把 Schema 映射为 tool-use 或 response_format。
type LLMClient interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResult, error)
}
```

- [ ] **Step 2: 验证编译**

Run: `go build ./...`
Expected: 退出码 0

- [ ] **Step 3: Commit**

```bash
git add ai/llm.go
git commit -m "feat(ai): LLMClient 抽象接口"
```

---

## Task 4: 可编程 FakeLLM 与 StructuredOf

**Files:**
- Create: `ai/llm_fake.go`
- Test: `ai/llm_fake_test.go`

- [ ] **Step 1: 写失败测试 ai/llm_fake_test.go**

```go
package ai

import (
	"context"
	"testing"
)

func TestFakeLLMReturnsScripted(t *testing.T) {
	f := &FakeLLM{On: func(req CompletionRequest) (CompletionResult, error) {
		if req.Schema.Name != "x" {
			t.Fatalf("未透传 schema name: %q", req.Schema.Name)
		}
		return StructuredOf(map[string]any{"ok": true}), nil
	}}
	res, err := f.Complete(context.Background(), CompletionRequest{Schema: OutputSchema{Name: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Structured) != `{"ok":true}` {
		t.Fatalf("结构化输出不符: %s", res.Structured)
	}
}

func TestFakeLLMHonorsCanceledContext(t *testing.T) {
	f := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		t.Fatal("ctx 已取消不应调用 On")
		return CompletionResult{}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Complete(ctx, CompletionRequest{}); err == nil {
		t.Fatal("期望 ctx.Err()")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./ai/ -run FakeLLM -v`
Expected: 编译失败（`FakeLLM` / `StructuredOf` 未定义）

- [ ] **Step 3: 创建 ai/llm_fake.go**

```go
package ai

import (
	"context"
	"encoding/json"
)

// FakeLLM 是测试用的可编程 LLMClient。On 既能断言入参又能返回构造输出。
type FakeLLM struct {
	On func(req CompletionRequest) (CompletionResult, error)
}

func (f *FakeLLM) Complete(ctx context.Context, req CompletionRequest) (CompletionResult, error) {
	if err := ctx.Err(); err != nil {
		return CompletionResult{}, err
	}
	return f.On(req)
}

// StructuredOf 把任意值 marshal 成 CompletionResult.Structured，便于构造预设输出。
func StructuredOf(v any) CompletionResult {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // 测试辅助：输入应可序列化
	}
	return CompletionResult{Structured: b, Raw: string(b)}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./ai/ -run FakeLLM -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add ai/llm_fake.go ai/llm_fake_test.go
git commit -m "test(ai): 可编程 FakeLLM 与 StructuredOf"
```

---

## Task 5: 上下文构建 buildMessages 与压缩

**Files:**
- Create: `ai/config.go`
- Create: `ai/snapshot.go`
- Test: `ai/snapshot_test.go`

- [ ] **Step 1: 创建 ai/config.go**

```go
package ai

const (
	// InterviewRawTurns 是 buildMessages 保留的最近原文轮数，更早折叠成 digest。
	InterviewRawTurns = 6
	// SchemaRetryMax 是 schema-invalid 时 agent 内部重试次数（K）。
	SchemaRetryMax = 2
)
```

- [ ] **Step 2: 写失败测试 ai/snapshot_test.go**

```go
package ai

import (
	"strings"
	"testing"
)

func TestBuildMessagesIncludesStructuredFields(t *testing.T) {
	s := Snapshot{
		Subjective:  map[string]any{"体温": "38.5", "主诉": "咽痛"},
		TestResults: []TestResult{{Item: "血常规", Value: "淋巴偏高"}},
		Diagnosis:   &Diagnosis{Name: "急性咽炎", Basis: "症状+检验", Confidence: 0.9},
	}
	msgs := buildMessages(s)
	if len(msgs) == 0 || msgs[0].Role != "user" {
		t.Fatalf("首条应为 user 快照块，得到 %+v", msgs)
	}
	block := msgs[0].Content
	for _, want := range []string{"体温", "主诉", "血常规", "急性咽炎"} {
		if !strings.Contains(block, want) {
			t.Fatalf("快照块缺少 %q：\n%s", want, block)
		}
	}
	// 主观信息按 key 排序，输出确定性："主诉" 应在 "体温" 之前（按 Unicode 码点）
	if strings.Index(block, "主诉") > strings.Index(block, "体温") {
		t.Fatal("主观信息未按 key 稳定排序")
	}
}

func TestBuildMessagesNoRoundCounters(t *testing.T) {
	// Snapshot 根本不含轮次计数；断言渲染结果不泄漏任何轮次字样。
	s := Snapshot{Subjective: map[string]any{"主诉": "咽痛"}}
	block := buildMessages(s)[0].Content
	for _, leak := range []string{"轮次", "InterviewRounds", "TestRounds", "rounds"} {
		if strings.Contains(block, leak) {
			t.Fatalf("快照块泄漏轮次信息 %q", leak)
		}
	}
}

func TestBuildMessagesRecentTurnsBecomeMessages(t *testing.T) {
	s := Snapshot{Interview: []DialogTurn{
		{Role: "patient", Text: "嗓子痛"},
		{Role: "doctor", Text: "发烧吗"},
		{Role: "patient", Text: "38.5"},
	}}
	msgs := buildMessages(s)
	// 1 条快照块 + 3 条原文
	if len(msgs) != 4 {
		t.Fatalf("期望 4 条消息，得到 %d", len(msgs))
	}
	if msgs[1].Role != "user" || msgs[1].Content != "嗓子痛" {
		t.Fatalf("patient→user 映射错误：%+v", msgs[1])
	}
	if msgs[2].Role != "assistant" || msgs[2].Content != "发烧吗" {
		t.Fatalf("doctor→assistant 映射错误：%+v", msgs[2])
	}
}

func TestBuildMessagesCompressesEarlyTurns(t *testing.T) {
	var turns []DialogTurn
	for i := 0; i < InterviewRawTurns+4; i++ {
		turns = append(turns, DialogTurn{Role: "patient", Text: "早期对话"})
	}
	turns = append(turns, DialogTurn{Role: "patient", Text: "最新一句"})
	s := Snapshot{Interview: turns}
	msgs := buildMessages(s)
	// 最近 InterviewRawTurns 轮成原文消息，其余进 digest（在快照块里）
	if got := len(msgs) - 1; got != InterviewRawTurns {
		t.Fatalf("期望 %d 条原文消息，得到 %d", InterviewRawTurns, got)
	}
	if !strings.Contains(msgs[0].Content, "早期对话摘要") {
		t.Fatalf("快照块缺少早期对话摘要：\n%s", msgs[0].Content)
	}
}

func TestBuildMessagesRendersFeedback(t *testing.T) {
	s := Snapshot{Feedback: &OrchestratorFeedback{
		LastReject:  RejectSubjectiveNotExhausted,
		MissingHint: []string{"基础病史", "用药史"},
	}}
	block := buildMessages(s)[0].Content
	if !strings.Contains(block, string(RejectSubjectiveNotExhausted)) {
		t.Fatal("未渲染 LastReject")
	}
	if !strings.Contains(block, "基础病史") {
		t.Fatal("未渲染 MissingHint")
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./ai/ -run BuildMessages -v`
Expected: 编译失败（`buildMessages` 未定义）

- [ ] **Step 4: 创建 ai/snapshot.go**

```go
package ai

import (
	"fmt"
	"sort"
	"strings"
)

// buildMessages 从 Snapshot 重建 LLM 上下文。
// 结构化字段全量带入；Interview 仅保留最近 InterviewRawTurns 轮原文，
// 更早的折叠成 digest 行并入快照块（阳性发现/关键体征不丢——按原文保留）。
func buildMessages(s Snapshot) []Message {
	split := len(s.Interview) - InterviewRawTurns
	if split < 0 {
		split = 0
	}
	early := s.Interview[:split]
	recent := s.Interview[split:]

	msgs := []Message{{Role: "user", Content: renderSnapshotBlock(s, early)}}
	for _, tn := range recent {
		msgs = append(msgs, Message{Role: roleOf(tn.Role), Content: tn.Text})
	}
	return msgs
}

func roleOf(turn string) string {
	if turn == "doctor" {
		return "assistant"
	}
	return "user"
}

func renderSnapshotBlock(s Snapshot, early []DialogTurn) string {
	var b strings.Builder
	b.WriteString("【就诊快照】\n")

	if len(s.Subjective) > 0 {
		b.WriteString("主观信息:\n")
		keys := make([]string, 0, len(s.Subjective))
		for k := range s.Subjective {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  - %s: %v\n", k, s.Subjective[k])
		}
	}
	if len(s.TestResults) > 0 {
		b.WriteString("检验结果:\n")
		for _, r := range s.TestResults {
			fmt.Fprintf(&b, "  - %s: %s\n", r.Item, r.Value)
		}
	}
	if s.Diagnosis != nil {
		fmt.Fprintf(&b, "已确诊: %s（依据: %s，置信度 %.2f）\n",
			s.Diagnosis.Name, s.Diagnosis.Basis, s.Diagnosis.Confidence)
	}
	if s.PriorVisit != nil && s.PriorVisit.Diagnosis != nil {
		fmt.Fprintf(&b, "上次就诊: %s\n", s.PriorVisit.Diagnosis.Name)
	}
	if len(s.Refusals) > 0 {
		b.WriteString("患者拒绝记录:\n")
		for _, r := range s.Refusals {
			fmt.Fprintf(&b, "  - %s\n", r.What)
		}
	}
	if len(early) > 0 {
		b.WriteString("早期对话摘要:\n")
		for _, tn := range early {
			who := "患者"
			if tn.Role == "doctor" {
				who = "医生"
			}
			fmt.Fprintf(&b, "  · %s: %s\n", who, tn.Text)
		}
	}
	if s.Feedback != nil {
		renderFeedback(&b, s.Feedback)
	}
	return b.String()
}

func renderFeedback(b *strings.Builder, f *OrchestratorFeedback) {
	b.WriteString("【编排反馈】\n")
	if f.LastReject != RejectNone {
		fmt.Fprintf(b, "上次意图被拒: %s\n", f.LastReject)
	}
	if len(f.MissingHint) > 0 {
		fmt.Fprintf(b, "需补充: %s\n", strings.Join(f.MissingHint, "、"))
	}
	if f.NextExpected != "" {
		fmt.Fprintf(b, "期望产出: %s\n", f.NextExpected)
	}
	if f.CardDeferred {
		b.WriteString("上一张卡已被暂缓\n")
	}
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./ai/ -run BuildMessages -v`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add ai/config.go ai/snapshot.go ai/snapshot_test.go
git commit -m "feat(ai): buildMessages 上下文构建与对话压缩"
```

---

## Task 6: prompts 与 OutputSchema

**Files:**
- Create: `ai/prompts.go`
- Test: `ai/prompts_test.go`

- [ ] **Step 1: 写失败测试 ai/prompts_test.go**

```go
package ai

import (
	"encoding/json"
	"testing"
)

func TestPromptsNonEmpty(t *testing.T) {
	for name, p := range map[string]string{
		"interview": promptInterview, "triage": promptTriage,
		"treatment": promptTreatment, "guardian": promptGuardian,
	} {
		if len(p) < 20 {
			t.Fatalf("prompt %q 过短或为空", name)
		}
	}
}

func TestSchemasAreValidJSONWithNames(t *testing.T) {
	for _, sc := range []OutputSchema{schemaInterview, schemaTriage, schemaTreatment, schemaEmergency} {
		if sc.Name == "" {
			t.Fatal("schema 缺少 Name")
		}
		if !json.Valid(sc.JSON) {
			t.Fatalf("schema %q 不是合法 JSON: %s", sc.Name, sc.JSON)
		}
	}
}

func TestSchemaNamesStable(t *testing.T) {
	want := map[*OutputSchema]string{
		&schemaInterview: "interview", &schemaTriage: "triage_decide",
		&schemaTreatment: "treatment_plan", &schemaEmergency: "emergency_interrupt",
	}
	for sc, n := range want {
		if sc.Name != n {
			t.Fatalf("schema name 漂移：期望 %q 得到 %q", n, sc.Name)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./ai/ -run "Prompts|Schema" -v`
Expected: 编译失败（prompt/schema 未定义）

- [ ] **Step 3: 创建 ai/prompts.go**

```go
package ai

import "encoding/json"

const promptInterview = `你是无人医院的 AI 导诊医生，正在问诊阶段采集主观信息。基于【就诊快照】中已采集的信息、对话历史和患者最新消息，判断信息是否足以交给决策环节。
规则：
- 一次只问一个最具鉴别诊断价值的问题，不要堆砌多个问题。
- 你只负责采集与追问，不下诊断（诊断由后续环节负责）。
- 若【编排反馈】指出“需补充”的项，优先围绕这些项提问。
- 当你判断主观信息已足够时，在 advance.subjective 中给出本轮采集到的结构化主诉/病史/体征，并在 reply 给患者一句简短过场。
- 语言为简体中文，口语化、患者能听懂。
- 这是概念性项目，按你的医学判断行事，不要套用固定的危险信号清单。
按给定 JSON schema 输出。`

const promptTriage = `你是无人医院的收敛判断器。基于【就诊快照】的全部信息（主观信息、检验结果、对话摘要）做三选一决策。
原则：主观信息优先，检验是最后手段。
- 能确诊：decision=CONFIRM，给出 diagnosis 的 name、basis、confidence(0~1)。
- 还缺主观信息：decision=INTERVIEW，在 missing_subjective 指明还需采集哪些项。
- 主观信息已问尽仍无法确诊：decision=TEST，必须 subjective_exhausted=true，给出 reason 说明为何必须借助检验，并在 test_items 列出检验项目。
- 若【编排反馈】指出上次意图被拒（如自证不成立），据此改选。
- 置信度由你自评。这是概念性项目，按你的医学判断行事，不套用固定危险信号清单。
按给定 JSON schema 输出。`

const promptTreatment = `你是无人医院的处置决策器，已经确诊（见【就诊快照】）。做四选一处置，并无条件写入医嘱 advice。
- advice 恒须给出（休息/饮水/观察/风险提示等），且不因患者态度而改变。
- MEDICATION 用药：在 medications 给出 name、dosage、schedule。
- TREATMENT 治疗：在 required_capability 给出用于比对本院能力清单的能力标识。
- ADVICE_ONLY 仅医嘱：只给 advice。
- REFERRAL 转诊：在 referral_reason 说明原因。
- 若【就诊快照】有患者拒绝记录，在 advice 注明相应执行状态与风险（如“未购药，注意…”）。
语言为简体中文。按给定 JSON schema 输出。`

const promptGuardian = `你是无人医院的急症守护，与主流程并行运行，实时读取全量信息流。基于【就诊快照】与【最新事件】，判断当前是否出现需要立即打断、转去急诊的危急情况。
- 若命中：hit=true，并在 reason 简述危急依据。
- 若未命中：hit=false。
- 这是概念性项目，按你的临床判断行事，不套用固定危险信号清单；宁严勿松，但不要无依据地打断。
按给定 JSON schema 输出。`

var schemaInterview = OutputSchema{Name: "interview", JSON: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reply": {"type": "string"},
    "advance": {"type": ["object", "null"], "properties": {"subjective": {"type": "object"}}, "required": ["subjective"]}
  },
  "required": ["reply"]
}`)}

var schemaTriage = OutputSchema{Name: "triage_decide", JSON: json.RawMessage(`{
  "type": "object",
  "properties": {
    "decision": {"type": "string", "enum": ["CONFIRM", "INTERVIEW", "TEST"]},
    "diagnosis": {"type": ["object", "null"], "properties": {"name": {"type": "string"}, "basis": {"type": "string"}, "confidence": {"type": "number"}}},
    "missing_subjective": {"type": "array", "items": {"type": "string"}},
    "subjective_exhausted": {"type": "boolean"},
    "reason": {"type": "string"},
    "test_items": {"type": "array", "items": {"type": "string"}}
  },
  "required": ["decision"]
}`)}

var schemaTreatment = OutputSchema{Name: "treatment_plan", JSON: json.RawMessage(`{
  "type": "object",
  "properties": {
    "plan": {"type": "string", "enum": ["MEDICATION", "TREATMENT", "ADVICE_ONLY", "REFERRAL"]},
    "advice": {"type": "string"},
    "medications": {"type": "array", "items": {"type": "object", "properties": {"name": {"type": "string"}, "dosage": {"type": "string"}, "schedule": {"type": "string"}}}},
    "required_capability": {"type": "string"},
    "referral_reason": {"type": "string"}
  },
  "required": ["plan", "advice"]
}`)}

var schemaEmergency = OutputSchema{Name: "emergency_interrupt", JSON: json.RawMessage(`{
  "type": "object",
  "properties": {
    "hit": {"type": "boolean"},
    "reason": {"type": "string"}
  },
  "required": ["hit"]
}`)}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./ai/ -run "Prompts|Schema" -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add ai/prompts.go ai/prompts_test.go
git commit -m "feat(ai): 4 个 agent 的 system prompt 与 OutputSchema"
```

---

## Task 7: 错误类型与通用 runStructured 骨架

**Files:**
- Create: `ai/errors.go`
- Create: `ai/agent.go`
- Test: `ai/agent_test.go`

- [ ] **Step 1: 创建 ai/errors.go**

```go
package ai

import (
	"errors"
	"fmt"
)

// ErrLLM 包装 LLM 传输/超时/取消类错误。
var ErrLLM = errors.New("llm call failed")

// SchemaError 表示模型输出在 K 次重试后仍不合 schema。
type SchemaError struct {
	Agent    string // "interview" | "triage" | "treatment" | "guardian"
	Attempts int
	LastRaw  string
	Cause    error
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf("agent %s: 输出在 %d 次尝试后仍不合 schema: %v", e.Agent, e.Attempts, e.Cause)
}

func (e *SchemaError) Unwrap() error { return e.Cause }
```

- [ ] **Step 2: 写失败测试 ai/agent_test.go**

```go
package ai

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// 测试用的最小 intent 类型。
type fakeIntent struct {
	OK bool `json:"ok"`
}

func (f fakeIntent) Validate() error {
	if !f.OK {
		return fmt.Errorf("not ok")
	}
	return nil
}

func run(ctx context.Context, llm LLMClient) (fakeIntent, error) {
	return runStructured[fakeIntent](ctx, llm, "test", "sys", OutputSchema{Name: "t"}, Snapshot{})
}

func TestRunStructuredHappyPath(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(fakeIntent{OK: true}), nil
	}}
	out, err := run(context.Background(), llm)
	if err != nil || !out.OK {
		t.Fatalf("期望成功，得到 out=%+v err=%v", out, err)
	}
}

func TestRunStructuredWrapsLLMError(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return CompletionResult{}, errors.New("boom")
	}}
	_, err := run(context.Background(), llm)
	if !errors.Is(err, ErrLLM) {
		t.Fatalf("期望 ErrLLM，得到 %v", err)
	}
}

func TestRunStructuredCanceledContext(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(fakeIntent{OK: true}), nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := run(ctx, llm); err == nil {
		t.Fatal("期望 ctx 取消错误")
	}
}

func TestRunStructuredRetriesThenSucceeds(t *testing.T) {
	n := 0
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		n++
		if n == 1 {
			return StructuredOf(fakeIntent{OK: false}), nil // 首次非法
		}
		return StructuredOf(fakeIntent{OK: true}), nil
	}}
	out, err := run(context.Background(), llm)
	if err != nil || !out.OK {
		t.Fatalf("期望 K 次内自纠，得到 out=%+v err=%v", out, err)
	}
	if n != 2 {
		t.Fatalf("期望调用 2 次，实际 %d", n)
	}
}

func TestRunStructuredExhaustsRetries(t *testing.T) {
	calls := 0
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		calls++
		return StructuredOf(fakeIntent{OK: false}), nil
	}}
	_, err := run(context.Background(), llm)
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("期望 *SchemaError，得到 %v", err)
	}
	if se.Attempts != SchemaRetryMax+1 || calls != SchemaRetryMax+1 {
		t.Fatalf("重试次数不符：attempts=%d calls=%d", se.Attempts, calls)
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./ai/ -run RunStructured -v`
Expected: 编译失败（`runStructured` 未定义）

- [ ] **Step 4: 创建 ai/agent.go**

```go
package ai

import (
	"context"
	"encoding/json"
	"fmt"
)

// validatable 是所有可结构校验的 intent 的约束。
type validatable interface {
	Validate() error
}

// runStructured 执行通用 6 步：构建上下文 → 调 LLM → 反序列化 → 结构校验 → 内部重试。
// schema-invalid 在包内自纠（≤SchemaRetryMax 次）；LLM 传输错误与 ctx 取消立即上抛。
func runStructured[T validatable](
	ctx context.Context, llm LLMClient, agentName, system string, schema OutputSchema, s Snapshot,
) (T, error) {
	var zero T
	msgs := buildMessages(s)
	var lastRaw string
	var lastErr error

	for attempt := 0; attempt <= SchemaRetryMax; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		res, err := llm.Complete(ctx, CompletionRequest{System: system, Messages: msgs, Schema: schema})
		if err != nil {
			return zero, fmt.Errorf("%w: %s: %v", ErrLLM, agentName, err)
		}
		lastRaw = res.Raw

		var out T
		if err := json.Unmarshal(res.Structured, &out); err != nil {
			lastErr = err
			msgs = appendCorrection(msgs, res.Raw, err)
			continue
		}
		if err := out.Validate(); err != nil {
			lastErr = err
			msgs = appendCorrection(msgs, res.Raw, err)
			continue
		}
		return out, nil
	}
	return zero, &SchemaError{Agent: agentName, Attempts: SchemaRetryMax + 1, LastRaw: lastRaw, Cause: lastErr}
}

func appendCorrection(msgs []Message, raw string, validationErr error) []Message {
	return append(msgs, Message{
		Role:    "user",
		Content: fmt.Sprintf("你上次的输出不符合要求：%v。原始输出：%s。请严格按 schema 重新输出。", validationErr, raw),
	})
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./ai/ -run RunStructured -v`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add ai/errors.go ai/agent.go ai/agent_test.go
git commit -m "feat(ai): 错误类型与通用 runStructured 6 步骨架"
```

---

## Task 8: 问诊 agent

**Files:**
- Create: `ai/agent_interview.go`
- Test: `ai/agent_interview_test.go`

- [ ] **Step 1: 写失败测试 ai/agent_interview_test.go**

```go
package ai

import (
	"context"
	"strings"
	"testing"
)

func TestInterviewReplyOnly(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(InterviewResult{Reply: "发烧最高多少度？"}), nil
	}}
	a := interviewAgent{llm: llm}
	res, err := a.decide(context.Background(), Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Advance != nil || res.Reply == "" {
		t.Fatalf("期望纯追问，得到 %+v", res)
	}
}

func TestInterviewAdvance(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(InterviewResult{
			Reply:   "信息够了",
			Advance: &AdvanceToTriage{Subjective: map[string]any{"主诉": "咽痛"}},
		}), nil
	}}
	a := interviewAgent{llm: llm}
	res, err := a.decide(context.Background(), Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Advance == nil || res.Advance.Subjective["主诉"] != "咽痛" {
		t.Fatalf("期望 advance，得到 %+v", res)
	}
}

func TestInterviewInjectsMissingHint(t *testing.T) {
	var seen string
	llm := &FakeLLM{On: func(req CompletionRequest) (CompletionResult, error) {
		seen = req.Messages[0].Content
		return StructuredOf(InterviewResult{Reply: "好的"}), nil
	}}
	a := interviewAgent{llm: llm}
	s := Snapshot{Feedback: &OrchestratorFeedback{MissingHint: []string{"用药史"}}}
	if _, err := a.decide(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen, "用药史") {
		t.Fatalf("MissingHint 未注入上下文：%s", seen)
	}
	if req := a.system(); req != promptInterview {
		t.Fatal("system prompt 不是 promptInterview")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./ai/ -run Interview -v`
Expected: 编译失败（`interviewAgent` 未定义）

- [ ] **Step 3: 创建 ai/agent_interview.go**

```go
package ai

import "context"

// interviewAgent 是问诊 agent：采集主观信息、对话追问。
type interviewAgent struct{ llm LLMClient }

func (a interviewAgent) system() string { return promptInterview }

func (a interviewAgent) decide(ctx context.Context, s Snapshot) (InterviewResult, error) {
	return runStructured[InterviewResult](ctx, a.llm, "interview", promptInterview, schemaInterview, s)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./ai/ -run Interview -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add ai/agent_interview.go ai/agent_interview_test.go
git commit -m "feat(ai): 问诊 agent"
```

---

## Task 9: triage agent

**Files:**
- Create: `ai/agent_triage.go`
- Test: `ai/agent_triage_test.go`

- [ ] **Step 1: 写失败测试 ai/agent_triage_test.go**

```go
package ai

import (
	"context"
	"errors"
	"testing"
)

func TestTriageThreeWay(t *testing.T) {
	cases := []TriageDecision{
		{Decision: TriageConfirm, Diagnosis: &Diagnosis{Name: "急性咽炎", Confidence: 0.9}},
		{Decision: TriageInterview, MissingSubjective: []string{"体温"}},
		{Decision: TriageTest, SubjectiveExhausted: true, Reason: "区分感染", TestItems: []string{"血常规"}},
	}
	for _, want := range cases {
		w := want
		llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
			return StructuredOf(w), nil
		}}
		got, err := (triageAgent{llm: llm}).decide(context.Background(), Snapshot{})
		if err != nil {
			t.Fatalf("%s: %v", w.Decision, err)
		}
		if got.Decision != w.Decision {
			t.Fatalf("期望 %s 得到 %s", w.Decision, got.Decision)
		}
	}
}

func TestTriageTestMissingSelfProofExhaustsRetries(t *testing.T) {
	// TEST 但缺 subjective_exhausted —— 结构校验失败，内部重试耗尽
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(TriageDecision{Decision: TriageTest, Reason: "x", TestItems: []string{"血常规"}}), nil
	}}
	_, err := (triageAgent{llm: llm}).decide(context.Background(), Snapshot{})
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("期望 SchemaError，得到 %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./ai/ -run Triage -v`
Expected: 编译失败（`triageAgent` 未定义）

- [ ] **Step 3: 创建 ai/agent_triage.go**

```go
package ai

import "context"

// triageAgent 是收敛环决策点：CONFIRM / INTERVIEW / TEST 三选一。
type triageAgent struct{ llm LLMClient }

func (a triageAgent) decide(ctx context.Context, s Snapshot) (TriageDecision, error) {
	return runStructured[TriageDecision](ctx, a.llm, "triage", promptTriage, schemaTriage, s)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./ai/ -run Triage -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add ai/agent_triage.go ai/agent_triage_test.go
git commit -m "feat(ai): triage agent"
```

---

## Task 10: 处置 agent

**Files:**
- Create: `ai/agent_treatment.go`
- Test: `ai/agent_treatment_test.go`

- [ ] **Step 1: 写失败测试 ai/agent_treatment_test.go**

```go
package ai

import (
	"context"
	"strings"
	"testing"
)

func TestTreatmentFourPlansAlwaysAdvice(t *testing.T) {
	plans := []TreatmentPlan{
		{Plan: PlanMedication, Advice: "多休息", Medications: []Medication{{Name: "对乙酰氨基酚"}}},
		{Plan: PlanTreatment, Advice: "复查", RequiredCapability: "理疗"},
		{Plan: PlanAdviceOnly, Advice: "观察体温"},
		{Plan: PlanReferral, Advice: "尽快就医", ReferralReason: "本院无能力"},
	}
	for _, want := range plans {
		w := want
		llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
			return StructuredOf(w), nil
		}}
		got, err := (treatmentAgent{llm: llm}).decide(context.Background(), Snapshot{Diagnosis: &Diagnosis{Name: "x"}})
		if err != nil {
			t.Fatalf("%s: %v", w.Plan, err)
		}
		if got.Advice == "" {
			t.Fatalf("%s: advice 不应为空", w.Plan)
		}
	}
}

func TestTreatmentInjectsRefusals(t *testing.T) {
	var seen string
	llm := &FakeLLM{On: func(req CompletionRequest) (CompletionResult, error) {
		seen = req.Messages[0].Content
		return StructuredOf(TreatmentPlan{Plan: PlanAdviceOnly, Advice: "未购药，注意复测"}), nil
	}}
	s := Snapshot{Diagnosis: &Diagnosis{Name: "x"}, Refusals: []RefusalRecord{{What: "med_pay"}}}
	if _, err := (treatmentAgent{llm: llm}).decide(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen, "med_pay") {
		t.Fatalf("拒绝记录未注入上下文：%s", seen)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./ai/ -run Treatment -v`
Expected: 编译失败（`treatmentAgent` 未定义）

- [ ] **Step 3: 创建 ai/agent_treatment.go**

```go
package ai

import "context"

// treatmentAgent 是处置决策器：确诊后四选一，并无条件写入医嘱。
type treatmentAgent struct{ llm LLMClient }

func (a treatmentAgent) decide(ctx context.Context, s Snapshot) (TreatmentPlan, error) {
	return runStructured[TreatmentPlan](ctx, a.llm, "treatment", promptTreatment, schemaTreatment, s)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./ai/ -run Treatment -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add ai/agent_treatment.go ai/agent_treatment_test.go
git commit -m "feat(ai): 处置 agent"
```

---

## Task 11: 急症守护 agent

**Files:**
- Create: `ai/agent_guardian.go`
- Test: `ai/agent_guardian_test.go`

- [ ] **Step 1: 写失败测试 ai/agent_guardian_test.go**

```go
package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGuardianHit(t *testing.T) {
	llm := &FakeLLM{On: func(req CompletionRequest) (CompletionResult, error) {
		if !strings.Contains(req.Messages[len(req.Messages)-1].Content, "胸痛") {
			t.Fatal("事件未注入上下文")
		}
		return StructuredOf(emergencyWire{Hit: true, Reason: "胸痛伴呼吸困难"}), nil
	}}
	ei, hit, err := guardianAgent{llm: llm}.Assess(context.Background(), Snapshot{}, Event{Kind: "vital", Data: "胸痛"})
	if err != nil || !hit || ei.Reason == "" {
		t.Fatalf("期望命中，得到 ei=%+v hit=%v err=%v", ei, hit, err)
	}
}

func TestGuardianNoHit(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(emergencyWire{Hit: false}), nil
	}}
	_, hit, err := guardianAgent{llm: llm}.Assess(context.Background(), Snapshot{}, Event{Kind: "dialog", Data: "嗓子痛"})
	if err != nil || hit {
		t.Fatalf("期望不命中，得到 hit=%v err=%v", hit, err)
	}
}

func TestGuardianLLMErrorDoesNotFabricateHit(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return CompletionResult{}, errors.New("down")
	}}
	_, hit, err := guardianAgent{llm: llm}.Assess(context.Background(), Snapshot{}, Event{})
	if hit {
		t.Fatal("基础设施故障不得臆造打断")
	}
	if !errors.Is(err, ErrLLM) {
		t.Fatalf("期望 ErrLLM，得到 %v", err)
	}
}

func TestGuardianHitWithEmptyReasonIsSchemaError(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(emergencyWire{Hit: true}), nil
	}}
	_, hit, err := guardianAgent{llm: llm}.Assess(context.Background(), Snapshot{}, Event{})
	var se *SchemaError
	if hit || !errors.As(err, &se) {
		t.Fatalf("命中但缺 reason 应为 SchemaError，得到 hit=%v err=%v", hit, err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./ai/ -run Guardian -v`
Expected: 编译失败（`guardianAgent` / `emergencyWire` 未定义）

- [ ] **Step 3: 创建 ai/agent_guardian.go**

```go
package ai

import (
	"context"
	"encoding/json"
	"fmt"
)

// guardianAgent 是急症守护：并行读全量信息流，命中即打断。单次判断，不内部重试。
type guardianAgent struct{ llm LLMClient }

// emergencyWire 是守护输出的线格式（hit + reason）。
type emergencyWire struct {
	Hit    bool   `json:"hit"`
	Reason string `json:"reason"`
}

func (a guardianAgent) Assess(ctx context.Context, s Snapshot, ev Event) (EmergencyInterrupt, bool, error) {
	if err := ctx.Err(); err != nil {
		return EmergencyInterrupt{}, false, err
	}
	msgs := append(buildMessages(s), Message{Role: "user", Content: renderEvent(ev)})
	res, err := a.llm.Complete(ctx, CompletionRequest{System: promptGuardian, Messages: msgs, Schema: schemaEmergency})
	if err != nil {
		return EmergencyInterrupt{}, false, fmt.Errorf("%w: guardian: %v", ErrLLM, err)
	}
	var w emergencyWire
	if err := json.Unmarshal(res.Structured, &w); err != nil {
		return EmergencyInterrupt{}, false, &SchemaError{Agent: "guardian", Attempts: 1, LastRaw: res.Raw, Cause: err}
	}
	if !w.Hit {
		return EmergencyInterrupt{}, false, nil
	}
	ei := EmergencyInterrupt{Reason: w.Reason}
	if err := ei.Validate(); err != nil {
		return EmergencyInterrupt{}, false, &SchemaError{Agent: "guardian", Attempts: 1, LastRaw: res.Raw, Cause: err}
	}
	return ei, true, nil
}

func renderEvent(ev Event) string {
	return fmt.Sprintf("【最新事件】类型 %s：%v", ev.Kind, ev.Data)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./ai/ -run Guardian -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add ai/agent_guardian.go ai/agent_guardian_test.go
git commit -m "feat(ai): 急症守护 agent"
```

---

## Task 12: DecisionLayer 组装

**Files:**
- Create: `ai/layer.go`
- Test: `ai/layer_test.go`

- [ ] **Step 1: 写失败测试 ai/layer_test.go**

```go
package ai

import (
	"context"
	"testing"
)

func TestLayerImplementsInterfaces(t *testing.T) {
	var _ DecisionLayer = NewDecisionLayer(&FakeLLM{})
	var _ Guardian = NewGuardian(&FakeLLM{})
}

func TestLayerRoutesToAgents(t *testing.T) {
	llm := &FakeLLM{On: func(req CompletionRequest) (CompletionResult, error) {
		switch req.Schema.Name {
		case "interview":
			return StructuredOf(InterviewResult{Reply: "hi"}), nil
		case "triage_decide":
			return StructuredOf(TriageDecision{Decision: TriageInterview, MissingSubjective: []string{"体温"}}), nil
		case "treatment_plan":
			return StructuredOf(TreatmentPlan{Plan: PlanAdviceOnly, Advice: "观察"}), nil
		}
		t.Fatalf("未预期 schema %q", req.Schema.Name)
		return CompletionResult{}, nil
	}}
	l := NewDecisionLayer(llm)
	ctx := context.Background()
	if r, err := l.Interview(ctx, Snapshot{}); err != nil || r.Reply != "hi" {
		t.Fatalf("Interview 路由错误 r=%+v err=%v", r, err)
	}
	if d, err := l.Triage(ctx, Snapshot{}); err != nil || d.Decision != TriageInterview {
		t.Fatalf("Triage 路由错误 d=%+v err=%v", d, err)
	}
	if p, err := l.Treatment(ctx, Snapshot{}); err != nil || p.Plan != PlanAdviceOnly {
		t.Fatalf("Treatment 路由错误 p=%+v err=%v", p, err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./ai/ -run Layer -v`
Expected: 编译失败（`NewDecisionLayer` / `NewGuardian` 未定义）

- [ ] **Step 3: 创建 ai/layer.go**

```go
package ai

import "context"

// Layer 组装 4 个 agent，实现 DecisionLayer。
type Layer struct {
	interview interviewAgent
	triage    triageAgent
	treatment treatmentAgent
}

// NewDecisionLayer 用给定 LLMClient 构造决策层。
func NewDecisionLayer(llm LLMClient) *Layer {
	return &Layer{
		interview: interviewAgent{llm: llm},
		triage:    triageAgent{llm: llm},
		treatment: treatmentAgent{llm: llm},
	}
}

func (l *Layer) Interview(ctx context.Context, s Snapshot) (InterviewResult, error) {
	return l.interview.decide(ctx, s)
}
func (l *Layer) Triage(ctx context.Context, s Snapshot) (TriageDecision, error) {
	return l.triage.decide(ctx, s)
}
func (l *Layer) Treatment(ctx context.Context, s Snapshot) (TreatmentPlan, error) {
	return l.treatment.decide(ctx, s)
}

// NewGuardian 用给定 LLMClient 构造急症守护。
func NewGuardian(llm LLMClient) Guardian { return guardianAgent{llm: llm} }

var (
	_ DecisionLayer = (*Layer)(nil)
	_ Guardian      = guardianAgent{}
)
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./ai/ -run Layer -v`
Expected: 全部 PASS

- [ ] **Step 5: 运行全部单测**

Run: `go test ./ai/ -v`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add ai/layer.go ai/layer_test.go
git commit -m "feat(ai): DecisionLayer 组装与构造函数"
```

---

## Task 13: mock 编排 harness 与急性咽炎 walkthrough（主路径）

**Files:**
- Create: `ai/internal/harness/harness.go`
- Test: `ai/internal/harness/harness_test.go`

- [ ] **Step 1: 写失败测试 ai/internal/harness/harness_test.go**

```go
package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"medagent/ai"
)

// scriptedLLM 按 schema name + 调用计数复刻急性咽炎场景。
func scriptedLLM(t *testing.T) *ai.FakeLLM {
	interviewN, triageN := 0, 0
	return &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		switch req.Schema.Name {
		case "interview":
			interviewN++
			if interviewN == 1 {
				return ai.StructuredOf(ai.InterviewResult{Reply: "发烧最高多少度？有没有咳嗽？"}), nil
			}
			return ai.StructuredOf(ai.InterviewResult{
				Reply: "信息够了，我来判断一下。",
				Advance: &ai.AdvanceToTriage{Subjective: map[string]any{
					"主诉": "咽痛发热", "体温": "38.5", "咳嗽": "干咳",
				}},
			}), nil
		case "triage_decide":
			triageN++
			if triageN == 1 {
				return ai.StructuredOf(ai.TriageDecision{
					Decision: ai.TriageTest, SubjectiveExhausted: true,
					Reason: "需区分细菌或病毒感染", TestItems: []string{"血常规"},
				}), nil
			}
			return ai.StructuredOf(ai.TriageDecision{
				Decision:  ai.TriageConfirm,
				Diagnosis: &ai.Diagnosis{Name: "急性咽炎", Basis: "症状+体温+血常规", Confidence: 0.9},
			}), nil
		case "treatment_plan":
			return ai.StructuredOf(ai.TreatmentPlan{
				Plan: ai.PlanMedication, Advice: "多休息、多饮水，观察体温",
				Medications: []ai.Medication{{Name: "对乙酰氨基酚", Dosage: "0.5g", Schedule: "每日3次"}},
			}), nil
		}
		t.Fatalf("未预期 schema %q", req.Schema.Name)
		return ai.CompletionResult{}, fmt.Errorf("unreachable")
	}}
}

func TestWalkthroughAcutePharyngitis(t *testing.T) {
	llm := scriptedLLM(t)
	answers := []string{"嗓子痛、有点发烧，从昨晚开始。", "38.5℃，有点干咳，呼吸正常。"}
	i := 0
	deps := Deps{
		Layer: ai.NewDecisionLayer(llm),
		Caps:  map[string]bool{},
		Patient: func(string) string {
			msg := answers[i]
			if i < len(answers)-1 {
				i++
			}
			return msg
		},
		TestResults: func([]string) []ai.TestResult {
			return []ai.TestResult{{Item: "血常规", Value: "淋巴细胞偏高，提示病毒"}}
		},
	}
	out, err := RunVisit(context.Background(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if out.Final != "ADVICE" || out.Plan != ai.PlanMedication {
		t.Fatalf("终态不符：%+v", out)
	}
	if out.Diagnosis == nil || out.Diagnosis.Name != "急性咽炎" {
		t.Fatalf("诊断不符：%+v", out.Diagnosis)
	}
	trace := strings.Join(out.Trace, ",")
	for _, want := range []string{"advance", "triage:TEST", "test_filled", "triage:CONFIRM", "treatment:MEDICATION"} {
		if !strings.Contains(trace, want) {
			t.Fatalf("轨迹缺少 %q：%s", want, trace)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./ai/internal/harness/ -run Walkthrough -v`
Expected: 编译失败（`Deps` / `RunVisit` 未定义）

- [ ] **Step 3: 创建 ai/internal/harness/harness.go**

```go
// Package harness 是极简 mock 编排层：只够驱动 AI 决策层走通主干，
// 用于端到端测试与 demo。它不是真实编排层（无卡片/saga/持久化）。
package harness

import (
	"context"
	"fmt"

	"medagent/ai"
)

// Deps 是一次就诊所需的外部依赖（测试注入）。
type Deps struct {
	Layer       ai.DecisionLayer
	Caps        map[string]bool                       // 本院能力清单
	Patient     func(lastDoctorReply string) string   // 返回下一句患者消息
	TestResults func(items []string) []ai.TestResult  // 桩化检验回填
	Prior       *ai.VisitSummary                      // 复诊上次摘要（可为 nil）
}

// Outcome 是一次就诊的终态。
type Outcome struct {
	Final     string // "ADVICE" | "REFERRAL"
	Diagnosis *ai.Diagnosis
	Plan      ai.PlanKind
	Advice    string
	Trace     []string
}

const (
	maxInterviewTurns = 20
	maxTriageRounds   = 10
)

// RunVisit 驱动一次就诊：问诊采集 → 收敛环 → 处置 → 终态。
func RunVisit(ctx context.Context, d Deps) (Outcome, error) {
	snap := ai.Snapshot{PriorVisit: d.Prior, Subjective: map[string]any{}}
	var trace []string

	if err := interviewPhase(ctx, d, &snap, &trace); err != nil {
		return Outcome{}, err
	}

	for round := 0; ; round++ {
		if round >= maxTriageRounds {
			return Outcome{}, fmt.Errorf("收敛环未在 %d 轮内收敛", maxTriageRounds)
		}
		td, err := d.Layer.Triage(ctx, snap)
		if err != nil {
			return Outcome{}, err
		}
		trace = append(trace, "triage:"+string(td.Decision))
		switch td.Decision {
		case ai.TriageConfirm:
			snap.Diagnosis = td.Diagnosis
			snap.Feedback = nil
			return treatmentPhase(ctx, d, &snap, trace)
		case ai.TriageInterview:
			snap.Feedback = &ai.OrchestratorFeedback{MissingHint: td.MissingSubjective}
			if err := interviewPhase(ctx, d, &snap, &trace); err != nil {
				return Outcome{}, err
			}
			snap.Feedback = nil
		case ai.TriageTest:
			snap.TestResults = append(snap.TestResults, d.TestResults(td.TestItems)...)
			trace = append(trace, "test_filled")
		default:
			return Outcome{}, fmt.Errorf("非法 triage decision %q", td.Decision)
		}
	}
}

func interviewPhase(ctx context.Context, d Deps, snap *ai.Snapshot, trace *[]string) error {
	reply := ""
	for turn := 0; turn < maxInterviewTurns; turn++ {
		msg := d.Patient(reply)
		snap.Interview = append(snap.Interview, ai.DialogTurn{Role: "patient", Text: msg})
		res, err := d.Layer.Interview(ctx, *snap)
		if err != nil {
			return err
		}
		if res.Advance != nil {
			for k, v := range res.Advance.Subjective {
				snap.Subjective[k] = v
			}
			*trace = append(*trace, "advance")
			return nil
		}
		snap.Interview = append(snap.Interview, ai.DialogTurn{Role: "doctor", Text: res.Reply})
		reply = res.Reply
	}
	return fmt.Errorf("问诊未在 %d 轮内收敛", maxInterviewTurns)
}

func treatmentPhase(ctx context.Context, d Deps, snap *ai.Snapshot, trace []string) (Outcome, error) {
	for {
		tp, err := d.Layer.Treatment(ctx, *snap)
		if err != nil {
			return Outcome{}, err
		}
		trace = append(trace, "treatment:"+string(tp.Plan))
		if tp.Plan == ai.PlanTreatment && !d.Caps[tp.RequiredCapability] {
			snap.Feedback = &ai.OrchestratorFeedback{LastReject: ai.RejectCapabilityMissing}
			continue
		}
		final := "ADVICE"
		if tp.Plan == ai.PlanReferral {
			final = "REFERRAL"
		}
		return Outcome{
			Final: final, Diagnosis: snap.Diagnosis, Plan: tp.Plan, Advice: tp.Advice, Trace: trace,
		}, nil
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./ai/internal/harness/ -run Walkthrough -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add ai/internal/harness/harness.go ai/internal/harness/harness_test.go
git commit -m "feat(harness): mock 编排驱动与急性咽炎 walkthrough"
```

---

## Task 14: harness 变体用例

**Files:**
- Test: `ai/internal/harness/variants_test.go`

- [ ] **Step 1: 写测试 ai/internal/harness/variants_test.go**

```go
package harness

import (
	"context"
	"strings"
	"testing"

	"medagent/ai"
)

// 简单患者：按序返回预设答案，用尽后重复最后一句。
func seqPatient(answers ...string) func(string) string {
	i := 0
	return func(string) string {
		msg := answers[i]
		if i < len(answers)-1 {
			i++
		}
		return msg
	}
}

func TestVariantMultiRoundTest(t *testing.T) {
	triageN := 0
	llm := &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		switch req.Schema.Name {
		case "interview":
			return ai.StructuredOf(ai.InterviewResult{Advance: &ai.AdvanceToTriage{
				Subjective: map[string]any{"主诉": "腹痛"}}}), nil
		case "triage_decide":
			triageN++
			if triageN <= 2 {
				return ai.StructuredOf(ai.TriageDecision{
					Decision: ai.TriageTest, SubjectiveExhausted: true,
					Reason: "需进一步检验", TestItems: []string{"血常规"}}), nil
			}
			return ai.StructuredOf(ai.TriageDecision{Decision: ai.TriageConfirm,
				Diagnosis: &ai.Diagnosis{Name: "胃肠炎", Confidence: 0.85}}), nil
		case "treatment_plan":
			return ai.StructuredOf(ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "清淡饮食"}), nil
		}
		return ai.CompletionResult{}, nil
	}}
	out, err := RunVisit(context.Background(), Deps{
		Layer: ai.NewDecisionLayer(llm), Caps: map[string]bool{},
		Patient:     seqPatient("肚子疼"),
		TestResults: func([]string) []ai.TestResult { return []ai.TestResult{{Item: "血常规", Value: "正常"}} },
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.Join(out.Trace, ","), "test_filled"); n != 2 {
		t.Fatalf("期望 2 轮检验，得到 %d；轨迹=%v", n, out.Trace)
	}
}

func TestVariantInterviewBounce(t *testing.T) {
	triageN := 0
	llm := &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		switch req.Schema.Name {
		case "interview":
			return ai.StructuredOf(ai.InterviewResult{Advance: &ai.AdvanceToTriage{
				Subjective: map[string]any{"主诉": "头痛"}}}), nil
		case "triage_decide":
			triageN++
			if triageN == 1 {
				return ai.StructuredOf(ai.TriageDecision{Decision: ai.TriageInterview,
					MissingSubjective: []string{"持续时间"}}), nil
			}
			return ai.StructuredOf(ai.TriageDecision{Decision: ai.TriageConfirm,
				Diagnosis: &ai.Diagnosis{Name: "紧张性头痛", Confidence: 0.8}}), nil
		case "treatment_plan":
			return ai.StructuredOf(ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "规律作息"}), nil
		}
		return ai.CompletionResult{}, nil
	}}
	out, err := RunVisit(context.Background(), Deps{
		Layer: ai.NewDecisionLayer(llm), Caps: map[string]bool{},
		Patient:     seqPatient("头痛", "持续两天"),
		TestResults: func([]string) []ai.TestResult { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.Join(out.Trace, ","), "advance"); n != 2 {
		t.Fatalf("期望问诊两次（含 INTERVIEW 回退），得到 %d；轨迹=%v", n, out.Trace)
	}
}

func TestVariantCapabilityMissingReferral(t *testing.T) {
	treatN := 0
	llm := &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		switch req.Schema.Name {
		case "interview":
			return ai.StructuredOf(ai.InterviewResult{Advance: &ai.AdvanceToTriage{
				Subjective: map[string]any{"主诉": "心悸"}}}), nil
		case "triage_decide":
			return ai.StructuredOf(ai.TriageDecision{Decision: ai.TriageConfirm,
				Diagnosis: &ai.Diagnosis{Name: "心律失常", Confidence: 0.9}}), nil
		case "treatment_plan":
			treatN++
			if treatN == 1 {
				return ai.StructuredOf(ai.TreatmentPlan{Plan: ai.PlanTreatment,
					Advice: "尽快处理", RequiredCapability: "心脏介入"}), nil
			}
			return ai.StructuredOf(ai.TreatmentPlan{Plan: ai.PlanReferral,
				Advice: "尽快前往上级医院", ReferralReason: "本院无心脏介入能力"}), nil
		}
		return ai.CompletionResult{}, nil
	}}
	out, err := RunVisit(context.Background(), Deps{
		Layer: ai.NewDecisionLayer(llm), Caps: map[string]bool{"理疗": true}, // 不含“心脏介入”
		Patient:     seqPatient("心慌"),
		TestResults: func([]string) []ai.TestResult { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Final != "REFERRAL" || out.Plan != ai.PlanReferral {
		t.Fatalf("期望转诊终态，得到 %+v", out)
	}
	trace := strings.Join(out.Trace, ",")
	if !strings.Contains(trace, "treatment:TREATMENT") || !strings.Contains(trace, "treatment:REFERRAL") {
		t.Fatalf("期望能力不具备→重决策→转诊，轨迹=%s", trace)
	}
}

func TestVariantRevisitCarriesPrior(t *testing.T) {
	var firstInterviewCtx string
	llm := &ai.FakeLLM{On: func(req ai.CompletionRequest) (ai.CompletionResult, error) {
		switch req.Schema.Name {
		case "interview":
			if firstInterviewCtx == "" {
				firstInterviewCtx = req.Messages[0].Content
			}
			return ai.StructuredOf(ai.InterviewResult{Advance: &ai.AdvanceToTriage{
				Subjective: map[string]any{"主诉": "复诊咽痛未愈"}}}), nil
		case "triage_decide":
			return ai.StructuredOf(ai.TriageDecision{Decision: ai.TriageConfirm,
				Diagnosis: &ai.Diagnosis{Name: "急性咽炎", Confidence: 0.88}}), nil
		case "treatment_plan":
			return ai.StructuredOf(ai.TreatmentPlan{Plan: ai.PlanAdviceOnly, Advice: "继续观察"}), nil
		}
		return ai.CompletionResult{}, nil
	}}
	out, err := RunVisit(context.Background(), Deps{
		Layer: ai.NewDecisionLayer(llm), Caps: map[string]bool{},
		Patient:     seqPatient("还是嗓子痛"),
		TestResults: func([]string) []ai.TestResult { return nil },
		Prior:       &ai.VisitSummary{Diagnosis: &ai.Diagnosis{Name: "急性咽炎"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Final != "ADVICE" {
		t.Fatalf("复诊应正常收尾，得到 %+v", out)
	}
	if !strings.Contains(firstInterviewCtx, "上次就诊") {
		t.Fatalf("复诊上下文未携带 PriorVisit：%s", firstInterviewCtx)
	}
}
```

- [ ] **Step 2: 运行测试确认通过**

Run: `go test ./ai/internal/harness/ -run Variant -v`
Expected: 全部 PASS（RunVisit 已支持这些路径，无需改实现）

- [ ] **Step 3: 运行全部测试 + vet**

Run: `go test ./... && go vet ./...`
Expected: 全部 PASS、vet 无告警

- [ ] **Step 4: Commit**

```bash
git add ai/internal/harness/variants_test.go
git commit -m "test(harness): 多轮检验/问诊回退/能力转诊/复诊 变体"
```

---

## Self-Review 结果

**1. Spec 覆盖：**
- §3 包结构与接口 → Task 1/3/12（contract、llm、layer）✅
- §4 数据契约 + Validate → Task 1/2 ✅
- §5 LLM 抽象 + 6 步骨架 + buildMessages → Task 3/4/5/7 ✅
- §6 4 个 agent + prompts → Task 6/8/9/10/11 ✅
- §7 数据流（主回路/外环/横切）→ Task 13/14（harness 驱动外环；guardian 纯函数 Task 11）✅
- §8 错误处理（ErrLLM / SchemaError / ctx 中止 / guardian 不臆造）→ Task 7/11 ✅
- §9 测试（fake / harness / walkthrough / 不变式 / 诚实边界）→ Task 4/13/14；不变式：检验回到 triage(Task14 多轮)、advice 恒输出(Task10)、counters 不喂(Task5)、自证结构强制(Task9)✅
- §10 配置项 → Task 5（config.go）✅
- §9.5 真实 LLM 集成测试 → 明确推迟（provider 未定），不在本计划，非遗漏。

**2. 占位符扫描：** 无 TBD/TODO；每个 code step 均含完整代码。✅

**3. 类型一致性：** `buildMessages`、`runStructured[T validatable]`、`interviewAgent/triageAgent/treatmentAgent/guardianAgent`、`NewDecisionLayer/NewGuardian`、schema 名（interview/triage_decide/treatment_plan/emergency_interrupt）、`emergencyWire`、`Deps/Outcome/RunVisit` 跨任务一致。✅