package codegen

import (
	"fmt"
	"strings"

	"github.com/lisama569/schema-wire-sdk-go/schema"
)

// runtimeImport 是生成代码依赖的运行时包导入路径。
const runtimeImport = "github.com/lisama569/schema-wire-sdk-go/wire"

// goType 返回字段值的 Go 类型（指针基类型）。
func goType(f *schema.Field) string {
	switch f.Kind {
	case schema.TypeInt32:
		return "int32"
	case schema.TypeInt64:
		return "int64"
	case schema.TypeUint32:
		return "uint32"
	case schema.TypeUint64:
		return "uint64"
	case schema.TypeBool:
		return "bool"
	case schema.TypeString:
		return "string"
	case schema.TypeBytes:
		return "[]byte"
	case schema.TypeFloat64:
		return "float64"
	case schema.TypeMessage:
		// 字段外层统一是指针，消息类型本身已是指针，无需再加 *。
		return exported(f.Type)
	default:
		return "invalid"
	}
}

func exported(name string) string { return schema.ExportName(name) }

func messageExported(m *schema.Message) string { return exported(m.Name) }

// wireType 返回字段对应的线缆类型常量。
func wireType(f *schema.Field) string {
	switch f.Kind {
	case schema.TypeFloat64:
		return "wire.WireFixed64"
	case schema.TypeString, schema.TypeBytes, schema.TypeMessage:
		return "wire.WireBytes"
	default:
		return "wire.WireVarint"
	}
}

func fieldVarName(f *schema.Field) string { return fieldVar(f) }

func encodeStmt(f *schema.Field) string {
	expr := "*(m." + fieldVar(f) + ")"
	if f.Kind == schema.TypeMessage {
		expr = "m." + fieldVar(f) // 消息字段本身就是指针
	}
	return encodeValue(f, expr)
}

func decodeStmt(f *schema.Field) string { return decodeAssign(f) }

func fieldGroupsOf(m *schema.Message) [][]*schema.Field { return m.FieldGroups() }

// identCondition 按“报文自报版本”选择同编号字段的身份。
func identCondition(f *schema.Field) string {
	return rangeCondition("wireVersion", f)
}

func rangeCondition(v string, f *schema.Field) string {
	if f.Until != 0 {
		return fmt.Sprintf("if %s >= %d && %s <= %d", v, f.Since, v, f.Until)
	}
	return fmt.Sprintf("if %s >= %d", v, f.Since)
}

// visibleCondition 判断该字段在“本地理解版本”是否被代码认识。
//
// 字段自 Since 起成为公开协议的一部分，此后发布的所有代码都认识它，
// 因此可见性下界为 Since，没有上界（Until 只影响“新版本是否写入”，
// 不影响“老数据能否读取”）。
func visibleCondition(f *schema.Field) string {
	return fmt.Sprintf("knownVersion >= %d", f.Since)
}

// rangeIf 生成形如 "if v >= since && v <= until" 的区间条件。
func rangeIf(f *schema.Field, v string) string { return rangeCondition(v, f) }

func wvCondition(f *schema.Field) string { return rangeCondition("wireVersion", f) }
func kvCondition(f *schema.Field) string { return rangeCondition("knownVersion", f) }

// tmplFuncs 汇总模板辅助函数。
var tmplFuncs = map[string]any{
	"str":         func(v any) string { return fmt.Sprint(v) },
	"export":      exported,
	"goType":      goType,
	"wireType":    wireType,
	"join":        strings.Join,
	"add":         func(a, b int) int { return a + b },
	"fieldVar":    fieldVarName,
	"encodeStmt":  encodeStmt,
	"decodeStmt":  decodeStmt,
	"fieldGroups": fieldGroupsOf,
	"num":         func(f *schema.Field) uint32 { return f.Number },
	"identIf":     identCondition,
	"rangeIf":     rangeIf,
	"wvIf":        wvCondition,
	"kvIf":        kvCondition,
	"visibleIf":   visibleCondition,
}
