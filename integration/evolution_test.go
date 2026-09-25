// Package integration 持有“生成产物 + 运行时”在协议演进场景下的端到端测试。
//
// examples/demo 与 examples/evolv 下的 Go 包由 schemagen 生成并签入仓库，
// 这里直接导入，模拟新老两端二进制互通。
package integration

import (
	"bytes"
	"testing"

	"github.com/lisama569/schema-wire-sdk-go/examples/demo/gen/demo"
	"github.com/lisama569/schema-wire-sdk-go/examples/evolv/gen/evolv"
	"github.com/lisama569/schema-wire-sdk-go/wire"
)

func ptr[T any](v T) *T { return &v }

// TestDemoCrossVersionRoundTrip: 新版（v3）消息经“老视角 v1”读取，
// 新增字段必须被保留并在重新编码时逐字节透传。
func TestDemoCrossVersionRoundTrip(t *testing.T) {
	home := &demo.Address{City: ptr("Shanghai"), Zip: ptr("200000")}
	full := &demo.User{
		Id:     ptr(int64(42)),
		Name:   ptr("alice"),
		Home:   home,
		Active: ptr(true),
		Avatar: ptr([]byte{0xde, 0xad}),
		Score:  ptr(3.5),
	}
	wireV3, err := demo.MarshalUser(full, 3)
	if err != nil {
		t.Fatalf("marshal v3: %v", err)
	}
	t.Logf("输入=v3 完整 User（含 home/active/avatar/score 新字段），报文长度=%d", len(wireV3))

	// 老代码视角解码：它仍按编号识别 id/name，其余字段落入 unknown。
	oldView := &demo.User{}
	envVer, err := demo.UnmarshalUser(wireV3, oldView, 1, false)
	if err != nil {
		t.Fatalf("old-view unmarshal: %v", err)
	}
	if envVer != 3 {
		t.Fatalf("envelope version: want 3 got %d", envVer)
	}
	if oldView.Id == nil || *oldView.Id != 42 || oldView.Name == nil || *oldView.Name != "alice" {
		t.Fatalf("old-view known fields wrong: %+v", oldView)
	}
	gotUnknown := oldView.UnknownFieldNumbers()
	t.Logf("判定依据=信封版本=%d；已知字段 id/name 可读；未知编号=%v（home=3,active=4,avatar=5,score=6）",
		envVer, gotUnknown)
	for _, want := range []uint32{3, 4, 5, 6} {
		if !containsU32(gotUnknown, want) {
			t.Fatalf("unknown field %d not preserved; got %v", want, gotUnknown)
		}
	}

	// 老代码不做任何修改，以 v1 视角重新编码：未知新增字段必须原样保留。
	reEncoded, err := demo.MarshalUser(oldView, 1)
	if err != nil {
		t.Fatalf("old-view remarshal v1: %v", err)
	}
	newView := &demo.User{}
	if _, err := demo.UnmarshalUser(reEncoded, newView, 3, false); err != nil {
		t.Fatalf("new-view decode of re-encoded bytes: %v", err)
	}
	if newView.Active == nil || !*newView.Active {
		t.Fatalf("unknown bool field not preserved through re-encode")
	}
	if newView.Avatar == nil || !bytes.Equal(*newView.Avatar, []byte{0xde, 0xad}) {
		t.Fatalf("unknown bytes field not preserved: %v", newView.Avatar)
	}
	if newView.Score == nil || *newView.Score != 3.5 {
		t.Fatalf("unknown float64 field not preserved: %v", newView.Score)
	}
	if newView.Home == nil || newView.Home.Zip == nil || *newView.Home.Zip != "200000" {
		t.Fatalf("unknown nested message not preserved: %+v", newView.Home)
	}
	t.Logf("判定依据=以 v1 重编码后再次解码，active/avatar/score/home 全部无损还原；错误归类=无（互通成功）")
}

// TestDemoUnknownBytesByteIdentical: 未知字段的原始字节必须逐字节保留。
func TestDemoUnknownBytesByteIdentical(t *testing.T) {
	full := &demo.User{Id: ptr(int64(7)), Active: ptr(true), Avatar: ptr([]byte("xyz"))}
	orig, err := demo.MarshalUser(full, 3)
	if err != nil {
		t.Fatal(err)
	}
	got := &demo.User{}
	if _, err := demo.UnmarshalUser(orig, got, 3, false); err != nil {
		t.Fatal(err)
	}
	out, err := demo.MarshalUser(got, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(orig, out) {
		t.Fatalf("re-encoded bytes differ:\n orig=%x\n got =%x", orig, out)
	}
	t.Logf("输入=%x；判定依据=未知字段以原始 TLV 记录透传；重编码=%x 逐字节一致；错误归类=无", orig, out)
}

// TestDemoTruncatedAndOutOfRangeRejectedAsCorrupt: 截断与越界必须判为损坏。
func TestDemoTruncatedAndOutOfRangeRejectedAsCorrupt(t *testing.T) {
	good, err := demo.MarshalUser(&demo.User{Id: ptr(int64(1)), Name: ptr("abc")}, 2)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"truncated-body":        good[:len(good)-2],
		"truncated-version":     {0x80},
		"varint-overflow":       append([]byte{0x01}, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f),
		"unknown-wire-type":     {0x01, 0x07},       // tag: field 1, wire type 7（非法）
		"field-number-zero":     {0x01, 0x04, 0x00}, // 内层字段编号 0
		"length-out-of-range":   buildOutOfRange(),  // 长度前缀超过剩余字节
		"nested-length-overrun": nestedOverrun(),    // 嵌套消息声明长度超过报文
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			var m demo.User
			_, err := demo.UnmarshalUser(in, &m, 3, false)
			if err == nil {
				t.Fatalf("输入=%x：期望被拒绝，却成功解码", in)
			}
			if !wire.IsCorruption(err) {
				t.Fatalf("输入=%x：判定依据=%v；错误归类=%s，期望 corrupt_data", in, err, kindOf(err))
			}
			t.Logf("输入=%x；判定依据=%v；错误归类=corrupt_data", in, err)
		})
	}
}

// TestVersionZeroIsIncompatibleNotCorrupt: 两类错误必须可区分。
func TestVersionZeroIsIncompatibleNotCorrupt(t *testing.T) {
	in := []byte{0x00, 0x08, 0x01}
	var m demo.User
	_, err := demo.UnmarshalUser(in, &m, 3, false)
	if err == nil {
		t.Fatal("version 0 must be rejected")
	}
	if !wire.IsIncompatibleVersion(err) || wire.IsCorruption(err) {
		t.Fatalf("输入=%x：判定依据=%v；错误归类=%s，期望 incompatible_version", in, err, kindOf(err))
	}
	t.Logf("输入=%x（可解析但信封版本为0）；判定依据=%v；错误归类=incompatible_version", in, err)

	// 损坏数据不应被误判为版本问题
	bad := []byte{0x80}
	if _, err := demo.UnmarshalUser(bad, &demo.User{}, 3, false); !wire.IsCorruption(err) {
		t.Fatalf("truncated prefix must be corrupt, got %v", err)
	}
}

// TestStrictRejectsFutureVersion: 严格模式拒绝高于代码上限的版本；
// 宽松模式（默认）保留未知字段并接受。
func TestStrictRejectsFutureVersion(t *testing.T) {
	// 直接手工构造一个 v=99 的信封，body 为空（结构完全合法）。
	future := wire.EncodeMessage(99, nil)
	t.Logf("输入=v99 空 body 合法报文 %x（schema 上限=3）", future)

	var lax demo.User
	if _, err := demo.UnmarshalUser(future, &lax, 3, false); err != nil {
		t.Fatalf("lax decode should accept future version: %v", err)
	}
	var strict demo.User
	_, err := demo.UnmarshalUser(future, &strict, 3, true)
	if !wire.IsIncompatibleVersion(err) {
		t.Fatalf("strict decode: want incompatible, got %v", err)
	}
	t.Logf("判定依据=strict=true 且信封99 > 本地理解版本3；错误归类=incompatible_version；宽松模式接受")
}

// TestEvolvNumberReuseAcrossVersions: 同一编号在不同版本身份不同，
// 旧报文按旧身份解，新报文按新身份解，两类版本都能读。
func TestEvolvNumberReuseAcrossVersions(t *testing.T) {
	// v1：字段1 是 int32 old
	old := &evolv.Event{Old: ptr(int32(-17)), Ts: ptr(int64(100))}
	b1, err := evolv.MarshalEvent(old, 1)
	if err != nil {
		t.Fatal(err)
	}
	got1 := &evolv.Event{}
	v1, err := evolv.UnmarshalEvent(b1, got1, 5, false)
	if err != nil || v1 != 1 {
		t.Fatalf("decode v1: v=%d err=%v", v1, err)
	}
	if got1.Old == nil || *got1.Old != -17 || got1.Handle != nil {
		t.Fatalf("v1 field #1 must decode as int32 old, got %+v", got1)
	}
	t.Logf("输入=v1 报文（#1=int32 -17）；判定依据=信封版本落在 [1,2]；解为 Old=%d；错误归类=无", *got1.Old)

	// v5：字段1 是 string handle
	fresh := &evolv.Event{Handle: ptr("alice-007"), Ts: ptr(int64(500)), Flagged: ptr(true)}
	b5, err := evolv.MarshalEvent(fresh, 5)
	if err != nil {
		t.Fatal(err)
	}
	got5 := &evolv.Event{}
	v5, err := evolv.UnmarshalEvent(b5, got5, 5, false)
	if err != nil || v5 != 5 {
		t.Fatalf("decode v5: v=%d err=%v", v5, err)
	}
	if got5.Handle == nil || *got5.Handle != "alice-007" || got5.Old != nil {
		t.Fatalf("v5 field #1 must decode as string handle, got %+v", got5)
	}
	if got5.Flagged == nil || !*got5.Flagged {
		t.Fatalf("v5 field #3 flagged lost: %+v", got5)
	}
	t.Logf("输入=v5 报文（#1=string handle）；判定依据=信封版本落在 [5,now]；解为 Handle=%q；错误归类=无", *got5.Handle)

	// v1 字节对 v5 视角：编号1在 v5 是另一个字段，但报文自报版本为 1，
	// 按报文版本路由，旧字段依然可读（多版本共存）。
	reread := &evolv.Event{}
	if _, err := evolv.UnmarshalEvent(b1, reread, 5, false); err != nil {
		t.Fatalf("v1 bytes must remain decodable: %v", err)
	}
	if reread.Old == nil || *reread.Old != -17 {
		t.Fatalf("v1 bytes lost their identity on reread: %+v", reread)
	}
}

// TestEvolvWireTypeMismatchIsCorrupt: v5 视角下编号1应为字符串，
// 若线缆类型不符（构造损坏），必须判 corrupt 而非版本问题。
func TestEvolvWireTypeMismatchIsCorrupt(t *testing.T) {
	// v5 body: tag(1, varint) 而非期望的 bytes
	body := []byte{0x08, 0x01}
	in := wire.EncodeMessage(5, body)
	_, err := evolv.UnmarshalEvent(in, &evolv.Event{}, 5, false)
	if !wire.IsCorruption(err) {
		t.Fatalf("输入=%x：期望 corrupt，got %v", in, err)
	}
	t.Logf("输入=%x（v5 编号1 线缆类型为 varint，与 string 不符）；判定依据=%v；错误归类=corrupt_data", in, err)
}

func nestedOverrun() []byte {
	// version=3; tag field#3(home)=0x1a; len=50 但只给 2 字节 -> 嵌套越界
	return []byte{0x03, 0x1a, 50, 0x0a, 0x01}
}

func buildOutOfRange() []byte {
	// version=2, 然后 field#2(string) tag=0x12，长度 0xff 但无后续字节
	return []byte{0x02, 0x12, 0xff}
}

func containsU32(xs []uint32, v uint32) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func kindOf(err error) string {
	if we, ok := err.(*wire.Error); ok {
		return string(we.Kind)
	}
	return "unknown"
}
