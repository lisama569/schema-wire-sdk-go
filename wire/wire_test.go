package wire

import (
	"errors"
	"testing"
)

// TestRoundTrip 验证基本编解码往返。
func TestRoundTrip(t *testing.T) {
	e := NewEncoder()
	e.WriteVersion(2)
	e.WriteTag(1, TypeBytes)
	e.WriteString("hello")
	e.WriteTag(2, TypeVarint)
	e.WriteInt64(-42)
	e.WriteTag(3, TypeVarint)
	e.WriteBool(true)
	data := e.Bytes()
	t.Logf("输入: 编码后数据 %d 字节: %x", len(data), data)

	d := NewDecoder(data)
	v, err := d.ReadVersion()
	if err != nil || v != 2 {
		t.Fatalf("版本头不符: v=%d err=%v", v, err)
	}
	if err := CheckVersion(v, 1, 3); err != nil {
		t.Fatalf("版本检查应通过: %v", err)
	}
	num, wt, _ := d.ReadTag()
	if num != 1 || wt != TypeBytes {
		t.Fatalf("标签不符: num=%d wt=%d", num, wt)
	}
	s, _ := d.ReadString()
	if s != "hello" {
		t.Fatalf("字符串不符: %q", s)
	}
	d.ReadTag()
	n, _ := d.ReadInt64()
	if n != -42 {
		t.Fatalf("整数不符: %d", n)
	}
	d.ReadTag()
	b, _ := d.ReadBool()
	if !b {
		t.Fatal("布尔不符")
	}
	if !d.Done() {
		t.Fatal("数据应消费完毕")
	}
	t.Log("判定: 往返一致")
}

// TestCorruptData 验证截断/越界/非法标签均归类为 ErrCorrupt。
func TestCorruptData(t *testing.T) {
	// 构造一条完整数据：version=1, field1=string "abc"
	e := NewEncoder()
	e.WriteVersion(1)
	e.WriteTag(1, TypeBytes)
	e.WriteString("abc")
	full := e.Bytes()

	cases := []struct {
		name string
		data []byte
		op   func(d *Decoder) error
	}{
		{"空数据读版本", nil, func(d *Decoder) error { _, err := d.ReadVersion(); return err }},
		{"版本头截断", []byte{0x80}, func(d *Decoder) error { _, err := d.ReadVersion(); return err }},
		{"标签截断", full[:1], func(d *Decoder) error {
			d.ReadVersion()
			_, _, err := d.ReadTag()
			return err
		}},
		{"字符串载荷截断", full[:len(full)-1], func(d *Decoder) error {
			d.ReadVersion()
			d.ReadTag()
			_, err := d.ReadString()
			return err
		}},
		{"长度越界", []byte{0x01, 0x03, 0x05, 'a'}, func(d *Decoder) error {
			d.ReadVersion()
			d.ReadTag()
			_, err := d.ReadBytes()
			return err
		}},
		{"非法字段编号0", []byte{0x01, 0x00}, func(d *Decoder) error {
			d.ReadVersion()
			_, _, err := d.ReadTag()
			return err
		}},
		{"varint溢出", append([]byte{0x01, 0x02}, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}...),
			func(d *Decoder) error {
				d.ReadVersion()
				d.ReadTag()
				_, err := d.ReadInt64()
				return err
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Logf("输入数据: %x", c.data)
			err := c.op(NewDecoder(c.data))
			if err == nil {
				t.Fatal("判定依据: 损坏数据应报错, 实际成功")
			}
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("判定依据: 应归类为 ErrCorrupt, 实际: %v", err)
			}
			if errors.Is(err, ErrIncompatibleVersion) {
				t.Fatalf("判定依据: 不应归类为 ErrIncompatibleVersion, 实际: %v", err)
			}
			t.Logf("判定依据: errors.Is(err, ErrCorrupt)=true -> 错误归类: 数据损坏 (%v)", err)
		})
	}
}

// TestIncompatibleVersion 验证版本不兼容归类为 ErrIncompatibleVersion，
// 且与 ErrCorrupt 可区分。
func TestIncompatibleVersion(t *testing.T) {
	t.Log("输入: 数据版本 5, 支持区间 [1, 3]")
	err := CheckVersion(5, 1, 3)
	if !errors.Is(err, ErrIncompatibleVersion) {
		t.Fatalf("判定依据: 应归类为 ErrIncompatibleVersion, 实际: %v", err)
	}
	if errors.Is(err, ErrCorrupt) {
		t.Fatalf("判定依据: 不应归类为 ErrCorrupt, 实际: %v", err)
	}
	t.Logf("判定依据: errors.Is(err, ErrIncompatibleVersion)=true 且 ErrCorrupt=false -> 错误归类: 版本不兼容 (%v)", err)

	if err := CheckVersion(2, 1, 3); err != nil {
		t.Fatalf("区间内版本应通过: %v", err)
	}
}

// TestUnknownFieldPreserved 验证未知字段经 UnknownSet 原样保留。
func TestUnknownFieldPreserved(t *testing.T) {
	e := NewEncoder()
	e.WriteTag(7, TypeBytes)
	e.WriteString("mystery")
	e.WriteTag(9, TypeVarint)
	e.WriteInt64(12345)
	data := e.Bytes()
	t.Logf("输入数据: %x", data)

	// 模拟一个不认识字段 7/9 的解码方
	var us UnknownSet
	d := NewDecoder(data)
	for !d.Done() {
		num, wt, err := d.ReadTag()
		if err != nil {
			t.Fatal(err)
		}
		switch wt {
		case TypeVarint:
			raw, err := d.ReadRawVarint()
			if err != nil {
				t.Fatal(err)
			}
			us.Add(UnknownField{Number: num, Type: wt, Data: raw})
		case TypeBytes:
			raw, err := d.ReadBytes()
			if err != nil {
				t.Fatal(err)
			}
			us.Add(UnknownField{Number: num, Type: wt, Data: raw})
		}
	}
	if len(us.Fields) != 2 {
		t.Fatalf("应保留 2 个未知字段, 实际 %d", len(us.Fields))
	}

	out := NewEncoder()
	us.EncodeTo(out)
	if string(out.Bytes()) != string(data) {
		t.Fatalf("判定依据: 未知字段重新编码应逐字节一致\n原始: %x\n重编: %x", data, out.Bytes())
	}
	t.Logf("判定依据: 重编码 %x 与原始输入逐字节一致 -> 未知字段原样保留", out.Bytes())
}
