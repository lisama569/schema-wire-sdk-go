package schema

import (
	"fmt"
	"go/token"
	"sort"
)

// Validate 校验已解析的 schema，并就地建立索引、规范化字段顺序。
//
// 校验项与失败类别：
//   - 空 schema / 无消息          -> KindEmpty
//   - 重复消息声明 / 重复 Go 标识 -> KindDuplicateDecl / KindInvalidName
//   - 未知类型引用                -> KindUnknownType
//   - 版本为 0、since>until 等    -> KindInvalidVersion
//   - 同编号字段活跃区间重叠      -> KindDuplicateNumber
//   - additive 策略下移除/复用字段-> KindCompatConflict
func (s *Schema) Validate() error {
	if s == nil {
		return &LoadError{Kind: KindEmpty, Msg: "nil schema"}
	}
	if s.Version == 0 {
		return &LoadError{Kind: KindInvalidVersion, Msg: "schema version must be >= 1"}
	}
	if len(s.Messages) == 0 {
		return &LoadError{Kind: KindEmpty, Msg: "schema must declare at least one message"}
	}

	byName := make(map[string]*Message, len(s.Messages))
	for _, m := range s.Messages {
		if m.Name == "" {
			return &LoadError{Kind: KindInvalidName, Msg: "anonymous message declaration"}
		}
		if token.Lookup(m.Name).IsKeyword() {
			return &LoadError{Kind: KindInvalidName,
				Msg: "message name " + m.Name + " collides with a Go keyword"}
		}
		if _, dup := byName[m.Name]; dup {
			return &LoadError{Kind: KindDuplicateDecl, Msg: "duplicate message: " + m.Name}
		}
		byName[m.Name] = m
		if len(m.Fields) == 0 {
			return &LoadError{Kind: KindEmpty,
				Msg: "message " + m.Name + " must declare at least one field"}
		}
	}

	for _, m := range s.Messages {
		if err := validateMessage(s, m, byName); err != nil {
			return err
		}
	}
	return nil
}

func validateMessage(s *Schema, m *Message, messages map[string]*Message) error {
	// 1. 逐字段：名称、类型、版本区间
	goNames := make(map[string]uint32, len(m.Fields))
	for _, f := range m.Fields {
		exported := ExportName(f.Name)
		if token.Lookup(f.Name).IsKeyword() {
			return &LoadError{Kind: KindInvalidName,
				Msg: fmt.Sprintf("field %q in message %s collides with a Go keyword", f.Name, m.Name)}
		}
		if f.Since == 0 {
			return &LoadError{Kind: KindInvalidVersion,
				Msg: fmt.Sprintf("field %s.%s: since must be >= 1", m.Name, f.Name)}
		}
		if f.Until != 0 && f.Since > f.Until {
			return &LoadError{Kind: KindInvalidVersion,
				Msg: fmt.Sprintf("field %s.%s #%d: invalid version range [%d,%d] (since > until)",
					m.Name, f.Name, f.Number, f.Since, f.Until)}
		}
		if f.Until != 0 && f.Until > s.Version {
			return &LoadError{Kind: KindInvalidVersion,
				Msg: fmt.Sprintf("field %s.%s #%d: until=%d beyond declared schema version %d",
					m.Name, f.Name, f.Number, f.Until, s.Version)}
		}
		if f.Since > s.Version {
			return &LoadError{Kind: KindInvalidVersion,
				Msg: fmt.Sprintf("field %s.%s #%d: since=%d beyond declared schema version %d",
					m.Name, f.Name, f.Number, f.Since, s.Version)}
		}
		// additive 承诺“只增不减”：给已有字段加 until 意味着它在未来版本
		// 被移除，与策略矛盾。
		if s.Policy == CompatAdditive && f.Until != 0 {
			return &LoadError{Kind: KindCompatConflict,
				Msg: fmt.Sprintf("field %s.%s #%d has until=%d, removing a field contradicts compat=additive",
					m.Name, f.Name, f.Number, f.Until)}
		}
		if f.Kind == TypeMessage {
			ref, ok := messages[f.Type]
			if !ok {
				return &LoadError{Kind: KindUnknownType,
					Msg: fmt.Sprintf("field %s.%s #%d references unknown message type %q",
						m.Name, f.Name, f.Number, f.Type)}
			}
			if ref == m {
				// 允许自引用（递归消息，可选指针），无额外检查。
			}
		}
		if other, ok := goNames[exported]; ok {
			return &LoadError{Kind: KindInvalidName,
				Msg: fmt.Sprintf("message %s: fields #%d and #%d export to the same Go identifier %s",
					m.Name, other, f.Number, exported)}
		}
		goNames[exported] = f.Number
	}

	// 2. 编号分组：同编号字段的活跃区间必须互不重叠
	byNum := make(map[uint32][]*Field)
	for _, f := range m.Fields {
		byNum[f.Number] = append(byNum[f.Number], f)
	}
	index := make(map[uint32]*Field, len(m.Fields))
	for num, group := range byNum {
		sort.Slice(group, func(i, j int) bool {
			if group[i].Since != group[j].Since {
				return group[i].Since < group[j].Since
			}
			return group[i].Name < group[j].Name
		})
		for _, f := range group {
			index[f.Number] = f // 同编号下保留首个（最早 since）
		}
		if len(group) > 1 {
			if err := validateNumberReuse(s, m, num, group); err != nil {
				return err
			}
		}
	}
	m.byNumber = index

	// 3. 规范化：字段按 (编号, since) 稳定排序，保证产物确定性
	sort.SliceStable(m.Fields, func(i, j int) bool {
		if m.Fields[i].Number != m.Fields[j].Number {
			return m.Fields[i].Number < m.Fields[j].Number
		}
		return m.Fields[i].Since < m.Fields[j].Since
	})
	return nil
}

// validateNumberReuse 校验同编号字段在版本轴上的复用是否合法。
func validateNumberReuse(s *Schema, m *Message, num uint32, group []*Field) error {
	for i := 1; i < len(group); i++ {
		prev, cur := group[i-1], group[i]
		// 活跃区间（prev 为开区间时延伸到无限大）不得与下一段重叠
		prevEnd := prev.Until
		if prevEnd == 0 {
			prevEnd = ^uint32(0)
		}
		if cur.Since <= prevEnd {
			return &LoadError{Kind: KindDuplicateNumber,
				Msg: fmt.Sprintf("message %s: field number %d is active for both %q[%d,%s] and %q[%d,%s]",
					m.Name, num,
					prev.Name, prev.Since, endLabel(prev.Until),
					cur.Name, cur.Since, endLabel(cur.Until))}
		}
	}
	// 与兼容性策略的矛盾：
	// additive 承诺只增不减，任何编号复用（即便区间不重叠）都意味着
	// “旧字段消失/语义改变”，必须拒绝。
	if s.Policy == CompatAdditive {
		names := group[0].Name
		for _, f := range group[1:] {
			names += ", " + f.Name
		}
		return &LoadError{Kind: KindCompatConflict,
			Msg: fmt.Sprintf("message %s: field number %d is reused across versions (%s); "+
				"this removes or changes an existing field, which contradicts compat=additive",
				m.Name, num, names)}
	}
	return nil
}

func endLabel(until uint32) string {
	if until == 0 {
		return "now"
	}
	return fmt.Sprintf("%d", until)
}
