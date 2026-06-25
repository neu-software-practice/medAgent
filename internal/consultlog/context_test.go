package consultlog

import (
	"context"
	"regexp"
	"testing"
)

func TestVisitIDRoundTrip(t *testing.T) {
	ctx := WithVisitID(context.Background(), "v-123")
	if got := VisitID(ctx); got != "v-123" {
		t.Fatalf("VisitID = %q, want v-123", got)
	}
}

func TestVisitIDAbsentEmpty(t *testing.T) {
	if got := VisitID(context.Background()); got != "" {
		t.Fatalf("VisitID on bare ctx = %q, want empty", got)
	}
}

func TestNewVisitIDFormatAndUniqueness(t *testing.T) {
	re := regexp.MustCompile(`^\d{8}-\d{6}-[0-9a-f]{8}$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := NewVisitID()
		if !re.MatchString(id) {
			t.Fatalf("NewVisitID 格式不符：%q", id)
		}
		if seen[id] {
			t.Fatalf("NewVisitID 重复：%q", id)
		}
		seen[id] = true
	}
}
