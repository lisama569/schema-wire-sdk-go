package codegen

import "fmt"

// FailureKind 是代码生成失败的可区分原因类别。
type FailureKind string

const (
	// KindInvalidSchema 输入 schema 未通过校验。
	KindInvalidSchema FailureKind = "invalid_schema"
	// KindIO 输出目录读写失败。
	KindIO FailureKind = "io_error"
	// KindConflict 输出目录中存在无法共存的既有文件。
	KindConflict FailureKind = "output_conflict"
	// KindInternal 代码生成器自身缺陷。
	KindInternal FailureKind = "internal_error"
)

// GenError 携带可区分的生成失败类别。
type GenError struct {
	Kind   FailureKind
	Detail string
}

func (e *GenError) Error() string {
	return fmt.Sprintf("codegen error (%s): %s", e.Kind, e.Detail)
}
