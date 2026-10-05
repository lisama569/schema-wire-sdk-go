package schema

import (
	"errors"
	"testing"
)

// TestValidateCodegenUnsafe 验证“语义合法但生成产物无法编译”的 schema
// 在生成之前即被拒绝，且失败类别可与其他校验失败区分。
func TestValidateCodegenUnsafe(t *testing.T) {
	cases := []struct {
		name string
		idl  string
	}{
		{"消息名是Go关键字", "package p; message func version 1..1 { 1: string a; }"},
		{"包名是Go关键字", "package type; message M version 1..1 { 1: string a; }"},
		{"字段导出名冲突", "package p; message M version 1..1 { 1: string foo_bar; 2: string fooBar; }"},
		{"字段名占用UnknownFields", "package p; message M version 1..1 { 1: string unknown_fields; }"},
		{"字段名占用生成的方法名", "package p; message M version 1..1 { 1: string decode; }"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Logf("输入 schema: %s", c.idl)
			f, err := Parse(c.idl)
			if err != nil {
				t.Fatalf("解析应成功: %v", err)
			}
			err = Validate(f)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("判定依据: 应返回 *ValidationError, 实际: %v", err)
			}
			if ve.Kind != ErrKindCodegenUnsafe {
				t.Fatalf("判定依据: 类别应为 ErrKindCodegenUnsafe, 实际: [%s] %v", ve.Kind, err)
			}
			// 必须能与其他类别区分。
			if ve.Kind == ErrKindDuplicateField || ve.Kind == ErrKindBadVersionRange ||
				ve.Kind == ErrKindUnknownType || ve.Kind == ErrKindCompatConflict {
				t.Fatalf("类别不应与既有校验失败混淆: %v", ve.Kind)
			}
			t.Logf("判定: 生成前即拒绝, 错误归类为 [%s] -> %v", ve.Kind, err)
		})
	}
}
