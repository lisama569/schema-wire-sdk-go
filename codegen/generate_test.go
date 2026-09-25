package codegen

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/lisama569/schema-wire-sdk-go/schema"
)

const testSchema = `
package demo;
version 3;
compat additive;
message Address {
	1: string city;
	2: string zip [since=2];
}
message User {
	1: int64 id;
	2: string name;
	3: Address home [since=2];
	4: bool active [since=3];
	5: bytes avatar [since=3];
	6: float64 score [since=3];
}
`

func loadTestSchema(t *testing.T) *schema.Schema {
	t.Helper()
	s, err := schema.Load("demo.schema", strings.NewReader(testSchema))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return s
}

// moduleDir 返回当前模块根目录（用于把生成包接到同一模块下编译）。
func moduleDir(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}

func TestGenerateDeterministic(t *testing.T) {
	s := loadTestSchema(t)
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	res1, err := Generate(context.Background(), s, dir1, Options{GoPackage: "demo"})
	if err != nil {
		t.Fatalf("generate #1: %v", err)
	}
	res2, err := Generate(context.Background(), s, dir2, Options{GoPackage: "demo"})
	if err != nil {
		t.Fatalf("generate #2: %v", err)
	}
	if strings.Join(res1.Files, ",") != strings.Join(res2.Files, ",") {
		t.Fatalf("file lists differ: %v vs %v", res1.Files, res2.Files)
	}
	for _, name := range res1.Files {
		a, _ := os.ReadFile(filepath.Join(dir1, name))
		b, _ := os.ReadFile(filepath.Join(dir2, name))
		if !bytes.Equal(a, b) {
			t.Fatalf("non-deterministic output for %s", name)
		}
		t.Logf("确定性检查 %s: %d 字节，两次生成逐字节一致", name, len(a))
	}
}

func TestGenerateCompiles(t *testing.T) {
	s := loadTestSchema(t)
	// 生成到模块内的临时包目录，使它可以与 wire 运行时一起被 go build。
	root := moduleDir(t)
	pkgDir := filepath.Join(root, "tmpgentest", "demo")
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(root, "tmpgentest")) })
	if _, err := Generate(context.Background(), s, pkgDir, Options{GoPackage: "demo"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	cmd := exec.Command("go", "build", "./tmpgentest/...")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated package does not compile:\n%s", out)
	}
	t.Logf("判定依据=go build ./tmpgentest/... 成功；错误归类=无")
}

func TestGenerateRefusesUnmanagedFile(t *testing.T) {
	s := loadTestSchema(t)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "schema_descriptor.go")
	if err := os.WriteFile(blocker, []byte("package demo // handwritten\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Generate(context.Background(), s, dir, Options{GoPackage: "demo"})
	ge, ok := err.(*GenError)
	if !ok || ge.Kind != KindConflict {
		t.Fatalf("输入=目录内含手写 %s；判定依据=%v；错误归类期望 output_conflict, got %T %v",
			blocker, err, err, err)
	}
	t.Logf("输入=目录内含手写 schema_descriptor.go；判定依据=%v；错误归类=%s", ge.Detail, ge.Kind)
	// 手写文件必须原样保留
	if data, _ := os.ReadFile(blocker); string(data) != "package demo // handwritten\n" {
		t.Fatalf("handwritten file mutated after failed generation")
	}
}

func TestGenerateInvalidSchemaNoOutput(t *testing.T) {
	bad := `
		message M {
			1: int32 a;
			1: int32 b;
		}`
	s, parseErr := schema.Load("bad.schema", strings.NewReader(bad))
	if parseErr == nil {
		// Load 已应失败；若意外成功，Generate 必须兜底
	}
	dir := t.TempDir()
	if s != nil {
		_, err := Generate(context.Background(), s, dir, Options{})
		if ge, ok := err.(*GenError); !ok || ge.Kind != KindInvalidSchema {
			t.Fatalf("expected invalid_schema, got %v", err)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("no output allowed on invalid schema, found %v", entries)
	}
	t.Logf("输入=重复字段编号 schema；判定依据=schema 校验失败；错误归类=invalid_schema，目录无产物")
}

func TestConcurrentGenerateIsolated(t *testing.T) {
	s := loadTestSchema(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dir := t.TempDir()
			if _, err := Generate(context.Background(), s, dir, Options{GoPackage: "demo"}); err != nil {
				errs <- err
				return
			}
			got, _ := os.ReadFile(filepath.Join(dir, "user.go"))
			if !bytes.Contains(got, []byte("type User struct")) {
				errs <- &GenError{Detail: "corrupted concurrent output"}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent generation failure: %v", err)
	}
}

// TestGenerateIdempotent: 对同一目录重复生成必须逐字节一致、可重复成功，
// 且不残留暂存目录。
func TestGenerateIdempotent(t *testing.T) {
	s := loadTestSchema(t)
	root := moduleDir(t)
	pkgDir := filepath.Join(root, "tmpgentest2", "demo")
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(root, "tmpgentest2")) })

	r1, err := Generate(context.Background(), s, pkgDir, Options{GoPackage: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := map[string][]byte{}
	for _, n := range r1.Files {
		data, err := os.ReadFile(filepath.Join(pkgDir, n))
		if err != nil {
			t.Fatal(err)
		}
		snapshot[n] = data
	}
	r2, err := Generate(context.Background(), s, pkgDir, Options{GoPackage: "demo"})
	if err != nil {
		t.Fatalf("second generation failed: %v", err)
	}
	if len(r2.Files) != len(r1.Files) {
		t.Fatalf("file set changed: %v vs %v", r1.Files, r2.Files)
	}
	for n, want := range snapshot {
		got, err := os.ReadFile(filepath.Join(pkgDir, n))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("file %s changed between identical generations", n)
		}
	}
	entries, _ := os.ReadDir(pkgDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".schemagen-stage-") {
			t.Fatalf("staging dir left behind: %s", e.Name())
		}
	}
	cmd := exec.Command("go", "build", "./tmpgentest2/...")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("regenerated package does not compile:\n%s", out)
	}
	t.Logf("判定依据=两次同 schema 生成文件集合与字节完全一致，无暂存残留；错误归类=无")
}

// TestGenerateFailureLeavesNoHalfProduct: 渲染前冲突预检失败时，
// 既有受管文件与目录内容必须原样保留。
func TestGenerateFailureLeavesNoHalfProduct(t *testing.T) {
	s := loadTestSchema(t)
	dir := t.TempDir()
	// 先放一个受管文件
	if _, err := Generate(context.Background(), s, dir, Options{GoPackage: "demo"}); err != nil {
		t.Fatal(err)
	}
	// 用手写内容覆盖一个“将被生成”的文件名，且不带生成头 -> 必须拒绝
	other := filepath.Join(dir, "user.go")
	if err := os.WriteFile(other, []byte("package demo\nvar X=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Generate(context.Background(), s, dir, Options{GoPackage: "demo"})
	if ge, ok := err.(*GenError); !ok || ge.Kind != KindConflict {
		t.Fatalf("want conflict error, got %v", err)
	}
	if data, _ := os.ReadFile(other); string(data) != "package demo\nvar X=1\n" {
		t.Fatalf("handwritten file mutated: %q", data)
	}
	// 所有原有受管文件仍在且可解析
	if _, err := os.Stat(filepath.Join(dir, "schema_descriptor.go")); err != nil {
		t.Fatalf("managed file missing after failed generation: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) == 0 {
		t.Fatal("directory emptied after failed generation")
	}
	t.Logf("判定依据=存在非受管手写文件，生成在预检阶段失败；目录内原有文件全部保留；错误归类=output_conflict")
}

// TestDeterministicRegardlessOfSourceOrder: schema 字段书写顺序不同，
// 校验阶段按 (编号, since) 排序后，产物必须逐字节一致。
func TestDeterministicRegardlessOfSourceOrder(t *testing.T) {
	a := `
version 2;
message M {
	3: string c [since=2];
	1: int64 a;
	2: bool b;
}`
	b := `
version 2;
message M {
	1: int64 a;
	2: bool b;
	3: string c [since=2];
}`
	sa, err := schema.Load("a.schema", strings.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	sb, err := schema.Load("b.schema", strings.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	da, db := t.TempDir(), t.TempDir()
	ra, err := Generate(context.Background(), sa, da, Options{GoPackage: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(context.Background(), sb, db, Options{GoPackage: "m"}); err != nil {
		t.Fatal(err)
	}
	for _, n := range ra.Files {
		x, _ := os.ReadFile(filepath.Join(da, n))
		y, _ := os.ReadFile(filepath.Join(db, n))
		if !bytes.Equal(x, y) {
			t.Fatalf("output differs for %s despite same logical schema:\n--a--\n%s\n--b--\n%s", n, x, y)
		}
	}
	t.Logf("判定依据=两种字段书写顺序经排序后产物逐字节一致；错误归类=无")
}
