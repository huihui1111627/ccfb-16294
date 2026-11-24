package zupu

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type tokenKind int

const (
	tEOF tokenKind = iota
	tIdent
	tVar
	tNumber
	tString
	tPunct
	tOp
)

type token struct {
	kind tokenKind
	lit  string
	line int
}

type lexer struct {
	src    string
	pos    int
	line   int
	tokens []token
}

func tokenize(src string) ([]token, error) {
	l := &lexer{src: src, line: 1}
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '\n' {
			l.line++
			l.pos++
			continue
		}
		if c == ' ' || c == '\t' || c == '\r' {
			l.pos++
			continue
		}
		if c == '#' || c == '%' || (c == '/' && l.peek(1) == '/') {
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
			continue
		}
		if c == '/' && l.peek(1) == '*' {
			l.pos += 2
			for l.pos+1 < len(l.src) && !(l.src[l.pos] == '*' && l.src[l.pos+1] == '/') {
				if l.src[l.pos] == '\n' {
					l.line++
				}
				l.pos++
			}
			l.pos += 2
			continue
		}
		startLine := l.line
		if c == '"' {
			start := l.pos
			l.pos++
			escaped := false
			for l.pos < len(l.src) {
				ch := l.src[l.pos]
				if ch == '\n' {
					l.line++
				}
				if escaped {
					escaped = false
				} else if ch == '\\' {
					escaped = true
				} else if ch == '"' {
					l.pos++
					break
				}
				l.pos++
			}
			raw := l.src[start:l.pos]
			v, err := strconv.Unquote(raw)
			if err != nil {
				return nil, fmt.Errorf("line %d: invalid string %s", startLine, raw)
			}
			l.tokens = append(l.tokens, token{tString, v, startLine})
			continue
		}
		if c == '?' {
			start := l.pos
			l.pos++
			for l.pos < len(l.src) && (isIdentByte(l.src[l.pos]) || unicode.IsDigit(rune(l.src[l.pos]))) {
				l.pos++
			}
			if l.pos == start+1 {
				return nil, fmt.Errorf("line %d: expected variable name after ?", startLine)
			}
			l.tokens = append(l.tokens, token{tVar, l.src[start:l.pos], startLine})
			continue
		}
		if isIdentStart(c) || c == '_' {
			start := l.pos
			l.pos++
			for l.pos < len(l.src) && isIdentByte(l.src[l.pos]) {
				l.pos++
			}
			l.tokens = append(l.tokens, token{tIdent, l.src[start:l.pos], startLine})
			continue
		}
		if unicode.IsDigit(rune(c)) {
			start := l.pos
			for l.pos < len(l.src) && unicode.IsDigit(rune(l.src[l.pos])) {
				l.pos++
			}
			if l.pos+1 < len(l.src) && l.src[l.pos] == '.' && unicode.IsDigit(rune(l.src[l.pos+1])) {
				for l.pos < len(l.src) && (unicode.IsDigit(rune(l.src[l.pos])) || l.src[l.pos] == '.') {
					l.pos++
				}
			}
			l.tokens = append(l.tokens, token{tNumber, l.src[start:l.pos], startLine})
			continue
		}
		two := ""
		if l.pos+1 < len(l.src) {
			two = l.src[l.pos : l.pos+2]
		}
		if two == ":-" || two == ":=" || two == "==" || two == "!=" || two == ">=" || two == "<=" {
			l.tokens = append(l.tokens, token{tOp, two, startLine})
			l.pos += 2
			continue
		}
		if strings.ContainsRune("+-*/<>=!,().[]", rune(c)) {
			kind := tPunct
			if strings.ContainsRune("+-*/<>=!", rune(c)) {
				kind = tOp
			}
			l.tokens = append(l.tokens, token{kind, string(c), startLine})
			l.pos++
			continue
		}
		return nil, fmt.Errorf("line %d: unexpected character %q", startLine, c)
	}
	l.tokens = append(l.tokens, token{tEOF, "", l.line})
	return l.tokens, nil
}

func (l *lexer) peek(offset int) byte {
	if l.pos+offset >= len(l.src) {
		return 0
	}
	return l.src[l.pos+offset]
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isIdentByte(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '-'
}

type parser struct {
	tokens []token
	pos    int
	wild   int
}

// ParseProgram parses facts and rules separated by '.'.
func ParseProgram(src string) (*Program, error) {
	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: toks}
	prog := &Program{}
	for !p.is(tEOF) {
		if p.isOp(".") {
			p.next()
			continue
		}
		startLine := p.cur().line
		head, err := p.parseAtom()
		if err != nil {
			return nil, err
		}
		if p.isOp(":-") {
			p.next()
			r := Rule{Head: head, Line: startLine}
			for !p.isOp(".") && !p.is(tEOF) {
				item, err := p.parseBodyItem()
				if err != nil {
					return nil, err
				}
				r.Body = append(r.Body, item)
				if p.isOp(",") {
					p.next()
					// Allow a trailing comma before '.'.
					if p.isOp(".") {
						break
					}
					continue
				}
				break
			}
			if err := p.expectOp("."); err != nil {
				return nil, err
			}
			prog.Rules = append(prog.Rules, r)
		} else {
			if err := p.expectOp("."); err != nil {
				return nil, err
			}
			args := make([]Value, len(head.Args))
			for i, a := range head.Args {
				lit, ok := a.(*Lit)
				if !ok {
					return nil, fmt.Errorf("line %d: fact %s argument %d must be a constant", startLine, head.Pred, i+1)
				}
				args[i] = lit.Value
			}
			prog.Facts = append(prog.Facts, Fact{Pred: head.Pred, Args: args, Line: startLine})
		}
	}
	return prog, nil
}

func (p *parser) cur() token          { return p.tokens[p.pos] }
func (p *parser) is(k tokenKind) bool { return p.cur().kind == k }
func (p *parser) isOp(lit string) bool {
	t := p.cur()
	return (t.kind == tOp || t.kind == tPunct) && t.lit == lit
}
func (p *parser) next() token {
	t := p.cur()
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return t
}
func (p *parser) expectOp(s string) error {
	if !p.isOp(s) {
		t := p.cur()
		return fmt.Errorf("line %d: expected %q, got %q", t.line, s, t.lit)
	}
	p.next()
	return nil
}
func (p *parser) expectIdent(s string) error {
	if !p.is(tIdent) || p.cur().lit != s {
		t := p.cur()
		return fmt.Errorf("line %d: expected keyword %q, got %q", t.line, s, t.lit)
	}
	p.next()
	return nil
}

func (p *parser) parseAtom() (Atom, error) {
	t := p.cur()
	if t.kind != tIdent {
		return Atom{}, fmt.Errorf("line %d: expected predicate name, got %q", t.line, t.lit)
	}
	p.next()
	a := Atom{Pred: t.lit, Line: t.line}
	if err := p.expectOp("("); err != nil {
		return Atom{}, err
	}
	for !p.isOp(")") {
		term, err := p.parseExpr()
		if err != nil {
			return Atom{}, err
		}
		a.Args = append(a.Args, term)
		if p.isOp(",") {
			p.next()
			continue
		}
		break
	}
	if err := p.expectOp(")"); err != nil {
		return Atom{}, err
	}
	return a, nil
}

func (p *parser) parseBodyItem() (BodyItem, error) {
	t := p.cur()
	if t.kind == tIdent && t.lit == "not" {
		p.next()
		a, err := p.parseAtom()
		if err != nil {
			return nil, err
		}
		return &NegatedAtom{Atom: a}, nil
	}
	if t.kind == tIdent && t.lit == "agg" {
		return p.parseAgg()
	}
	if t.kind == tIdent && t.lit == "let" {
		line := t.line
		p.next()
		v, err := p.parseVar()
		if err != nil {
			return nil, err
		}
		if err := p.expectOp("="); err != nil {
			return nil, err
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &Let{Out: v, Expr: e, Line: line}, nil
	}

	left, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.isComparisonOp() {
		op := p.next().lit
		right, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &Comparison{Op: op, Left: left, Right: right, Line: t.line}, nil
	}
	call, ok := left.(*Expr)
	if !ok || len(call.Args) == 0 {
		return nil, fmt.Errorf("line %d: body item must be a predicate atom, comparison, let or agg", t.line)
	}
	return &PositiveAtom{Atom: Atom{Pred: call.Op, Args: call.Args, Line: t.line}}, nil
}

func (p *parser) isComparisonOp() bool {
	if !p.is(tOp) {
		return false
	}
	switch p.cur().lit {
	case "=", "==", "!=", ">", "<", ">=", "<=":
		return true
	}
	return false
}

func (p *parser) parseVar() (*Var, error) {
	t := p.cur()
	if t.kind == tVar {
		p.next()
		return &Var{Name: t.lit, Index: -1}, nil
	}
	if t.kind == tIdent && t.lit == "_" {
		p.next()
		p.wild++
		return &Var{Name: fmt.Sprintf("__wild_%d", p.wild), Index: -1}, nil
	}
	return nil, fmt.Errorf("line %d: expected variable, got %q", t.line, t.lit)
}

func (p *parser) parseAgg() (BodyItem, error) {
	line := p.cur().line
	p.next() // agg
	if err := p.expectOp("("); err != nil {
		return nil, err
	}
	funcTok := p.cur()
	if funcTok.kind != tIdent {
		return nil, fmt.Errorf("line %d: expected aggregate function", line)
	}
	funcName := strings.ToLower(funcTok.lit)
	p.next()
	if err := p.expectOp("("); err != nil {
		return nil, err
	}
	spec := &AggregateSpec{Func: funcName, Line: line}
	if p.is(tIdent) && strings.EqualFold(p.cur().lit, "distinct") {
		spec.Distinct = true
		p.next()
	}
	if p.isOp("*") {
		if funcName != "count" {
			return nil, fmt.Errorf("line %d: * is only supported by count", line)
		}
		p.next()
	} else {
		v, err := p.parseVar()
		if err != nil {
			return nil, err
		}
		spec.Input = v
	}
	if err := p.expectOp(")"); err != nil {
		return nil, err
	}
	if err := p.expectOp(","); err != nil {
		return nil, err
	}
	src, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	spec.Source = src
	if err := p.expectOp(","); err != nil {
		return nil, err
	}
	if err := p.expectIdent("group"); err != nil {
		return nil, err
	}
	if err := p.expectOp("("); err != nil {
		return nil, err
	}
	for !p.isOp(")") {
		v, err := p.parseVar()
		if err != nil {
			return nil, err
		}
		spec.GroupBy = append(spec.GroupBy, v)
		if p.isOp(",") {
			p.next()
			continue
		}
		break
	}
	if err := p.expectOp(")"); err != nil {
		return nil, err
	}
	if err := p.expectOp(","); err != nil {
		return nil, err
	}
	out, err := p.parseVar()
	if err != nil {
		return nil, err
	}
	spec.Output = out
	if err := p.expectOp(")"); err != nil {
		return nil, err
	}
	return spec, nil
}

// Arithmetic precedence is intentionally small but sufficient for generation
// indices, distances and counts.
func (p *parser) parseExpr() (Term, error) { return p.parseAdd() }

func (p *parser) parseAdd() (Term, error) {
	left, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.is(tOp) && (p.cur().lit == "+" || p.cur().lit == "-") {
		op := p.next().lit
		right, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		left = &Expr{Op: op, Args: []Term{left, right}}
	}
	return left, nil
}

func (p *parser) parseMul() (Term, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.is(tOp) && (p.cur().lit == "*" || p.cur().lit == "/") {
		op := p.next().lit
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &Expr{Op: op, Args: []Term{left, right}}
	}
	return left, nil
}

func (p *parser) parseUnary() (Term, error) {
	if p.isOp("-") {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Expr{Op: "neg", Args: []Term{x}}, nil
	}
	if p.isOp("+") {
		p.next()
		return p.parseUnary()
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Term, error) {
	t := p.cur()
	switch t.kind {
	case tNumber:
		p.next()
		if i, err := strconv.ParseInt(t.lit, 10, 64); err == nil {
			return &Lit{Value: Int(i)}, nil
		}
		f, err := strconv.ParseFloat(t.lit, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid number %q", t.line, t.lit)
		}
		return &Lit{Value: Int(int64(f))}, nil
	case tString:
		p.next()
		return &Lit{Value: String(t.lit)}, nil
	case tVar:
		return p.parseVar()
	case tIdent:
		if t.lit == "_" {
			p.next()
			p.wild++
			return &Var{Name: fmt.Sprintf("__wild_%d", p.wild), Index: -1}, nil
		}
		if t.lit == "true" || t.lit == "false" {
			p.next()
			return &Lit{Value: Bool(t.lit == "true")}, nil
		}
		name := p.next().lit
		if p.isOp("(") {
			p.next()
			call := &Expr{Op: name}
			for !p.isOp(")") {
				a, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				call.Args = append(call.Args, a)
				if p.isOp(",") {
					p.next()
					continue
				}
				break
			}
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			return call, nil
		}
		// Unquoted identifiers are accepted as string constants in concise
		// generated programs, e.g. sex values M/F.
		return &Lit{Value: String(name)}, nil
	case tPunct:
		if t.lit == "(" {
			p.next()
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			return e, nil
		}
		if t.lit == "*" {
			p.next()
			return &Lit{Value: String("*")}, nil
		}
	}
	return nil, fmt.Errorf("line %d: expected expression, got %q", t.line, t.lit)
}
