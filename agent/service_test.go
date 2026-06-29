package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"medagent/internal/ai"
)

func testService(t *testing.T) *Service {
	t.Helper()
	// 这些用例不触发 LLM（仅 Start/Export/TTL）：引擎/守护用不会被调用的 fake。
	return newService(Config{}, ai.NewEngine(chatScript()), ai.NewGuardian(noGuardian()))
}

func TestStartExport(t *testing.T) {
	s := testService(t)
	defer s.Close()
	id, err := s.Start(map[string]any{"年龄": 30, "性别": "男"}, true, nil)
	if err != nil || id == "" {
		t.Fatalf("Start: id=%q err=%v", id, err)
	}
	rec, err := s.Export(id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.SessionID != id || !rec.Initial || rec.StartedAt.IsZero() {
		t.Fatalf("record 不符：%+v", rec)
	}
	if !strings.Contains(string(rec.Profile), "年龄") {
		t.Fatalf("profile 未存：%s", rec.Profile)
	}
}

func TestExportUnknownSession(t *testing.T) {
	s := testService(t)
	defer s.Close()
	if _, err := s.Export("nope"); err != ErrSessionNotFound {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}
}

func TestStartRendersPriorHistory(t *testing.T) {
	s := testService(t)
	defer s.Close()
	prior := []SessionRecord{{
		SessionID: "v0", Initial: true,
		Outcome: &Result{Diagnosis: &Diagnosis{Name: "急性咽炎"}, Advice: "多休息"},
		Turns:   []RecordedTurn{{At: time.Now(), Kind: "patient", Text: "嗓子疼"}},
	}}
	id, _ := s.Start(nil, false, prior)
	sess, _ := s.get(id)
	if !strings.Contains(sess.snap.History, "急性咽炎") {
		t.Fatalf("history 未渲染进 snapshot：%q", sess.snap.History)
	}
}

func TestTTLReaping(t *testing.T) {
	s := testService(t)
	defer s.Close()
	id, _ := s.Start(nil, true, nil)
	sess, _ := s.get(id)
	sess.lastActive = time.Now().Add(-time.Hour) // 强制过期
	s.reapOnce(time.Now())
	if _, err := s.Export(id); err != ErrSessionNotFound {
		t.Fatalf("过期会话应被回收，got %v", err)
	}
}

var _ = json.Marshal
