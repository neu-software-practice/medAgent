package ai

import (
	"encoding/json"
	"testing"
)

func TestPromptsNonEmpty(t *testing.T) {
	for name, p := range map[string]string{
		"interview": promptInterview, "triage": promptTriage,
		"treatment": promptTreatment, "guardian": promptGuardian,
	} {
		if len(p) < 20 {
			t.Fatalf("prompt %q 过短或为空", name)
		}
	}
}

func TestSchemasAreValidJSONWithNames(t *testing.T) {
	for _, sc := range []OutputSchema{schemaInterview, schemaTriage, schemaTreatment, schemaEmergency} {
		if sc.Name == "" {
			t.Fatal("schema 缺少 Name")
		}
		if !json.Valid(sc.JSON) {
			t.Fatalf("schema %q 不是合法 JSON: %s", sc.Name, sc.JSON)
		}
	}
}

func TestSchemaNamesStable(t *testing.T) {
	want := map[*OutputSchema]string{
		&schemaInterview: "interview", &schemaTriage: "triage_decide",
		&schemaTreatment: "treatment_plan", &schemaEmergency: "emergency_interrupt",
	}
	for sc, n := range want {
		if sc.Name != n {
			t.Fatalf("schema name 漂移：期望 %q 得到 %q", n, sc.Name)
		}
	}
}
