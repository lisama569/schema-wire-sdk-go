package schema

// CompatPolicy 表示 schema 声明的兼容性策略。
type CompatPolicy string

const (
	// CompatAdditive 表示纯新增式演进：字段只能加入，不能移除或复用编号。
	CompatAdditive CompatPolicy = "additive"
	// CompatNone 表示不做兼容性承诺（允许字段移除，但编号仍不可重叠复用）。
	CompatNone CompatPolicy = "none"
)

// ScalarType 是 schema 支持的标量类型。
type ScalarType string

const (
	TypeInt32   ScalarType = "int32"
	TypeInt64   ScalarType = "int64"
	TypeUint32  ScalarType = "uint32"
	TypeUint64  ScalarType = "uint64"
	TypeBool    ScalarType = "bool"
	TypeString  ScalarType = "string"
	TypeBytes   ScalarType = "bytes"
	TypeFloat64 ScalarType = "float64"
	TypeMessage ScalarType = "message" // Type 字段存放引用的消息名
)

// Field 表示单个字段声明。
type Field struct {
	Number uint32
	Name   string
	Kind   ScalarType
	Type   string // Kind == TypeMessage 时为被引用消息名
	Since  uint32
	Until  uint32 // 0 表示活跃到最新版本（开区间）
}

// ActiveAt 报告字段在版本 v 是否处于活跃状态。
func (f *Field) ActiveAt(v uint32) bool {
	if v < f.Since {
		return false
	}
	return f.Until == 0 || v <= f.Until
}

// Message 表示一条消息声明。
type Message struct {
	Name   string
	Fields []*Field
	// byNumber 在校验阶段建立，用于 O(1) 查找。
	byNumber map[uint32]*Field
}

// Schema 是经过解析与校验的 schema 模型。
type Schema struct {
	Package  string
	Policy   CompatPolicy
	Version  uint32 // schema 声明的当前版本
	Messages []*Message
}

// FieldByNumber 返回消息中携带给定编号的字段（含已移除字段），不存在时返回 nil。
func (m *Message) FieldByNumber(n uint32) *Field {
	if m.byNumber == nil {
		return nil
	}
	return m.byNumber[n]
}

// FieldsAt 返回在版本 v 处于活跃状态的字段，按编号升序排列。
func (m *Message) FieldsAt(v uint32) []*Field {
	out := make([]*Field, 0, len(m.Fields))
	for _, f := range m.Fields {
		if f.ActiveAt(v) {
			out = append(out, f)
		}
	}
	return out
}

// FieldAt 返回编号为 n 且在版本 v 活跃的字段；不存在返回 nil。
// compat=none 时同一编号在不同版本区间可能对应不同字段，故需要 v。
func (m *Message) FieldAt(n uint32, v uint32) *Field {
	for _, f := range m.Fields {
		if f.Number == n && f.ActiveAt(v) {
			return f
		}
	}
	return nil
}

// FieldGroups 返回按编号分组的字段（编号升序），每组内按 since 升序。
// 用于代码生成：同一编号可能在不同版本区间对应不同字段（compat=none）。
func (m *Message) FieldGroups() [][]*Field {
	groups := map[uint32][]*Field{}
	nums := make([]uint32, 0)
	for _, f := range m.Fields {
		if _, ok := groups[f.Number]; !ok {
			nums = append(nums, f.Number)
		}
		groups[f.Number] = append(groups[f.Number], f)
	}
	sortUint32s(nums)
	out := make([][]*Field, 0, len(nums))
	for _, n := range nums {
		out = append(out, groups[n])
	}
	return out
}
