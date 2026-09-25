package schema

import (
	"errors"
	"testing"
)

// TestParseValid 验证合法 schema 能完整解析。
func TestParseValid(t *testing.T) {
	input := `
package shop;

message User version 1..3 {
    1: string name since 1;
    2: int32 age since 1 until 2;
    3: string email since 2;
}

compat User 1 2;
incompat User 1 3;
`
	t.Logf("输入 schema:\n%s", input)
	f, err := Parse(input)
	if err != nil {
		t.Fatalf("判定依据: 合法 schema 应解析成功, 实际失败: %v", err)
	}
	if f.Package != "shop" {
		t.Fatalf("包名不符: %q", f.Package)
	}
	if len(f.Messages) != 1 || len(f.Messages[0].Fields) != 3 {
		t.Fatalf("消息/字段数量不符: %+v", f.Messages)
	}
	if len(f.Compats) != 2 {
		t.Fatalf("兼容性声明数量不符: %+v", f.Compats)
	}
	if err := Validate(f); err != nil {
		t.Fatalf("判定依据: 合法 schema 应通过校验, 实际失败: %v", err)
	}
	t.Log("判定: 解析与校验均通过")
}

// TestParseSyntaxError 验证语法错误归类为 *ParseError。
func TestParseSyntaxError(t *testing.T) {
	inputs := []string{
		"message User version 1..2 {}",       // 缺少 package
		"package shop\nmessage X {}",         // package 后缺少分号与 version
		"package shop; message X version {}", // version 区间缺数字
	}
	for _, in := range inputs {
		_, err := Parse(in)
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Fatalf("输入 %q: 判定依据: 语法错误应归类为 *ParseError, 实际: %v", in, err)
		}
		t.Logf("输入 %q -> 错误归类: 语法错误 (%v)", in, pe)
	}
}

// TestValidateFailures 验证各类语义错误可区分归类。
func TestValidateFailures(t *testing.T) {
	cases := []struct {
		name  string
		input string
		kind  ErrKind
	}{
		{"字段编号重复", `package p;
message M version 1..2 {
    1: string a;
    1: int32 b;
}`, ErrKindDuplicateField},
		{"未知类型", `package p;
message M version 1..2 {
    1: NoSuchType a;
}`, ErrKindUnknownType},
		{"消息版本区间非法", `package p;
message M version 3..2 {
    1: string a;
}`, ErrKindBadVersionRange},
		{"字段版本区间非法", `package p;
message M version 1..2 {
    1: string a since 2 until 1;
}`, ErrKindBadVersionRange},
		{"字段since越界", `package p;
message M version 1..2 {
    1: string a since 5;
}`, ErrKindBadVersionRange},
		{"兼容性声明矛盾", `package p;
message M version 1..3 {
    1: string a;
}
compat M 1 2;
incompat M 2 1;`, ErrKindCompatConflict},
		{"兼容性声明引用未知消息", `package p;
message M version 1..2 {
    1: string a;
}
compat Nope 1 2;`, ErrKindUnknownType},
		{"兼容性声明版本越界", `package p;
message M version 1..2 {
    1: string a;
}
compat M 1 9;`, ErrKindBadVersionRange},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Logf("输入 schema:\n%s", c.input)
			f, err := Parse(c.input)
			if err != nil {
				t.Fatalf("解析应成功, 实际: %v", err)
			}
			err = Validate(f)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("判定依据: 应返回 *ValidationError, 实际: %v", err)
			}
			if ve.Kind != c.kind {
				t.Fatalf("判定依据: 错误类别应为 %v, 实际为 %v (%v)", c.kind, ve.Kind, ve)
			}
			t.Logf("判定依据: Kind=%v 与预期一致 -> 错误归类: %v", ve.Kind, ve)
		})
	}
}

// TestValidateOk 验证合法变体均通过。
func TestValidateOk(t *testing.T) {
	inputs := []string{
		`package p; message M version 1..1 { 1: string a; }`,
		`package p; message M version 1..3 { 1: M self; }`, // 自引用
		`package p;
message A version 1..2 { 1: string x; }
message B version 1..2 { 1: A a; 2: bytes raw; 3: bool ok; 4: int64 n; }
compat B 1 2;`,
	}
	for _, in := range inputs {
		f, err := Parse(in)
		if err != nil {
			t.Fatalf("输入 %q 解析失败: %v", in, err)
		}
		if err := Validate(f); err != nil {
			t.Fatalf("输入 %q: 判定依据: 应通过校验, 实际: %v", in, err)
		}
		t.Logf("输入 %q -> 判定: 校验通过", in)
	}
}
