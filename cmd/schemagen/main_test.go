package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeIDL 把 idl 写入临时目录并返回路径。
func writeIDL(t *testing.T, dir, name, idl string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(idl), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const safeIDL = `package shopv;
message User version 1..2 {
    1: string name since 1;
    2: int64 age since 2;
}
`

const publishedIDL = `package shopv;
message User version 1..2 {
    1: string name since 1;
}
`

// TestCLIFirstPublish 无 baseline：正常生成，退出码 0。
func TestCLIFirstPublish(t *testing.T) {
	dir := t.TempDir()
	schema := writeIDL(t, dir, "v.idl", safeIDL)
	out := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-schema", schema, "-out", out, "-pkg", "shopv"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("判定依据: 首次发布退出码应为 0，实际 %d；stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(out, "shopv_wire_gen.go")); err != nil {
		t.Fatalf("生成文件应存在: %v", err)
	}
	t.Logf("判定: 退出码 %d，stdout=%q", code, strings.TrimSpace(stdout.String()))
}

// TestCLIUnsafeEvolutionExitCode 带不安全基线：审查拦截，退出码 10，且无文件产出。
func TestCLIUnsafeEvolutionExitCode(t *testing.T) {
	dir := t.TempDir()
	// name 的类型由 string 改为 int64。
	unsafe := `package shopv;
message User version 1..2 {
    1: int64 name since 1;
}
`
	baseline := writeIDL(t, dir, "published.idl", publishedIDL)
	candidate := writeIDL(t, dir, "candidate.idl", unsafe)
	out := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-schema", candidate, "-baseline", baseline, "-out", out, "-pkg", "shopv"},
		&stdout, &stderr)
	if code != exitReviewRejected {
		t.Fatalf("判定依据: 不安全演进退出码应为 %d，实际 %d；stderr=%s",
			exitReviewRejected, code, stderr.String())
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("判定依据: 审查被拦时不得产出任何文件，实际 %v", entries)
	}
	if !strings.Contains(stderr.String(), "字段类型被修改") {
		t.Fatalf("判定依据: stderr 应逐条列出原因，实际: %s", stderr.String())
	}
	t.Logf("判定: 退出码 %d 且无产物；stderr 含分类原因:\n%s", code, stderr.String())
}

// TestCLISafeEvolutionExitCode 带安全基线（仅新增字段）：退出码 0。
func TestCLISafeEvolutionExitCode(t *testing.T) {
	dir := t.TempDir()
	baseline := writeIDL(t, dir, "published.idl", publishedIDL)
	candidate := writeIDL(t, dir, "candidate.idl", safeIDL)
	out := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-schema", candidate, "-baseline", baseline, "-out", out, "-pkg", "shopv"},
		&stdout, &stderr)
	if code != exitOK {
		t.Fatalf("判定依据: 安全演进退出码应为 0，实际 %d；stderr=%s", code, stderr.String())
	}
	t.Logf("判定: 安全演进退出码 0，已生成: %s", strings.TrimSpace(stdout.String()))
}

// TestCLIInvalidSchemaExitCode schema 语义非法：退出码 11。
func TestCLIInvalidSchemaExitCode(t *testing.T) {
	dir := t.TempDir()
	bad := "package p; message M version 1..2 { 1: Nope x; }" // 未知类型
	schema := writeIDL(t, dir, "bad.idl", bad)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-schema", schema, "-out", filepath.Join(dir, "out")}, &stdout, &stderr)
	if code != exitSchemaInvalid {
		t.Fatalf("判定依据: 语义非法退出码应为 %d，实际 %d；stderr=%s",
			exitSchemaInvalid, code, stderr.String())
	}
	t.Logf("判定: 语义非法退出码 %d (%s)", code, strings.TrimSpace(stderr.String()))
}

// TestCLISyntaxErrorExitCode 语法错误：退出码 13。
func TestCLISyntaxErrorExitCode(t *testing.T) {
	dir := t.TempDir()
	schema := writeIDL(t, dir, "syntax.idl", "package p message X {}")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-schema", schema, "-out", filepath.Join(dir, "out")}, &stdout, &stderr)
	if code != exitSyntax {
		t.Fatalf("判定依据: 语法错误退出码应为 %d，实际 %d；stderr=%s",
			exitSyntax, code, stderr.String())
	}
	t.Logf("判定: 语法错误退出码 %d (%s)", code, strings.TrimSpace(stderr.String()))
}

// TestCLIUncompilableExitCode schema 合法但生成包编译不过：退出码 12。
func TestCLIUncompilableExitCode(t *testing.T) {
	dir := t.TempDir()
	idl := "package p; message M version 1..1 { 1: string type; }"
	schema := writeIDL(t, dir, "u.idl", idl)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-schema", schema, "-out", filepath.Join(dir, "out")}, &stdout, &stderr)
	if code != exitUncompilable {
		t.Fatalf("判定依据: 不可编译退出码应为 %d，实际 %d；stderr=%s",
			exitUncompilable, code, stderr.String())
	}
	t.Logf("判定: 不可编译退出码 %d", code)
}

// TestCLIUsageExitCode 缺参数：退出码 2。
func TestCLIUsageExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != exitUsage {
		t.Fatalf("缺参数退出码应为 %d，实际 %d", exitUsage, code)
	}
	t.Log("判定: 缺参数退出码 2")
}
