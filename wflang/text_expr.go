package wflang

import (
	"encoding/json"
	"strings"
)

func (p *textParser) parseExpr(minPrec int) (any, error) {
	left, err := p.parsePrefix()
	if err != nil {
		return nil, err
	}
	for {
		op := p.l.peek()
		if op.kind != textIdent && op.kind != textSymbol {
			break
		}
		prec, ok := infixPrec(op.lit)
		if !ok || prec < minPrec {
			break
		}
		p.take()
		right, err := p.parseExpr(prec + 1)
		if err != nil {
			return nil, err
		}
		if op.lit == "??" {
			if v, ok := left.(map[string]any)["var"].(string); ok {
				left = map[string]any{"var": []any{v, right}}
			} else {
				return nil, p.err(op, "?? left operand must be a variable")
			}
			continue
		}
		left = map[string]any{op.lit: []any{left, right}}
	}
	return left, nil
}

func (p *textParser) parsePrefix() (any, error) {
	if p.accept("-") {
		t := p.l.peek()
		if t.kind != textNumber {
			return nil, p.err(t, "expected number after -")
		}
		p.take()
		lit := "-" + t.lit
		if strings.Contains(t.lit, ".") {
			return literalJSON("float64", lit), nil
		}
		return literalJSON("int64", lit), nil
	}
	if p.accept("!") {
		ex, err := p.parseExpr(8)
		if err != nil {
			return nil, err
		}
		return map[string]any{"!": []any{ex}}, nil
	}
	if p.accept("&") {
		name, err := p.parseDottedName()
		if err != nil {
			return nil, err
		}
		return map[string]any{"out": name}, nil
	}
	if p.peekLit("symbol") {
		p.take()
		name, err := p.parseDottedName()
		if err != nil {
			return nil, err
		}
		return map[string]any{"symbol": name}, nil
	}
	if p.peekLit("method") {
		p.take()
		return p.parseMethodValue()
	}
	if p.peekLit("zero") {
		p.take()
		typ, err := p.parseTypeName(exprTypeStops()...)
		if err != nil {
			return nil, err
		}
		return map[string]any{"zero": typ}, nil
	}
	return p.parsePostfix()
}

func (p *textParser) parsePostfix() (any, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		if p.accept(".") {
			name, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			if name == "length" {
				left = map[string]any{"arr.len": []any{left}}
				continue
			}
			if p.peekLit("(") {
				args, err := p.parseCallArgs()
				if err != nil {
					return nil, err
				}
				left = map[string]any{name: append([]any{left}, args...)}
				continue
			}
			if v, ok := left.(map[string]any)["var"].(string); ok {
				left = map[string]any{"var": v + "." + name}
				continue
			}
			left = map[string]any{"method": []any{left, name}}
			continue
		}
		if p.accept("[") {
			from, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			if p.accept(":") {
				to, err := p.parseExpr(0)
				if err != nil {
					return nil, err
				}
				if err := p.expect("]"); err != nil {
					return nil, err
				}
				left = map[string]any{"arr.slice": []any{left, from, to}}
				continue
			}
			if err := p.expect("]"); err != nil {
				return nil, err
			}
			op := "arr.get"
			if isStringLiteral(from) {
				op = "map.value"
			}
			left = map[string]any{op: []any{left, from}}
			continue
		}
		break
	}
	return left, nil
}

func (p *textParser) parsePrimary() (any, error) {
	t := p.l.peek()
	switch t.kind {
	case textString:
		p.take()
		var s string
		if err := json.Unmarshal([]byte(t.lit), &s); err != nil {
			return nil, p.err(t, "invalid string literal")
		}
		return literalJSON("string", s), nil
	case textNumber:
		p.take()
		if strings.Contains(t.lit, ".") {
			return literalJSON("float64", t.lit), nil
		}
		return literalJSON("int64", t.lit), nil
	case textIdent:
		return p.parseIdentPrimary()
	case textSymbol:
		if t.lit == "(" {
			p.take()
			ex, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			if err := p.expect(")"); err != nil {
				return nil, err
			}
			return ex, nil
		}
	}
	return nil, p.err(t, "expected expression")
}

func (p *textParser) parseIdentPrimary() (any, error) {
	t := p.l.peek()
	switch t.lit {
	case "true":
		p.take()
		return literalJSON("boolean", true), nil
	case "false":
		p.take()
		return literalJSON("boolean", false), nil
	case "null":
		p.take()
		return map[string]any{"literal": map[string]any{"type": "null", "value": nil}}, nil
	case "fn":
		p.take()
		return p.parseFuncLit()
	case "call":
		p.take()
		return p.parseFuncCall()
	case "array":
		p.take()
		return p.parseArrayLit()
	case "map":
		p.take()
		if !p.peekLit("<") {
			return p.parseBuiltinOrDottedPrimary("map")
		}
		return p.parseMapLit()
	case "struct":
		p.take()
		return p.parseStructLit()
	case "chan":
		p.take()
		return p.parseChanLit()
	case "match":
		p.take()
		return p.parseMatch()
	case "routine":
		p.take()
		return p.parseRoutineExpr()
	case "pkg":
		p.take()
		return p.parseExplicitPackageCall()
	}
	name, err := p.parseDottedName()
	if err != nil {
		return nil, err
	}
	return p.parseNamedPrimary(name)
}

func (p *textParser) parseBuiltinOrDottedPrimary(root string) (any, error) {
	name := root
	for p.accept(".") {
		part, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		name += "." + part
	}
	return p.parseNamedPrimary(name)
}

func (p *textParser) parseNamedPrimary(name string) (any, error) {
	if p.peekLit("(") {
		args, err := p.parseCallArgs()
		if err != nil {
			return nil, err
		}
		if name == "await" && len(args) == 1 {
			return map[string]any{"await": args[0]}, nil
		}
		if name == "delete" && len(args) == 2 {
			return map[string]any{"map.del": args}, nil
		}
		if isBuiltinCallOp(name) {
			return map[string]any{canonicalDisplayOp(name): args}, nil
		}
		if root, op, ok := p.packageCallParts(name); ok {
			return map[string]any{op: append([]any{map[string]any{"pkg": root}}, args...)}, nil
		}
		return map[string]any{name: args}, nil
	}
	if p.imports[rootName(name)] && !strings.Contains(name, ".") {
		return map[string]any{"pkg": name}, nil
	}
	return map[string]any{"var": name}, nil
}

func (p *textParser) parseFuncLit() (any, error) {
	if err := p.expect("("); err != nil {
		return nil, err
	}
	var params []any
	if !p.accept(")") {
		for {
			name, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			if err := p.expect(":"); err != nil {
				return nil, err
			}
			typ, err := p.parseTypeName(",", ")")
			if err != nil {
				return nil, err
			}
			params = append(params, []any{name, typ})
			if p.accept(")") {
				break
			}
			if err := p.expect(","); err != nil {
				return nil, err
			}
		}
	}
	var returns []any
	if p.accept("->") {
		for {
			typ, err := p.parseTypeName(",", "{")
			if err != nil {
				return nil, err
			}
			returns = append(returns, typ)
			if !p.accept(",") {
				break
			}
		}
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	out := map[string]any{"params": params, "do": body}
	if len(returns) > 0 {
		out["returns"] = returns
	}
	return map[string]any{"fn": out}, nil
}

func (p *textParser) parseFuncCall() (any, error) {
	fn, err := p.parseFuncTarget()
	if err != nil {
		return nil, err
	}
	args, err := p.parseCallArgs()
	if err != nil {
		return nil, err
	}
	return map[string]any{"call": map[string]any{"fn": fn, "args": args}}, nil
}

func (p *textParser) parseFuncTarget() (any, error) {
	if p.peekLit("method") {
		p.take()
		if p.peekLit("(") {
			return map[string]any{"var": "method"}, nil
		}
		return p.parseMethodValue()
	}
	name, err := p.parseDottedName()
	if err != nil {
		return nil, err
	}
	if strings.Contains(name, ".") {
		parts := strings.Split(name, ".")
		recv := map[string]any{"var": strings.Join(parts[:len(parts)-1], ".")}
		return map[string]any{"method": []any{recv, parts[len(parts)-1]}}, nil
	}
	return map[string]any{"var": name}, nil
}

func (p *textParser) parseMethodValue() (any, error) {
	if p.accept("(") {
		recv, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		if err := p.expect("."); err != nil {
			return nil, err
		}
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		return map[string]any{"method": []any{recv, name}}, nil
	}
	name, err := p.parseDottedName()
	if err != nil {
		return nil, err
	}
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return nil, p.err(p.l.peek(), "method value requires receiver.method")
	}
	recv := map[string]any{"var": strings.Join(parts[:len(parts)-1], ".")}
	return map[string]any{"method": []any{recv, parts[len(parts)-1]}}, nil
}

func (p *textParser) parseExplicitPackageCall() (any, error) {
	name, err := p.parseDottedName()
	if err != nil {
		return nil, err
	}
	root := rootName(name)
	if root == name {
		return map[string]any{"pkg": root}, nil
	}
	args, err := p.parseCallArgs()
	if err != nil {
		return nil, err
	}
	return map[string]any{strings.TrimPrefix(name, root+"."): append([]any{map[string]any{"pkg": root}}, args...)}, nil
}

func (p *textParser) parseArrayLit() (any, error) {
	if err := p.expect("<"); err != nil {
		return nil, err
	}
	elem, err := p.parseTypeName(">")
	if err != nil {
		return nil, err
	}
	if err := p.expect(">"); err != nil {
		return nil, err
	}
	if err := p.expect("["); err != nil {
		return nil, err
	}
	items, err := p.parseExprList("]")
	if err != nil {
		return nil, err
	}
	return map[string]any{"array": map[string]any{"elem": elem, "items": items}}, nil
}

func (p *textParser) parseMapLit() (any, error) {
	if err := p.expect("<"); err != nil {
		return nil, err
	}
	keyType, err := p.parseTypeName(",")
	if err != nil {
		return nil, err
	}
	if err := p.expect(","); err != nil {
		return nil, err
	}
	valType, err := p.parseTypeName(">")
	if err != nil {
		return nil, err
	}
	if err := p.expect(">"); err != nil {
		return nil, err
	}
	if p.accept("{") {
		var entries []any
		p.skipNewlines()
		for !p.accept("}") {
			k, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			if err := p.expect(":"); err != nil {
				return nil, err
			}
			v, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			entries = append(entries, []any{k, v})
			p.skipNewlines()
		}
		return map[string]any{"map": map[string]any{"type": []any{keyType, valType}, "entries": entries}}, nil
	}
	return nil, p.err(p.l.peek(), "expected map literal body")
}

func (p *textParser) parseStructLit() (any, error) {
	typ, err := p.parseTypeName("{")
	if err != nil {
		return nil, err
	}
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	fields := map[string]any{}
	p.skipNewlines()
	for !p.accept("}") {
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		if err := p.expect(":"); err != nil {
			return nil, err
		}
		ex, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		fields[name] = ex
		p.skipNewlines()
	}
	return map[string]any{"struct": []any{typ, fields}}, nil
}

func (p *textParser) parseChanLit() (any, error) {
	if err := p.expect("<"); err != nil {
		return nil, err
	}
	elem, err := p.parseTypeName(">")
	if err != nil {
		return nil, err
	}
	if err := p.expect(">"); err != nil {
		return nil, err
	}
	if err := p.expect("("); err != nil {
		return nil, err
	}
	out := []any{elem}
	if !p.accept(")") {
		buf, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		out = append(out, buf)
		if err := p.expect(")"); err != nil {
			return nil, err
		}
	}
	return map[string]any{"chan": out}, nil
}

func (p *textParser) parseMatch() (any, error) {
	value, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	var cases []any
	var def []any
	p.skipNewlines()
	for !p.accept("}") {
		if p.peekLit("when") {
			p.take()
			when, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			do, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			cases = append(cases, map[string]any{"when": when, "do": do})
		} else if p.peekLit("default") {
			p.take()
			def, err = p.parseBlock()
			if err != nil {
				return nil, err
			}
		} else {
			return nil, p.err(p.l.peek(), "expected match arm")
		}
		p.skipNewlines()
	}
	body := map[string]any{"value": value, "cases": cases}
	if def != nil {
		body["default"] = def
	}
	return map[string]any{"match": body}, nil
}

func (p *textParser) parseRoutineExpr() (any, error) {
	if p.peekLit("{") {
		body, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		return map[string]any{"routine": map[string]any{"do": body}}, nil
	}
	call, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	return map[string]any{"routine": call}, nil
}

func (p *textParser) parseCallArgs() ([]any, error) {
	if err := p.expect("("); err != nil {
		return nil, err
	}
	return p.parseExprList(")")
}

func (p *textParser) parseExprList(end string) ([]any, error) {
	var out []any
	if p.accept(end) {
		return out, nil
	}
	for {
		ex, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		out = append(out, ex)
		if p.accept(end) {
			return out, nil
		}
		if err := p.expect(","); err != nil {
			return nil, err
		}
	}
}
