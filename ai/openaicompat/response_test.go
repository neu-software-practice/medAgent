package openaicompat

import (
	"errors"
	"testing"

	"medagent/ai"
)

func TestParseResult_ExtractsToolArguments(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"tool_calls":[{"function":{"name":"triage_decision","arguments":"{\"action\":\"treat\"}"}}]}}]}`)
	got, err := parseResult(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(got.Structured) != `{"action":"treat"}` {
		t.Errorf("Structured = %s", got.Structured)
	}
	if got.Raw != `{"action":"treat"}` {
		t.Errorf("Raw = %s", got.Raw)
	}
}

func TestParseResult_MissingToolCallsIsErrLLM(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"对不起"}}]}`)
	if _, err := parseResult(body); !errors.Is(err, ai.ErrLLM) {
		t.Fatalf("want ErrLLM, got %v", err)
	}
}

func TestParseResult_EmptyChoicesIsErrLLM(t *testing.T) {
	if _, err := parseResult([]byte(`{"choices":[]}`)); !errors.Is(err, ai.ErrLLM) {
		t.Fatalf("want ErrLLM, got %v", err)
	}
}

func TestParseResult_MalformedEnvelopeIsErrLLM(t *testing.T) {
	if _, err := parseResult([]byte(`not json`)); !errors.Is(err, ai.ErrLLM) {
		t.Fatalf("want ErrLLM, got %v", err)
	}
}
