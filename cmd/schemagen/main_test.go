package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// TestCLIInvalidSchemaExitsNonZero: schema 校验失败时 CLI 必须非零退出，
// stderr 中带可区分的错误类别，且不产出文件。
func TestCLIInvalidSchemaExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	schemaFile := filepath.Join(dir, "bad.schema")
	if err := os.WriteFile(schemaFile, []byte(`
version 3;
compat additive;
message M {
	1: int32 gone [until=2];
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	cmd := exec.Command("go", "run", "./cmd/schemagen",
		"-schema", schemaFile, "-out", outDir, "-package", "bad")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("输入=additive 下移除字段的 schema；期望非零退出，却成功: %s", out)
	}
	if !strings.Contains(string(out), "compat_conflict") {
		t.Fatalf("输入=非法 schema；判定依据=%s；错误归类期望 compat_conflict", out)
	}
	if entries, _ := os.ReadDir(outDir); len(entries) != 0 {
		t.Fatalf("validation failure must produce no files, got %v", entries)
	}
	t.Logf("输入=additive+until 矛盾 schema；判定依据=%s；错误归类=compat_conflict，无产物", strings.TrimSpace(string(out)))
}

// TestCLISuccessGeneratesFiles: 合法 schema 生成后目录内含 .go 文件且可编译。
func TestCLISuccessGeneratesFiles(t *testing.T) {
	dir := t.TempDir()
	schemaFile := filepath.Join(dir, "ok.schema")
	if err := os.WriteFile(schemaFile, []byte(`
version 2;
message M { 1: int64 id; 2: string name [since=2]; }`), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "ok")
	cmd := exec.Command("go", "run", "./cmd/schemagen",
		"-schema", schemaFile, "-out", outDir, "-package", "ok")
	cmd.Dir = moduleRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generation failed: %s", out)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no generated files: %v", err)
	}
	t.Logf("判定依据=CLI 成功生成 %d 个文件；错误归类=无", len(entries))
}
