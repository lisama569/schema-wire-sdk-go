package schema

import (
	"fmt"
	"strings"
	"unicode"
)

// IDL 语法（示例）：
//
//	package shop
//
//	message User version 1..3 {
//	    1: string name since 1;
//	    2: int32 age since 1 until 2;
//	    3: string email since 2;
//	}
//
//	compat User 1 2;
//	incompat User 1 3;

type token struct {
	kind      tokKind
	text      string
	line, col int
}

type tokKind int

const (
	tokEOF tokKind = iota
	tokIdent
	tokNumber
	tokDotDot // ..
	tokColon  // :
	tokSemi   // ;
	tokLBrace // {
	tokRBrace // }
)

type lexer struct {
	src       string
	pos       int
	line, col int
}

func (l *lexer) errf(format string, args ...any) *ParseError {
	return &ParseError{Line: l.line, Col: l.col, Msg: fmt.Sprintf(format, args...)}
}

func (l *lexer) next() (token, error) {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == '\n':
			l.pos++
			l.line++
			l.col = 1
			continue
		case c == ' ' || c == '\t' || c == '\r':
			l.pos++
			l.col++
			continue
		case c == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '/':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
			continue
		}
		break
	}
	if l.pos >= len(l.src) {
		return token{kind: tokEOF, line: l.line, col: l.col}, nil
	}
	start := l.pos
	line, col := l.line, l.col
	c := l.src[l.pos]
	advance := func() { l.pos++; l.col++ }
	switch {
	case c == '.' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '.':
		advance()
		advance()
		return token{kind: tokDotDot, text: "..", line: line, col: col}, nil
	case c == ':':
		advance()
		return token{kind: tokColon, text: ":", line: line, col: col}, nil
	case c == ';':
		advance()
		return token{kind: tokSemi, text: ";", line: line, col: col}, nil
	case c == '{':
		advance()
		return token{kind: tokLBrace, text: "{", line: line, col: col}, nil
	case c == '}':
		advance()
		return token{kind: tokRBrace, text: "}", line: line, col: col}, nil
	case unicode.IsDigit(rune(c)):
		for l.pos < len(l.src) && unicode.IsDigit(rune(l.src[l.pos])) {
			advance()
		}
		return token{kind: tokNumber, text: l.src[start:l.pos], line: line, col: col}, nil
	case unicode.IsLetter(rune(c)) || c == '_':
		for l.pos < len(l.src) && (unicode.IsLetter(rune(l.src[l.pos])) ||
			unicode.IsDigit(rune(l.src[l.pos])) || l.src[l.pos] == '_') {
			advance()
		}
		return token{kind: tokIdent, text: l.src[start:l.pos], line: line, col: col}, nil
	}
	return token{}, l.errf("无法识别的字符 %q", rune(c))
}

type parser struct {
	lex  *lexer
	peek token
	err  error
}

func (p *parser) next() token {
	t := p.peek
	if p.err == nil {
		p.peek, p.err = p.lex.next()
	}
	return t
}

func (p *parser) expect(kind tokKind, what string) (token, error) {
	if p.err != nil {
		return token{}, p.err
	}
	if p.peek.kind != kind {
		return token{}, &ParseError{Line: p.peek.line, Col: p.peek.col,
			Msg: fmt.Sprintf("期望 %s，得到 %q", what, p.peek.text)}
	}
	return p.next(), nil
}

func (p *parser) expectIdent(what string) (string, error) {
	t, err := p.expect(tokIdent, what)
	return t.text, err
}

func (p *parser) expectNumber(what string) (int, error) {
	t, err := p.expect(tokNumber, what)
	if err != nil {
		return 0, err
	}
	var n int
	if _, err := fmt.Sscanf(t.text, "%d", &n); err != nil {
		return 0, &ParseError{Line: t.line, Col: t.col, Msg: "非法数字 " + t.text}
	}
	return n, nil
}

// Parse 解析 IDL 文本为 AST。语法错误返回 *ParseError。
func Parse(input string) (*File, error) {
	p := &parser{lex: &lexer{src: input, line: 1, col: 1}}
	p.peek, p.err = p.lex.next()
	f := &File{}

	// package 声明
	kw, err := p.expectIdent("package 声明")
	if err != nil {
		return nil, err
	}
	if kw != "package" {
		return nil, &ParseError{Line: 1, Col: 1, Msg: "文件必须以 package 声明开头"}
	}
	if f.Package, err = p.expectIdent("包名"); err != nil {
		return nil, err
	}
	if _, err = p.expect(tokSemi, "';'"); err != nil {
		return nil, err
	}

	for p.peek.kind != tokEOF {
		kw, err := p.expectIdent("顶层声明 (message/compat/incompat)")
		if err != nil {
			return nil, err
		}
		switch kw {
		case "message":
			m, err := p.parseMessage()
			if err != nil {
				return nil, err
			}
			f.Messages = append(f.Messages, m)
		case "compat", "incompat":
			cd, err := p.parseCompat(kw == "compat")
			if err != nil {
				return nil, err
			}
			f.Compats = append(f.Compats, cd)
		default:
			return nil, &ParseError{Line: p.peek.line, Col: p.peek.col,
				Msg: "未知顶层声明 " + kw}
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	return f, nil
}

func (p *parser) parseMessage() (*Message, error) {
	m := &Message{}
	var err error
	if m.Name, err = p.expectIdent("消息名"); err != nil {
		return nil, err
	}
	kw, err := p.expectIdent("version 关键字")
	if err != nil {
		return nil, err
	}
	if kw != "version" {
		return nil, &ParseError{Line: p.peek.line, Col: p.peek.col,
			Msg: "消息声明缺少 version 区间"}
	}
	if m.MinVersion, err = p.expectNumber("最小版本"); err != nil {
		return nil, err
	}
	if _, err = p.expect(tokDotDot, "'..'"); err != nil {
		return nil, err
	}
	if m.MaxVersion, err = p.expectNumber("最大版本"); err != nil {
		return nil, err
	}
	if _, err = p.expect(tokLBrace, "'{'"); err != nil {
		return nil, err
	}
	for p.peek.kind == tokNumber {
		fld, err := p.parseField(m)
		if err != nil {
			return nil, err
		}
		m.Fields = append(m.Fields, fld)
	}
	if _, err = p.expect(tokRBrace, "'}'"); err != nil {
		return nil, err
	}
	return m, nil
}

func (p *parser) parseField(m *Message) (*Field, error) {
	f := &Field{Since: m.MinVersion}
	var err error
	if f.Number, err = p.expectNumber("字段编号"); err != nil {
		return nil, err
	}
	if _, err = p.expect(tokColon, "':'"); err != nil {
		return nil, err
	}
	if f.Type, err = p.expectIdent("字段类型"); err != nil {
		return nil, err
	}
	if f.Name, err = p.expectIdent("字段名"); err != nil {
		return nil, err
	}
	// 可选的 since / until 子句
	for p.peek.kind == tokIdent {
		switch p.peek.text {
		case "since":
			p.next()
			if f.Since, err = p.expectNumber("since 版本"); err != nil {
				return nil, err
			}
		case "until":
			p.next()
			if f.Until, err = p.expectNumber("until 版本"); err != nil {
				return nil, err
			}
		default:
			return nil, &ParseError{Line: p.peek.line, Col: p.peek.col,
				Msg: "字段子句仅支持 since/until，得到 " + p.peek.text}
		}
	}
	if _, err = p.expect(tokSemi, "';'"); err != nil {
		return nil, err
	}
	return f, nil
}

func (p *parser) parseCompat(compat bool) (*CompatDecl, error) {
	cd := &CompatDecl{Compat: compat}
	var err error
	if cd.Message, err = p.expectIdent("消息名"); err != nil {
		return nil, err
	}
	if cd.A, err = p.expectNumber("版本 A"); err != nil {
		return nil, err
	}
	if cd.B, err = p.expectNumber("版本 B"); err != nil {
		return nil, err
	}
	if _, err = p.expect(tokSemi, "';'"); err != nil {
		return nil, err
	}
	return cd, nil
}

// builtinTypes 是内建标量类型集合。
var builtinTypes = map[string]bool{
	"int32": true, "int64": true, "bool": true, "string": true, "bytes": true,
}

// IsBuiltin 报告类型名是否为内建类型。
func IsBuiltin(t string) bool { return builtinTypes[t] }

// ExportedName 将 snake_case 字段名转为 Go 导出标识（稳定规则）。
func ExportedName(name string) string {
	parts := strings.Split(name, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	if b.Len() == 0 {
		return "X"
	}
	return b.String()
}
