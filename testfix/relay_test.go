package testfix_test

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"github.com/lisama569/schema-wire-sdk-go/testfix/newshop"
	"github.com/lisama569/schema-wire-sdk-go/testfix/oldshop"
	"github.com/lisama569/schema-wire-sdk-go/testfix/relay"
	"github.com/lisama569/schema-wire-sdk-go/wire"
)

// TestStrictDecodeOutOfVersionField 验证严格版本解码：数据自带版本下无效的
// 字段一律按未知字段处理，重新编码逐字节还原。
func TestStrictDecodeOutOfVersionField(t *testing.T) {
	// 手工构造 v1 报文，但夹带 v2 才引入的字段 3 (email)。
	e := wire.NewEncoder()
	e.WriteVersion(1)
	e.WriteTag(1, wire.TypeBytes)
	e.WriteString("alice")
	e.WriteTag(2, wire.TypeVarint)
	e.WriteInt64(30)
	e.WriteTag(4, wire.TypeVarint) // active=false（已知零值字段，重编码时会被补写）
	e.WriteBool(false)
	e.WriteTag(3, wire.TypeBytes)
	e.WriteString("a@b.c")
	data := e.Bytes()
	t.Logf("输入: v1 报文 %x（夹带 v2 字段 3=email）", data)

	var strict newshop.User
	if err := strict.DecodeStrict(data); err != nil {
		t.Fatalf("严格解码失败: %v", err)
	}
	if strict.Email != "" {
		t.Fatalf("判定依据: v1 下 email 不应被当作已知字段, 实际 %q", strict.Email)
	}
	if len(strict.UnknownFields.Fields) != 1 || strict.UnknownFields.Fields[0].Number != 3 {
		t.Fatalf("判定依据: 字段 3 应进入未知字段集, 实际 %+v", strict.UnknownFields.Fields)
	}
	t.Logf("判定: 字段 3 在 v1 下无效 -> 按未知字段保留: %+v", strict.UnknownFields.Fields)

	re, err := strict.EncodeVersion(1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(re, data) {
		t.Fatalf("判定依据: 严格解码后重编码应逐字节一致\n原始: %x\n重编: %x", data, re)
	}
	t.Logf("判定: 重编码 %x 与输入逐字节一致", re)

	// 对照：普通 Decode 保持既有宽松行为（字段 3 仍按已知字段解析）。
	var loose newshop.User
	if err := loose.Decode(data); err != nil {
		t.Fatal(err)
	}
	if loose.Email != "a@b.c" {
		t.Fatalf("普通 Decode 行为不应改变, 实际 email=%q", loose.Email)
	}
	t.Log("判定: 普通 Decode 的既有宽松行为不受影响")
}

// TestUnknownFieldByteFidelity 验证未知字段的非常规编码（非紧凑 varint）
// 在中转后逐字节还原。
func TestUnknownFieldByteFidelity(t *testing.T) {
	e := wire.NewEncoder()
	e.WriteVersion(2)
	e.WriteTag(1, wire.TypeBytes)
	e.WriteString("bob") // 已知字段 name
	e.WriteTag(2, wire.TypeVarint)
	e.WriteInt64(30) // 已知字段 age
	// 未知字段 7：bytes，长度前缀用非紧凑 varint (5 -> 0x85 0x00)
	e.WriteTag(7, wire.TypeBytes)
	e.WriteRaw([]byte{0x85, 0x00})
	e.WriteRaw([]byte("hello"))
	// 未知字段 8：varint，值用非紧凑写法 (1 -> 0x81 0x00)
	e.WriteTag(8, wire.TypeVarint)
	e.WriteRaw([]byte{0x81, 0x00})
	// 未知字段 9：标签本身用非紧凑写法 (18 -> 0x92 0x00)
	e.WriteRaw([]byte{0x92, 0x00})
	e.WriteUvarint(42)
	data := e.Bytes()
	t.Logf("输入: 含三处非紧凑编码的报文 %x", data)

	var old oldshop.User // 旧运行时只认识字段 1、2
	if err := old.DecodeStrict(data); err != nil {
		t.Fatalf("严格解码失败: %v", err)
	}
	if len(old.UnknownFields.Fields) != 3 {
		t.Fatalf("判定依据: 应有 3 个未知字段, 实际 %d", len(old.UnknownFields.Fields))
	}
	t.Logf("判定: 未知字段 %d 个被保留", len(old.UnknownFields.Fields))

	re, err := old.EncodeVersion(2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(re, data) {
		t.Fatalf("判定依据: 中转后应逐字节一致\n原始: %x\n重编: %x", data, re)
	}
	t.Logf("判定: 非紧凑写法（长度前缀/值/标签）经中转逐字节还原: %x", re)
}

// TestGatewayRelayStrict 模拟网关中转：旧运行时严格解码新生产者数据，
// 转发给新消费者，字节与字段都不丢失。
func TestGatewayRelayStrict(t *testing.T) {
	src := newshop.User{Name: "carol", Age: 41, Email: "c@x.io", Active: true}
	upstream, err := src.EncodeVersion(2)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: 新生产者 v2 报文 %x", upstream)

	// 网关：旧运行时严格解码。
	var gw oldshop.User
	if err := gw.DecodeStrict(upstream); err != nil {
		t.Fatalf("网关严格解码失败: %v", err)
	}
	t.Logf("判定: 网关保留未知字段 %d 个: %+v", len(gw.UnknownFields.Fields), gw.UnknownFields.Fields)

	// 网关：原样转发。
	downstream, err := gw.EncodeVersion(2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downstream, upstream) {
		t.Fatalf("判定依据: 网关转发后字节不应改变\n上游: %x\n下游: %x", upstream, downstream)
	}
	t.Log("判定: 网关进出报文逐字节一致")

	// 新消费者：字段完好。
	var dst newshop.User
	if err := dst.Decode(downstream); err != nil {
		t.Fatal(err)
	}
	if dst.Email != "c@x.io" || !dst.Active || dst.Name != "carol" {
		t.Fatalf("中转后字段丢失: %+v", dst)
	}
	t.Logf("判定: 下游消费者读到完整字段 email=%q active=%v", dst.Email, dst.Active)
}

// TestGeneratedDecodeLimits 验证生成代码的资源上限解码。
func TestGeneratedDecodeLimits(t *testing.T) {
	u := newshop.User{Name: "dave", Age: 9, Email: "d@x.io", Active: true}
	data, err := u.EncodeVersion(2) // 4 个字段
	if err != nil {
		t.Fatal(err)
	}

	// 字节数超限。
	var v newshop.User
	err = v.DecodeLimits(data, &wire.Limits{MaxBytes: len(data) - 1})
	if !errors.Is(err, wire.ErrLimitExceeded) {
		t.Fatalf("判定依据: 字节超限应归类为 ErrLimitExceeded, 实际: %v", err)
	}
	t.Logf("判定: MaxBytes=%d 拒绝 %d 字节报文 -> %v", len(data)-1, len(data), err)

	// 字段数超限。
	err = v.DecodeLimits(data, &wire.Limits{MaxFields: 3})
	if !errors.Is(err, wire.ErrLimitExceeded) {
		t.Fatalf("判定依据: 字段数超限应归类为 ErrLimitExceeded, 实际: %v", err)
	}
	if errors.Is(err, wire.ErrCorrupt) || errors.Is(err, wire.ErrIncompatibleVersion) {
		t.Fatalf("不应与损坏/版本不兼容混淆: %v", err)
	}
	t.Logf("判定: MaxFields=3 拒绝 4 字段报文 -> %v", err)

	// 上限内行为与 Decode 完全一致。
	var limited, plain newshop.User
	if err := limited.DecodeLimits(data, &wire.Limits{MaxBytes: 1 << 20, MaxFields: 4, MaxDepth: 4}); err != nil {
		t.Fatal(err)
	}
	if err := plain.Decode(data); err != nil {
		t.Fatal(err)
	}
	if limited.Name != plain.Name || limited.Age != plain.Age ||
		limited.Email != plain.Email || limited.Active != plain.Active {
		t.Fatalf("判定依据: 上限内解码结果应与 Decode 一致\n%+v\n%+v", limited, plain)
	}
	t.Log("判定: 上限内解码结果与普通 Decode 完全一致")
}

// TestGeneratedDecodeDepthLimit 验证嵌套消息的层数上限（端到端）。
func TestGeneratedDecodeDepthLimit(t *testing.T) {
	o := relay.Outer{Inner: &relay.Inner{Id: 7, Label: "x"}, Tag: "t", Level: 3}
	data, err := o.EncodeVersion(2)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: 两层嵌套报文 %x", data)

	var v relay.Outer
	err = v.DecodeLimits(data, &wire.Limits{MaxDepth: 1})
	if !errors.Is(err, wire.ErrLimitExceeded) {
		t.Fatalf("判定依据: 层数超限应归类为 ErrLimitExceeded, 实际: %v", err)
	}
	t.Logf("判定: MaxDepth=1 拒绝两层嵌套 -> %v", err)

	var ok relay.Outer
	if err := ok.DecodeLimits(data, &wire.Limits{MaxDepth: 2}); err != nil {
		t.Fatalf("MaxDepth=2 应放行: %v", err)
	}
	if ok.Inner == nil || ok.Inner.Id != 7 || ok.Inner.Label != "x" {
		t.Fatalf("上限内嵌套解码结果不符: %+v", ok)
	}
	t.Log("判定: MaxDepth=2 正常解码嵌套消息")
}

// TestGeneratedDecodeLimitsConcurrent 验证生成代码并发解码时上限配置互不串扰。
func TestGeneratedDecodeLimitsConcurrent(t *testing.T) {
	small := newshop.User{Name: "s"}
	smallData, err := small.EncodeVersion(1)
	if err != nil {
		t.Fatal(err)
	}
	big := newshop.User{Name: "b", Age: 1, Email: "b@x.io", Active: true}
	bigData, err := big.EncodeVersion(2)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			var v newshop.User
			if err := v.DecodeLimits(smallData, &wire.Limits{MaxFields: 3}); err != nil {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			var v newshop.User
			if err := v.DecodeLimits(bigData, &wire.Limits{MaxFields: 3}); !errors.Is(err, wire.ErrLimitExceeded) {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	t.Log("判定: 64 路并发解码，小报文放行、大报文被限，配置互不串扰")
}
