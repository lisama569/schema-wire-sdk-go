package review

import (
	"errors"
	"fmt"
	"strings"
)

// ReviewError 表示演进审查未通过。可用 errors.As 归类，并通过 Report
// 拿到逐原因分列的全部不安全条目；它与 schema.ParseError、
// schema.ValidationError、gen.UncompilableError 均不同。
type ReviewError struct {
	Report *Report
}

// ErrReviewRejected 是审查被拦下的哨兵错误，便于 errors.Is 判定。
var ErrReviewRejected = errors.New("review: 协议演进审查未通过")

func (e *ReviewError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s：发现 %d 条不安全改动:\n", ErrReviewRejected, len(e.Report.Findings))
	counts := map[Reason]int{}
	for _, f := range e.Report.Findings {
		counts[f.Reason]++
	}
	reasons := make([]Reason, 0, len(counts))
	for k := range counts {
		reasons = append(reasons, k)
	}
	// 稳定输出原因统计。
	for i := Reason(1); i <= ReasonMessageRemoved; i++ {
		if counts[i] > 0 {
			fmt.Fprintf(&b, "  - %s: %d 条\n", i, counts[i])
		}
	}
	for _, f := range e.Report.Findings {
		fmt.Fprintf(&b, "  [%s] 消息=%s", f.Reason, f.Message)
		if f.Field != 0 {
			fmt.Fprintf(&b, " 字段=%d", f.Field)
		}
		fmt.Fprintf(&b, ": %s\n", f.Detail)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Unwrap 使 errors.Is(err, ErrReviewRejected) 成立。
func (e *ReviewError) Unwrap() error { return ErrReviewRejected }

// AsReviewError 从任意错误中取出 *ReviewError（errors.As 的便捷封装）。
func AsReviewError(err error) (*ReviewError, bool) {
	var re *ReviewError
	if errors.As(err, &re) {
		return re, true
	}
	return nil, false
}
