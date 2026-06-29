package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPDrugInfoFlow(t *testing.T) {
	s := svcGuarded(chatScript(
		queryDrugT("对乙酰氨基酚"),
		purchaseT(map[string]any{"name": "对乙酰氨基酚", "quantity": 2}),
	), noGuardian())
	t.Cleanup(func() { s.Close() })
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	var start struct {
		SessionID string `json:"session_id"`
	}
	postJSON(t, srv.URL+"/sessions", map[string]any{"initial": true}, &start)

	var step Step
	postJSON(t, srv.URL+"/sessions/"+start.SessionID+"/patient-say", map[string]any{"message": "嗓子疼"}, &step)
	if step.Kind != StepDrugQuery || len(step.DrugNames) != 1 {
		t.Fatalf("应 DRUG_QUERY：%+v", step)
	}

	var step2 Step
	postJSON(t, srv.URL+"/sessions/"+start.SessionID+"/drug-info",
		map[string]any{"infos": []map[string]any{{"name": "对乙酰氨基酚", "spec": "每盒12片×0.5g"}}}, &step2)
	if step2.Kind != StepPurchase || step2.Orders[0].Quantity != 2 {
		t.Fatalf("应 PURCHASE 盒数2：%+v", step2)
	}
}

func TestHTTPDrugInfoWrongStep409(t *testing.T) {
	srv := httpSvc(t) // 复用 httpapi_test.go 的 helper（finish 流，不进 DRUG_QUERY）
	defer srv.Close()
	var start struct {
		SessionID string `json:"session_id"`
	}
	postJSON(t, srv.URL+"/sessions", map[string]any{"initial": true}, &start)
	resp, _ := http.Post(srv.URL+"/sessions/"+start.SessionID+"/drug-info", "application/json",
		strings.NewReader(`{"infos":[]}`))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("非 DRUG_QUERY 态调 drug-info 应 409，得 %d", resp.StatusCode)
	}
}
