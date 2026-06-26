package agent

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
