package schema

import "fmt"

// ErrKind 区分 schema 失败的类别。
type ErrKind int

const (
	ErrKindSyntax          ErrKind = iota + 1 // 语法错误
	ErrKindDuplicateField                     // 字段编号重复
	ErrKindUnknownType                        // 未知类型引用
	ErrKindBadVersionRange                    // 非法版本区间
	ErrKindCompatConflict                     // 兼容性声明互相矛盾
	ErrKindCodegenUnsafe                      // 能通过语义校验但生成的 Go 代码无法编译
)

// ParseError 表示语法解析错误。
type ParseError struct {
	Line, Col int
	Msg       string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("schema 语法错误 (行 %d, 列 %d): %s", e.Line, e.Col, e.Msg)
}

// ValidationError 表示语义校验错误，Kind 可区分失败原因。
type ValidationError struct {
	Kind ErrKind
	Msg  string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("schema 校验失败 [%s]: %s", e.Kind, e.Msg)
}

func (k ErrKind) String() string {
	switch k {
	case ErrKindSyntax:
		return "语法错误"
	case ErrKindDuplicateField:
		return "字段编号重复"
	case ErrKindUnknownType:
		return "未知类型"
	case ErrKindBadVersionRange:
		return "非法版本区间"
	case ErrKindCompatConflict:
		return "兼容性声明矛盾"
	case ErrKindCodegenUnsafe:
		return "生成产物不可编译"
	}
	return "未知错误"
}
