package ai

import (
	"strings"
	"testing"
)

func TestTreatmentPromptUsesBoxQuantity(t *testing.T) {
	for _, want := range []string{"盒数", "药品规格"} {
		if !strings.Contains(promptTreatment, want) {
			t.Errorf("promptTreatment 缺 %q", want)
		}
	}
}
