package schema

import "fmt"

// goKeywords 是 Go 语言关键字集合；标识符命中后生成产物无法编译。
var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true,
	"for": true, "func": true, "go": true, "goto": true, "if": true,
	"import": true, "interface": true, "map": true, "package": true,
	"range": true, "return": true, "select": true, "struct": true,
	"switch": true, "type": true, "var": true,
}

// 生成代码中占用的结构体成员/方法名，字段导出名与之冲突会导致编译失败。
var reservedMemberNames = map[string]bool{
	"UnknownFields": true,
	"Encode": true, "EncodeVersion": true,
	"Decode": true, "DecodeLimits": true,
	"DecodeStrict": true, "DecodeStrictLimits": true,
}

// validateCodegen 检查“语义合法但生成的 Go 代码无法编译”的情形，
// 失败返回 Kind 为 ErrKindCodegenUnsafe 的 *ValidationError，
// 使这类问题在生成之前即被拒绝，且能与其他校验失败类别区分。
func validateCodegen(f *File) error {
	if goKeywords[f.Package] {
		return &ValidationError{Kind: ErrKindCodegenUnsafe,
			Msg: fmt.Sprintf("包名 %q 是 Go 关键字，生成产物无法编译", f.Package)}
	}

	msgNames := map[string]bool{}
	for _, m := range f.Messages {
		if goKeywords[m.Name] {
			return &ValidationError{Kind: ErrKindCodegenUnsafe,
				Msg: fmt.Sprintf("消息名 %q 是 Go 关键字，生成产物无法编译", m.Name)}
		}
		if msgNames[m.Name+"MinVersion"] || msgNames[m.Name+"MaxVersion"] {
			return &ValidationError{Kind: ErrKindCodegenUnsafe,
				Msg: fmt.Sprintf("消息名 %q 与其它消息生成的版本常量名冲突", m.Name)}
		}
		msgNames[m.Name] = true

		seen := map[string]string{}
		for _, fld := range m.Fields {
			ex := ExportedName(fld.Name)
			if reservedMemberNames[ex] {
				return &ValidationError{Kind: ErrKindCodegenUnsafe,
					Msg: fmt.Sprintf("消息 %q 字段 %q 的导出名 %q 与生成代码占用的成员名冲突",
						m.Name, fld.Name, ex)}
			}
			if prev, dup := seen[ex]; dup {
				return &ValidationError{Kind: ErrKindCodegenUnsafe,
					Msg: fmt.Sprintf("消息 %q 字段 %q 与 %q 的导出名同为 %q，生成产物无法编译",
						m.Name, prev, fld.Name, ex)}
			}
			seen[ex] = fld.Name
		}
	}
	return nil
}
