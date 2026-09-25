package codegen

import (
	"fmt"

	"github.com/lisama569/schema-wire-sdk-go/schema"
)

// fieldVar 是生成结构中字段的 Go 标识符。
func fieldVar(f *schema.Field) string { return exported(f.Name) }

// encodeValue 返回“把字段值写入 b”的 Go 语句（不含 tag）。
// valueExpr 为字段值表达式（已解引用）。
func encodeValue(f *schema.Field, valueExpr string) string {
	switch f.Kind {
	case schema.TypeInt32, schema.TypeInt64:
		return fmt.Sprintf("b = wire.AppendSVarint(b, int64(%s))", valueExpr)
	case schema.TypeUint32, schema.TypeUint64:
		return fmt.Sprintf("b = wire.AppendUVarint(b, uint64(%s))", valueExpr)
	case schema.TypeBool:
		return fmt.Sprintf("var bv uint64\nif %s { bv = 1 }\nb = wire.AppendUVarint(b, bv)", valueExpr)
	case schema.TypeFloat64:
		return fmt.Sprintf("b = wire.AppendFixed64(b, %s)", valueExpr)
	case schema.TypeString:
		return fmt.Sprintf("b = wire.AppendString(b, %s)", valueExpr)
	case schema.TypeBytes:
		return fmt.Sprintf("b = wire.AppendBytes(b, %s)", valueExpr)
	case schema.TypeMessage:
		return fmt.Sprintf("nb, err := encodeBody%s(%s, viewVersion)\nif err != nil { return nil, err }\nb = wire.AppendBytes(b, nb)",
			exported(f.Type), valueExpr)
	default:
		return "// invalid type"
	}
}

// decodeAssign 返回按线缆类型读取并赋值到 tgt.<Field> 的 Go 语句。
// 使用变量 r *wire.Reader；失败时 r 进入损坏态，由调用方统一返回。
func decodeAssign(f *schema.Field) string {
	fv := "tgt." + fieldVar(f)
	switch f.Kind {
	case schema.TypeInt32:
		return fmt.Sprintf("v := int32(r.ReadSVarint()); %s = &v", fv)
	case schema.TypeInt64:
		return fmt.Sprintf("v := r.ReadSVarint(); %s = &v", fv)
	case schema.TypeUint32:
		return fmt.Sprintf("v := uint32(r.ReadUVarint()); %s = &v", fv)
	case schema.TypeUint64:
		return fmt.Sprintf("v := r.ReadUVarint(); %s = &v", fv)
	case schema.TypeBool:
		return fmt.Sprintf("v := r.ReadUVarint() != 0; %s = &v", fv)
	case schema.TypeFloat64:
		return fmt.Sprintf("v := r.ReadFixed64(); %s = &v", fv)
	case schema.TypeString:
		return fmt.Sprintf("v := r.ReadString(); %s = &v", fv)
	case schema.TypeBytes:
		return fmt.Sprintf("v := append([]byte(nil), r.ReadBytes()...); %s = &v", fv)
	case schema.TypeMessage:
		return fmt.Sprintf(`raw := r.ReadBytes()
sub := &%s{}
if err := decodeBody%s(raw, sub, wireVersion, knownVersion); err != nil {
	return err
}
%s = sub`, exported(f.Type), exported(f.Type), fv)
	default:
		return ""
	}
}
