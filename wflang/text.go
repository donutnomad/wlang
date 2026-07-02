package wflang

import (
	"strings"

	"github.com/donutnomad/wlang/ast"
	"github.com/donutnomad/wlang/compiler"
)

// CompileText parses wflang pseudocode and returns canonical executable JSON.
func CompileText(src []byte) ([]byte, error) {
	p := newTextParser(src)
	raw, err := p.parseProgramJSON()
	if err != nil {
		return nil, err
	}
	jsonRaw := mustJSON(raw)
	if _, err := compiler.ParseProgram(jsonRaw); err != nil {
		return nil, err
	}
	return FormatJSON(jsonRaw)
}

// ParsePseudoCode parses wflang pseudocode into the same AST used by JSON
// programs. CompileText exposes the canonical JSON form.
func ParsePseudoCode(src []byte) (*ast.Program, error) {
	raw, err := CompileText(src)
	if err != nil {
		return nil, err
	}
	return compiler.ParseProgram(raw)
}

type textParser struct {
	l       *textLexer
	imports map[string]bool
}

func newTextParser(src []byte) *textParser {
	return &textParser{l: newTextLexer(src), imports: map[string]bool{}}
}

func (p *textParser) parseProgramJSON() (any, error) {
	var imports []any
	var program []any
	p.skipNewlines()
	for p.peekLit("import") {
		p.take()
		name, err := p.parseDottedName()
		if err != nil {
			return nil, err
		}
		imports = append(imports, name)
		p.imports[rootName(name)] = true
		if err := p.consumeLineEnd(); err != nil {
			return nil, err
		}
		p.skipNewlines()
	}
	for !p.peekKind(textEOF) {
		stmt, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		program = append(program, stmt)
		p.skipNewlines()
	}
	env := map[string]any{"lang": "wflang/v1", "program": program}
	if len(imports) > 0 {
		env["imports"] = imports
	}
	return env, nil
}

func (p *textParser) parseStmt() (any, error) {
	p.skipNewlines()
	t := p.l.peek()
	if t.kind == textEOF {
		return nil, p.err(t, "expected statement")
	}
	if t.kind == textIdent {
		switch t.lit {
		case "let":
			p.take()
			return p.parseLet()
		case "return":
			p.take()
			return p.parseReturn()
		case "if":
			p.take()
			return p.parseIf()
		case "foreach":
			p.take()
			return p.parseForeach()
		case "for":
			p.take()
			return p.parseFori()
		case "break":
			p.take()
			_ = p.consumeLineEnd()
			return map[string]any{"break": map[string]any{}}, nil
		case "continue":
			p.take()
			_ = p.consumeLineEnd()
			return map[string]any{"continue": map[string]any{}}, nil
		case "panic":
			p.take()
			return p.parsePanic()
		case "defer":
			p.take()
			ex, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			_ = p.consumeLineEnd()
			return map[string]any{"defer": ex}, nil
		case "select":
			p.take()
			return p.parseSelect()
		}
	}
	ex, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	if p.accept("=") {
		rhs, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		_ = p.consumeLineEnd()
		return assignmentJSON(ex, rhs), nil
	}
	_ = p.consumeLineEnd()
	return exprStmtJSON(ex), nil
}

func (p *textParser) parseLet() (any, error) {
	type binding struct {
		name string
		typ  string
	}
	var bindings []binding
	for {
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		b := binding{name: name}
		if p.accept(":") {
			typ, err := p.parseTypeName(",", "=")
			if err != nil {
				return nil, err
			}
			b.typ = typ
		}
		bindings = append(bindings, b)
		if !p.accept(",") {
			break
		}
	}
	if err := p.expect("="); err != nil {
		return nil, err
	}
	ex, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	_ = p.consumeLineEnd()
	if len(bindings) == 1 && bindings[0].name != "_" {
		body := map[string]any{}
		if bindings[0].typ != "" {
			body[bindings[0].name] = map[string]any{"type": bindings[0].typ, "value": ex}
		} else {
			body[bindings[0].name] = ex
		}
		return map[string]any{"let": body}, nil
	}
	names := make([]any, len(bindings))
	types := make([]any, len(bindings))
	hasType := false
	for i, b := range bindings {
		names[i] = b.name
		types[i] = b.typ
		hasType = hasType || b.typ != ""
	}
	if hasType {
		return map[string]any{"let": []any{names, types, ex}}, nil
	}
	return map[string]any{"let": []any{names, ex}}, nil
}

func (p *textParser) parseReturn() (any, error) {
	if p.peekLit("named") {
		p.take()
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		_ = p.consumeLineEnd()
		return map[string]any{"return": map[string]any{"named": name}}, nil
	}
	ex, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	_ = p.consumeLineEnd()
	return map[string]any{"return": ex}, nil
}

func (p *textParser) parseIf() (any, error) {
	cond, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	thenBody, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	body := map[string]any{"cond": cond, "then": thenBody}
	p.skipNewlines()
	if p.peekLit("else") {
		p.take()
		elseBody, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		body["else"] = elseBody
	}
	return map[string]any{"if": body}, nil
}

func (p *textParser) parseForeach() (any, error) {
	as, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	idx := ""
	if p.accept(",") {
		idx, err = p.expectIdent()
		if err != nil {
			return nil, err
		}
	}
	if err := p.expectIdentLit("in"); err != nil {
		return nil, err
	}
	target, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	do, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	body := map[string]any{"target": target, "as": as, "do": do}
	if idx != "" {
		body["index"] = idx
	}
	return map[string]any{"foreach": body}, nil
}

func (p *textParser) parseFori() (any, error) {
	vr, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	if err := p.expect(":="); err != nil {
		return nil, err
	}
	from, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	if err := p.expect(";"); err != nil {
		return nil, err
	}
	condVar, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	if condVar != vr {
		return nil, p.err(p.l.peek(), "for condition variable must match "+vr)
	}
	if err := p.expect("<"); err != nil {
		return nil, err
	}
	to, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"var": vr, "from": from, "to": to}
	if p.accept(";") {
		if err := p.expectIdentLit("step"); err != nil {
			return nil, err
		}
		step, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		body["step"] = step
	}
	do, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	body["do"] = do
	return map[string]any{"fori": body}, nil
}

func (p *textParser) parsePanic() (any, error) {
	if err := p.expect("("); err != nil {
		return nil, err
	}
	ex, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	if err := p.expect(")"); err != nil {
		return nil, err
	}
	_ = p.consumeLineEnd()
	return map[string]any{"panic": ex}, nil
}

func (p *textParser) parseSelect() (any, error) {
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	var arms []any
	p.skipNewlines()
	for !p.accept("}") {
		switch {
		case p.peekLit("recv"):
			p.take()
			ch, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			inner := map[string]any{"recv": []any{ch}}
			if p.accept("->") {
				var binds []any
				name, err := p.expectIdent()
				if err != nil {
					return nil, err
				}
				binds = append(binds, name)
				if p.accept(",") {
					name, err = p.expectIdent()
					if err != nil {
						return nil, err
					}
					binds = append(binds, name)
				}
				inner["bind"] = binds
			}
			do, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			inner["do"] = do
			arms = append(arms, map[string]any{"case": inner})
		case p.peekLit("send"):
			p.take()
			ch, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			if err := p.expect("<-"); err != nil {
				return nil, err
			}
			val, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			do, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			arms = append(arms, map[string]any{"case": map[string]any{"send": []any{ch, val}, "do": do}})
		case p.peekLit("default"):
			p.take()
			do, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			arms = append(arms, map[string]any{"default": do})
		default:
			return nil, p.err(p.l.peek(), "expected select arm")
		}
		p.skipNewlines()
	}
	return map[string]any{"select": arms}, nil
}

func (p *textParser) parseBlock() ([]any, error) {
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	var body []any
	p.skipNewlines()
	for !p.accept("}") {
		if p.peekKind(textEOF) {
			return nil, p.err(p.l.peek(), "expected }")
		}
		stmt, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		body = append(body, stmt)
		p.skipNewlines()
	}
	return body, nil
}

func (p *textParser) parseDottedName() (string, error) {
	name, err := p.expectIdent()
	if err != nil {
		return "", err
	}
	var parts []string
	parts = append(parts, name)
	for p.accept(".") {
		part, err := p.expectIdent()
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "."), nil
}

func (p *textParser) parseTypeName(stops ...string) (string, error) {
	stop := map[string]bool{}
	for _, s := range stops {
		stop[s] = true
	}
	var b strings.Builder
	depth := 0
	for {
		t := p.l.peek()
		if t.kind == textEOF || t.kind == textNewline {
			if b.Len() == 0 {
				return "", p.err(t, "expected type name")
			}
			return b.String(), nil
		}
		if depth == 0 && stop[t.lit] {
			if b.Len() == 0 {
				return "", p.err(t, "expected type name")
			}
			return b.String(), nil
		}
		if t.lit == "<" {
			depth++
		}
		if t.lit == ">" {
			if depth == 0 && stop[">"] {
				if b.Len() == 0 {
					return "", p.err(t, "expected type name")
				}
				return b.String(), nil
			}
			depth--
		}
		p.take()
		b.WriteString(t.lit)
	}
}
