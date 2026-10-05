package wire

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestLimitsDistinctFromCorruptAndVersion 验证超限错误能与数据损坏、
// 版本不兼容明确区分。
func TestLimitsDistinctFromCorruptAndVersion(t *testing.T) {
	good := []byte{0x01} // 合法但 1 字节的输入
	t.Logf("输入: 合法数据 %x", good)
	// 数据本身 1 字节，上限 1 应通过。
	d, err := NewLimitedDecoder(good, Limits{MaxBytes: 1})
	if err != nil {
		t.Fatalf("1 字节数据在上限 1 下应通过: %v", err)
	}
	if _, err := d.ReadVersion(); err != nil {
		t.Fatalf("读取版本失败: %v", err)
	}

	_, err = NewLimitedDecoder([]byte{0x01, 0x02}, Limits{MaxBytes: 1})
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("判定依据: 超 MaxBytes 应归类 ErrResourceLimit, 实际 %v", err)
	}
	if errors.Is(err, ErrCorrupt) || errors.Is(err, ErrIncompatibleVersion) {
		t.Fatalf("判定依据: 超限不应被归入损坏/版本错误, 实际 %v", err)
	}
	t.Logf("判定: 超 MaxBytes -> 错误归类: 资源超限 (%v)", err)
}

// TestFieldCountLimit 验证单条消息字段数上限。
func TestFieldCountLimit(t *testing.T) {
	// version=1，后面 3 个 varint 字段。
	e := NewEncoder()
	e.WriteVersion(1)
	for i := 1; i <= 3; i++ {
		e.WriteTag(i, TypeVarint)
		e.WriteUvarint(uint64(i))
	}
	data := e.Bytes()
	t.Logf("输入: 含 3 个字段的报文 %x", data)

	// 上限 3：全部读出。
	d, err := NewLimitedDecoder(data, Limits{MaxFields: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ReadVersion(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, _, err := d.ReadTag(); err != nil {
			t.Fatalf("第 %d 个字段应在上限内: %v", i+1, err)
		}
		if _, err := d.ReadUvarint(); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("判定: MaxFields=3 时 3 个字段全部可读")

	// 上限 2：读到第 3 个标签时被拒。
	d2, _ := NewLimitedDecoder(data, Limits{MaxFields: 2})
	_, _ = d2.ReadVersion()
	_, _, _ = d2.ReadTag()
	_, _ = d2.ReadUvarint()
	_, _, _ = d2.ReadTag()
	_, _ = d2.ReadUvarint()
	_, _, err = d2.ReadTag()
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("判定依据: 第 3 个字段应触发 ErrResourceLimit, 实际 %v", err)
	}
	t.Logf("判定: MaxFields=2 时第 3 个字段被拒 -> 资源超限 (%v)", err)
}

// TestDepthLimit 验证嵌套层数上限：手工构造嵌套 length-delimited 结构，
// 通过 Child 逐层下钻。
func TestDepthLimit(t *testing.T) {
	// inner raw: version=1
	inner := []byte{0x01}
	// outer: version=1, field1(bytes) = inner
	e := NewEncoder()
	e.WriteVersion(1)
	e.WriteTag(1, TypeBytes)
	e.WriteBytes(inner)
	outer := e.Bytes()
	t.Logf("输入: 两层嵌套报文 %x (外层 bytes 载荷 %x)", outer, inner)

	// MaxDepth=2：允许下钻一层。
	top, err := NewLimitedDecoder(outer, Limits{MaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = top.ReadVersion()
	_, wt, _ := top.ReadTag()
	raw, _ := top.ReadBytes()
	if wt != TypeBytes {
		t.Fatal("线上类型应为 bytes")
	}
	child, err := top.Child(raw)
	if err != nil {
		t.Fatalf("判定依据: MaxDepth=2 应允许第 2 层, 实际 %v", err)
	}
	if v, err := child.ReadVersion(); err != nil || v != 1 {
		t.Fatalf("第 2 层版本读取失败: v=%d err=%v", v, err)
	}
	t.Log("判定: MaxDepth=2 允许进入第 2 层")

	// MaxDepth=1：下钻即被拒。
	top2, _ := NewLimitedDecoder(outer, Limits{MaxDepth: 1})
	_, _ = top2.ReadVersion()
	_, _, _ = top2.ReadTag()
	raw2, _ := top2.ReadBytes()
	_, err = top2.Child(raw2)
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("判定依据: MaxDepth=1 时下钻应返回 ErrResourceLimit, 实际 %v", err)
	}
	t.Logf("判定: MaxDepth=1 拒绝进入第 2 层 -> 资源超限 (%v)", err)
}

// TestLimitsZeroMeansUnlimited 验证零值 Limits 保持既有行为：不设防。
func TestLimitsZeroMeansUnlimited(t *testing.T) {
	big := bytes.Repeat([]byte{0x7f}, 100000)
	d, err := NewLimitedDecoder(big, Limits{})
	if err != nil {
		t.Fatalf("零值上限不应拒绝任何输入: %v", err)
	}
	for !d.Done() {
		if _, err := d.ReadUvarint(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("判定: 零值 Limits 对 %d 字节输入不设防，行为与旧版一致", len(big))
}

// TestLimitsPerDecoderIsolated 验证上限状态按解码器独立，并发互不串扰。
func TestLimitsPerDecoderIsolated(t *testing.T) {
	const goroutines = 32
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			// 每个 goroutine 用不同的 MaxFields，读取共享的同一份数据：
			// v=1, f1(varint)=0, f2(varint)=0, f3(varint)=0
			data := []byte{0x01, 0x02, 0x00, 0x04, 0x00, 0x06, 0x00}
			limit := Limits{MaxFields: 1 + (g % 3)} // 1 或 2 或 3
			d, err := NewLimitedDecoder(data, limit)
			if err != nil {
				t.Errorf("goroutine %d 建解码器失败: %v", g, err)
				return
			}
			_, _ = d.ReadVersion()
			got := 0
			for !d.Done() {
				_, _, err := d.ReadTag()
				if err != nil {
					if !errors.Is(err, ErrResourceLimit) {
						t.Errorf("goroutine %d 非预期错误: %v", g, err)
					}
					break
				}
				_, _ = d.ReadUvarint()
				got++
			}
			want := 1 + (g % 3)
			if got != want {
				t.Errorf("goroutine %d (MaxFields=%d): 读到 %d 个字段，配置似乎串扰",
					g, limit.MaxFields, got)
			}
		}(g)
	}
	wg.Wait()
	t.Logf("判定: %d 个 goroutine 各自持有不同 MaxFields，结果互不串扰", goroutines)
}

// TestUnknownFieldRawPreservesNonCanonicalVarint 验证未知 varint 字段使用
// 非紧凑写法（带前导 0x80 续位字节）时，Raw 会逐字节还原。
func TestUnknownFieldRawPreservesNonCanonicalVarint(t *testing.T) {
	// 字段 7 (tag=(7<<1)|0 = 0x0e)，值 1 用非紧凑 3 字节写法 80 80 01。
	nonCanonical := []byte{0x0e, 0x80, 0x80, 0x01}
	t.Logf("输入: 非紧凑 varint 未知字段 %x（紧凑写法应为 %x）",
		nonCanonical, []byte{0x0e, 0x01})

	var us UnknownSet
	d := NewDecoder(nonCanonical)
	for !d.Done() {
		start := d.Offset()
		num, wt, err := d.ReadTag()
		if err != nil {
			t.Fatal(err)
		}
		if wt != TypeVarint {
			t.Fatalf("线上类型应为 varint, 实际 %d", wt)
		}
		if _, err := d.ReadRawVarint(); err != nil {
			t.Fatal(err)
		}
		us.Add(UnknownField{Number: num, Type: wt, Raw: d.RawField(start)})
	}
	out := NewEncoder()
	us.EncodeTo(out)
	if !bytes.Equal(out.Bytes(), nonCanonical) {
		t.Fatalf("判定依据: 非紧凑写法必须逐字节还原\n输入: %x\n输出: %x",
			nonCanonical, out.Bytes())
	}
	t.Logf("判定: 中转输出 %x 与上游输入逐字节一致（非紧凑 varint 未被规范化）", out.Bytes())

	// 对比：手动构造（只有 Data、没有 Raw）仍走紧凑重建路径，保持向后兼容。
	legacy := UnknownSet{Fields: []UnknownField{{Number: 7, Type: TypeVarint, Data: []byte{0x01}}}}
	e2 := NewEncoder()
	legacy.EncodeTo(e2)
	if fmt.Sprintf("%x", e2.Bytes()) != "0e01" {
		t.Fatalf("旧式 Data-only 构造应紧凑重建, 实际 %x", e2.Bytes())
	}
	t.Log("判定: 无 Raw 的旧式 UnknownField 仍按紧凑形式重建（向后兼容）")
}

// TestUnknownFieldRawPreservesNonCanonicalLength 验证未知 bytes 字段的长度
// 前缀使用非紧凑写法时同样逐字节还原。
func TestUnknownFieldRawPreservesNonCanonicalLength(t *testing.T) {
	// 字段 1 bytes (tag=(1<<1)|1 = 0x03)，长度 2 用非紧凑写法 82 00，载荷 "ok"。
	nonCanonical := []byte{0x03, 0x82, 0x00, 'o', 'k'}
	t.Logf("输入: 非紧凑长度前缀的未知 bytes 字段 %x", nonCanonical)

	var us UnknownSet
	d := NewDecoder(nonCanonical)
	for !d.Done() {
		start := d.Offset()
		num, wt, err := d.ReadTag()
		if err != nil {
			t.Fatal(err)
		}
		if wt != TypeBytes {
			t.Fatalf("线上类型应为 bytes, 实际 %d", wt)
		}
		if _, err := d.ReadBytes(); err != nil {
			t.Fatal(err)
		}
		us.Add(UnknownField{Number: num, Type: wt, Raw: d.RawField(start)})
	}
	out := NewEncoder()
	us.EncodeTo(out)
	if !bytes.Equal(out.Bytes(), nonCanonical) {
		t.Fatalf("判定依据: 非紧凑长度前缀必须逐字节还原\n输入: %x\n输出: %x",
			nonCanonical, out.Bytes())
	}
	t.Logf("判定: 中转输出 %x 与上游输入逐字节一致", out.Bytes())
}

// TestDefaultLimitsSane 验证默认上限是一组有限的非零值。
func TestDefaultLimitsSane(t *testing.T) {
	l := DefaultLimits()
	if l.MaxBytes <= 0 || l.MaxDepth <= 0 || l.MaxFields <= 0 {
		t.Fatalf("默认上限应全部为正数, 实际 %+v", l)
	}
	t.Logf("判定: 默认上限 %+v", l)
}
