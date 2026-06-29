package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"medagent/internal/ai"
)

func httpSvc(t *testing.T) *httptest.Server {
	s := svcGuarded(chatScript(finishAdviceT("急性咽炎", "多休息")), noGuardian())
	t.Cleanup(func() { s.Close() })
	return httptest.NewServer(s.Handler())
}

func TestHTTPTimeoutMapsTo504(t *testing.T) {
	chat := chatFn(func(int) (ai.AssistantTurn, error) {
		// 模拟 LLM 客户端自身超时（http.Client.Timeout）：错误链含 context.DeadlineExceeded，请求 ctx 未取消
		return ai.AssistantTurn{}, fmt.Errorf("openaicompat: 请求失败 (%w): %w", context.DeadlineExceeded, ai.ErrLLM)
	})
	s := newService(Config{DisableGuardian: true}, ai.NewEngine(chat), ai.NewGuardian(noGuardian()))
	t.Cleanup(func() { s.Close() })
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	var start struct {
		SessionID string `json:"session_id"`
	}
	postJSON(t, srv.URL+"/sessions", map[string]any{"initial": true}, &start)

	resp, err := http.Post(srv.URL+"/sessions/"+start.SessionID+"/patient-say",
		"application/json", bytes.NewReader([]byte(`{"message":"hi"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("LLM 超时应映射 504，got %d", resp.StatusCode)
	}
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
