package agent

import (
	"context"
	"testing"

	"medagent/internal/ai"
)

func TestGuardianHitPreempts(t *testing.T) {
	s := svcGuarded(chatScript(askT("继续问")), guardianHit("疑似心梗"))
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
	s := svcGuarded(chatScript(askT("请问哪里不舒服？")), guardianErr())
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
	guard := &ai.FakeLLM{On: func(ai.CompletionRequest) (ai.CompletionResult, error) {
		return ai.StructuredOf(map[string]any{"hit": hit, "reason": "血压骤降"}), nil
	}}
	s := svcGuarded(chatScript(askT("x")), guard)
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
