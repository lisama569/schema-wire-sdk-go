package wire

import (
	"errors"
	"fmt"
)

// 资源超限错误类别，与数据损坏（ErrCorrupt）、版本不兼容
// （ErrIncompatibleVersion）互不归属，便于上层按类别限流和告警。
var ErrResourceLimit = errors.New("wire: 超出资源上限")

// Limits 描述单次解码的资源上限。零值 Limits 表示全部不设限；
// Limits 是纯值类型，按值传递即可，多条报文在不同 goroutine 中并发解码时
// 各自持有自己的副本与计数器，配置不会互相串扰。
type Limits struct {
	MaxBytes  int // 单条报文（每一层消息载荷）的最大字节数；0 表示不设限
	MaxDepth  int // 嵌套消息的最大层数；1 表示只允许顶层消息；0 表示不设限
	MaxFields int // 单条消息内字段个数上限（每层独立计数）；0 表示不设限
}

// DefaultLimits 返回一组保守的默认上限：
// 64MiB 字节、100 层嵌套、每层 10000 个字段。
func DefaultLimits() Limits {
	return Limits{MaxBytes: 64 << 20, MaxDepth: 100, MaxFields: 10000}
}

// checkBytes 校验报文字节数是否超限。
func (l Limits) checkBytes(n int) error {
	if l.MaxBytes > 0 && n > l.MaxBytes {
		return fmt.Errorf("%w: 报文 %d 字节超过上限 %d", ErrResourceLimit, n, l.MaxBytes)
	}
	return nil
}

// checkDepth 校验即将进入的消息层级 depth 是否超过嵌套层数上限。
func (l Limits) checkDepth(depth int) error {
	if l.MaxDepth > 0 && depth > l.MaxDepth {
		return fmt.Errorf("%w: 嵌套层数 %d 超过上限 %d", ErrResourceLimit, depth, l.MaxDepth)
	}
	return nil
}

// checkFields 校验单条消息内已出现的字段个数是否超限。
func (l Limits) checkFields(n int) error {
	if l.MaxFields > 0 && n > l.MaxFields {
		return fmt.Errorf("%w: 单条消息字段数 %d 超过上限 %d", ErrResourceLimit, n, l.MaxFields)
	}
	return nil
}
