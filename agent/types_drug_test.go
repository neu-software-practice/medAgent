package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStepDrugQueryMarshals(t *testing.T) {
	b, err := json.Marshal(Step{Kind: StepDrugQuery, DrugNames: []string{"布洛芬", "阿莫西林"}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"kind":"DRUG_QUERY"`) || !strings.Contains(s, `"drug_names":["布洛芬","阿莫西林"]`) {
		t.Fatalf("Step 序列化不符：%s", s)
	}
}

func TestDrugInfoMarshals(t *testing.T) {
	b, _ := json.Marshal(DrugInfo{Name: "布洛芬缓释胶囊", Spec: "每盒24粒×0.3g"})
	if string(b) != `{"name":"布洛芬缓释胶囊","spec":"每盒24粒×0.3g"}` {
		t.Fatalf("DrugInfo 序列化不符：%s", b)
	}
}
