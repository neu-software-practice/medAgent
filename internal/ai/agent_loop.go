package ai

import (
	"context"
	"encoding/json"
	"fmt"
)

// BoundaryKind 标识 agent 让出给后端的边界动作类型（与 facade 的 Step.kind 对应）。
type BoundaryKind string

const (
	BoundaryAsk       BoundaryKind = "ASK"
	BoundaryTest      BoundaryKind = "NEED_TESTS"
	BoundaryDrugQuery BoundaryKind = "DRUG_QUERY"
	BoundaryPurchase  BoundaryKind = "PURCHASE"
)

// DrugOrder 是 ai 层表达的购药盒数（与 agent.DrugOrder 同义，避免 internal→facade 的反向依赖）。
type DrugOrder struct {
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
}

// Boundary 是一次让出：模型调用了需要外部世界的工具，挂起等后端回填。
// ToolCall 携带触发让出的工具调用，其 ID 供回填 tool_result 时配对。
type Boundary struct {
	Kind     BoundaryKind
	ToolCall ToolCall
	Question string      // ASK
	Items    []string    // NEED_TESTS（已归一为血常规）
	Names    []string    // DRUG_QUERY
	Orders   []DrugOrder // PURCHASE（盒数兜底由 facade 负责并记 warn）
}

// Terminal 是一次终态：模型调用 finish/refer 收尾，产出可校验的处置方案。
type Terminal struct {
	Plan      TreatmentPlan
	Diagnosis *Diagnosis
}

// StepResult 是 Engine.Step 的产出。Assistant 须由调用方 append 进 transcript；
// Boundary 与 Terminal 恰一非空。PromptTokens 为本步真实输入 token（压缩阈值用）。
type StepResult struct {
	Assistant    Message
	Boundary     *Boundary
	Terminal     *Terminal
	PromptTokens int
}

// Engine 是单 agent 的工具循环引擎：一次 Step 调一次 Chat 并解码模型选定的工具。
// 无状态——transcript 由调用方（facade）持有，每步传入。
type Engine struct {
	chat   ChatClient
	tools  []ToolSpec
	system string
}

// NewEngine 用给定 ChatClient 构造引擎（固定 toolset 与系统提示词）。
func NewEngine(chat ChatClient) *Engine {
	return &Engine{chat: chat, tools: toolset, system: systemPrompt}
}

// Step 跑一次循环：从 transcript 调一次 Chat（tool_choice=required，每步必选一个工具），
// 解码首个工具调用为 Boundary 或 Terminal。工具入参不合法时在包内自纠（≤SchemaRetryMax 次）：
// 把"不合法"作为该 tool_call 的 tool_result 回灌，重新让模型调用。
// LLM 传输错误与 ctx 取消立即上抛（不归类 SchemaError），便于 facade 区分"被打断"与"故障"。
func (e *Engine) Step(ctx context.Context, transcript []Message) (StepResult, error) {
	msgs := append([]Message(nil), transcript...)
	var lastErr error
	var lastRaw string

	for attempt := 0; attempt <= SchemaRetryMax; attempt++ {
		if err := ctx.Err(); err != nil {
			return StepResult{}, err
		}
		turn, err := e.chat.Chat(ctx, ChatRequest{System: e.system, Messages: msgs, Tools: e.tools, ToolChoice: "required"})
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return StepResult{}, ctxErr
			}
			return StepResult{}, fmt.Errorf("%w: engine: %w", ErrLLM, err)
		}
		if len(turn.ToolCalls) == 0 {
			// required 模式理论上必有工具调用；缺失则提示补一个工具后重试。
			lastErr, lastRaw = fmt.Errorf("模型未调用任何工具"), turn.Text
			msgs = append(msgs,
				Message{Role: "assistant", Content: turn.Text},
				Message{Role: "user", Content: "你必须调用恰好一个工具，请重新输出。"})
			continue
		}

		tc := turn.ToolCalls[0]
		assistant := Message{Role: "assistant", Content: turn.Text, ToolCalls: []ToolCall{tc}}

		b, t, derr := decodeTool(tc)
		if derr != nil {
			lastErr, lastRaw = derr, string(tc.Arguments)
			msgs = append(msgs, assistant, Message{Role: "tool", ToolCallID: tc.ID,
				Content: "参数不合法：" + derr.Error() + "。请按工具 schema 重新调用。"})
			continue
		}
		return StepResult{Assistant: assistant, Boundary: b, Terminal: t, PromptTokens: turn.PromptTokens}, nil
	}
	return StepResult{}, &SchemaError{Agent: "engine", Attempts: SchemaRetryMax + 1, LastRaw: lastRaw, Cause: lastErr}
}

// decodeTool 把模型选定的工具调用解码为 Boundary 或 Terminal（恰一非空）。
// 终态工具复用 TreatmentPlan.Validate 做结构校验；不合法返回 error 触发包内自纠。
func decodeTool(tc ToolCall) (*Boundary, *Terminal, error) {
	switch tc.Name {
	case "ask_patient":
		var a struct {
			Question string `json:"question"`
		}
		if err := json.Unmarshal(tc.Arguments, &a); err != nil {
			return nil, nil, err
		}
		if a.Question == "" {
			return nil, nil, fmt.Errorf("ask_patient: question 为空")
		}
		return &Boundary{Kind: BoundaryAsk, ToolCall: tc, Question: a.Question}, nil, nil

	case "order_test":
		// 本系统只支持血常规：无论模型给什么，归一为血常规。
		return &Boundary{Kind: BoundaryTest, ToolCall: tc, Items: []string{"血常规"}}, nil, nil

	case "query_drug_spec":
		var a struct {
			Names []string `json:"names"`
		}
		if err := json.Unmarshal(tc.Arguments, &a); err != nil {
			return nil, nil, err
		}
		if len(a.Names) == 0 {
			return nil, nil, fmt.Errorf("query_drug_spec: names 为空")
		}
		return &Boundary{Kind: BoundaryDrugQuery, ToolCall: tc, Names: a.Names}, nil, nil

	case "purchase_drug":
		var a struct {
			Orders []DrugOrder `json:"orders"`
		}
		if err := json.Unmarshal(tc.Arguments, &a); err != nil {
			return nil, nil, err
		}
		if len(a.Orders) == 0 {
			return nil, nil, fmt.Errorf("purchase_drug: orders 为空")
		}
		return &Boundary{Kind: BoundaryPurchase, ToolCall: tc, Orders: a.Orders}, nil, nil

	case "finish":
		var a struct {
			Diagnosis   *Diagnosis   `json:"diagnosis"`
			Plan        PlanKind     `json:"plan"`
			Medications []Medication `json:"medications"`
			Advice      string       `json:"advice"`
		}
		if err := json.Unmarshal(tc.Arguments, &a); err != nil {
			return nil, nil, err
		}
		if a.Plan == PlanReferral {
			return nil, nil, fmt.Errorf("finish 不接受 REFERRAL，转诊请用 refer 工具")
		}
		plan := TreatmentPlan{Plan: a.Plan, Advice: a.Advice, Medications: a.Medications}
		if err := plan.Validate(); err != nil {
			return nil, nil, err
		}
		return nil, &Terminal{Plan: plan, Diagnosis: a.Diagnosis}, nil

	case "refer":
		var a struct {
			Reason    string     `json:"reason"`
			Advice    string     `json:"advice"`
			Diagnosis *Diagnosis `json:"diagnosis"`
		}
		if err := json.Unmarshal(tc.Arguments, &a); err != nil {
			return nil, nil, err
		}
		plan := TreatmentPlan{Plan: PlanReferral, Advice: a.Advice, ReferralReason: a.Reason}
		if err := plan.Validate(); err != nil {
			return nil, nil, err
		}
		return nil, &Terminal{Plan: plan, Diagnosis: a.Diagnosis}, nil

	default:
		return nil, nil, fmt.Errorf("未知工具：%s", tc.Name)
	}
}
