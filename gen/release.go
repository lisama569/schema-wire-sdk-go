package gen

import (
	"fmt"

	"github.com/lisama569/schema-wire-sdk-go/review"
	"github.com/lisama569/schema-wire-sdk-go/schema"
)

// GenerateRelease 是 CI/发布链路使用的生成入口：先把线上已发布 schema
// （baseline）与待发布 schema（candidate）做协议演进审查；审查发现任何
// 不安全改动都直接返回 *review.ReviewError 且不产出任何生成结果。
//
// baseline 为 nil 时跳过演进审查（首次发布），但语义校验与可编译性预检
// 仍然执行。GenerateRelease 无请求级共享状态，可并发调用。
func GenerateRelease(baseline, candidate *schema.File, opts Options) (map[string][]byte, error) {
	if baseline != nil {
		report, err := review.Review(baseline, candidate)
		if err != nil {
			return nil, fmt.Errorf("演进审查无法完成，已拦截生成: %w", err)
		}
		if !report.Safe() {
			return nil, &review.ReviewError{Report: report}
		}
	}
	return Generate(candidate, opts)
}
