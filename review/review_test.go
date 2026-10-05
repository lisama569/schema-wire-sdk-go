package review

import (
	"errors"
	"testing"

	"github.com/lisama569/schema-wire-sdk-go/schema"
)

func mustParse(t *testing.T, idl string) *schema.File {
	t.Helper()
	f, err := schema.Parse(idl)
	if err != nil {
		t.Fatalf("解析失败: %v\nIDL:\n%s", err, idl)
	}
	return f
}

// findReasons 收集报告中出现的原因集合。
func findReasons(r *Report) map[Reason][]*Finding {
	out := map[Reason][]*Finding{}
	for i := range r.Findings {
		f := &r.Findings[i]
		out[f.Reason] = append(out[f.Reason], f)
	}
	return out
}

// TestReviewSafeEvolutions 验证公认安全的演进不产生任何条目：
// 新增字段、新增消息、新增 compat 声明、放宽版本区间。
func TestReviewSafeEvolutions(t *testing.T) {
	old := `package p;
message M version 1..2 {
    1: string name since 1;
}
compat M 1 2;
`
	cases := []struct {
		name string
		nu   string
	}{
		{"新增字段与新增消息", `package p;
message M version 1..3 {
    1: string name since 1;
    2: int64 age since 2;
}
message Added version 1..1 {
    1: string x;
}
compat M 1 2;
`},
		{"版本区间放宽", `package p;
message M version 1..4 {
    1: string name since 1;
}
compat M 1 2;
`},
		{"追加 compat 声明", `package p;
message M version 1..3 {
    1: string name since 1;
}
compat M 1 2;
compat M 2 3;
`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Logf("输入: 旧 schema:\n%s\n候选 schema:\n%s", old, c.nu)
			r, err := Review(mustParse(t, old), mustParse(t, c.nu))
			if err != nil {
				t.Fatalf("审查应可完成: %v", err)
			}
			if !r.Safe() {
				for _, f := range r.Findings {
					t.Errorf("意外条目 [%s] %s: %s", f.Reason, f.Message, f.Detail)
				}
				t.Fatal("判定依据: 安全演进不应有任何不安全条目")
			}
			t.Log("判定: 无任何不安全条目，演进安全")
		})
	}
}

// TestReviewUnsafeReasons 逐类验证不安全改动被分别列出，且不同原因不混淆。
func TestReviewUnsafeReasons(t *testing.T) {
	base := `package p;
message M version 1..3 {
    1: string name since 1;
    2: int32 age since 1;
    3: string mail since 1 until 2;
}
compat M 1 2;
incompat M 1 3;
`
	cases := []struct {
		name   string
		nu     string
		expect Reason
	}{
		{
			"字段编号回收复用",
			`package p;
message M version 1..3 {
    1: string name since 1;
    2: int64 birthday since 1;
    3: string mail since 1 until 2;
}
compat M 1 2;
incompat M 1 3;
`,
			ReasonFieldNumberReused,
		},
		{
			"字段类型被修改",
			`package p;
message M version 1..3 {
    1: string name since 1;
    2: int64 age since 1;
    3: string mail since 1 until 2;
}
compat M 1 2;
incompat M 1 3;
`,
			ReasonFieldTypeChanged,
		},
		{
			"存活区间内删除字段",
			`package p;
message M version 1..3 {
    1: string name since 1;
    3: string mail since 1 until 2;
}
compat M 1 2;
incompat M 1 3;
`,
			ReasonFieldRemovedActive,
		},
		{
			"版本区间被收窄(上界压低)",
			`package p;
message M version 1..2 {
    1: string name since 1;
    2: int32 age since 1;
    3: string mail since 1 until 2;
}
compat M 1 2;
`,
			ReasonVersionRangeNarrowed,
		},
		{
			"兼容性声明被反转",
			`package p;
message M version 1..3 {
    1: string name since 1;
    2: int32 age since 1;
    3: string mail since 1 until 2;
}
incompat M 1 2;
incompat M 1 3;
`,
			ReasonCompatReversed,
		},
		{
			"兼容性声明被删除",
			`package p;
message M version 1..3 {
    1: string name since 1;
    2: int32 age since 1;
    3: string mail since 1 until 2;
}
incompat M 1 3;
`,
			ReasonCompatRemoved,
		},
		{
			"消息被删除",
			`package p;
message Other version 1..1 {
    1: string x;
}
`,
			ReasonMessageRemoved,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Logf("输入候选 schema:\n%s", c.nu)
			r, err := Review(mustParse(t, base), mustParse(t, c.nu))
			if err != nil {
				t.Fatalf("审查应可完成: %v", err)
			}
			if r.Safe() {
				t.Fatal("判定依据: 不安全演进必须被拦下，报告却为空")
			}
			byReason := findReasons(r)
			fs, ok := byReason[c.expect]
			if !ok {
				var got []string
				for _, f := range r.Findings {
					got = append(got, f.Reason.String())
				}
				t.Fatalf("判定依据: 应包含原因 [%s]，实际条目原因: %v", c.expect, got)
			}
			t.Logf("判定依据: 找到 %d 条 [%s]: %s", len(fs), c.expect, fs[0].Detail)
		})
	}
}

// TestReviewReusedNumberAlsoChangedTypeReportsBoth 验证编号回收且类型改变时
// 两种原因分别列出，而不是混成一句。
func TestReviewReusedNumberAlsoChangedTypeReportsBoth(t *testing.T) {
	old := `package p;
message M version 1..2 {
    2: int32 age since 1;
}
`
	nu := `package p;
message M version 1..2 {
    2: string birthday since 1;
}
`
	r, err := Review(mustParse(t, old), mustParse(t, nu))
	if err != nil {
		t.Fatal(err)
	}
	byReason := findReasons(r)
	if _, ok := byReason[ReasonFieldNumberReused]; !ok {
		t.Fatalf("应列出编号回收复用，实际: %+v", r.Findings)
	}
	if _, ok := byReason[ReasonFieldTypeChanged]; !ok {
		t.Fatalf("应同时列出类型被修改（原因分列），实际: %+v", r.Findings)
	}
	t.Logf("判定: 同一条改动产出两条不同原因的条目: 回收复用 + 类型修改，共 %d 条", len(r.Findings))
}

// TestReviewFieldAlreadyDeadRemovalIsSafe 验证字段只在旧版本有效
// （其区间与候选支持区间已无交集）时删除是安全的：旧版本本来就无法
// 被新运行时处理，而该字段在仍受支持的版本里从未出现。
func TestReviewFieldAlreadyDeadRemovalIsSafe(t *testing.T) {
	// 候选把上界保持在 3，字段 mail 的旧区间是 1..2，仍在候选区间内 -> 危险。
	old := `package p;
message M version 1..3 {
    1: string name since 1;
    2: string mail since 1 until 2;
}
`
	unsafe := `package p;
message M version 1..3 {
    1: string name since 1;
}
`
	r, _ := Review(mustParse(t, old), mustParse(t, unsafe))
	if r.Safe() {
		t.Fatal("判定依据: 字段在候选仍支持的 1..2 内有效，删除应判危险")
	}
	t.Logf("判定: 字段区间与候选区间有交集 -> %s", r.Findings[0].Detail)

	// 候选同时把上界压到 0（区间起点抬高到 3），此时 mail 区间 1..2 与
	// 候选 [3,3] 无交集：字段本身删除不再单列（区间收窄已单独成条）。
	safeish := `package p;
message M version 3..3 {
    1: string name since 3;
}
`
	r2, _ := Review(mustParse(t, old), mustParse(t, safeish))
	for _, f := range r2.Findings {
		if f.Reason == ReasonFieldRemovedActive {
			t.Fatalf("判定依据: 与候选区间无交集的字段删除不应单列 RemovedActive: %s", f.Detail)
		}
	}
	var hasNarrow bool
	for _, f := range r2.Findings {
		if f.Reason == ReasonVersionRangeNarrowed {
			hasNarrow = true
		}
	}
	if !hasNarrow {
		t.Fatal("判定依据: 区间收窄本身仍应被报告")
	}
	t.Logf("判定: 区间收窄单列 %d 条，已死字段删除不重复报", len(r2.Findings))
}

// TestReviewRejectsInvalidInputs 验证任一侧 schema 不合法时审查中止，
// 不返回可能误导的报告。
func TestReviewRejectsInvalidInputs(t *testing.T) {
	bad := `package p;
message M version 3..1 {
    1: string a;
}
`
	good := `package p;
message M version 1..2 {
    1: string a;
}
`
	if _, err := Review(mustParse(t, bad), mustParse(t, good)); err == nil {
		t.Fatal("基线不合法时审查应返回错误")
	} else {
		t.Logf("判定: 基线不合法 -> %v", err)
	}
	if _, err := Review(mustParse(t, good), mustParse(t, bad)); err == nil {
		t.Fatal("候选不合法时审查应返回错误")
	} else {
		t.Logf("判定: 候选不合法 -> %v", err)
	}
}

// TestReviewErrorClassification 验证审查失败可以用 errors.Is/As 明确归类，
// 且错误文本逐条列出原因。
func TestReviewErrorClassification(t *testing.T) {
	old := `package p; message M version 1..2 { 1: string a; } compat M 1 2;`
	nu := `package p; message M version 1..2 { 1: int32 a; }`
	r, err := Review(mustParse(t, old), mustParse(t, nu))
	if err != nil {
		t.Fatal(err)
	}
	re := &ReviewError{Report: r}
	if !errors.Is(re, ErrReviewRejected) {
		t.Fatal("errors.Is(ReviewError, ErrReviewRejected) 应为 true")
	}
	if got, ok := AsReviewError(re); !ok || got != re {
		t.Fatal("AsReviewError 应能取回原错误")
	}
	t.Logf("判定: 审查失败可归类为 ErrReviewRejected；错误文本:\n%v", re)
}

// TestReviewDeterministic 验证同一对比重复审查，条目顺序稳定。
func TestReviewDeterministic(t *testing.T) {
	old := `package p;
message M version 1..3 {
    1: string a; 2: string b; 3: string c;
}
compat M 1 2;
`
	nu := `package p;
message M version 1..3 {
    1: int32 a; 2: int64 b;
}
`
	var first string
	for i := 0; i < 3; i++ {
		r, err := Review(mustParse(t, old), mustParse(t, nu))
		if err != nil {
			t.Fatal(err)
		}
		var s string
		for _, f := range r.Findings {
			s += f.Reason.String() + ":" + f.Message + ":" +
				string(rune('0'+f.Field)) + ";"
		}
		if i == 0 {
			first = s
		} else if s != first {
			t.Fatalf("第 %d 次审查顺序不稳定:\n%s\n%s", i, first, s)
		}
	}
	t.Logf("判定: 三次审查条目顺序一致: %s", first)
}
