package wire

import "fmt"

// ErrorKind 标识运行时解码失败的两大类原因。
type ErrorKind string

const (
	// KindCorrupt 数据损坏：截断、越界、非法 varint、长度不匹配等。
	KindCorrupt ErrorKind = "corrupt_data"
	// KindIncompatibleVersion 版本不兼容：数据本身结构完整，
	// 但其版本与当前代码的接受区间不兼容。
	KindIncompatibleVersion ErrorKind = "incompatible_version"
)

// Error 是运行时编解码错误，携带可区分的错误类别。
type Error struct {
	Kind   ErrorKind
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("wire error (%s): %s", e.Kind, e.Detail)
}

// IsCorruption 报告错误是否属于“损坏数据”类别。
func IsCorruption(err error) bool {
	if we, ok := err.(*Error); ok {
		return we.Kind == KindCorrupt
	}
	return false
}

// IsIncompatibleVersion 报告错误是否属于“版本不兼容”类别。
func IsIncompatibleVersion(err error) bool {
	if we, ok := err.(*Error); ok {
		return we.Kind == KindIncompatibleVersion
	}
	return false
}
