package wire

import (
	"errors"
	"fmt"
)

// ErrLimitExceeded 表示解码超出资源上限，
// 可用 errors.Is 与 ErrCorrupt、ErrIncompatibleVersion 区分。
var ErrLimitExceeded = errors.New("wire: 超出资源上限")

// Limits 配置单条报文解码的资源上限；各维度为零表示不限制。
//
// 配置在创建解码器时被拷贝，之后修改原 Limits 不影响已创建的解码器；
// 每个解码器持有独立的计数状态，多条报文并发解码互不影响。
type Limits struct {
	MaxBytes  int // 单条报文最大字节数（仅顶层解码时检查）
	MaxDepth  int // 嵌套消息最大层数（顶层消息为第 1 层）
	MaxFields int // 单条消息最大字段个数（含未知字段）
}

// limitState 是单个解码器的资源计数状态，不跨解码器共享。
type limitState struct {
	maxDepth  int
	maxFields int
	depth     int // 当前嵌套层数（顶层为 1）
	fields    int // 当前消息已读字段数
}

// NewDecoderLimits 创建带资源上限的解码器；lim 为 nil 时行为与 NewDecoder 一致。
// 报文字节数超限立即返回包装 ErrLimitExceeded 的错误。
func NewDecoderLimits(data []byte, lim *Limits) (*Decoder, error) {
	if lim == nil {
		return NewDecoder(data), nil
	}
	if lim.MaxBytes > 0 && len(data) > lim.MaxBytes {
		return nil, fmt.Errorf("%w: 报文字节数 %d 超过上限 %d",
			ErrLimitExceeded, len(data), lim.MaxBytes)
	}
	return &Decoder{data: data, lim: &limitState{
		maxDepth:  lim.MaxDepth,
		maxFields: lim.MaxFields,
		depth:     1,
	}}, nil
}

// Derive 为嵌套消息派生子解码器：继承上限配置、层数加一、字段计数独立。
// 层数超限返回包装 ErrLimitExceeded 的错误。
func (d *Decoder) Derive(data []byte) (*Decoder, error) {
	if d.lim == nil {
		return NewDecoder(data), nil
	}
	depth := d.lim.depth + 1
	if d.lim.maxDepth > 0 && depth > d.lim.maxDepth {
		return nil, fmt.Errorf("%w: 嵌套层数 %d 超过上限 %d",
			ErrLimitExceeded, depth, d.lim.maxDepth)
	}
	return &Decoder{data: data, lim: &limitState{
		maxDepth:  d.lim.maxDepth,
		maxFields: d.lim.maxFields,
		depth:     depth,
	}}, nil
}

// countField 在读取每个字段标签时计数，超限返回 ErrLimitExceeded。
func (d *Decoder) countField() error {
	if d.lim == nil {
		return nil
	}
	d.lim.fields++
	if d.lim.maxFields > 0 && d.lim.fields > d.lim.maxFields {
		return fmt.Errorf("%w: 单条消息字段数超过上限 %d",
			ErrLimitExceeded, d.lim.maxFields)
	}
	return nil
}
