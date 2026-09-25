// Package testfix_test 使用 schemagen 生成的两套版本化产物
// （oldshop: 版本 1..2；newshop: 版本 1..3）验证协议演进场景。
//
// 重新生成夹具：
//
//	go run ./cmd/schemagen -schema testfix/oldshop.idl -out testfix/oldshop
//	go run ./cmd/schemagen -schema testfix/newshop.idl -out testfix/newshop
package testfix_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/lisama569/schema-wire-sdk-go/testfix/newshop"
	"github.com/lisama569/schema-wire-sdk-go/testfix/oldshop"
	"github.com/lisama569/schema-wire-sdk-go/wire"
)

// TestOldDataDecodedByNew 验证旧版本数据能被新运行时解码。
func TestOldDataDecodedByNew(t *testing.T) {
	old := oldshop.User{Name: "alice", Age: 30}
	data, err := old.EncodeVersion(1)
	if err != nil {
		t.Fatalf("旧版编码失败: %v", err)
	}
	t.Logf("输入: 旧版 v1 数据 %x", data)

	var nu newshop.User
	if err := nu.Decode(data); err != nil {
		t.Fatalf("判定依据: 新运行时应能解码旧数据, 实际: %v", err)
	}
	if nu.Name != "alice" || nu.Age != 30 {
		t.Fatalf("字段值不符: %+v", nu)
	}
	if nu.Email != "" {
		t.Fatalf("v1 数据不应包含 v2 才引入的 email, 实际 %q", nu.Email)
	}
	t.Logf("判定: 新运行时解码旧数据成功, name=%q age=%d email=%q(空,符合预期)", nu.Name, nu.Age, nu.Email)
}

// TestNewDataDecodedByOld_unknownFieldsPreserved 验证新版本数据能被旧运行时
// 解码，未知新增字段在重新编码时逐字节保留。
func TestNewDataDecodedByOld_unknownFieldsPreserved(t *testing.T) {
	nu := newshop.User{Name: "bob", Age: 25, Email: "bob@x.io", Active: true}
	data, err := nu.EncodeVersion(2)
	if err != nil {
		t.Fatalf("新版编码失败: %v", err)
	}
	t.Logf("输入: 新版 v2 数据 %x (含旧版不认识的字段 3=email, 4=active)", data)

	var old oldshop.User
	if err := old.Decode(data); err != nil {
		t.Fatalf("判定依据: 旧运行时应能解码 v2 数据, 实际: %v", err)
	}
	if old.Name != "bob" || old.Age != 25 {
		t.Fatalf("已知字段值不符: %+v", old)
	}
	if len(old.UnknownFields.Fields) != 2 {
		t.Fatalf("判定依据: 应保留 2 个未知字段, 实际 %d", len(old.UnknownFields.Fields))
	}
	t.Logf("判定: 旧运行时保留未知字段 %d 个: %+v", len(old.UnknownFields.Fields), old.UnknownFields.Fields)

	reEncoded, err := old.EncodeVersion(2)
	if err != nil {
		t.Fatalf("旧版重编码失败: %v", err)
	}
	if !bytes.Equal(reEncoded, data) {
		t.Fatalf("判定依据: 重编码应逐字节一致\n原始: %x\n重编: %x", data, reEncoded)
	}
	t.Logf("判定依据: 重编码 %x 与原始输入逐字节一致 -> 未知字段原样保留", reEncoded)

	// 再交还给新运行时，新增字段应完好。
	var back newshop.User
	if err := back.Decode(reEncoded); err != nil {
		t.Fatalf("新运行时回读失败: %v", err)
	}
	if back.Email != "bob@x.io" || !back.Active {
		t.Fatalf("未知字段经旧运行时往返后丢失: %+v", back)
	}
	t.Logf("判定: 跨版本往返后 email=%q active=%v 完好", back.Email, back.Active)
}

// TestCrossVersionRoundTrip 验证同一逻辑对象在多个版本间往返一致。
func TestCrossVersionRoundTrip(t *testing.T) {
	src := newshop.User{Name: "carol", Age: 41, Email: "c@x.io", Active: true}
	for _, v := range []int{1, 2, 3} {
		data, err := src.EncodeVersion(v)
		if err != nil {
			t.Fatalf("v%d 编码失败: %v", v, err)
		}
		var dst newshop.User
		if err := dst.Decode(data); err != nil {
			t.Fatalf("v%d 解码失败: %v", v, err)
		}
		if dst.Name != src.Name {
			t.Fatalf("v%d 往返 name 丢失: %q", v, dst.Name)
		}
		// age 在 v3 已移除，email 在 v1 不存在：按版本区间断言。
		if v <= 2 && dst.Age != src.Age {
			t.Fatalf("v%d 往返 age 丢失", v)
		}
		if v >= 2 && dst.Email != src.Email {
			t.Fatalf("v%d 往返 email 丢失", v)
		}
		t.Logf("判定: v%d 往返一致 (name=%q age=%d email=%q active=%v)",
			v, dst.Name, dst.Age, dst.Email, dst.Active)
	}
}

// TestIncompatibleVersionRejected 验证不兼容版本被明确拒绝，
// 且与数据损坏可区分。
func TestIncompatibleVersionRejected(t *testing.T) {
	nu := newshop.User{Name: "dave", Active: true, Avatar: []byte{1, 2, 3}}
	data, err := nu.EncodeVersion(3) // v3 超出 oldshop 的 1..2
	if err != nil {
		t.Fatalf("新版编码失败: %v", err)
	}
	t.Logf("输入: v3 数据 %x, 旧运行时支持区间 [1, 2]", data)

	var old oldshop.User
	err = old.Decode(data)
	if !errors.Is(err, wire.ErrIncompatibleVersion) {
		t.Fatalf("判定依据: 应归类为 ErrIncompatibleVersion, 实际: %v", err)
	}
	if errors.Is(err, wire.ErrCorrupt) {
		t.Fatalf("判定依据: 不应归类为 ErrCorrupt, 实际: %v", err)
	}
	t.Logf("判定依据: errors.Is(err, ErrIncompatibleVersion)=true 且 ErrCorrupt=false -> 错误归类: 版本不兼容 (%v)", err)

	// 编码方向同样拒绝越界版本。
	if _, err := old.EncodeVersion(3); !errors.Is(err, wire.ErrIncompatibleVersion) {
		t.Fatalf("编码越界版本应归类为 ErrIncompatibleVersion, 实际: %v", err)
	}
	t.Log("判定: 编码方向越界版本同样归类为版本不兼容")
}

// TestCorruptDataRejected 验证截断与越界数据被明确拒绝并归类为数据损坏。
func TestCorruptDataRejected(t *testing.T) {
	nu := newshop.User{Name: "erin", Age: 9, Email: "e@x.io", Active: true}
	full, err := nu.EncodeVersion(2)
	if err != nil {
		t.Fatal(err)
	}

	// 注意：截断必须落在字段编码内部；落在字段边界上的前缀
	// 本身就是一条合法但更短的消息（自描述格式的固有特性）。
	cases := []struct {
		name string
		data []byte
	}{
		{"空数据", nil},
		{"版本头截断(varint未完结)", []byte{0x80}},
		{"字符串载荷截断", full[:5]}, // 字段1声明长度4，只剩2字节载荷
		{"字段值varint截断", append(append([]byte(nil), full[:8]...), 0x80)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Logf("输入数据(%d 字节): %x", len(c.data), c.data)
			var u newshop.User
			err := u.Decode(c.data)
			if !errors.Is(err, wire.ErrCorrupt) {
				t.Fatalf("判定依据: 应归类为 ErrCorrupt, 实际: %v", err)
			}
			if errors.Is(err, wire.ErrIncompatibleVersion) {
				t.Fatalf("判定依据: 不应归类为 ErrIncompatibleVersion, 实际: %v", err)
			}
			t.Logf("判定依据: errors.Is(err, ErrCorrupt)=true 且 ErrIncompatibleVersion=false -> 错误归类: 数据损坏 (%v)", err)
		})
	}

	// 越界：篡改长度前缀使其超过剩余字节。
	corrupt := append([]byte(nil), full...)
	corrupt[2] = 0x7f // 字段 1(name) 的长度改为 127
	t.Logf("输入: 篡改长度前缀后的数据 %x", corrupt)
	var u newshop.User
	err = u.Decode(corrupt)
	if !errors.Is(err, wire.ErrCorrupt) {
		t.Fatalf("越界长度应归类为 ErrCorrupt, 实际: %v", err)
	}
	t.Logf("判定依据: errors.Is(err, ErrCorrupt)=true -> 错误归类: 数据损坏 (%v)", err)
}
