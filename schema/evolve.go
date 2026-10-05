package schema

import (
	"fmt"
	"strings"
)

// ViolationKind 区分演进审查不通过的具体原因。
type ViolationKind int

const (
	ViolFieldNumberReused ViolationKind = iota + 1 // 字段编号被回收复用
	ViolFieldTypeChanged                           // 字段类型被修改
	ViolFieldRemoved                               // 在仍有效的版本区间内删除字段
	ViolRangeNarrowed                              // 版本区间被收窄
	ViolCompatReversed                             // 兼容性声明被反转或删除
)

func (k ViolationKind) String() string {
	switch k {
	case ViolFieldNumberReused:
		return "字段编号被回收复用"
	case ViolFieldTypeChanged:
		return "字段类型被修改"
	case ViolFieldRemoved:
		return "有效区间内删除字段"
	case ViolRangeNarrowed:
		return "版本区间被收窄"
	case ViolCompatReversed:
		return "兼容性声明被反转或删除"
	}
	return "未知违规"
}

// Violation 描述一条具体的演进违规。
type Violation struct {
	Kind    ViolationKind
	Message string // 相关消息名
	Field   string // 相关字段名（如适用）
	Msg     string // 人类可读的判定依据
}

// EvolutionError 汇总一次演进审查发现的全部违规。
type EvolutionError struct {
	Violations []Violation
}

func (e *EvolutionError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "演进审查未通过（%d 条违规）:", len(e.Violations))
	for _, v := range e.Violations {
		fmt.Fprintf(&b, "\n  - [%s] %s", v.Kind, v.Msg)
	}
	return b.String()
}

// Has 报告审查结果中是否包含某类违规。
func (e *EvolutionError) Has(kind ViolationKind) bool {
	for _, v := range e.Violations {
		if v.Kind == kind {
			return true
		}
	}
	return false
}

// ReviewEvolution 对比已发布 schema（oldF）与待发布 schema（newF），
// 判定这次演进是否安全。不安全时返回 *EvolutionError，逐条列出全部违规；
// 安全时返回 nil。任一 schema 自身未通过 Validate 时直接返回该校验错误。
func ReviewEvolution(oldF, newF *File) error {
	if err := Validate(oldF); err != nil {
		return fmt.Errorf("已发布 schema 自身未通过校验: %w", err)
	}
	if err := Validate(newF); err != nil {
		return fmt.Errorf("待发布 schema 自身未通过校验: %w", err)
	}

	var errs []Violation
	add := func(kind ViolationKind, msg, field, format string, args ...any) {
		errs = append(errs, Violation{
			Kind: kind, Message: msg, Field: field,
			Msg: fmt.Sprintf(format, args...),
		})
	}

	newMsgs := map[string]*Message{}
	for _, m := range newF.Messages {
		newMsgs[m.Name] = m
	}

	for _, om := range oldF.Messages {
		nm, ok := newMsgs[om.Name]
		if !ok {
			add(ViolRangeNarrowed, om.Name, "",
				"消息 %q 在新 schema 中被整体删除（原版本区间 [%d, %d]）",
				om.Name, om.MinVersion, om.MaxVersion)
			continue
		}
		reviewMessage(om, nm, add)
	}

	reviewCompats(oldF, newF, add)

	if len(errs) > 0 {
		return &EvolutionError{Violations: errs}
	}
	return nil
}

// fieldActiveRange 返回字段的有效版本区间（闭区间）。
func fieldActiveRange(m *Message, f *Field) (lo, hi int) {
	lo, hi = f.Since, m.MaxVersion
	if f.Until != 0 {
		hi = f.Until
	}
	return lo, hi
}

func reviewMessage(om, nm *Message, add func(ViolationKind, string, string, string, ...any)) {
	// 消息级版本区间收窄
	if nm.MinVersion > om.MinVersion || nm.MaxVersion < om.MaxVersion {
		add(ViolRangeNarrowed, om.Name, "",
			"消息 %q 的版本区间由 [%d, %d] 收窄为 [%d, %d]",
			om.Name, om.MinVersion, om.MaxVersion, nm.MinVersion, nm.MaxVersion)
	}

	newByNum := map[int]*Field{}
	newByName := map[string]*Field{}
	for _, f := range nm.Fields {
		newByNum[f.Number] = f
		newByName[f.Name] = f
	}

	for _, of := range om.Fields {
		nf, ok := newByNum[of.Number]
		if ok {
			if nf.Name != of.Name {
				add(ViolFieldNumberReused, om.Name, of.Name,
					"消息 %q 的字段编号 %d 原属字段 %q，现被字段 %q 复用",
					om.Name, of.Number, of.Name, nf.Name)
				continue
			}
			if nf.Type != of.Type {
				add(ViolFieldTypeChanged, om.Name, of.Name,
					"消息 %q 字段 %q（编号 %d）的类型由 %q 改为 %q",
					om.Name, of.Name, of.Number, of.Type, nf.Type)
			}
			// 字段级版本区间收窄：since 变大，或 until 提前（含由“仍有效”变为有截止）
			oLo, oHi := fieldActiveRange(om, of)
			nLo, nHi := fieldActiveRange(nm, nf)
			if nLo > oLo || nHi < oHi {
				add(ViolRangeNarrowed, om.Name, of.Name,
					"消息 %q 字段 %q 的有效区间由 [%d, %d] 收窄为 [%d, %d]",
					om.Name, of.Name, oLo, oHi, nLo, nHi)
			}
			continue
		}
		// 旧编号在新 schema 中不存在：若字段有效区间与新消息仍支持的
		// 版本区间有交集，则属于“在仍有效的版本区间里删字段”。
		oLo, oHi := fieldActiveRange(om, of)
		if oLo <= nm.MaxVersion && oHi >= nm.MinVersion {
			extra := ""
			if renamed, ok := newByName[of.Name]; ok {
				extra = fmt.Sprintf("（同名字段出现在编号 %d，等同于改号）", renamed.Number)
			}
			add(ViolFieldRemoved, om.Name, of.Name,
				"消息 %q 字段 %q（编号 %d）在仍有效的版本区间 [%d, %d] 内被删除%s",
				om.Name, of.Name, of.Number, oLo, oHi, extra)
		}
	}
}

type compatKey struct {
	msg  string
	a, b int
}

func normCompatKey(msg string, a, b int) compatKey {
	if a > b {
		a, b = b, a
	}
	return compatKey{msg: msg, a: a, b: b}
}

func reviewCompats(oldF, newF *File, add func(ViolationKind, string, string, string, ...any)) {
	newDecls := map[compatKey]bool{} // key -> compat?
	for _, cd := range newF.Compats {
		newDecls[normCompatKey(cd.Message, cd.A, cd.B)] = cd.Compat
	}
	for _, cd := range oldF.Compats {
		key := normCompatKey(cd.Message, cd.A, cd.B)
		nv, ok := newDecls[key]
		switch {
		case !ok:
			add(ViolCompatReversed, cd.Message, "",
				"消息 %q 版本 %d 与 %d 之间原有的 %s 声明在新 schema 中被删除",
				cd.Message, key.a, key.b, compatWord(cd.Compat))
		case nv != cd.Compat:
			add(ViolCompatReversed, cd.Message, "",
				"消息 %q 版本 %d 与 %d 的兼容性声明由 %s 反转为 %s",
				cd.Message, key.a, key.b, compatWord(cd.Compat), compatWord(nv))
		}
	}
}

func compatWord(compat bool) string {
	if compat {
		return "compat（兼容）"
	}
	return "incompat（不兼容）"
}
