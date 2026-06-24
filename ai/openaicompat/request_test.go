package openaicompat

import (
	"encoding/json"
	"testing"

	"medagent/ai"
)

func TestBuildRequest_SystemFirstAndForcedToolChoice(t *testing.T) {
	req := ai.CompletionRequest{
		System: "你是医生",
		Messages: []ai.Message{
			{Role: "user", Content: "快照"},
			{Role: "user", Content: "患者轮"},
		},
		Schema: ai.OutputSchema{
			Name: "triage_decision",
			JSON: json.RawMessage(`{"type":"object"}`),
		},
	}
	got := buildRequest(req, "deepseek-chat")

	if got.Model != "deepseek-chat" {
		t.Errorf("model = %q", got.Model)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("want system + 1 merged user = 2, got %d: %+v", len(got.Messages), got.Messages)
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "你是医生" {
		t.Errorf("system msg = %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "user" || got.Messages[1].Content != "快照\n\n患者轮" {
		t.Errorf("merged user msg = %+v", got.Messages[1])
	}
	if len(got.Tools) != 1 || got.Tools[0].Type != "function" || got.Tools[0].Function.Name != "triage_decision" {
		t.Fatalf("tools = %+v", got.Tools)
	}
	if string(got.Tools[0].Function.Parameters) != `{"type":"object"}` {
		t.Errorf("parameters = %s", got.Tools[0].Function.Parameters)
	}
	if got.ToolChoice.Type != "function" || got.ToolChoice.Function.Name != "triage_decision" {
		t.Errorf("tool_choice = %+v", got.ToolChoice)
	}
}

func TestBuildRequest_NoSystemOmitsSystemMessage(t *testing.T) {
	req := ai.CompletionRequest{
		Messages: []ai.Message{{Role: "user", Content: "hi"}},
		Schema:   ai.OutputSchema{Name: "x", JSON: json.RawMessage(`{}`)},
	}
	got := buildRequest(req, "m")
	if len(got.Messages) != 1 || got.Messages[0].Role != "user" {
		t.Fatalf("want single user msg, got %+v", got.Messages)
	}
}
