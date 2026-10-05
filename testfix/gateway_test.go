package testfix_test

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"github.com/lisama569/schema-wire-sdk-go/testfix/newshop"
	"github.com/lisama569/schema-wire-sdk-go/testfix/oldshop"
	"github.com/lisama569/schema-wire-sdk-go/wire"
)

// TestGatewayStrictVersionByteFidelity 网关核心场景：解码时只把数据自带版本
// 下确实有效的字段当已知字段，其余全部按未知字段处理；重新编码必须与上游
// 报文逐字节一致，包括交错的字段顺序与非紧凑 varint/长度/版本头写法。
func TestGatewayStrictVersionByteFidelity(t *testing.T) {
	// 手工构造一条 v1 报文，字段顺序刻意交错：1(name), 3(email), 4(active)。
	// 在 v=1 下字段 3(since 2) 无效，必须按未知字段对待，即使新 schema 认识编号 3。
	e := wire.NewEncoder()
	e.WriteRaw([]byte{0x81, 0x00}) // 版本头 1 的非紧凑写法（81 00）
	e.WriteTag(1, wire.TypeBytes)
	e.WriteString("alice")
	e.WriteTag(3, wire.TypeBytes)
	e.WriteRaw([]byte{0x86, 0x00}) // 长度 6 的非紧凑写法
	e.WriteRaw([]byte("a@b.io"))
	e.WriteTag(4, wire.TypeVarint)
	e.WriteBool(true)
	data := e.Bytes()
	t.Logf("输入: v1 上游报文 %x（非紧凑版本头；字段顺序 1,3(该版本无效),4；非紧凑长度）", data)

	var mid newshop.User
	if err := mid.Decode(data); err != nil {
		t.Fatalf("网关切入解码失败: %v", err)
	}
	if mid.Name != "alice" || !mid.Active {
		t.Fatalf("判定依据: v1 有效字段应被解析, 实际 name=%q active=%v", mid.Name, mid.Active)
	}
	if mid.Email != "" {
		t.Fatalf("判定依据: 严格版本处理：字段3 在数据自带 v1 下无效，必须按未知字段对待，Email 必须为空, 实际 %q", mid.Email)
	}
	if len(mid.UnknownFields.Fields) != 1 || mid.UnknownFields.Fields[0].Number != 3 {
		t.Fatalf("判定依据: 编号3 应作为唯一未知字段保留, 实际 %+v", mid.UnknownFields.Fields)
	}
	t.Logf("判定: name/active 按已知解析，email(字段3) 在 v1 下按未知字段保留（严格版本）")

	// 网关原样转发：重新编码必须与上游完全一致。
	out, err := mid.EncodeVersion(1)
	if err != nil {
		t.Fatalf("中转重编码失败: %v", err)
	}
	if !bytes.Equal(out, data) {
		t.Fatalf("判定依据: 中转必须逐字节保真\n上游: %x\n下游: %x", data, out)
	}
	t.Logf("判定: 下游收到 %x，与上游发出的逐字节一致（顺序/非紧凑写法全部保留）", out)
}

// TestStrictVersionInactiveKnownNumberTreatedUnknown 再验一个方向：
// v2 数据携带 v3 才引入的字段 5(avatar)，编号虽被 schema 认识，但在 v2 下
// 无效，必须未知化并逐字节透传。
func TestStrictVersionInactiveKnownNumberTreatedUnknown(t *testing.T) {
	e := wire.NewEncoder()
	e.WriteVersion(2)
	e.WriteTag(2, wire.TypeVarint) // age（v2 仍有效）
	e.WriteInt64(25)
	e.WriteTag(5, wire.TypeBytes) // avatar：v2 下无效
	e.WriteBytes([]byte{0xde, 0xad})
	e.WriteTag(3, wire.TypeBytes) // email（v2 有效）
	e.WriteString("bob@x.io")
	data := e.Bytes()
	t.Logf("输入: v2 报文，字段顺序 2,5(该版本无效),3: %x", data)

	var mid newshop.User
	if err := mid.Decode(data); err != nil {
		t.Fatal(err)
	}
	if mid.Age != 25 || mid.Email != "bob@x.io" {
		t.Fatalf("v2 有效字段解析错误: %+v", mid)
	}
	if mid.Avatar != nil {
		t.Fatalf("判定依据: 字段5 在 v2 下必须未知化，Avatar 不得被赋值, 实际 %x", mid.Avatar)
	}
	out, err := mid.EncodeVersion(2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, data) {
		t.Fatalf("判定依据: v2 中转也必须逐字节保真\n上游: %x\n下游: %x", data, out)
	}
	t.Logf("判定: 字段5 未知化、字段顺序保留，下游报文 %x 与上游一致", out)
}

// TestGatewayOldProducerNewConsumer 老生产者(oldshop)发出 v1，经网关(newshop
// 视角)转发，新消费者(newshop)收到的字节与生产者发出的一致；已知字段语义
// 也不受影响。
func TestGatewayOldProducerNewConsumer(t *testing.T) {
	prod := oldshop.User{Name: "carol", Age: 41}
	data, err := prod.EncodeVersion(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: 旧生产者 v1 报文 %x", data)

	var gw newshop.User
	if err := gw.Decode(data); err != nil {
		t.Fatalf("网关解码失败: %v", err)
	}
	out, err := gw.EncodeVersion(1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, data) {
		t.Fatalf("判定依据: 网关转发必须字节一致\n上游: %x\n下游: %x", data, out)
	}
	var consumer newshop.User
	if err := consumer.Decode(out); err != nil {
		t.Fatalf("新消费者解码失败: %v", err)
	}
	if consumer.Name != "carol" || consumer.Age != 41 {
		t.Fatalf("新消费者字段不符: %+v", consumer)
	}
	t.Logf("判定: 新消费者得到 name=%q age=%d，且报文逐字节未变", consumer.Name, consumer.Age)
}

// TestResourceLimitsEndToEnd 端到端验证三类上限，且错误类别与损坏/版本不兼容可分。
func TestResourceLimitsEndToEnd(t *testing.T) {
	env := &newshop.Envelope{User: &newshop.User{Name: "dave"}, Tag: "t"}
	data, err := env.EncodeVersion(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: 嵌套 Envelope 报文 %d 字节 %x", len(data), data)

	t.Run("嵌套层数超限", func(t *testing.T) {
		var got newshop.Envelope
		err := got.DecodeWith(data, wire.Limits{MaxDepth: 1})
		if !errors.Is(err, wire.ErrResourceLimit) {
			t.Fatalf("判定依据: MaxDepth=1 应拒绝嵌套, 实际: %v", err)
		}
		if errors.Is(err, wire.ErrCorrupt) || errors.Is(err, wire.ErrIncompatibleVersion) {
			t.Fatalf("判定依据: 超限不应归入损坏/版本类: %v", err)
		}
		t.Logf("判定依据: 错误归类=资源超限 (%v)", err)
	})
	t.Run("嵌套层数放行", func(t *testing.T) {
		var got newshop.Envelope
		if err := got.DecodeWith(data, wire.Limits{MaxDepth: 2}); err != nil {
			t.Fatalf("MaxDepth=2 应放行两层: %v", err)
		}
		if got.User == nil || got.User.Name != "dave" || got.Tag != "t" {
			t.Fatalf("嵌套内容解析错误: %+v", got)
		}
		t.Log("判定: MaxDepth=2 放行，嵌套字段正常解析；上限以内行为与无上限一致")
	})
	t.Run("字节数超限", func(t *testing.T) {
		var got newshop.Envelope
		err := got.DecodeWith(data, wire.Limits{MaxBytes: len(data) - 1})
		if !errors.Is(err, wire.ErrResourceLimit) {
			t.Fatalf("判定依据: 超 MaxBytes 应归类资源超限, 实际: %v", err)
		}
		t.Logf("判定依据: %d 字节 > 上限 %d -> 资源超限", len(data), len(data)-1)
	})
	t.Run("字段数超限", func(t *testing.T) {
		u := newshop.User{Name: "e", Age: 1, Email: "e@x.io", Active: true}
		d, err := u.EncodeVersion(2) // 4 个字段
		if err != nil {
			t.Fatal(err)
		}
		var got newshop.User
		err = got.DecodeWith(d, wire.Limits{MaxFields: 2})
		if !errors.Is(err, wire.ErrResourceLimit) {
			t.Fatalf("判定依据: 超 MaxFields 应归类资源超限, 实际: %v", err)
		}
		t.Logf("判定依据: 4 个字段 > MaxFields=2 -> 资源超限 (%v)", err)
	})
	t.Run("损坏数据不归为超限", func(t *testing.T) {
		var got newshop.Envelope
		err := got.DecodeWith(append(append([]byte{}, data...), 0x80), wire.DefaultLimits())
		if !errors.Is(err, wire.ErrCorrupt) {
			t.Fatalf("判定依据: 截断 varint 应归类数据损坏, 实际: %v", err)
		}
		if errors.Is(err, wire.ErrResourceLimit) {
			t.Fatalf("判定依据: 损坏数据不应触发资源超限, 实际: %v", err)
		}
		t.Logf("判定依据: 即使配置了上限，损坏仍归类 ErrCorrupt (%v)", err)
	})
}

// TestNestedUnknownFieldsByteFidelity 嵌套消息中：内层未知字段与外层未知字段
// 都必须逐字节透传（含非紧凑写法）。
func TestNestedUnknownFieldsByteFidelity(t *testing.T) {
	// 内层 User v1：字段1 + 未知字段9（非紧凑 varint 值 1）。
	inner := wire.NewEncoder()
	inner.WriteVersion(1)
	inner.WriteTag(1, wire.TypeBytes)
	inner.WriteString("frank")
	inner.WriteTag(9, wire.TypeVarint)
	inner.WriteRaw([]byte{0x80, 0x80, 0x01}) // 非紧凑的 1
	innerBytes := inner.Bytes()

	// 外层 Envelope v1：字段1=内层，字段2=tag，未知字段8(bytes)。
	e := wire.NewEncoder()
	e.WriteVersion(1)
	e.WriteTag(1, wire.TypeBytes)
	e.WriteBytes(innerBytes)
	e.WriteTag(8, wire.TypeBytes)
	e.WriteRaw([]byte{0x83, 0x00}) // 长度 3 的非紧凑写法
	e.WriteRaw([]byte("xyz"))
	e.WriteTag(2, wire.TypeBytes)
	e.WriteString("gw")
	data := e.Bytes()
	t.Logf("输入: 双层报文（内层未知字段9非紧凑，外层未知字段8非紧凑）: %x", data)

	var env newshop.Envelope
	if err := env.DecodeWith(data, wire.Limits{MaxDepth: 10}); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if env.User.Name != "frank" {
		t.Fatalf("内层已知字段错误: %q", env.User.Name)
	}
	if len(env.User.UnknownFields.Fields) != 1 || env.User.UnknownFields.Fields[0].Number != 9 {
		t.Fatalf("内层未知字段应保留, 实际 %+v", env.User.UnknownFields.Fields)
	}
	if len(env.UnknownFields.Fields) != 1 || env.UnknownFields.Fields[0].Number != 8 {
		t.Fatalf("外层未知字段应保留, 实际 %+v", env.UnknownFields.Fields)
	}
	out, err := env.EncodeVersion(1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, data) {
		t.Fatalf("判定依据: 双层报文必须逐字节保真\n上游: %x\n下游: %x", data, out)
	}
	t.Logf("判定: 下游 %x 与上游逐字节一致（内外层未知字段与非紧凑写法全保留）", out)
}

// TestConcurrentDecodeLimitsIsolation 端到端：同一报文被多 goroutine 以不同
// 上限并发解码，被拒/放行各自独立，不串配置。
func TestConcurrentDecodeLimitsIsolation(t *testing.T) {
	u := newshop.User{Name: "g", Age: 2, Email: "g@x.io", Active: true}
	data, err := u.EncodeVersion(2) // 4 字段
	if err != nil {
		t.Fatal(err)
	}
	const n = 24
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var got newshop.User
			err := got.DecodeWith(data, wire.Limits{MaxFields: 1 + i%5}) // 1..5
			if i%5+1 >= 4 {
				if err != nil {
					t.Errorf("goroutine %d: MaxFields=%d 应放行, 实际 %v", i, 1+i%5, err)
				}
			} else {
				if !errors.Is(err, wire.ErrResourceLimit) {
					t.Errorf("goroutine %d: MaxFields=%d 应被资源上限拒绝, 实际 %v",
						i, 1+i%5, err)
				}
			}
		}(i)
	}
	wg.Wait()
	t.Logf("判定: %d 个 goroutine 以各自不同 MaxFields 并发解码，放行/拒绝互不串扰", n)
}

// TestWithinLimitsBehaviorUnchanged 上限以内的正常数据，解码语义与无上限完全一致。
func TestWithinLimitsBehaviorUnchanged(t *testing.T) {
	src := newshop.User{Name: "h", Age: 9, Email: "h@x.io", Active: true}
	d2, err := src.EncodeVersion(2)
	if err != nil {
		t.Fatal(err)
	}
	var a, b newshop.User
	if err := a.Decode(d2); err != nil {
		t.Fatal(err)
	}
	if err := b.DecodeWith(d2, wire.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if a.Name != b.Name || a.Age != b.Age || a.Email != b.Email || a.Active != b.Active {
		t.Fatalf("上限以内行为不一致: %+v vs %+v", a, b)
	}
	if len(b.UnknownFields.Fields) != len(a.UnknownFields.Fields) {
		t.Fatal("两种解码路径的未知字段保留数量应一致")
	}
	out, err := b.EncodeVersion(2)
	if err != nil || !bytes.Equal(out, d2) {
		t.Fatalf("上限内中转应字节一致: err=%v\n%x\n%x", err, d2, out)
	}
	t.Log("判定: 默认上限内正常数据的解析与中转保真均与无上限一致")
}

// TestInProcessRoundTripIncludingNested 程序内构造（非解码得到）的消息，
// 含嵌套与 nil 嵌套字段，走 fieldOrder 为空的规范编码分支，往返一致。
func TestInProcessRoundTripIncludingNested(t *testing.T) {
	cases := []*newshop.Envelope{
		{User: &newshop.User{Name: "i", Age: 3, Active: true}, Tag: "a"}, // Envelope 仅 v1：内层也按 v1，email(since 2) 不应填
		{User: nil, Tag: "no-user"},                                      // nil 嵌套字段不应被写出，也不应 panic
	}
	for i, src := range cases {
		data, err := src.EncodeVersion(1)
		if err != nil {
			t.Fatalf("case %d 编码失败: %v", i, err)
		}
		var got newshop.Envelope
		if err := got.Decode(data); err != nil {
			t.Fatalf("case %d 解码失败: %v", i, err)
		}
		if got.Tag != src.Tag {
			t.Fatalf("case %d tag 不符: %q", i, got.Tag)
		}
		if src.User == nil {
			if got.User != nil {
				t.Fatalf("case %d nil 嵌套字段不应出现", i)
			}
			t.Log("判定: nil 嵌套字段编码时被省略，往返正常")
			continue
		}
		if got.User.Name != src.User.Name || got.User.Active != src.User.Active {
			t.Fatalf("case %d 嵌套内容不符: %+v", i, got.User)
		}
		// 再次编码应字节一致（编码是确定性的）。
		data2, err := got.EncodeVersion(1)
		if err != nil || !bytes.Equal(data2, data) {
			t.Fatalf("case %d 重编码不一致: %v\n%x\n%x", i, err, data, data2)
		}
		t.Logf("判定: case %d 程序内构造消息（含嵌套）往返一致 %x", i, data)
	}
}
