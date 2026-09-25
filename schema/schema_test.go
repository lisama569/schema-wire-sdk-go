package schema

import (
	"strings"
	"testing"
)

func mustLoad(t *testing.T, text string) *Schema {
	t.Helper()
	s, err := Load("test.schema", strings.NewReader(text))
	if err != nil {
		t.Fatalf("expected valid schema, got %v\ninput:\n%s", err, text)
	}
	return s
}

func expectKind(t *testing.T, text string, want FailureKind) {
	t.Helper()
	_, err := Load("test.schema", strings.NewReader(text))
	le, ok := AsLoadError(err)
	if !ok {
		t.Fatalf("input:\n%s\n  判定依据=未返回 *LoadError, got %v\n  错误归类=<%v>", text, err, want)
	}
	if le.Kind != want {
		t.Fatalf("input:\n%s\n  判定依据=%v\n  错误归类=%s, 期望 %s", text, le, le.Kind, want)
	}
	t.Logf("schema 校验被正确拒绝\n  输入=%q\n  判定依据=%s\n  错误归类=%s",
		oneLine(text), le.Msg, le.Kind)
}

func oneLine(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", " | ")
}

func TestValidSchema(t *testing.T) {
	s := mustLoad(t, `
		# 服务端事件 schema
		package demo;
		version 3;
		compat additive;
		message Address {
			1: string city;
			2: string zip [since=2];
		}
		message User {
			1: int64 id;
			2: string name;
			3: Address home [since=2];
			4: bool active [since=3];
		}
	`)
	if s.Package != "demo" || s.Version != 3 || s.Policy != CompatAdditive {
		t.Fatalf("header parsed wrong: %+v", s)
	}
	u := s.Messages[1]
	if u.Name != "User" || len(u.Fields) != 4 {
		t.Fatalf("user message wrong: %+v", u)
	}
	home := u.Fields[2]
	if home.Kind != TypeMessage || home.Type != "Address" || home.Since != 2 {
		t.Fatalf("field home wrong: %+v", home)
	}
	if f := u.FieldByNumber(4); f == nil || f.Name != "active" {
		t.Fatalf("field #4 lookup wrong: %+v", f)
	}
}

func TestRejectDuplicateNumber(t *testing.T) {
	expectKind(t, `
		message M {
			1: int32 a;
			1: int32 b;
		}`, KindDuplicateNumber)
}

func TestRejectOverlappingRanges(t *testing.T) {
	// none 策略允许复用编号，但活跃区间重叠仍必须拒绝
	expectKind(t, `
		version 5;
		compat none;
		message M {
			1: int32 old [until=3];
			1: string fresh [since=3];
		}`, KindDuplicateNumber)
}

func TestRejectUnknownType(t *testing.T) {
	expectKind(t, `
		message M {
			1: Missing ref;
		}`, KindUnknownType)
}

func TestRejectInvalidVersionRange(t *testing.T) {
	expectKind(t, `
		version 2;
		message M {
			1: int32 a [since=3];
		}`, KindInvalidVersion)
	expectKind(t, `
		version 5;
		message M {
			1: int32 a [since=4, until=2];
		}`, KindInvalidVersion)
}

func TestRejectAdditiveConflict(t *testing.T) {
	// additive 下编号即使区间不重叠也禁止复用
	expectKind(t, `
		version 4;
		compat additive;
		message M {
			1: int32 old [until=2];
			1: string fresh [since=3];
		}`, KindCompatConflict)
}

func TestRejectSyntax(t *testing.T) {
	expectKind(t, `
		message M {
			1 int32 a;
		}`, KindSyntax)
	expectKind(t, `
		message M {
			1: int32 a
		}`, KindSyntax)
	expectKind(t, `
		compat strict;
		message M { 1: int32 a; }`, KindSyntax)
}

func TestRejectEmptyAndDup(t *testing.T) {
	expectKind(t, `# only comment`, KindEmpty)
	expectKind(t, `
		message M { 1: int32 a; }
		message M { 1: int32 b; }`, KindDuplicateDecl)
}

func TestRejectExportNameCollision(t *testing.T) {
	expectKind(t, `
		message M {
			1: int32 foo_bar;
			2: int32 FooBar;
		}`, KindInvalidName)
}

func TestNonePolicyDisjointRangesAccepted(t *testing.T) {
	s := mustLoad(t, `
		version 5;
		compat none;
		message M {
			1: int32 old [until=2];
			1: string fresh [since=3];
			2: int64 keep;
		}
	`)
	m := s.Messages[0]
	if m.Fields[0].Name != "old" || m.Fields[1].Name != "fresh" {
		t.Fatalf("fields not sorted as expected: %+v", m.Fields)
	}
}

func TestRejectAdditiveRemoval(t *testing.T) {
	// additive 下给字段加 until（移除）必须以 compat_conflict 拒绝
	expectKind(t, `
		version 4;
		compat additive;
		message M {
			1: int32 legacy [until=2];
			2: int64 keep;
		}`, KindCompatConflict)
}

func TestUnknownFutureNumberPreservedAtOldVersion(t *testing.T) {
	// 字段在 v3 才加入；老版本 schema 视图下它不影响校验通过
	s := mustLoad(t, `
		version 3;
		message M {
			1: int32 a;
			2: string b [since=3];
		}`)
	m := s.Messages[0]
	if m.FieldAt(2, 1) != nil {
		t.Fatalf("field #2 must be invisible at v1")
	}
	if m.FieldAt(2, 3) == nil {
		t.Fatalf("field #2 must be visible at v3")
	}
}
