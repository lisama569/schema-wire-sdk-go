package gen

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lisama569/schema-wire-sdk-go/schema"
)

const testIDL = `
package shop;

message User version 1..3 {
    1: string name since 1;
    2: int32 age since 1 until 2;
    3: string email since 2;
    4: bool active since 1;
}

message Order version 1..2 {
    1: int64 id since 1;
    2: User buyer since 1;
    3: bytes note since 2;
}

compat User 1 2;
incompat User 1 3;
`

func mustParse(t *testing.T, idl string) *schema.File {
	t.Helper()
	f, err := schema.Parse(idl)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	return f
}

// TestGenerateDeterministic 验证同一 schema 重复生成逐字节一致。
func TestGenerateDeterministic(t *testing.T) {
	f := mustParse(t, testIDL)
	first, err := Generate(f, Options{})
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	for i := 0; i < 10; i++ {
		again, err := Generate(f, Options{})
		if err != nil {
			t.Fatalf("第 %d 次生成失败: %v", i, err)
		}
		if len(first) != len(again) {
			t.Fatalf("第 %d 次生成文件数不一致", i)
		}
		for name, content := range first {
			if !bytes.Equal(content, again[name]) {
				t.Fatalf("判定依据: 第 %d 次生成的 %s 与首次不逐字节一致", i, name)
			}
		}
	}
	for name, content := range first {
		t.Logf("判定: 文件 %s 在 11 次生成中逐字节一致 (%d 字节)", name, len(content))
	}
}

// TestGenerateRejectsInvalidSchema 验证校验未通过时不产出任何结果。
func TestGenerateRejectsInvalidSchema(t *testing.T) {
	invalid := []struct{ name, idl string }{
		{"字段编号重复", "package p; message M version 1..2 { 1: string a; 1: int32 b; }"},
		{"未知类型", "package p; message M version 1..2 { 1: Nope a; }"},
		{"非法版本区间", "package p; message M version 3..1 { 1: string a; }"},
		{"兼容性矛盾", "package p; message M version 1..3 { 1: string a; } compat M 1 2; incompat M 1 2;"},
	}
	for _, c := range invalid {
		t.Run(c.name, func(t *testing.T) {
			t.Logf("输入 schema: %s", c.idl)
			f := mustParse(t, c.idl)
			files, err := Generate(f, Options{})
			if err == nil {
				t.Fatal("判定依据: 非法 schema 应被拒绝")
			}
			if files != nil {
				t.Fatalf("判定依据: 校验未通过时不得产出任何生成结果, 实际产出 %d 个文件", len(files))
			}
			var ve *schema.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("错误应可归类为 *schema.ValidationError, 实际: %v", err)
			}
			t.Logf("判定依据: 无产出且错误归类为 [%s] -> %v", ve.Kind, err)
		})
	}
}

// TestConcurrentGenerate 验证并发生成多个目标包互不污染。
func TestConcurrentGenerate(t *testing.T) {
	const n = 16
	files := make([]map[string][]byte, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			idl := fmt.Sprintf(`package pkg%02d;
message M%d version 1..2 {
    1: string f%d;
    2: int64 g;
}`, i, i, i)
			f, err := schema.Parse(idl)
			if err != nil {
				errs[i] = err
				return
			}
			files[i], errs[i] = Generate(f, Options{})
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("包 %d 生成失败: %v", i, errs[i])
		}
		content := string(files[i][fmt.Sprintf("pkg%02d_wire_gen.go", i)])
		// 每个包的产物只能包含自己的标识符，不得混入其他包的内容。
		want := fmt.Sprintf("package pkg%02d", i)
		if !bytes.Contains([]byte(content), []byte(want)) {
			t.Fatalf("包 %d 的产物缺少自身包名", i)
		}
		for j := 0; j < n; j++ {
			if j == i {
				continue
			}
			other := fmt.Sprintf("package pkg%02d", j)
			if bytes.Contains([]byte(content), []byte(other)) {
				t.Fatalf("判定依据: 包 %d 的产物混入了包 %d 的内容（竞态污染）", i, j)
			}
		}
	}
	t.Logf("判定: %d 个目标包并发生成，产物各自独立、无交叉污染", n)
}

// TestWriteFilesAtomic 验证写盘失败时不留下半成品。
func TestWriteFilesAtomic(t *testing.T) {
	dir := t.TempDir()
	good := map[string][]byte{
		"a_gen.go": []byte("package a\n"),
		"b_gen.go": []byte("package b\n"),
	}
	if err := WriteFiles(dir, good); err != nil {
		t.Fatalf("正常写入失败: %v", err)
	}
	for name := range good {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("文件 %s 应存在: %v", name, err)
		}
	}
	t.Log("判定: 正常写入全部落盘")

	// 制造失败：目标目录不可写。
	dir2 := t.TempDir()
	sub := filepath.Join(dir2, "readonly")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(sub, 0o755) // 让 TempDir 清理可行

	bad := map[string][]byte{
		"x_gen.go": []byte("package x\n"),
		"y_gen.go": []byte("package y\n"),
	}
	err := WriteFiles(sub, bad)
	if err == nil {
		t.Skip("当前环境可写只读目录（如 root），跳过失败路径断言")
	}
	t.Logf("输入: 只读目录, 判定依据: 写入应失败 -> %v", err)
	entries, _ := os.ReadDir(sub)
	for _, e := range entries {
		t.Fatalf("判定依据: 失败后不得留下半成品, 发现残留: %s", e.Name())
	}
	t.Log("判定: 失败路径无任何残留文件（含临时文件）")
}

// TestWriteFilesNoTempLeftover 验证成功后无临时文件残留。
func TestWriteFilesNoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFiles(dir, map[string][]byte{"z_gen.go": []byte("package z\n")}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "z_gen.go" {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("判定依据: 成功后目录应只含目标文件, 实际: %v", names)
	}
	t.Log("判定: 成功后无临时文件残留")
}
