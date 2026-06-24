package consultlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// FileLogger 把每条 CallRecord 追加到 {dir}/{visitID}.jsonl —— 一次诊疗一份文件，
// 按 visitID 直接可寻。并发安全。
type FileLogger struct {
	dir string
	mu  sync.Mutex
}

// NewFileLogger 返回写入 dir 的 FileLogger。dir 需已存在。
func NewFileLogger(dir string) *FileLogger { return &FileLogger{dir: dir} }

// Write 把 rec 追加为一行 JSON。visitID 为空时归到 unknown.jsonl。
func (f *FileLogger) Write(rec CallRecord) error {
	visit := rec.VisitID
	if visit == "" {
		visit = "unknown"
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fh, err := os.OpenFile(filepath.Join(f.dir, visit+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer fh.Close()
	_, err = fh.Write(append(line, '\n'))
	return err
}
