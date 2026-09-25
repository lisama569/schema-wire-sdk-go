// Package wire 提供版本化二进制编解码运行时。
//
// 消息线上格式：
//
//	varint(version)            —— 消息版本
//	重复出现的字段编码：
//	  varint(fieldNum<<1|wt)   —— 字段标签
//	  wt==TypeVarint: varint(value)
//	  wt==TypeBytes:  varint(len) + 原始字节
//
// 解码时遇到的未知字段被完整保留，重新编码时原样写回。
package wire

import (
	"errors"
	"fmt"
)

// 错误类别：不兼容版本 vs 数据损坏（截断/越界），可用 errors.Is 区分。
var (
	// ErrIncompatibleVersion 表示数据版本不在本运行时支持的版本区间内。
	ErrIncompatibleVersion = errors.New("wire: 不兼容的版本")
	// ErrCorrupt 表示数据损坏（截断、越界、非法标签等）。
	ErrCorrupt = errors.New("wire: 数据损坏")
)

// WireType 字段的线上编码类型。
type WireType int

const (
	TypeVarint WireType = iota // int32/int64/bool
	TypeBytes                  // string/bytes/嵌套消息
)

// UnknownField 保存解码时遇到、但当前 schema 版本未知的字段，
// 重新编码时原样写回。
type UnknownField struct {
	Number int
	Type   WireType
	Data   []byte // varint 的原始字节，或 length-delimited 的载荷
}

// UnknownSet 是未知字段集合，按解码出现顺序保留。
type UnknownSet struct {
	Fields []UnknownField
}

// Add 记录一个未知字段。
func (u *UnknownSet) Add(f UnknownField) { u.Fields = append(u.Fields, f) }

// EncodeTo 将全部未知字段按原顺序写入编码器。
func (u *UnknownSet) EncodeTo(e *Encoder) {
	for _, f := range u.Fields {
		e.WriteTag(f.Number, f.Type)
		switch f.Type {
		case TypeVarint:
			e.WriteRawVarint(f.Data)
		case TypeBytes:
			e.WriteBytes(f.Data)
		}
	}
}

// CheckVersion 校验数据版本是否落在 [min, max] 支持区间内。
func CheckVersion(version, min, max int) error {
	if version < min || version > max {
		return fmt.Errorf("%w: 数据版本 %d 不在支持区间 [%d, %d]",
			ErrIncompatibleVersion, version, min, max)
	}
	return nil
}

// ---------------- Encoder ----------------

// Encoder 增量写入编码结果。
type Encoder struct {
	buf []byte
}

// NewEncoder 创建编码器。
func NewEncoder() *Encoder { return &Encoder{} }

// Bytes 返回已编码的字节。
func (e *Encoder) Bytes() []byte { return e.buf }

// WriteRawVarint 写入原始 varint 字节（用于未知字段透传）。
func (e *Encoder) WriteRawVarint(b []byte) { e.buf = append(e.buf, b...) }

// WriteUvarint 写入无符号 varint。
func (e *Encoder) WriteUvarint(v uint64) {
	var tmp [10]byte
	n := 0
	for v >= 0x80 {
		tmp[n] = byte(v) | 0x80
		v >>= 7
		n++
	}
	tmp[n] = byte(v)
	e.buf = append(e.buf, tmp[:n+1]...)
}

// WriteInt64 以 zigzag 编码写入有符号整数。
func (e *Encoder) WriteInt64(v int64) {
	e.WriteUvarint(uint64(v<<1) ^ uint64(v>>63))
}

// WriteBool 写入布尔值。
func (e *Encoder) WriteBool(v bool) {
	if v {
		e.WriteUvarint(1)
	} else {
		e.WriteUvarint(0)
	}
}

// WriteTag 写入字段标签。
func (e *Encoder) WriteTag(fieldNum int, wt WireType) {
	e.WriteUvarint(uint64(fieldNum)<<1 | uint64(wt))
}

// WriteBytes 写入 length-delimited 字节串。
func (e *Encoder) WriteBytes(b []byte) {
	e.WriteUvarint(uint64(len(b)))
	e.buf = append(e.buf, b...)
}

// WriteString 写入字符串。
func (e *Encoder) WriteString(s string) { e.WriteBytes([]byte(s)) }

// WriteVersion 写入消息版本头。
func (e *Encoder) WriteVersion(v int) { e.WriteUvarint(uint64(v)) }

// ---------------- Decoder ----------------

// Decoder 增量读取编码数据。
type Decoder struct {
	data []byte
	off  int
}

// NewDecoder 基于完整数据创建解码器。
func NewDecoder(data []byte) *Decoder { return &Decoder{data: data} }

// Done 报告数据是否已消费完毕。
func (d *Decoder) Done() bool { return d.off >= len(d.data) }

// ReadUvarint 读取无符号 varint；数据不足或溢出返回 ErrCorrupt。
func (d *Decoder) ReadUvarint() (uint64, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		if d.off >= len(d.data) {
			return 0, fmt.Errorf("%w: varint 截断（偏移 %d）", ErrCorrupt, d.off)
		}
		b := d.data[d.off]
		d.off++
		if b < 0x80 {
			if i == 9 && b > 1 {
				return 0, fmt.Errorf("%w: varint 溢出", ErrCorrupt)
			}
			return v | uint64(b)<<uint(7*i), nil
		}
		v |= uint64(b&0x7f) << uint(7*i)
	}
	return 0, fmt.Errorf("%w: varint 溢出", ErrCorrupt)
}

// ReadRawVarint 读取 varint 并返回其原始字节（用于未知字段透传）。
func (d *Decoder) ReadRawVarint() ([]byte, error) {
	start := d.off
	if _, err := d.ReadUvarint(); err != nil {
		return nil, err
	}
	return d.data[start:d.off], nil
}

// ReadInt64 读取 zigzag 编码的有符号整数。
func (d *Decoder) ReadInt64() (int64, error) {
	u, err := d.ReadUvarint()
	if err != nil {
		return 0, err
	}
	return int64(u>>1) ^ -int64(u&1), nil
}

// ReadBool 读取布尔值。
func (d *Decoder) ReadBool() (bool, error) {
	u, err := d.ReadUvarint()
	if err != nil {
		return false, err
	}
	if u > 1 {
		return false, fmt.Errorf("%w: 非法布尔值 %d", ErrCorrupt, u)
	}
	return u == 1, nil
}

// ReadTag 读取字段标签，返回字段编号与线上类型。
func (d *Decoder) ReadTag() (int, WireType, error) {
	u, err := d.ReadUvarint()
	if err != nil {
		return 0, 0, err
	}
	num := int(u >> 1)
	wt := WireType(u & 1)
	if num <= 0 {
		return 0, 0, fmt.Errorf("%w: 非法字段编号 %d", ErrCorrupt, num)
	}
	if wt != TypeVarint && wt != TypeBytes {
		return 0, 0, fmt.Errorf("%w: 未知线上类型 %d", ErrCorrupt, wt)
	}
	return num, wt, nil
}

// ReadBytes 读取 length-delimited 字节串；长度越界返回 ErrCorrupt。
func (d *Decoder) ReadBytes() ([]byte, error) {
	n, err := d.ReadUvarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(d.data)-d.off) {
		return nil, fmt.Errorf("%w: 长度 %d 超出剩余 %d 字节",
			ErrCorrupt, n, len(d.data)-d.off)
	}
	b := d.data[d.off : d.off+int(n)]
	d.off += int(n)
	return b, nil
}

// ReadString 读取字符串。
func (d *Decoder) ReadString() (string, error) {
	b, err := d.ReadBytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ReadVersion 读取消息版本头。
func (d *Decoder) ReadVersion() (int, error) {
	u, err := d.ReadUvarint()
	if err != nil {
		return 0, err
	}
	return int(u), nil
}
