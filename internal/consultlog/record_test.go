package consultlog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCallRecordUsesSnakeCaseMessageKeys(t *testing.T) {
	rec := CallRecord{VisitID: "v", Messages: []Message{{Role: "user", Content: "hi"}}}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"role":"user"`) || !strings.Contains(s, `"content":"hi"`) {
		t.Fatalf("message 键应为 snake_case：%s", s)
	}
	if strings.Contains(s, `"Role"`) || strings.Contains(s, `"Content"`) {
		t.Fatalf("message 仍是大写键：%s", s)
	}
}
