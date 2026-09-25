package wire

import (
	"bytes"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 16383, 16384, 1<<63 - 1, 1 << 63}
	for _, v := range cases {
		b := AppendUVarint(nil, v)
		r := NewReader(b)
		got := r.ReadUVarint()
		if err := r.Err(); err != nil {
			t.Fatalf("uvarint %d unexpected error: %v", v, err)
		}
		if got != v || !r.EOF() {
			t.Fatalf("uvarint %d got %d eof=%v", v, got, r.EOF())
		}
	}
}

func TestSVarintRoundTrip(t *testing.T) {
	for _, v := range []int64{0, 1, -1, 63, -64, 1 << 40, -(1 << 40)} {
		b := AppendSVarint(nil, v)
		r := NewReader(b)
		got := r.ReadSVarint()
		if got != v {
			t.Fatalf("svarint %d got %d", v, got)
		}
	}
}

func TestCorruptionCases(t *testing.T) {
	inputs := [][]byte{
		{0x80},       // 截断的 varint
		{0x80, 0x80}, // 继续截断
		{0x02, 0xff}, // 长度前缀 2，只剩 1 字节 -> 越界
		{0x01},       // tag 合法(字段0? 0x01 => num=0) -> 字段编号 0
		{0x0d, 0, 0}, // fixed64 tag 但只有 2 字节 -> 截断
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}, // 溢出 varint
	}
	for i, in := range inputs {
		r := NewReader(in)
		num, wt := r.ReadTag()
		if r.Err() == nil {
			r.SkipField(wt)
		}
		_ = num
		if err := r.Err(); err == nil {
			t.Fatalf("case %d input %v: expected corruption, got nil", i, in)
		} else if !IsCorruption(err) {
			t.Fatalf("case %d input %v: expected corrupt, got %v", i, in, classify(err))
		} else {
			t.Logf("input=%v 判定依据=读取失败 %v 错误归类=corrupt_data", in, err)
		}
	}
}

func TestEnvelopeErrorClassification(t *testing.T) {
	// 版本前缀被截断 -> 损坏
	if _, _, err := DecodeEnvelope([]byte{0x80}); !IsCorruption(err) {
		t.Fatalf("truncated version prefix: want corrupt, got %v", err)
	}
	// 版本为 0 -> 版本不兼容（结构可解析）
	v, body, err := DecodeEnvelope([]byte{0x00, 0xaa})
	if !IsIncompatibleVersion(err) {
		t.Fatalf("version 0: want incompatible, got %v", err)
	}
	t.Logf("input=[0x00 0xaa] 判定依据=版本号可解析但为0 version=%d body=%v 错误归类=incompatible_version", v, bytes.TrimSpace(body))
	// 正常信封
	v, body, err = DecodeEnvelope(EncodeMessage(3, []byte{0xde}))
	if err != nil || v != 3 || !bytes.Equal(body, []byte{0xde}) {
		t.Fatalf("good envelope: v=%d body=%v err=%v", v, body, err)
	}
}

func classify(err error) string {
	if we, ok := err.(*Error); ok {
		return string(we.Kind)
	}
	return "unknown"
}
