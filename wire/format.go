package wire

import "math"

// WireType 是 TLV 中字段值的线缆类型。
type WireType uint8

const (
	WireVarint  WireType = 0 // uvarint / zigzag varint / bool
	WireFixed64 WireType = 1 // float64
	WireBytes   WireType = 2 // string / bytes / 嵌套消息
	WireFixed32 WireType = 5 // 32 位定长（保留）
)

// AppendUVarint 追加一个无符号 LEB128 varint。
func AppendUVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// AppendSVarint 追加一个 zigzag 编码的有符号 varint。
func AppendSVarint(b []byte, v int64) []byte {
	return AppendUVarint(b, uint64(v<<1)^uint64(v>>63))
}

// AppendFixed64 追加一个 8 字节小端定长值（float64）。
func AppendFixed64(b []byte, v float64) []byte {
	var buf [8]byte
	u := math.Float64bits(v)
	buf[0] = byte(u)
	buf[1] = byte(u >> 8)
	buf[2] = byte(u >> 16)
	buf[3] = byte(u >> 24)
	buf[4] = byte(u >> 32)
	buf[5] = byte(u >> 40)
	buf[6] = byte(u >> 48)
	buf[7] = byte(u >> 56)
	return append(b, buf[:]...)
}

// AppendFixed32 追加一个 4 字节小端定长值（保留类型）。
func AppendFixed32(b []byte, v uint32) []byte {
	var buf [4]byte
	buf[0] = byte(v)
	buf[1] = byte(v >> 8)
	buf[2] = byte(v >> 16)
	buf[3] = byte(v >> 24)
	return append(b, buf[:]...)
}

// AppendTag 追加字段标签：(field_number << 3) | wire_type 的 varint。
func AppendTag(b []byte, num uint32, wt WireType) []byte {
	return AppendUVarint(b, uint64(num)<<3|uint64(wt))
}

// AppendBytes 追加长度前缀的原始字节区间。
func AppendBytes(b []byte, v []byte) []byte {
	b = AppendUVarint(b, uint64(len(v)))
	return append(b, v...)
}

// AppendString 追加长度前缀的 UTF-8 字符串。
func AppendString(b []byte, v string) []byte {
	b = AppendUVarint(b, uint64(len(v)))
	return append(b, v...)
}

// EncodeMessage 组装顶层消息信封：schema 版本前缀 + 消息体。
// 版本必须 >= 1，由调用方（生成代码）保证。
func EncodeMessage(version uint32, body []byte) []byte {
	out := make([]byte, 0, len(body)+8)
	out = AppendUVarint(out, uint64(version))
	out = append(out, body...)
	return out
}
