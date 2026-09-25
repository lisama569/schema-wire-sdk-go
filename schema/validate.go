package schema

import "fmt"

// Validate 校验 AST 的语义合法性。
// 校验失败返回可区分类别的 *ValidationError。
func Validate(f *File) error {
	if f == nil {
		return &ValidationError{Kind: ErrKindSyntax, Msg: "空的 schema 文件"}
	}
	if f.Package == "" {
		return &ValidationError{Kind: ErrKindSyntax, Msg: "缺少 package 声明"}
	}

	// 消息名唯一 + 建立类型表
	declared := map[string]*Message{}
	for _, m := range f.Messages {
		if m.Name == "" {
			return &ValidationError{Kind: ErrKindSyntax, Msg: "存在未命名消息"}
		}
		if _, dup := declared[m.Name]; dup {
			return &ValidationError{Kind: ErrKindDuplicateField,
				Msg: fmt.Sprintf("消息 %q 重复声明", m.Name)}
		}
		declared[m.Name] = m
	}

	// 逐消息校验
	for _, m := range f.Messages {
		if err := validateMessage(m, declared); err != nil {
			return err
		}
	}

	// 兼容性声明校验
	type pair struct {
		msg  string
		a, b int
	}
	seen := map[pair]bool{}
	for _, cd := range f.Compats {
		m, ok := declared[cd.Message]
		if !ok {
			return &ValidationError{Kind: ErrKindUnknownType,
				Msg: fmt.Sprintf("兼容性声明引用了未声明的消息 %q", cd.Message)}
		}
		if cd.A == cd.B {
			return &ValidationError{Kind: ErrKindBadVersionRange,
				Msg: fmt.Sprintf("消息 %q 的兼容性声明两端版本相同 (%d)", cd.Message, cd.A)}
		}
		for _, v := range []int{cd.A, cd.B} {
			if v < m.MinVersion || v > m.MaxVersion {
				return &ValidationError{Kind: ErrKindBadVersionRange,
					Msg: fmt.Sprintf("兼容性声明 %q 的版本 %d 超出消息版本区间 [%d, %d]",
						cd.Message, v, m.MinVersion, m.MaxVersion)}
			}
		}
		a, b := cd.A, cd.B
		if a > b {
			a, b = b, a
		}
		key := pair{msg: cd.Message, a: a, b: b}
		if prev, ok := seen[key]; ok && prev != cd.Compat {
			return &ValidationError{Kind: ErrKindCompatConflict,
				Msg: fmt.Sprintf("消息 %q 的版本 %d 与 %d 同时被声明为兼容与不兼容",
					cd.Message, a, b)}
		}
		seen[key] = cd.Compat
	}
	return nil
}

func validateMessage(m *Message, declared map[string]*Message) error {
	if m.MinVersion < 1 {
		return &ValidationError{Kind: ErrKindBadVersionRange,
			Msg: fmt.Sprintf("消息 %q 的最小版本 %d 小于 1", m.Name, m.MinVersion)}
	}
	if m.MinVersion > m.MaxVersion {
		return &ValidationError{Kind: ErrKindBadVersionRange,
			Msg: fmt.Sprintf("消息 %q 的版本区间 [%d, %d] 非法（下界大于上界）",
				m.Name, m.MinVersion, m.MaxVersion)}
	}

	nums := map[int]string{}
	for _, fld := range m.Fields {
		if fld.Number <= 0 {
			return &ValidationError{Kind: ErrKindBadVersionRange,
				Msg: fmt.Sprintf("消息 %q 字段 %q 的编号 %d 必须为正整数",
					m.Name, fld.Name, fld.Number)}
		}
		if prev, dup := nums[fld.Number]; dup {
			return &ValidationError{Kind: ErrKindDuplicateField,
				Msg: fmt.Sprintf("消息 %q 中字段编号 %d 被 %q 与 %q 重复使用",
					m.Name, fld.Number, prev, fld.Name)}
		}
		nums[fld.Number] = fld.Name

		if !IsBuiltin(fld.Type) {
			if _, ok := declared[fld.Type]; !ok {
				return &ValidationError{Kind: ErrKindUnknownType,
					Msg: fmt.Sprintf("消息 %q 字段 %q 引用了未知类型 %q",
						m.Name, fld.Name, fld.Type)}
			}
		}

		if fld.Since < m.MinVersion || fld.Since > m.MaxVersion {
			return &ValidationError{Kind: ErrKindBadVersionRange,
				Msg: fmt.Sprintf("消息 %q 字段 %q 的 since %d 超出消息版本区间 [%d, %d]",
					m.Name, fld.Name, fld.Since, m.MinVersion, m.MaxVersion)}
		}
		if fld.Until != 0 {
			if fld.Until < fld.Since {
				return &ValidationError{Kind: ErrKindBadVersionRange,
					Msg: fmt.Sprintf("消息 %q 字段 %q 的版本区间 [%d, %d] 非法（until 早于 since）",
						m.Name, fld.Name, fld.Since, fld.Until)}
			}
			if fld.Until > m.MaxVersion {
				return &ValidationError{Kind: ErrKindBadVersionRange,
					Msg: fmt.Sprintf("消息 %q 字段 %q 的 until %d 超出消息最大版本 %d",
						m.Name, fld.Name, fld.Until, m.MaxVersion)}
			}
		}
	}
	return nil
}
