package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMedicationCarriesQuantity(t *testing.T) {
	var m Medication
	if err := json.Unmarshal([]byte(`{"name":"阿莫西林","dosage":"0.5g","schedule":"每日3次","quantity":2}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.Quantity != 2 {
		t.Errorf("Quantity = %d, want 2", m.Quantity)
	}
}

func TestTreatmentSchemaHasQuantity(t *testing.T) {
	if !strings.Contains(string(schemaTreatment.JSON), `"quantity"`) {
		t.Errorf("schemaTreatment 缺 quantity：%s", schemaTreatment.JSON)
	}
}

func TestTriagePromptFixesBloodTest(t *testing.T) {
	if !strings.Contains(promptTriage, "血常规") {
		t.Errorf("promptTriage 未固定血常规")
	}
}
