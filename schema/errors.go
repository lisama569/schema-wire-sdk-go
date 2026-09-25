package schema

import "fmt"

// FailureKind 是 schema 解析/校验失败的可区分原因类别。
type FailureKind string

const (
	// KindSyntax 词法/语法错误（无法解析的语句、畸形参数等）。
	KindSyntax FailureKind = "syntax"
	// KindDuplicateNumber 同一消息内字段编号在同一版本上重复。
	KindDuplicateNumber FailureKind = "duplicate_field_number"
	// KindUnknownType 字段引用了未定义的标量类型或消息类型。
	KindUnknownType FailureKind = "unknown_type"
	// KindInvalidVersion 版本区间非法（since>until、v<1、version 倒退等）。
	KindInvalidVersion FailureKind = "invalid_version"
	// KindCompatConflict 演进声明与兼容性策略互相矛盾。
	KindCompatConflict FailureKind = "compat_conflict"
	// KindInvalidName 标识符非法（空、冲突、不满足导出规则等）。
	KindInvalidName FailureKind = "invalid_name"
	// KindDuplicateDecl 重复的消息声明或重复的头部指令。
	KindDuplicateDecl FailureKind = "duplicate_declaration"
	// KindIO 读取 schema 输入流失败。
	KindIO FailureKind = "io_error"
	// KindEmpty 必要声明缺失（无消息、空 schema 等）。
	KindEmpty FailureKind = "empty_schema"
)

// LoadError 携带可区分的失败类别与定位信息。
type LoadError struct {
	Kind   FailureKind
	Line   int // 1-based；与行无关时为 0
	Msg    string
	Source string // 来源名（文件路径等），可为空
}

func (e *LoadError) Error() string {
	loc := e.Source
	if e.Line > 0 {
		if loc != "" {
			loc += ":"
		}
		loc += fmt.Sprintf("line %d", e.Line)
	}
	if loc == "" {
		loc = "<schema>"
	}
	return fmt.Sprintf("schema load error (%s) at %s: %s", e.Kind, loc, e.Msg)
}

// AsLoadError 从任意错误中提取 *LoadError。
func AsLoadError(err error) (*LoadError, bool) {
	if err == nil {
		return nil, false
	}
	le, ok := err.(*LoadError)
	return le, ok
}
