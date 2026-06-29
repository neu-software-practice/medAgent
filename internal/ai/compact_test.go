package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEngineSummarize(t *testing.T) {
	var noTools bool
	e := NewEngine(&FakeChat{OnChat: func(req ChatRequest) (AssistantTurn, error) {
		noTools = len(req.Tools) == 0 && req.System == summarizePrompt
		return AssistantTurn{Text: "  摘要内容  "}, nil
	}})
	got, err := e.Summarize(context.Background(), []Message{{Role: "user", Content: "发烧"}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "摘要内容" {
		t.Fatalf("应 TrimSpace：%q", got)
	}
	if !noTools {
		t.Fatal("Summarize 应无工具、用 summarizePrompt")
	}
}

func TestEngineSummarizeError(t *testing.T) {
	e := NewEngine(&FakeChat{OnChat: func(ChatRequest) (AssistantTurn, error) {
		return AssistantTurn{}, errors.New("boom")
	}})
	if _, err := e.Summarize(context.Background(), nil); !errors.Is(err, ErrLLM) {
		t.Fatalf("应 ErrLLM，得 %v", err)
	}
}

func TestRenderTranscript(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "嗓子疼"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "ask_patient", Arguments: []byte(`{"question":"几天"}`)}}},
		{Role: "tool", Content: "三天"},
	}
	got := renderTranscript(msgs)
	for _, want := range []string{"患者/上下文: 嗓子疼", "医生动作[ask_patient]", "回填结果: 三天"} {
		if !strings.Contains(got, want) {
			t.Errorf("渲染缺 %q：%s", want, got)
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	if EstimateTokens(nil) != 0 {
		t.Fatal("空应为 0")
	}
	if n := EstimateTokens([]Message{{Role: "user", Content: "发烧咳嗽"}}); n != 4 {
		t.Fatalf("4 个 rune 应估 4，得 %d", n)
	}
}

func TestDecodeToolErrors(t *testing.T) {
	cases := []struct {
		name string
		tc   ToolCall
	}{
		{"ask_empty", ToolCall{Name: "ask_patient", Arguments: []byte(`{"question":""}`)}},
		{"drug_empty", ToolCall{Name: "query_drug_spec", Arguments: []byte(`{"names":[]}`)}},
		{"purchase_empty", ToolCall{Name: "purchase_drug", Arguments: []byte(`{"orders":[]}`)}},
		{"finish_referral", ToolCall{Name: "finish", Arguments: []byte(`{"plan":"REFERRAL","advice":"x","diagnosis":{"name":"y"}}`)}},
		{"finish_no_advice", ToolCall{Name: "finish", Arguments: []byte(`{"plan":"ADVICE_ONLY","advice":"","diagnosis":{"name":"y"}}`)}},
		{"refer_no_reason", ToolCall{Name: "refer", Arguments: []byte(`{"reason":"","advice":"x"}`)}},
		{"unknown", ToolCall{Name: "nope", Arguments: []byte(`{}`)}},
		{"bad_json", ToolCall{Name: "ask_patient", Arguments: []byte(`not json`)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := decodeTool(c.tc); err == nil {
				t.Fatal("应返回错误")
			}
		})
	}
}

func TestDecodeFinishMedication(t *testing.T) {
	b, tt, err := decodeTool(ToolCall{Name: "finish", Arguments: []byte(`{"plan":"MEDICATION","advice":"按医嘱","medications":[{"name":"阿莫西林","quantity":1}],"diagnosis":{"name":"咽炎"}}`)})
	if err != nil || b != nil || tt == nil {
		t.Fatalf("应得 Terminal：b=%v tt=%v err=%v", b, tt, err)
	}
	if tt.Plan.Plan != PlanMedication || len(tt.Plan.Medications) != 1 {
		t.Fatalf("plan 错：%+v", tt.Plan)
	}
}
