package schema

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"unicode"
)

// Parse 将 schema 文本解析为未校验的中间结构。
//
// 语法（行导向）：
//
//	# 行注释；空行被忽略
//	package <ident>;
//	version <uint>;
//	compat additive|none;
//	message <Name> {
//	    <num>: <type> <name> [since=<v>[, until=<v>]];
//	}
//
// 解析阶段只做词法与语法检查，不做跨声明语义校验（类型解析、
// 编号冲突、版本区间合法性等在 Validate 中完成）。
func Parse(sourceName string, r io.Reader) (*Schema, error) {
	p := &parser{src: sourceName}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		p.lineNo++
		line := stripComment(sc.Text())
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if err := p.line(line); err != nil {
			return nil, err
		}
	}
	if err := sc.Err(); err != nil {
		return nil, &LoadError{Kind: KindIO, Source: sourceName, Msg: err.Error()}
	}
	if p.pending != nil {
		return nil, &LoadError{Kind: KindSyntax, Line: p.braceLine, Source: sourceName,
			Msg: "message " + p.pending.Name + " missing closing '}'"}
	}
	if p.schema == nil {
		p.schema = &Schema{Policy: CompatAdditive, Version: 1}
	}
	if p.schema.Policy == "" {
		p.schema.Policy = CompatAdditive
	}
	if p.schema.Version == 0 {
		p.schema.Version = 1
	}
	return p.schema, nil
}

type parser struct {
	src       string
	lineNo    int
	braceLine int
	schema    *Schema
	pending   *Message // 正在定义的消息
}

func (p *parser) err(kind FailureKind, msg string) error {
	return &LoadError{Kind: kind, Line: p.lineNo, Source: p.src, Msg: msg}
}

// stripComment 去掉 # 之后的内容。当前语法中 # 没有其他用途。
func stripComment(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		return line[:i]
	}
	return line
}

func (p *parser) line(line string) error {
	if p.pending != nil {
		return p.messageLine(line)
	}
	// 头部 / 消息开始
	switch {
	case strings.HasPrefix(line, "package"):
		return p.directive(line, "package", func(v string) error {
			if p.schema != nil && p.schema.Package != "" {
				return p.err(KindDuplicateDecl, "duplicate package directive")
			}
			p.ensureSchema()
			p.schema.Package = strings.TrimSuffix(v, ";")
			return nil
		})
	case strings.HasPrefix(line, "version"):
		return p.directive(line, "version", func(v string) error {
			if p.schema != nil && p.schema.Version != 0 {
				return p.err(KindDuplicateDecl, "duplicate version directive")
			}
			n, err := parseDirectiveUint(v)
			if err != nil {
				return p.err(KindSyntax, "version: "+err.Error())
			}
			p.ensureSchema()
			p.schema.Version = n
			return nil
		})
	case strings.HasPrefix(line, "compat"):
		return p.directive(line, "compat", func(v string) error {
			pol := CompatPolicy(strings.TrimSuffix(v, ";"))
			if pol != CompatAdditive && pol != CompatNone {
				return p.err(KindSyntax, "compat must be 'additive' or 'none', got "+string(pol))
			}
			if p.schema != nil && p.schema.Policy != "" {
				return p.err(KindDuplicateDecl, "duplicate compat directive")
			}
			p.ensureSchema()
			p.schema.Policy = pol
			return nil
		})
	case strings.HasPrefix(line, "message"):
		return p.startMessage(line)
	default:
		return p.err(KindSyntax, "unexpected statement outside message: "+line)
	}
}

func (p *parser) ensureSchema() {
	if p.schema == nil {
		p.schema = &Schema{}
	}
}

func (p *parser) directive(line, name string, fn func(v string) error) error {
	rest := strings.TrimSpace(strings.TrimPrefix(line, name))
	if rest == "" {
		return p.err(KindSyntax, name+" directive requires a value")
	}
	return fn(rest)
}

func parseDirectiveUint(v string) (uint32, error) {
	v = strings.TrimSuffix(v, ";")
	n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(n), nil
}

func (p *parser) startMessage(line string) error {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "message"))
	// 期望: Name {   或单行: Name { ... }
	br := strings.IndexByte(rest, '{')
	if br < 0 {
		return p.err(KindSyntax, "message header must contain '{' on the same line")
	}
	name := strings.TrimSpace(rest[:br])
	if !isIdent(name) {
		return p.err(KindInvalidName, "invalid message name: "+name)
	}
	p.ensureSchema()
	p.pending = &Message{Name: name}
	p.braceLine = p.lineNo
	tail := strings.TrimSpace(rest[br+1:])
	if tail != "" {
		return p.messageLine(tail)
	}
	return nil
}

func (p *parser) messageLine(line string) error {
	// 允许单行形式: <field>; } 或 <field> };
	if strings.HasSuffix(line, "}") {
		head := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(line, "}"), ";"))
		if err := p.consumeStatements(head); err != nil {
			return err
		}
		p.schema.Messages = append(p.schema.Messages, p.pending)
		p.pending = nil
		return nil
	}
	return p.consumeStatements(line)
}

// consumeStatements 处理一行内的一条或多条以 ';' 分隔的字段声明。
// 整行必须以 ';' 结尾（在去掉消息闭合括号后判断），否则为语法错误。
func (p *parser) consumeStatements(line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if !strings.HasSuffix(line, ";") {
		return p.err(KindSyntax, "field declaration must end with ';': "+line)
	}
	stmts := strings.Split(line, ";")
	// 末尾 ';' 会产生一个空串
	for _, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := p.consumeField(stmt + ";"); err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) consumeField(line string) error {
	f, err := p.parseField(line)
	if err != nil {
		return err
	}
	p.pending.Fields = append(p.pending.Fields, f)
	return nil
}

// parseField 解析: <num>: <type> <name> [since=v, until=v];
func (p *parser) parseField(line string) (*Field, error) {
	if !strings.HasSuffix(line, ";") {
		return nil, p.err(KindSyntax, "field declaration must end with ';': "+line)
	}
	line = strings.TrimSuffix(line, ";")

	// 选项段
	var opts string
	if i := strings.IndexByte(line, '['); i >= 0 {
		rest := line[i+1:]
		close := strings.IndexByte(rest, ']')
		if close < 0 {
			return nil, p.err(KindSyntax, "field options missing closing ']'")
		}
		opts = strings.TrimSpace(rest[:close])
		line = strings.TrimSpace(line[:i])
	}

	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return nil, p.err(KindSyntax, "field must be '<number>: <type> <name>': "+line)
	}
	numStr := strings.TrimSpace(line[:colon])
	n64, err := strconv.ParseUint(numStr, 10, 32)
	if err != nil {
		return nil, p.err(KindSyntax, "invalid field number "+numStr+": "+err.Error())
	}
	if n64 == 0 {
		return nil, p.err(KindSyntax, "field number must be >= 1")
	}

	tokens := strings.Fields(line[colon+1:])
	if len(tokens) != 2 {
		return nil, p.err(KindSyntax, "field requires exactly a type and a name, got: "+line[colon+1:])
	}
	typeTok, nameTok := tokens[0], tokens[1]
	if !isIdent(typeTok) {
		return nil, p.err(KindSyntax, "invalid type token: "+typeTok)
	}
	if !isIdent(nameTok) {
		return nil, p.err(KindInvalidName, "invalid field name: "+nameTok)
	}

	f := &Field{Number: uint32(n64), Name: nameTok, Since: 1}
	if k, ok := builtinTypes[typeTok]; ok {
		f.Kind = k
	} else {
		// 标识符类型先按消息引用记录，Validate 阶段解析是否存在。
		f.Kind = TypeMessage
		f.Type = typeTok
	}

	if opts != "" {
		if err := p.parseOptions(f, opts); err != nil {
			return nil, err
		}
	}
	return f, nil
}

func (p *parser) parseOptions(f *Field, opts string) error {
	for _, part := range strings.Split(opts, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return p.err(KindSyntax, "empty field option")
		}
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			return p.err(KindSyntax, "option must be key=value: "+part)
		}
		key := strings.TrimSpace(part[:eq])
		val := strings.TrimSpace(part[eq+1:])
		n, err := strconv.ParseUint(val, 10, 32)
		if err != nil {
			return p.err(KindSyntax, "option "+key+" expects an integer, got "+val)
		}
		switch key {
		case "since":
			f.Since = uint32(n)
		case "until":
			f.Until = uint32(n)
		default:
			return p.err(KindSyntax, "unknown field option: "+key)
		}
	}
	return nil
}

var builtinTypes = map[string]ScalarType{
	"int32":   TypeInt32,
	"int64":   TypeInt64,
	"uint32":  TypeUint32,
	"uint64":  TypeUint64,
	"bool":    TypeBool,
	"string":  TypeString,
	"bytes":   TypeBytes,
	"float64": TypeFloat64,
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || unicode.IsLetter(r) {
			continue
		}
		if i > 0 && unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}
