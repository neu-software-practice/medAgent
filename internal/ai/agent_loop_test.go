package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func engineWith(on func(req ChatRequest) (AssistantTurn, error)) *Engine {
	return NewEngine(&FakeChat{OnChat: on})
}

func TestStep_AskBoundaryInjectsSystemAndTools(t *testing.T) {
	e := engineWith(func(req ChatRequest) (AssistantTurn, error) {
		if req.ToolChoice != "required" || len(req.Tools) != 6 || req.System == "" {
			t.Fatalf("请求未正确注入: choice=%q tools=%d system?=%v", req.ToolChoice, len(req.Tools), req.System != "")
		}
		return ToolCallTurn("c1", "ask_patient", map[string]any{"question": "发烧几天了？"}), nil
	})
	res, err := e.Step(context.Background(), []Message{{Role: "user", Content: "我发烧"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Boundary == nil || res.Boundary.Kind != BoundaryAsk || res.Boundary.Question != "发烧几天了？" {
		t.Fatalf("boundary = %+v", res.Boundary)
	}
	if res.Terminal != nil {
		t.Fatal("不应有 terminal")
	}
	if res.Assistant.Role != "assistant" || len(res.Assistant.ToolCalls) != 1 || res.Assistant.ToolCalls[0].ID != "c1" {
		t.Fatalf("assistant 须带 tool_call: %+v", res.Assistant)
	}
}

func TestStep_OrderTestNormalizedToBloodPanel(t *testing.T) {
	e := engineWith(func(req ChatRequest) (AssistantTurn, error) {
		return ToolCallTurn("c1", "order_test", map[string]any{"items": []string{"CT", "核磁"}}), nil
	})
	res, err := e.Step(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Boundary == nil || res.Boundary.Kind != BoundaryTest || len(res.Boundary.Items) != 1 || res.Boundary.Items[0] != "血常规" {
		t.Fatalf("应归一为血常规: %+v", res.Boundary)
	}
}

func TestStep_PurchaseBoundaryPassesOrders(t *testing.T) {
	e := engineWith(func(req ChatRequest) (AssistantTurn, error) {
		return ToolCallTurn("c1", "purchase_drug", map[string]any{
			"orders": []map[string]any{{"name": "阿莫西林", "quantity": 2}},
		}), nil
	})
	res, err := e.Step(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Boundary == nil || res.Boundary.Kind != BoundaryPurchase ||
		len(res.Boundary.Orders) != 1 || res.Boundary.Orders[0].Name != "阿莫西林" || res.Boundary.Orders[0].Quantity != 2 {
		t.Fatalf("orders = %+v", res.Boundary)
	}
}

func TestStep_FinishTerminal(t *testing.T) {
	e := engineWith(func(req ChatRequest) (AssistantTurn, error) {
		return ToolCallTurn("c1", "finish", map[string]any{
			"diagnosis": map[string]any{"name": "急性咽炎", "basis": "症状", "confidence": 0.9},
			"plan":      "ADVICE_ONLY",
			"advice":    "多休息多饮水",
		}), nil
	})
	res, err := e.Step(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Terminal == nil || res.Terminal.Plan.Plan != PlanAdviceOnly || res.Terminal.Diagnosis == nil || res.Terminal.Diagnosis.Name != "急性咽炎" {
		t.Fatalf("terminal = %+v", res.Terminal)
	}
}

func TestStep_ReferTerminal(t *testing.T) {
	e := engineWith(func(req ChatRequest) (AssistantTurn, error) {
		return ToolCallTurn("c1", "refer", map[string]any{"reason": "需手术", "advice": "尽快去上级医院"}), nil
	})
	res, err := e.Step(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Terminal == nil || res.Terminal.Plan.Plan != PlanReferral || res.Terminal.Plan.ReferralReason != "需手术" {
		t.Fatalf("terminal = %+v", res.Terminal)
	}
}

func TestStep_InvalidThenCorrected(t *testing.T) {
	n := 0
	e := engineWith(func(req ChatRequest) (AssistantTurn, error) {
		n++
		if n == 1 {
			// finish 缺 advice → Validate 失败 → 触发包内自纠
			return ToolCallTurn("c1", "finish", map[string]any{
				"diagnosis": map[string]any{"name": "x"}, "plan": "ADVICE_ONLY", "advice": "",
			}), nil
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role != "tool" || !strings.Contains(last.Content, "参数不合法") {
			t.Fatalf("未把纠错作为 tool_result 回灌: %+v", last)
		}
		return ToolCallTurn("c2", "finish", map[string]any{
			"diagnosis": map[string]any{"name": "急性咽炎"}, "plan": "ADVICE_ONLY", "advice": "多休息",
		}), nil
	})
	res, err := e.Step(context.Background(), []Message{{Role: "user", Content: "嗓子疼"}})
	if err != nil {
		t.Fatalf("自纠后应成功: %v", err)
	}
	if res.Terminal == nil || res.Terminal.Plan.Advice != "多休息" {
		t.Fatalf("terminal = %+v", res.Terminal)
	}
	if n != 2 {
		t.Fatalf("应调用 2 次，实际 %d", n)
	}
}

func TestStep_SchemaErrorAfterRetries(t *testing.T) {
	e := engineWith(func(req ChatRequest) (AssistantTurn, error) {
		return ToolCallTurn("c1", "finish", map[string]any{
			"diagnosis": map[string]any{"name": "x"}, "plan": "ADVICE_ONLY", "advice": "",
		}), nil
	})
	_, err := e.Step(context.Background(), nil)
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("应为 SchemaError, got %v", err)
	}
	if se.Attempts != SchemaRetryMax+1 {
		t.Fatalf("attempts = %d", se.Attempts)
	}
}

func TestStep_LLMErrorIsErrLLM(t *testing.T) {
	e := engineWith(func(req ChatRequest) (AssistantTurn, error) {
		return AssistantTurn{}, errors.New("boom")
	})
	if _, err := e.Step(context.Background(), nil); !errors.Is(err, ErrLLM) {
		t.Fatalf("应为 ErrLLM, got %v", err)
	}
}

func TestStep_CanceledCtx(t *testing.T) {
	e := engineWith(func(req ChatRequest) (AssistantTurn, error) {
		t.Fatal("ctx 取消不应调用 Chat")
		return AssistantTurn{}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Step(ctx, nil); err == nil {
		t.Fatal("应返回 ctx 错误")
	}
}
