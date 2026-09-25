// Package schema 定义 IDL schema 的抽象语法树（AST）。
package schema

// File 表示一个解析后的 schema 文件。
type File struct {
	Package  string
	Messages []*Message
	Compats  []*CompatDecl
}

// Message 表示一个消息类型声明。
type Message struct {
	Name       string
	MinVersion int
	MaxVersion int
	Fields     []*Field
}

// Field 表示消息中的一个字段。
type Field struct {
	Number int
	Type   string // 内建类型名或消息名
	Name   string
	Since  int // 引入版本（含）
	Until  int // 移除版本（含，0 表示仍有效）
}

// CompatDecl 表示一条兼容性声明：同一消息的两个版本是否兼容。
type CompatDecl struct {
	Message string
	A, B    int
	Compat  bool // true=compat 声明，false=incompat 声明
}
