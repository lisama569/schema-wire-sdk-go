package schema

import (
	"io"
)

// Load 解析并校验 schema；校验未通过时返回非 nil 错误，
// 此时返回的 schema 不应被代码生成使用。
func Load(sourceName string, r io.Reader) (*Schema, error) {
	s, err := Parse(sourceName, r)
	if err != nil {
		return nil, err
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}
