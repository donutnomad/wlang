package wflang

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (p *textParser) skipNewlines() {
	for p.peekKind(textNewline) {
		p.take()
	}
}

func (p *textParser) consumeLineEnd() error {
	if p.peekKind(textEOF) || p.peekLit("}") {
		return nil
	}
	if p.peekKind(textNewline) {
		p.skipNewlines()
		return nil
	}
	return p.err(p.l.peek(), "expected line end")
}

func (p *textParser) peekKind(k textTokenKind) bool {
	return p.l.peek().kind == k
}

func (p *textParser) peekLit(lit string) bool {
	return p.l.peek().lit == lit
}

func (p *textParser) accept(lit string) bool {
	if p.peekLit(lit) {
		p.take()
		return true
	}
	return false
}

func (p *textParser) take() textToken { return p.l.take() }

func (p *textParser) expect(lit string) error {
	t := p.l.take()
	if t.lit != lit {
		return p.err(t, "expected "+lit)
	}
	return nil
}

func (p *textParser) expectIdent() (string, error) {
	t := p.l.take()
	if t.kind != textIdent {
		return "", p.err(t, "expected identifier")
	}
	return t.lit, nil
}

func (p *textParser) expectIdentLit(lit string) error {
	t := p.l.take()
	if t.kind != textIdent || t.lit != lit {
		return p.err(t, "expected "+lit)
	}
	return nil
}

func (p *textParser) err(t textToken, msg string) error {
	return fmt.Errorf("text parse: line %d, column %d: %s", t.line, t.col, msg)
}

func infixPrec(op string) (int, bool) {
	switch op {
	case "??":
		return 1, true
	case "or":
		return 2, true
	case "and":
		return 3, true
	case "==", "!=", ">", ">=", "<", "<=", "contains", "startsWith", "endsWith":
		return 4, true
	case "+", "-":
		return 5, true
	case "*", "/":
		return 6, true
	}
	return 0, false
}

func exprTypeStops() []string {
	return []string{
		")", "]", "}", ",",
		"??", "or", "and",
		"==", "!=", ">", ">=", "<", "<=", "contains", "startsWith", "endsWith",
		"+", "-", "*", "/",
	}
}

func literalJSON(typ string, value any) any {
	return map[string]any{"literal": map[string]any{"type": typ, "value": value}}
}

func exprStmtJSON(ex any) any {
	return map[string]any{"expr": ex}
}

func assignmentJSON(lhs, rhs any) any {
	if v, ok := lhs.(map[string]any)["var"].(string); ok {
		return map[string]any{"set": map[string]any{v: rhs}}
	}
	if c, ok := singleCall(lhs); ok && len(c.args) == 2 {
		switch c.op {
		case "arr.get":
			return exprStmtJSON(map[string]any{"arr.set": []any{c.args[0], c.args[1], rhs}})
		case "map.value", "map.get":
			return exprStmtJSON(map[string]any{"map.set": []any{c.args[0], c.args[1], rhs}})
		}
	}
	return exprStmtJSON(map[string]any{"=": []any{lhs, rhs}})
}

type callShape struct {
	op   string
	args []any
}

func singleCall(v any) (callShape, bool) {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return callShape{}, false
	}
	for k, raw := range m {
		if arr, ok := raw.([]any); ok {
			return callShape{op: k, args: arr}, true
		}
	}
	return callShape{}, false
}

func isStringLiteral(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	l, ok := m["literal"].(map[string]any)
	return ok && l["type"] == "string"
}

func rootName(name string) string {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i]
	}
	return name
}

func (p *textParser) packageCallParts(name string) (string, string, bool) {
	root := rootName(name)
	if root == name {
		return "", "", false
	}
	if !p.imports[root] && !isLikelyPackageRoot(root) {
		return "", "", false
	}
	return root, strings.TrimPrefix(name, root+"."), true
}

func isLikelyPackageRoot(name string) bool {
	if name == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name)
	return r == '_' || unicode.IsLower(r)
}
