package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_KeyValue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "FOO=bar\nBAZ=qux\n")
	t.Cleanup(func() { os.Unsetenv("FOO"); os.Unsetenv("BAZ") })

	if err := Load(filepath.Join(dir, ".env")); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("FOO"); v != "bar" {
		t.Errorf("FOO = %q, want bar", v)
	}
	if v := os.Getenv("BAZ"); v != "qux" {
		t.Errorf("BAZ = %q, want qux", v)
	}
}

func TestLoad_QuotedValue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "KEY=\"hello world\"\nOTHER='single'\n")
	t.Cleanup(func() { os.Unsetenv("KEY"); os.Unsetenv("OTHER") })

	if err := Load(filepath.Join(dir, ".env")); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("KEY"); v != "hello world" {
		t.Errorf("KEY = %q, want \"hello world\"", v)
	}
	if v := os.Getenv("OTHER"); v != "single" {
		t.Errorf("OTHER = %q, want single", v)
	}
}

func TestLoad_SkipsCommentsAndBlankLines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "# 这是注释\n\n  # 缩进注释\nKEY=val\n\n")
	t.Cleanup(func() { os.Unsetenv("KEY") })

	if err := Load(filepath.Join(dir, ".env")); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("KEY"); v != "val" {
		t.Errorf("KEY = %q, want val", v)
	}
}

func TestLoad_FileNotExist(t *testing.T) {
	if err := Load("/nonexistent/path/.env"); err != nil {
		t.Fatalf("文件不存在应返回 nil，实际: %v", err)
	}
}

func TestLoad_NoOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("EXISTING", "original")

	writeFile(t, dir, ".env", "EXISTING=new\n")

	if err := Load(filepath.Join(dir, ".env")); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("EXISTING"); v != "original" {
		t.Errorf("已有 env 不应被覆盖，got %q", v)
	}
}

func TestLoad_TrimWhitespace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "  KEY =  val  \n")
	t.Cleanup(func() { os.Unsetenv("KEY") })

	if err := Load(filepath.Join(dir, ".env")); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("KEY"); v != "val" {
		t.Errorf("KEY = %q, want val", v)
	}
}

func TestLoad_EmptyKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "=val\nKEY=ok\n")
	t.Cleanup(func() { os.Unsetenv("KEY") })

	if err := Load(filepath.Join(dir, ".env")); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("KEY"); v != "ok" {
		t.Errorf("KEY = %q, want ok", v)
	}
	if _, exists := os.LookupEnv(""); exists {
		t.Error("空 key 不应被设置")
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
