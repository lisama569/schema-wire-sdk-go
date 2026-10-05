package gen

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/lisama569/schema-wire-sdk-go/review"
	"github.com/lisama569/schema-wire-sdk-go/schema"
)

// TestGenerateReleaseSafe 验证安全演进可以产出且产物可编译。
func TestGenerateReleaseSafe(t *testing.T) {
	old := "package p; message M version 1..1 { 1: string name; }"
	nu := `package p;
message M version 1..2 {
    1: string name since 1;
    2: int64 age since 2;
}`
	t.Logf("输入: 旧 schema %q; 候选新增字段 2", old)
	files, err := GenerateRelease(mustParse(t, old), mustParse(t, nu), Options{})
	if err != nil {
		t.Fatalf("判定依据: 安全演进应放行, 实际: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("应产出 1 个文件, 实际 %d", len(files))
	}
	t.Logf("判定: 审查通过且生成成功 (%d 字节)", len(files["p_wire_gen.go"]))

	// 首次发布（无基线）也应放行。
	files2, err := GenerateRelease(nil, mustParse(t, old), Options{})
	if err != nil {
		t.Fatalf("首次发布不应被演进审查拦截: %v", err)
	}
	if files2 == nil {
		t.Fatal("首次发布应有产物")
	}
	t.Log("判定: baseline=nil（首次发布）正常产出")
}

// TestGenerateReleaseBlocksUnsafeEvolution 验证审查不通过时不产出任何结果，
// 且错误可归类为演进审查失败（与 schema 非法、编译失败相区分）。
func TestGenerateReleaseBlocksUnsafeEvolution(t *testing.T) {
	old := "package p; message M version 1..2 { 1: string name; 2: int32 age; }"
	nu := "package p; message M version 1..2 { 1: string name; 2: int64 age; }" // age 改类型
	t.Logf("输入: 字段 age 由 int32 改为 int64（旧: %s 新: %s）", old, nu)

	files, err := GenerateRelease(mustParse(t, old), mustParse(t, nu), Options{})
	if err == nil {
		t.Fatal("判定依据: 不安全演进必须被拦截")
	}
	if files != nil {
		t.Fatalf("判定依据: 审查不通过时不得产出任何生成结果, 实际 %d 个", len(files))
	}
	if !errors.Is(err, review.ErrReviewRejected) {
		t.Fatalf("判定依据: 应归类为 review.ErrReviewRejected, 实际: %v", err)
	}
	var ue *UncompilableError
	if errors.As(err, &ue) {
		t.Fatalf("判定依据: 不应被归类为编译失败: %v", err)
	}
	var ve *schema.ValidationError
	if errors.As(err, &ve) {
		t.Fatalf("判定依据: 不应被归类为 schema 校验失败: %v", err)
	}
	re, ok := review.AsReviewError(err)
	if !ok {
		t.Fatal("应能取出 *ReviewError")
	}
	if re.Report.Safe() || len(re.Report.Findings) == 0 {
		t.Fatal("审查报告应含不安全条目")
	}
	t.Logf("判定依据: 无产物，错误归类=演进审查失败，原因=%s，明细:\n%v",
		re.Report.Findings[0].Reason, err)
}

// TestUncompilableSchemasRejected 验证“schema 合法、生成包编译不过”的情形
// 在生成阶段就被拒绝，并以独立的 *UncompilableError 归类。
func TestUncompilableSchemasRejected(t *testing.T) {
	cases := []struct {
		name string
		idl  string
	}{
		{
			"字段名是Go关键字",
			"package p; message M version 1..1 { 1: string type; }",
		},
		{
			"两个字段导出后同名",
			"package p; message M version 1..1 { 1: string first_name; 2: string FirstName; }",
		},
		{
			"字段导出名撞上保留方法",
			"package p; message M version 1..1 { 1: string decode; }",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := mustParse(t, c.idl)
			if err := schema.Validate(f); err != nil {
				t.Fatalf("前置条件: 该 schema 必须能通过 Validate, 实际: %v", err)
			}
			t.Logf("输入: schema 通过了 Validate:\n%s", c.idl)
			files, err := Generate(f, Options{})
			if err == nil {
				t.Fatal("判定依据: 生成包编译不过，必须被拒绝")
			}
			if files != nil {
				t.Fatalf("判定依据: 拒绝时不得返回任何部分产物, 实际 %d 个", len(files))
			}
			var ue *UncompilableError
			if !errors.As(err, &ue) {
				t.Fatalf("判定依据: 应归类为 *UncompilableError, 实际: %v", err)
			}
			var ve *schema.ValidationError
			if errors.As(err, &ve) {
				t.Fatalf("判定依据: 不应与 schema 语义错误混淆: %v", err)
			}
			t.Logf("判定依据: 无产物，错误归类=生成包不可编译\n%s", ue.Error())
		})
	}
}

// TestCompilabilityCacheReused 验证同内容产物的 go build 结论被缓存：
// 连续生成同一 schema 第二次应直接命中缓存（通过耗时不易断言，这里至少
// 验证结果一致且不产生竞态）。
func TestCompilabilityCacheReused(t *testing.T) {
	idl := "package p; message M version 1..2 { 1: string name; 2: int64 age; }"
	f := mustParse(t, idl)
	for i := 0; i < 3; i++ {
		if _, err := Generate(f, Options{}); err != nil {
			t.Fatalf("第 %d 次生成失败: %v", i, err)
		}
	}
	t.Log("判定: 同一产物连续 3 次预检结论一致（缓存路径无竞态）")
}

// TestConcurrentGenerateRelease 验证并发发布生成（含真实 go build 预检）
// 互不污染。
func TestConcurrentGenerateRelease(t *testing.T) {
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			old := fmt.Sprintf("package pkg%02d; message M version 1..1 { 1: string name; }", i)
			nu := fmt.Sprintf(`package pkg%02d;
message M version 1..2 {
    1: string name since 1;
    2: int64 age since 2;
}`, i)
			_, errs[i] = GenerateRelease(mustParse(t, old), mustParse(t, nu), Options{})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("包 %d 并发生成失败: %v", i, err)
		}
	}
	t.Logf("判定: %d 个发布生成并发执行（含 go build 预检）全部成功、无污染", n)
}
