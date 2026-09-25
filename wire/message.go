package wire

// RawRecord 保存一个字段（通常是未知字段）从 tag 到值结束的原始字节，
// 重新编码时原样追加，从而无损透传新版本新增字段。
type RawRecord struct {
	Num uint32
	WT  WireType
	// Raw 包含 tag 与完整值的原始字节区间。
	Raw []byte
}

// AppendTo 将记录的原始字节原样追加到 b。
func (rec RawRecord) AppendTo(b []byte) []byte {
	return append(b, rec.Raw...)
}

// ReadRawRecord 读取一个完整字段记录（tag + 值），并保留其原始字节区间。
// 读取失败时 Reader 进入损坏态。
func (r *Reader) ReadRawRecord() RawRecord {
	if r.err != nil {
		return RawRecord{}
	}
	start := r.pos
	num, wt := r.ReadTag()
	if r.err != nil {
		return RawRecord{}
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
	if r.err != nil {
		return RawRecord{}
	}
	return RawRecord{Num: num, WT: wt, Raw: r.buf[start:r.pos]}
}

// DecodeEnvelope 拆读顶层消息信封：uvarint 版本前缀 + 消息体。
//
// 错误分类：
//   - 版本前缀截断/非法 varint：KindCorrupt（数据损坏）；
//   - 版本号为 0：KindIncompatibleVersion（结构可解析但版本不可接受）。
//
// 版本上限的接受策略由生成代码按其 schema 描述符决定，本函数不做判断。
func DecodeEnvelope(b []byte) (version uint32, body []byte, err error) {
	r := NewReader(b)
	v := r.ReadUVarint()
	if r.Err() != nil {
		return 0, nil, r.Err()
	}
	if v == 0 {
		return 0, nil, &Error{
			Kind:   KindIncompatibleVersion,
			Detail: "envelope version is 0, the lowest accepted version is 1",
		}
	}
	if v > uint64(^uint32(0)) {
		return 0, nil, &Error{
			Kind:   KindIncompatibleVersion,
			Detail: "envelope version exceeds uint32 range",
		}
	}
	return uint32(v), b[r.Pos():], nil
}
