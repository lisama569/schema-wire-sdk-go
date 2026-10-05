package wire

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// encodeMsg 手工构造一条报文：版本头 + n 个 varint 字段。
func encodeMsg(version, nFields int) []byte {
	e := NewEncoder()
	e.WriteVersion(version)
	for i := 1; i <= nFields; i++ {
		e.WriteTag(i, TypeVarint)
		e.WriteInt64(int64(i))
	}
	return e.Bytes()
}

// drainAll 逐字段读完整条报文（不关心字段含义）。
func drainAll(d *Decoder) error {
	if _, err := d.ReadVersion(); err != nil {
		return err
	}
	for !d.Done() {
		if _, _, err := d.ReadTag(); err != nil {
			return err
		}
		if _, err := d.ReadUvarint(); err != nil {
			return err
		}
	}
	return nil
}

// TestMaxBytesLimit 验证报文字节数超限被拒，且类别可区分。
func TestMaxBytesLimit(t *testing.T) {
	data := encodeMsg(1, 3)
	t.Logf("输入: %d 字节报文, 上限 MaxBytes=%d", len(data), len(data)-1)
	_, err := NewDecoderLimits(data, &Limits{MaxBytes: len(data) - 1})
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("判定依据: 应归类为 ErrLimitExceeded, 实际: %v", err)
	}
	if errors.Is(err, ErrCorrupt) || errors.Is(err, ErrIncompatibleVersion) {
		t.Fatalf("判定依据: 不应与数据损坏/版本不兼容混淆, 实际: %v", err)
	}
	t.Logf("判定: 超限拒绝, 错误归类为 ErrLimitExceeded -> %v", err)

	// 边界：恰好等于上限应放行。
	d, err := NewDecoderLimits(data, &Limits{MaxBytes: len(data)})
	if err != nil {
		t.Fatalf("等于上限应放行: %v", err)
	}
	if err := drainAll(d); err != nil {
		t.Fatalf("上限内解码应成功: %v", err)
	}
	t.Log("判定: 恰好等于上限的报文正常解码")
}

// TestMaxFieldsLimit 验证单条消息字段数超限被拒。
func TestMaxFieldsLimit(t *testing.T) {
	data := encodeMsg(1, 5)
	t.Logf("输入: 含 5 个字段的报文, 上限 MaxFields=3")
	d, err := NewDecoderLimits(data, &Limits{MaxFields: 3})
	if err != nil {
		t.Fatal(err)
	}
	err = drainAll(d)
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("判定依据: 应归类为 ErrLimitExceeded, 实际: %v", err)
	}
	if errors.Is(err, ErrCorrupt) {
		t.Fatalf("不应归类为数据损坏: %v", err)
	}
	t.Logf("判定: 第 4 个字段触发拒绝 -> %v", err)

	// 上限内行为与无限制完全一致。
	d1 := NewDecoder(data)
	d2, _ := NewDecoderLimits(data, &Limits{MaxFields: 5})
	if err := drainAll(d1); err != nil {
		t.Fatal(err)
	}
	if err := drainAll(d2); err != nil {
		t.Fatalf("上限内行为应与无限制一致: %v", err)
	}
	t.Log("判定: 上限内解码行为与无限制完全一致")
}

// TestMaxDepthLimit 验证嵌套层数超限被拒。
func TestMaxDepthLimit(t *testing.T) {
	inner := encodeMsg(1, 1)
	var mid []byte
	{
		e := NewEncoder()
		e.WriteVersion(1)
		e.WriteTag(1, TypeBytes)
		e.WriteBytes(inner)
		mid = e.Bytes()
	}
	t.Logf("输入: 两层嵌套报文, 上限 MaxDepth=2")
	top, err := NewDecoderLimits(mid, &Limits{MaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := top.ReadVersion(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := top.ReadTag(); err != nil {
		t.Fatal(err)
	}
	raw, err := top.ReadBytes()
	if err != nil {
		t.Fatal(err)
	}
	// 第 2 层：放行。
	sub, err := top.Derive(raw)
	if err != nil {
		t.Fatalf("第 2 层应放行: %v", err)
	}
	// 第 3 层：拒绝。
	if _, err := sub.Derive(inner); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("判定依据: 第 3 层应归类为 ErrLimitExceeded, 实际: %v", err)
	} else {
		t.Logf("判定: 嵌套层数超限拒绝 -> %v", err)
	}
}

// TestLimitsConcurrentIsolation 验证并发解码时各报文的上限配置互不干扰。
func TestLimitsConcurrentIsolation(t *testing.T) {
	small := encodeMsg(1, 2)
	big := encodeMsg(1, 50)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() { // 严格上限 + 小报文：必须成功
			defer wg.Done()
			d, err := NewDecoderLimits(small, &Limits{MaxFields: 2})
			if err != nil {
				errs <- err
				return
			}
			if err := drainAll(d); err != nil {
				errs <- fmt.Errorf("严格上限小报文应成功: %w", err)
			}
		}()
		go func() { // 严格上限 + 大报文：必须被限
			defer wg.Done()
			d, err := NewDecoderLimits(big, &Limits{MaxFields: 2})
			if err != nil {
				errs <- err
				return
			}
			if err := drainAll(d); !errors.Is(err, ErrLimitExceeded) {
				errs <- fmt.Errorf("大报文应被字段数上限拦截, 实际: %v", err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	t.Log("判定: 64 路并发解码，各自上限配置独立生效、互不串扰")
}

// TestLimitsCopiedAtConstruction 验证创建后修改原 Limits 不影响已建解码器。
func TestLimitsCopiedAtConstruction(t *testing.T) {
	data := encodeMsg(1, 3)
	lim := &Limits{MaxFields: 10}
	d, err := NewDecoderLimits(data, lim)
	if err != nil {
		t.Fatal(err)
	}
	lim.MaxFields = 1 // 创建后收紧配置
	if err := drainAll(d); err != nil {
		t.Fatalf("判定依据: 已建解码器不应受后续配置修改影响: %v", err)
	}
	t.Log("判定: 上限配置在建解码器时拷贝，外部修改不串扰")
}
