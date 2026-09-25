package schema

import (
	"strings"
	"unicode"
)

// exportName 把 schema 字段名规范化为稳定的 Go 导出标识符：
// 按下划线/大小写边界拆分后首字母大写拼接（snake_case 与 camelCase 统一）。
func ExportName(name string) string {
	var b strings.Builder
	up := true // 下一个字母片段首字符大写
	for _, r := range name {
		switch {
		case r == '_':
			up = true
		case up:
			b.WriteRune(unicode.ToUpper(r))
			up = false
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

func sortUint32s(xs []uint32) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
