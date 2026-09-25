package wire

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Reader 是对单个连续字节区间的顺序游标读取器。
//
// 任何一次读取失败（截断、越界、非法 varint）后，Reader 进入
// 不可恢复的错误态，后续所有操作都返回同一个 *Error(KindCorrupt)。
// Reader 只负责字节级损坏，不做版本判断。
type Reader struct {
	buf []byte
	pos int
	err *Error
}

// NewReader 基于给定字节区间构造 Reader。
func NewReader(b []byte) *Reader {
	return &Reader{buf: b}
}

func corruptf(format string, args ...any) *Error {
	return &Error{Kind: KindCorrupt, Detail: fmt.Sprintf(format, args...)}
}

// Err 返回读取器当前的错误（无错时为 nil）。
func (r *Reader) Err() error {
	if r.err == nil {
		return nil
	}
	return r.err
}

// fail 将读取器置为错误态并返回该错误（仅设置一次）。
func (r *Reader) fail(e *Error) error {
	if r.err == nil {
		r.err = e
	}
	return r.err
}

// Pos 返回当前游标位置。
func (r *Reader) Pos() int { return r.pos }

// Len 返回剩余可读字节数。
func (r *Reader) Len() int {
	if r.err != nil || r.pos > len(r.buf) {
		return 0
	}
	return len(r.buf) - r.pos
}

// EOF 报告区间是否已被完整消费且无错误。
func (r *Reader) EOF() bool {
	return r.err == nil && r.pos == len(r.buf)
}

// ReadUVarint 读取一个无符号 varint；超过 10 字节或被截断视为损坏。
func (r *Reader) ReadUVarint() uint64 {
	if r.err != nil {
		return 0
	}
	var x uint64
	var s uint
	for i := 0; ; i++ {
		if r.pos >= len(r.buf) {
			r.fail(corruptf("varint truncated at byte %d", i))
			return 0
		}
		c := r.buf[r.pos]
		r.pos++
		if i == 9 && c > 1 { // 第 10 字节只允许最低位为 1
			r.fail(corruptf("varint overflow at byte %d (c=%#x)", i, c))
			return 0
		}
		if c < 0x80 {
			if i > 9 {
				r.fail(corruptf("varint too long (%d bytes)", i+1))
				return 0
			}
			return x | uint64(c)<<s
		}
		x |= uint64(c&0x7f) << s
		s += 7
	}
}

// ReadSVarint 读取一个 zigzag 编码的有符号 varint。
func (r *Reader) ReadSVarint() int64 {
	u := r.ReadUVarint()
	if r.err != nil {
		return 0
	}
	return int64(u>>1) ^ -int64(u&1)
}

// ReadFixed64 读取 8 字节小端定长值。
func (r *Reader) ReadFixed64() float64 {
	if r.err != nil {
		return 0
	}
	if r.Len() < 8 {
		r.fail(corruptf("fixed64 truncated: need 8, have %d", r.Len()))
		return 0
	}
	u := binary.LittleEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return math.Float64frombits(u)
}

// ReadFixed32 读取 4 字节小端定长值。
func (r *Reader) ReadFixed32() uint32 {
	if r.err != nil {
		return 0
	}
	if r.Len() < 4 {
		r.fail(corruptf("fixed32 truncated: need 4, have %d", r.Len()))
		return 0
	}
	u := binary.LittleEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return u
}

// ReadBytes 读取长度前缀的字节区间，返回指向原缓冲区的切片。
func (r *Reader) ReadBytes() []byte {
	if r.err != nil {
		return nil
	}
	n := r.ReadUVarint()
	if r.err != nil {
		return nil
	}
	if n > uint64(r.Len()) {
		r.fail(corruptf("length-delimited field out of range: need %d, have %d", n, r.Len()))
		return nil
	}
	out := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return out
}

// ReadString 读取长度前缀的字符串（拷贝语义由调用方按需处理）。
func (r *Reader) ReadString() string {
	b := r.ReadBytes()
	if r.err != nil {
		return ""
	}
	return string(b)
}

// ReadTag 读取字段标签并拆分为字段编号与线缆类型。
func (r *Reader) ReadTag() (num uint32, wt WireType) {
	if r.err != nil {
		return 0, 0
	}
	tag := r.ReadUVarint()
	if r.err != nil {
		return 0, 0
	}
	num = uint32(tag >> 3)
	wt = WireType(tag & 0x7)
	if num == 0 {
		r.fail(corruptf("invalid tag with field number 0 (tag=%#x)", tag))
		return 0, 0
	}
	switch wt {
	case WireVarint, WireFixed64, WireBytes, WireFixed32:
		return num, wt
	default:
		r.fail(corruptf("unknown wire type %d for field %d", wt, num))
		return 0, 0
	}
}

// SkipField 跳过当前字段值；wt 来自刚读出的 tag。
// 对 WireBytes 会做长度越界检查；重复/打包语义由生成代码处理。
func (r *Reader) SkipField(wt WireType) {
	if r.err != nil {
		return
	}
	switch wt {
	case WireVarint:
		r.ReadUVarint()
	case WireFixed64:
		r.ReadFixed64()
	case WireFixed32:
		r.ReadFixed32()
	case WireBytes:
		r.ReadBytes()
	}
}

// Advance 在不做语义解析的情况下前进 n 字节；n 超过剩余长度时报损坏。
// 用于“已通过子读取器确认结构合法，只需同步主游标”的场景。
func (r *Reader) Advance(n int) {
	if r.err != nil {
		return
	}
	if n < 0 || n > r.Len() {
		r.fail(corruptf("advance %d out of range (remaining %d)", n, r.Len()))
		return
	}
	r.pos += n
}
