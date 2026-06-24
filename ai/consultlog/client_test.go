package consultlog

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"medagent/ai"
)

var _ ai.LLMClient = (*Client)(nil)

// memSink 是测试用内存 sink；err 非 nil 时 Write 返回它。
type memSink struct {
	recs []CallRecord
	err  error
}

func (m *memSink) Write(rec CallRecord) error {
	m.recs = append(m.recs, rec)
	return m.err
}

func TestWrapRecordsSuccessfulCall(t *testing.T) {
	inner := &ai.FakeLLM{On: func(ai.CompletionRequest) (ai.CompletionResult, error) {
		return ai.CompletionResult{Structured: json.RawMessage(`{"ok":true}`), Raw: `{"ok":true}`}, nil
	}}
	sink := &memSink{}
	c := Wrap(inner, sink)

	ctx := WithVisitID(context.Background(), "visit-1")
	res, err := c.Complete(ctx, ai.CompletionRequest{
		System:   "你是医生",
		Messages: []ai.Message{{Role: "user", Content: "发烧"}},
		Schema:   ai.OutputSchema{Name: "interview"},
	})
	if err != nil {
		t.Fatalf("Complete err: %v", err)
	}
	if res.Raw != `{"ok":true}` {
		t.Errorf("结果未透传：%q", res.Raw)
	}
	if len(sink.recs) != 1 {
		t.Fatalf("应记 1 条，得 %d", len(sink.recs))
	}
	r := sink.recs[0]
	if r.VisitID != "visit-1" || r.Schema != "interview" || r.System != "你是医生" {
		t.Errorf("记录字段错：%+v", r)
	}
	if len(r.Messages) != 1 || r.Messages[0].Content != "发烧" {
		t.Errorf("messages 未记：%+v", r.Messages)
	}
	if string(r.Structured) != `{"ok":true}` || r.Error != "" {
		t.Errorf("成功记录的 structured/error 错：%+v", r)
	}
	if r.LatencyMS < 0 {
		t.Errorf("latency 异常：%d", r.LatencyMS)
	}
}

func TestWrapRecordsErrorAndPropagates(t *testing.T) {
	boom := errors.New("boom")
	inner := &ai.FakeLLM{On: func(ai.CompletionRequest) (ai.CompletionResult, error) {
		return ai.CompletionResult{}, boom
	}}
	sink := &memSink{}
	c := Wrap(inner, sink)

	_, err := c.Complete(context.Background(), ai.CompletionRequest{Schema: ai.OutputSchema{Name: "triage_decide"}})
	if !errors.Is(err, boom) {
		t.Fatalf("应透传原 error，得 %v", err)
	}
	if len(sink.recs) != 1 || sink.recs[0].Error == "" {
		t.Fatalf("错误未被记录：%+v", sink.recs)
	}
}

func TestWrapSinkFailureDoesNotBreakComplete(t *testing.T) {
	inner := &ai.FakeLLM{On: func(ai.CompletionRequest) (ai.CompletionResult, error) {
		return ai.CompletionResult{Raw: "ok"}, nil
	}}
	c := Wrap(inner, &memSink{err: errors.New("sink down")})
	var captured error
	c.onErr = func(e error) { captured = e }

	res, err := c.Complete(context.Background(), ai.CompletionRequest{Schema: ai.OutputSchema{Name: "x"}})
	if err != nil {
		t.Fatalf("sink 失败不应影响 Complete：%v", err)
	}
	if res.Raw != "ok" {
		t.Errorf("结果应透传：%q", res.Raw)
	}
	if captured == nil {
		t.Errorf("sink 错误应交给 onErr")
	}
}
