package review

import (
	"fmt"
	"sort"

	"github.com/lisama569/schema-wire-sdk-go/schema"
)

// Reason 是单条不安全改动的原因类别。不同原因互不混淆，CI 与评审可以
// 按类别分别统计或豁免。
type Reason int

const (
	// ReasonFieldNumberReused 已发布 schema 中出现过的字段编号，在候选
	// schema 里被另一个名字（或换类型后）重新占用。编号一旦发布就只能
	// 随同一个字段复用，不能回收。
	ReasonFieldNumberReused Reason = iota + 1
	// ReasonFieldTypeChanged 同一个字段（同编号同名）类型被修改。
	ReasonFieldTypeChanged
	// ReasonFieldRemovedActive 字段在候选 schema 仍然支持的版本区间内、
	// 且其旧版本区间仍覆盖的某个版本上被删除——该版本可能仍在线上流通。
	ReasonFieldRemovedActive
	// ReasonVersionRangeNarrowed 消息版本区间被收窄（下界抬高或上界压低），
	// 已发布的版本将不再被新运行时接受。
	ReasonVersionRangeNarrowed
	// ReasonCompatReversed 同一条 (消息, 版本A, 版本B) 兼容性声明被反转。
	ReasonCompatReversed
	// ReasonCompatRemoved 已发布的兼容性声明在候选 schema 中消失。
	ReasonCompatRemoved
	// ReasonMessageRemoved 已发布的消息类型被整体删除。
	ReasonMessageRemoved
)

// String 返回原因类别的中文名，用于日志与 CI 输出。
func (r Reason) String() string {
	switch r {
	case ReasonFieldNumberReused:
		return "字段编号回收复用"
	case ReasonFieldTypeChanged:
		return "字段类型被修改"
	case ReasonFieldRemovedActive:
		return "存活版本区间内删除字段"
	case ReasonVersionRangeNarrowed:
		return "版本区间被收窄"
	case ReasonCompatReversed:
		return "兼容性声明被反转"
	case ReasonCompatRemoved:
		return "兼容性声明被删除"
	case ReasonMessageRemoved:
		return "消息被删除"
	}
	return "未知原因"
}

// Finding 描述一条不安全改动，含判定依据。
type Finding struct {
	Reason  Reason
	Message string // 受影响的消息名
	Field   int    // 受影响的字段编号（非字段类原因为 0）
	Detail  string // 人可读的判定依据
}

// Report 是一次演进审查的结果。
type Report struct {
	Findings []Finding
}

// Safe 报告本次改动是否安全（无任何不安全条目）。
func (r *Report) Safe() bool { return r == nil || len(r.Findings) == 0 }

// add 追加一条发现。
func (r *Report) add(reason Reason, msg string, field int, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{
		Reason:  reason,
		Message: msg,
		Field:   field,
		Detail:  fmt.Sprintf(format, args...),
	})
}

// sortFindings 按 (消息名, 字段编号, 原因) 稳定排序，保证报告确定性。
func (r *Report) sortFindings() {
	sort.SliceStable(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if a.Message != b.Message {
			return a.Message < b.Message
		}
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		return a.Reason < b.Reason
	})
}

type fieldKey struct {
	number int
}

func fieldMap(m *schema.Message) map[int]*schema.Field {
	mp := make(map[int]*schema.Field, len(m.Fields))
	for _, f := range m.Fields {
		mp[f.Number] = f
	}
	return mp
}

func fieldUntil(f *schema.Field, msgMax int) int {
	if f.Until != 0 {
		return f.Until
	}
	return msgMax
}

// Review 对比“线上已发布”的 schema 与“本次准备发布”的候选 schema，
// 返回逐条分类的不安全改动。
//
// 两份 schema 都必须先通过 schema.Validate，否则返回校验错误（不会产出报告）。
// 审查只关心会破坏线上互通的改动；新增消息、新增字段（占用未发布编号）、
// 放宽消息版本区间等都是安全的，不出现在报告中。
func Review(published, candidate *schema.File) (*Report, error) {
	if err := schema.Validate(published); err != nil {
		return nil, fmt.Errorf("已发布 schema 自身不合法，无法作为对比基线: %w", err)
	}
	if err := schema.Validate(candidate); err != nil {
		return nil, fmt.Errorf("候选 schema 不合法，演进审查中止: %w", err)
	}

	r := &Report{}
	newMsgs := make(map[string]*schema.Message, len(candidate.Messages))
	for _, m := range candidate.Messages {
		newMsgs[m.Name] = m
	}

	oldNames := make([]string, 0, len(published.Messages))
	for _, m := range published.Messages {
		oldNames = append(oldNames, m.Name)
	}
	sort.Strings(oldNames)

	for _, name := range oldNames {
		old := messageByName(published, name)
		nu, ok := newMsgs[name]
		if !ok {
			r.add(ReasonMessageRemoved, name, 0,
				"已发布的消息 %q 在候选 schema 中被整体删除，旧版本数据将无人可解", name)
			continue
		}
		reviewMessage(r, old, nu)
	}

	reviewCompats(r, published, candidate)
	r.sortFindings()
	return r, nil
}

func messageByName(f *schema.File, name string) *schema.Message {
	for _, m := range f.Messages {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// overlapClosed 返回闭区间 [aLo,aHi] 与 [bLo,bHi] 的交集中是否还有版本。
func overlapClosed(aLo, aHi, bLo, bHi int) bool {
	lo := aLo
	if bLo > lo {
		lo = bLo
	}
	hi := aHi
	if bHi < hi {
		hi = bHi
	}
	return lo <= hi
}

func reviewMessage(r *Report, old, nu *schema.Message) {
	// 1) 消息版本区间收窄：下界抬高 或 上界压低。
	if nu.MinVersion > old.MinVersion {
		r.add(ReasonVersionRangeNarrowed, old.Name, 0,
			"消息 %q 的版本下界由 %d 抬高到 %d，旧版本 %d..%d 的数据将被拒绝",
			old.Name, old.MinVersion, nu.MinVersion, old.MinVersion, nu.MinVersion-1)
	}
	if nu.MaxVersion < old.MaxVersion {
		r.add(ReasonVersionRangeNarrowed, old.Name, 0,
			"消息 %q 的版本上界由 %d 压低到 %d，已发布版本 %d..%d 将被拒绝",
			old.Name, old.MaxVersion, nu.MaxVersion, nu.MaxVersion+1, old.MaxVersion)
	}

	// 2) 逐字段对比。
	oldFields := fieldMap(old)
	newFields := fieldMap(nu)
	numbers := make([]int, 0, len(oldFields))
	for n := range oldFields {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)

	for _, n := range numbers {
		of := oldFields[n]
		nf, exists := newFields[n]
		if !exists {
			// 字段编号在候选 schema 中完全消失。
			// 仅当该字段在“候选仍然支持的版本区间”里确实有效过才算危险：
			// 即旧字段区间与候选消息区间还有交集。
			oLo, oHi := of.Since, fieldUntil(of, old.MaxVersion)
			if overlapClosed(oLo, oHi, nu.MinVersion, nu.MaxVersion) {
				r.add(ReasonFieldRemovedActive, old.Name, n,
					"字段 %d(%s %q) 在候选 schema 中被删除，但它在仍受支持的版本区间 %d..%d 内有效（原字段区间 %d..%d）",
					n, of.Type, of.Name, maxInt(oLo, nu.MinVersion), minInt(oHi, nu.MaxVersion), oLo, oHi)
			}
			continue
		}

		// 编号仍在：同编号不同名字 => 编号被回收复用（换皮）。
		if nf.Name != of.Name {
			r.add(ReasonFieldNumberReused, old.Name, n,
				"字段编号 %d 原属 %q(%s)，候选 schema 将同一编号给了 %q(%s)；已发布编号不得回收复用",
				n, of.Name, of.Type, nf.Name, nf.Type)
		}
		// 同编号同名但类型变了 => 类型被修改（与复用分开报告）。
		if nf.Name == of.Name && nf.Type != of.Type {
			r.add(ReasonFieldTypeChanged, old.Name, n,
				"字段 %d(%q) 的类型由 %s 改为 %s，线上载荷无法按新类型解析",
				n, of.Name, of.Type, nf.Type)
		}
		// 同编号换名同时也换类型时，除“回收复用”外类型不兼容也单列一条，
		// 避免把多个原因混成一句。
		if nf.Name != of.Name && nf.Type != of.Type {
			r.add(ReasonFieldTypeChanged, old.Name, n,
				"字段编号 %d 复用为 %q 时类型也由 %s 变为 %s，线上载荷不兼容",
				n, nf.Name, of.Type, nf.Type)
		}

		// 版本区间被收窄（字段仍在，但有效区间缩小）。
		if nf.Name == of.Name {
			oLo, oHi := of.Since, fieldUntil(of, old.MaxVersion)
			nLo, nHi := nf.Since, fieldUntil(nf, nu.MaxVersion)
			if nLo > oLo {
				r.add(ReasonVersionRangeNarrowed, old.Name, n,
					"字段 %d(%q) 的 since 由 %d 推后到 %d，版本 %d..%d 将读不到该字段",
					n, of.Name, oLo, nLo, oLo, nLo-1)
			}
			if nHi < oHi {
				r.add(ReasonVersionRangeNarrowed, old.Name, n,
					"字段 %d(%q) 的 until 由 %d 提前到 %d，版本 %d..%d 将读不到该字段",
					n, of.Name, oHi, nHi, nHi+1, oHi)
			}
		}
	}
}

type compatKey struct {
	msg  string
	a, b int
}

func canonKey(cd *schema.CompatDecl) compatKey {
	a, b := cd.A, cd.B
	if a > b {
		a, b = b, a
	}
	return compatKey{msg: cd.Message, a: a, b: b}
}

func reviewCompats(r *Report, published, candidate *schema.File) {
	// 候选声明表（规范化后的键 -> 是否兼容）。候选 schema 已通过校验，
	// 同一键最多一条。
	newDecls := map[compatKey]bool{}
	for _, cd := range candidate.Compats {
		newDecls[canonKey(cd)] = cd.Compat
	}

	// 对旧声明去重（旧 schema 已校验，不会冲突）。
	seen := map[compatKey]bool{}
	keys := make([]compatKey, 0)
	for _, cd := range published.Compats {
		k := canonKey(cd)
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].msg != keys[j].msg {
			return keys[i].msg < keys[j].msg
		}
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].b < keys[j].b
	})

	publishedCompat := map[compatKey]bool{}
	for _, cd := range published.Compats {
		publishedCompat[canonKey(cd)] = cd.Compat
	}

	for _, k := range keys {
		newCompat, ok := newDecls[k]
		oldCompat := publishedCompat[k]
		if !ok {
			r.add(ReasonCompatRemoved, k.msg, 0,
				"消息 %q 版本 %d 与 %d 的 %s 声明在候选 schema 中被删除",
				k.msg, k.a, k.b, compatWord(oldCompat))
			continue
		}
		if newCompat != oldCompat {
			r.add(ReasonCompatReversed, k.msg, 0,
				"消息 %q 版本 %d 与 %d 的兼容性声明由 %s 被反转为 %s",
				k.msg, k.a, k.b, compatWord(oldCompat), compatWord(newCompat))
		}
	}
}

func compatWord(compat bool) string {
	if compat {
		return "compat(兼容)"
	}
	return "incompat(不兼容)"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
