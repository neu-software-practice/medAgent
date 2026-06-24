package consultlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestFileLoggerWritesPerVisitFile(t *testing.T) {
	dir := t.TempDir()
	fl := NewFileLogger(dir)
	mustWrite(t, fl, CallRecord{VisitID: "v1", Schema: "interview"})
	mustWrite(t, fl, CallRecord{VisitID: "v1", Schema: "triage_decide"})
	mustWrite(t, fl, CallRecord{VisitID: "v2", Schema: "interview"})

	v1 := readRecords(t, filepath.Join(dir, "v1.jsonl"))
	if len(v1) != 2 || v1[0].Schema != "interview" || v1[1].Schema != "triage_decide" {
		t.Fatalf("v1 记录不符（应按写入顺序）：%+v", v1)
	}
	v2 := readRecords(t, filepath.Join(dir, "v2.jsonl"))
	if len(v2) != 1 || v2[0].Schema != "interview" {
		t.Fatalf("v2 记录不符：%+v", v2)
	}
}

func TestFileLoggerEmptyVisitIDGoesToUnknown(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, NewFileLogger(dir), CallRecord{Schema: "x"})
	if _, err := os.Stat(filepath.Join(dir, "unknown.jsonl")); err != nil {
		t.Fatalf("空 visitID 未落到 unknown.jsonl：%v", err)
	}
}

func TestFileLoggerConcurrentWritesSafe(t *testing.T) {
	dir := t.TempDir()
	fl := NewFileLogger(dir)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = fl.Write(CallRecord{VisitID: "v", Schema: "s"})
		}()
	}
	wg.Wait()
	if got := len(readRecords(t, filepath.Join(dir, "v.jsonl"))); got != 20 {
		t.Fatalf("并发写丢失：得 %d 行，want 20", got)
	}
}

func mustWrite(t *testing.T, fl *FileLogger, rec CallRecord) {
	t.Helper()
	if err := fl.Write(rec); err != nil {
		t.Fatal(err)
	}
}

func readRecords(t *testing.T, path string) []CallRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []CallRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var r CallRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("反序列化失败：%v", err)
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
