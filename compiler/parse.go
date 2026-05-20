// Package compiler implements wflang's compilation pipeline (LANGUAGE.md §7).
// Stages: Decode → Normalize → Parse → Resolve → TypeCheck → Capability → Lower.
// This file handles Decode+Parse. Resolve/TypeCheck live in resolve.go.
package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/wflang/wflang/ast"
	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/types"
)

// literalScalarToString normalizes a JSON scalar into the canonical string
// representation expected by types.NewLiteral.
func literalScalarToString(raw any) (string, error) {
	switch rv := raw.(type) {
	case string:
		return rv, nil
	case bool:
		return fmt.Sprintf("%v", rv), nil
	case json.Number:
		return rv.String(), nil
	case nil:
		return "", fmt.Errorf("value cannot be null")
	}
	return "", fmt.Errorf("unsupported scalar type %T", raw)
}

// Built-in operator keywords (single-key AST nodes that are NOT host calls).
var builtinKeywords = map[string]bool{
	"literal":  true,
	"var":      true,
	"pkg":      true,
	"if":       true,
	"let":      true,
	"set":      true,
	"return":   true,
	"foreach":  true,
	"fori":     true,
	"break":    true,
	"continue": true,
	"panic":    true,
	"expr":     true,
	"routine":  true,
	"await":    true,
	"array":    true,
	"defer":    true,
	"map":      true,
	"struct":   true,
	"chan":     true,
	"select":   true,
	// Logical / boolean operators
	"and": true,
	"or":  true,
	"!":   true,
	// Arithmetic / comparison / string operators
	"+":          true,
	"-":          true,
	"*":          true,
	"/":          true,
	">":          true,
	">=":         true,
	"<":          true,
	"<=":         true,
	"==":         true,
	"!=":         true,
	"contains":   true,
	"endsWith":   true,
	"startsWith": true,
}

// IsBuiltinOperator reports whether op is a builtin.
func IsBuiltinOperator(op string) bool { return builtinKeywords[op] }

// ParseProgram parses a full program or envelope.
func ParseProgram(raw []byte) (*ast.Program, error) {
	var any any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&any); err != nil {
		return nil, werr.Newf(werr.CodeJSONDecode, "json decode failed: %v", err)
	}
	return parseProgram(any)
}

func parseProgram(v any) (*ast.Program, error) {
	switch x := v.(type) {
	case []any:
		stmts, err := parseStatements(x, "/program")
		if err != nil {
			return nil, err
		}
		return &ast.Program{Lang: "wflang/v1", Body: stmts}, nil
	case map[string]any:
		// envelope: {lang, imports, program}
		if _, ok := x["lang"]; ok || x["program"] != nil {
			lang, _ := x["lang"].(string)
			if lang == "" {
				lang = "wflang/v1"
			}
			var imports []string
			if arr, ok := x["imports"].([]any); ok {
				for _, im := range arr {
					if s, ok := im.(string); ok {
						imports = append(imports, s)
					}
				}
			}
			progRaw, ok := x["program"].([]any)
			if !ok {
				return nil, werr.New(werr.CodeASTShape,
					"envelope.program must be an array of statements")
			}
			stmts, err := parseStatements(progRaw, "/program")
			if err != nil {
				return nil, err
			}
			return &ast.Program{Lang: lang, Imports: imports, Body: stmts}, nil
		}
		// Single statement wrapper
		n, err := parseStatement(x, "/program/0")
		if err != nil {
			return nil, err
		}
		return &ast.Program{Lang: "wflang/v1", Body: []ast.Node{n}}, nil
	default:
		return nil, werr.Newf(werr.CodeASTShape,
			"program must be array or envelope, got %T", v)
	}
}

func parseStatements(arr []any, base string) ([]ast.Node, error) {
	out := make([]ast.Node, 0, len(arr))
	for i, s := range arr {
		path := fmt.Sprintf("%s/%d", base, i)
		n, err := parseStatement(s, path)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// parseStatement parses a single statement object.
// It reuses parseNode for expression-shaped elements (if/expr/panic have their
// own statement fields so they need dedicated handling).
func parseStatement(v any, path string) (ast.Node, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"statement must be an object, got %T", v).WithPath(path)
	}
	keys := sortedKeys(m)
	if len(keys) != 1 {
		return nil, werr.Newf(werr.CodeASTShape,
			"statement must have exactly one key, got %d (keys=%v)", len(keys), keys).
			WithPath(path)
	}
	return parseNodeWithKey(keys[0], m[keys[0]], path)
}

// parseNode parses a single-key expression/statement object or primitive.
func parseNode(v any, path string) (ast.Node, error) {
	switch x := v.(type) {
	case map[string]any:
		keys := sortedKeys(x)
		if len(keys) != 1 {
			return nil, werr.Newf(werr.CodeASTShape,
				"operator expression must have exactly one key, got %d (keys=%v)",
				len(keys), keys).WithPath(path)
		}
		return parseNodeWithKey(keys[0], x[keys[0]], path)
	default:
		// Reject bare JSON primitives (TC-100).
		return nil, werr.Newf(werr.CodeASTShape,
			"bare JSON %T is not allowed; use {\"literal\":{...}}", v).WithPath(path)
	}
}

func parseNodeWithKey(key string, body any, path string) (ast.Node, error) {
	nodePath := path + "/" + key
	switch key {
	case "literal":
		return parseLiteral(body, nodePath)
	case "var":
		return parseVar(body, nodePath)
	case "pkg":
		return parsePkg(body, nodePath)
	case "if":
		return parseIf(body, nodePath)
	case "let":
		return parseLet(body, nodePath)
	case "set":
		return parseSet(body, nodePath)
	case "return":
		return parseReturn(body, nodePath)
	case "foreach":
		return parseForeach(body, nodePath)
	case "fori":
		return parseFori(body, nodePath)
	case "break":
		return &ast.Break{Base: ast.Base{P: nodePath}}, nil
	case "continue":
		return &ast.Continue{Base: ast.Base{P: nodePath}}, nil
	case "panic":
		expr, err := parseExpr(body, nodePath)
		if err != nil {
			return nil, err
		}
		return &ast.Panic{Base: ast.Base{P: nodePath}, Expr: expr}, nil
	case "expr":
		expr, err := parseExpr(body, nodePath)
		if err != nil {
			return nil, err
		}
		return &ast.ExprStmt{Base: ast.Base{P: nodePath}, Expr: expr}, nil
	case "routine":
		return parseRoutine(body, nodePath)
	case "await":
		return parseAwait(body, nodePath)
	case "array":
		return parseArrayLit(body, nodePath)
	case "defer":
		return parseDefer(body, nodePath)
	case "map":
		return parseMapLit(body, nodePath)
	case "struct":
		return parseStructLit(body, nodePath)
	case "chan":
		return parseChanLit(body, nodePath)
	case "select":
		return parseSelect(body, nodePath)
	case "match":
		return parseMatch(body, nodePath)
	}
	// Any other key is an operator call. Args must be an array.
	args, err := parseArgList(body, nodePath)
	if err != nil {
		return nil, err
	}
	return &ast.Call{Base: ast.Base{P: nodePath}, Op: key, Args: args}, nil
}

func parseExpr(v any, path string) (ast.Node, error) {
	return parseNode(v, path)
}

func parseArgList(body any, path string) ([]ast.Node, error) {
	arr, ok := body.([]any)
	if !ok {
		// Accept a single-argument form to be normalized (§7.2)
		n, err := parseNode(body, path+"/0")
		if err != nil {
			return nil, werr.Newf(werr.CodeASTShape,
				"operator args must be an array").WithPath(path)
		}
		return []ast.Node{n}, nil
	}
	out := make([]ast.Node, 0, len(arr))
	for i, a := range arr {
		sub := fmt.Sprintf("%s/%d", path, i)
		n, err := parseNode(a, sub)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func parseLiteral(body any, path string) (ast.Node, error) {
	m, ok := body.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"literal body must be an object").WithPath(path)
	}
	typ, _ := m["type"].(string)
	if typ == "" {
		return nil, werr.Newf(werr.CodeASTShape,
			"literal.type must be a non-empty string").WithPath(path)
	}
	rawVal, hasVal := m["value"]
	if !hasVal {
		return nil, werr.Newf(werr.CodeASTShape,
			"literal.value is required").WithPath(path)
	}
	// null literal: value is JSON null.
	if typ == types.TNull {
		if rawVal != nil {
			return nil, werr.New(werr.CodeASTShape,
				"null literal must have value:null").WithPath(path)
		}
		v, _ := types.NewNull()
		return &ast.Literal{Base: ast.Base{P: path}, Value: v}, nil
	}
	// array<T> typed literal: value is JSON array of typed raw elements.
	if strings.HasPrefix(typ, "array<") && strings.HasSuffix(typ, ">") {
		elem := typ[6 : len(typ)-1]
		arr, ok := rawVal.([]any)
		if !ok {
			return nil, werr.Newf(werr.CodeASTShape,
				"array<%s> literal value must be JSON array, got %T", elem, rawVal).WithPath(path)
		}
		elems := make([]types.Value, 0, len(arr))
		for i, raw := range arr {
			rs, err := literalScalarToString(raw)
			if err != nil {
				return nil, werr.Newf(werr.CodeASTShape,
					"array<%s> element %d: %v", elem, i, err).WithPath(path)
			}
			ev, err := types.NewLiteral(elem, rs)
			if err != nil {
				if le, ok := err.(*werr.LangError); ok {
					return nil, le.WithPath(path)
				}
				return nil, err
			}
			elems = append(elems, ev)
		}
		v, err := types.NewArray(elem, elems)
		if err != nil {
			if le, ok := err.(*werr.LangError); ok {
				return nil, le.WithPath(path)
			}
			return nil, err
		}
		return &ast.Literal{Base: ast.Base{P: path}, Value: v}, nil
	}
	// boolean allows either "true"/"false" string or a bool JSON.
	var rawStr string
	switch rv := rawVal.(type) {
	case string:
		rawStr = rv
	case bool:
		rawStr = fmt.Sprintf("%v", rv)
	case json.Number:
		rawStr = rv.String()
	default:
		return nil, werr.Newf(werr.CodeASTShape,
			"literal.value must be string/number/bool (got %T)", rawVal).WithPath(path)
	}
	v, err := types.NewLiteral(typ, rawStr)
	if err != nil {
		if le, ok := err.(*werr.LangError); ok {
			return nil, le.WithPath(path)
		}
		return nil, err
	}
	return &ast.Literal{Base: ast.Base{P: path}, Value: v}, nil
}

func parseVar(body any, path string) (ast.Node, error) {
	switch b := body.(type) {
	case string:
		return &ast.Var{Base: ast.Base{P: path}, Name: b}, nil
	case []any:
		// ["name"] or ["name", defaultExpr]
		if len(b) == 0 || len(b) > 2 {
			return nil, werr.Newf(werr.CodeASTShape,
				"var array form expects 1 or 2 elements, got %d", len(b)).WithPath(path)
		}
		name, ok := b[0].(string)
		if !ok {
			return nil, werr.Newf(werr.CodeASTShape,
				"var path must be a string").WithPath(path)
		}
		var def ast.Node
		if len(b) == 2 {
			d, err := parseExpr(b[1], path+"/1")
			if err != nil {
				return nil, err
			}
			def = d
		}
		return &ast.Var{Base: ast.Base{P: path}, Name: name, Default: def}, nil
	default:
		return nil, werr.Newf(werr.CodeASTShape,
			"var body must be string or [string,default]").WithPath(path)
	}
}

func parsePkg(body any, path string) (ast.Node, error) {
	s, ok := body.(string)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"pkg body must be string").WithPath(path)
	}
	return &ast.Pkg{Base: ast.Base{P: path}, Name: s}, nil
}

func parseIf(body any, path string) (ast.Node, error) {
	m, ok := body.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"if body must be object").WithPath(path)
	}
	cond, err := parseExpr(m["cond"], path+"/cond")
	if err != nil {
		return nil, err
	}
	var thenNodes, elseNodes []ast.Node
	if t, ok := m["then"].([]any); ok {
		thenNodes, err = parseStatements(t, path+"/then")
		if err != nil {
			return nil, err
		}
	}
	if e, ok := m["else"].([]any); ok {
		elseNodes, err = parseStatements(e, path+"/else")
		if err != nil {
			return nil, err
		}
	}
	return &ast.IfStmt{Base: ast.Base{P: path}, Cond: cond, Then: thenNodes, Else: elseNodes}, nil
}

// parseLet handles the accepted shapes of `let` (LANGUAGE.md §3.4):
//
//  1. Untyped single binding: {"let": {"x": <expr>}}.
//  2. Destructuring multi-binding (TC-231): {"let": {"x": <expr>, ...}}.
//  3. Typed binding (TC-232) — two equivalent forms per §3.4:
//     a. Sibling reserved key: {"let": {"@type": T, "x": <expr>}}.
//     b. Wrapped binding value: {"let": {"x": {"type": T, "value": <expr>}}}.
//  4. Tuple destructure (LANGUAGE.md §3.4.1): {"let": [["v","err"], <expr>]}
//     — the right-hand expression must evaluate to a tuple<T1,...,Tn> at
//     runtime. An entry of "_" discards the matching position.
func parseLet(body any, path string) (ast.Node, error) {
	// Tuple destructure form: array of [names, expr] (LANGUAGE.md §3.4.1).
	if arr, ok := body.([]any); ok {
		return parseLetDestructure(arr, path)
	}
	m, ok := body.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"let body must be object or tuple-destructure array").WithPath(path)
	}
	// Form (a): legacy `@type` sibling key applies to the (single) binding.
	siblingType, _ := m["@type"].(string)

	type binding struct {
		name, declType string
		raw            any
	}
	bindings := make([]binding, 0, len(m))
	for k, v := range m {
		if k == "@type" {
			continue
		}
		bindings = append(bindings, binding{name: k, declType: siblingType, raw: v})
	}
	if len(bindings) == 0 {
		return nil, werr.New(werr.CodeASTShape, "let empty").WithPath(path)
	}
	// Deterministic order: sort bindings by name so JSON map non-determinism
	// does not affect compile/run order. (Destructuring's spec value is the
	// set of bindings; observable evaluation order is left-to-right by name.)
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].name < bindings[j].name })

	// `@type` is only meaningful with a single binding (cannot apply one type
	// to multiple variables). Reject the ambiguous case to keep the rule sharp.
	if siblingType != "" && len(bindings) > 1 {
		return nil, werr.Newf(werr.CodeASTShape,
			`let "@type" sibling form requires exactly one binding, got %d; `+
				`use the per-binding {"type":..., "value":...} form for multi-let`,
			len(bindings)).WithPath(path)
	}

	out := &ast.Let{Base: ast.Base{P: path}}
	for _, b := range bindings {
		bindPath := path + "/" + b.name
		// Form (b): wrapped {"type":T, "value":expr}. Detected when the binding
		// value is an object whose keys are exactly {"type","value"}.
		declType := b.declType
		raw := b.raw
		if wrapped, ok := isTypedBindingWrapper(raw); ok {
			declType = wrapped.typeName
			raw = wrapped.value
		}
		expr, err := parseExpr(raw, bindPath)
		if err != nil {
			return nil, err
		}
		out.Bindings = append(out.Bindings, ast.LetBinding{
			Name: b.name,
			Type: declType,
			Expr: expr,
		})
	}
	// Mirror single-binding into legacy fields for backward compatibility
	// with consumers that read Name/Type/Expr directly.
	if len(out.Bindings) == 1 {
		out.Name = out.Bindings[0].Name
		out.Type = out.Bindings[0].Type
		out.Expr = out.Bindings[0].Expr
	}
	return out, nil
}

// parseLetDestructure decodes the tuple-destructure form of let. The body is
// [names, expr] or [names, types, expr]. names is an array of strings (use
// "_" to discard a position); types, when present, is a parallel array of
// optional declared type names ("" = inferred).
func parseLetDestructure(arr []any, path string) (ast.Node, error) {
	if len(arr) != 2 && len(arr) != 3 {
		return nil, werr.Newf(werr.CodeASTShape,
			"let tuple-destructure expects [names, expr] or [names, types, expr], got %d",
			len(arr)).WithPath(path)
	}
	rawNames, ok := arr[0].([]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"let destructure first element must be a names array").WithPath(path)
	}
	if len(rawNames) == 0 {
		return nil, werr.Newf(werr.CodeASTShape,
			"let destructure names array cannot be empty").WithPath(path)
	}
	names := make([]string, len(rawNames))
	for i, rn := range rawNames {
		s, ok := rn.(string)
		if !ok {
			return nil, werr.Newf(werr.CodeASTShape,
				"let destructure name at %d must be string, got %T", i, rn).WithPath(path)
		}
		names[i] = s
	}
	declTypes := make([]string, len(names))
	exprIdx := 1
	if len(arr) == 3 {
		rawTypes, ok := arr[1].([]any)
		if !ok {
			return nil, werr.Newf(werr.CodeASTShape,
				"let destructure types element must be an array").WithPath(path)
		}
		if len(rawTypes) != len(names) {
			return nil, werr.Newf(werr.CodeASTShape,
				"let destructure types length %d != names length %d",
				len(rawTypes), len(names)).WithPath(path)
		}
		for i, rt := range rawTypes {
			s, ok := rt.(string)
			if !ok {
				return nil, werr.Newf(werr.CodeASTShape,
					"let destructure type at %d must be string, got %T", i, rt).
					WithPath(path)
			}
			declTypes[i] = s
		}
		exprIdx = 2
	}
	expr, err := parseExpr(arr[exprIdx], path+"/expr")
	if err != nil {
		return nil, err
	}
	return &ast.Let{
		Base: ast.Base{P: path},
		Expr: expr,
		Destructure: &ast.LetDestructure{
			Names: names,
			Types: declTypes,
		},
	}, nil
}

// typedBindingWrapper is the {"type":T, "value":expr} form per §3.4.
type typedBindingWrapper struct {
	typeName string
	value    any
}

// isTypedBindingWrapper detects the {"type":T, "value":expr} binding form.
// The wrapper has exactly two keys "type" (string) and "value" (any). Any
// other shape is treated as a normal expression so that programs whose
// bindings happen to evaluate to a {"type","value"} map (an unlikely
// collision) continue to compile.
func isTypedBindingWrapper(raw any) (typedBindingWrapper, bool) {
	m, ok := raw.(map[string]any)
	if !ok || len(m) != 2 {
		return typedBindingWrapper{}, false
	}
	t, hasT := m["type"].(string)
	v, hasV := m["value"]
	if !hasT || !hasV || t == "" {
		return typedBindingWrapper{}, false
	}
	return typedBindingWrapper{typeName: t, value: v}, true
}

func parseSet(body any, path string) (ast.Node, error) {
	m, ok := body.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"set body must be object").WithPath(path)
	}
	if len(m) != 1 {
		return nil, werr.Newf(werr.CodeASTShape,
			"set accepts exactly one binding, got %d", len(m)).WithPath(path)
	}
	for name, raw := range m {
		expr, err := parseExpr(raw, path+"/"+name)
		if err != nil {
			return nil, err
		}
		return &ast.Set{Base: ast.Base{P: path}, Name: name, Expr: expr}, nil
	}
	return nil, werr.New(werr.CodeASTShape, "set empty").WithPath(path)
}

func parseReturn(body any, path string) (ast.Node, error) {
	expr, err := parseExpr(body, path)
	if err != nil {
		return nil, err
	}
	return &ast.Return{Base: ast.Base{P: path}, Expr: expr}, nil
}

func parseForeach(body any, path string) (ast.Node, error) {
	m, ok := body.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"foreach body must be object").WithPath(path)
	}
	target, err := parseExpr(m["target"], path+"/target")
	if err != nil {
		return nil, err
	}
	as, _ := m["as"].(string)
	idx, _ := m["index"].(string)
	if as != "" && idx != "" && as == idx {
		return nil, werr.Newf(werr.CodeASTShape,
			"foreach.as and foreach.index must differ").WithPath(path)
	}
	do, err := parseStatements(toArr(m["do"]), path+"/do")
	if err != nil {
		return nil, err
	}
	return &ast.Foreach{Base: ast.Base{P: path}, Target: target, As: as, Index: idx, Do: do}, nil
}

func parseFori(body any, path string) (ast.Node, error) {
	m, ok := body.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"fori body must be object").WithPath(path)
	}
	vr, _ := m["var"].(string)
	from, err := parseExpr(m["from"], path+"/from")
	if err != nil {
		return nil, err
	}
	to, err := parseExpr(m["to"], path+"/to")
	if err != nil {
		return nil, err
	}
	var step ast.Node
	if s, ok := m["step"]; ok {
		step, err = parseExpr(s, path+"/step")
		if err != nil {
			return nil, err
		}
	}
	do, err := parseStatements(toArr(m["do"]), path+"/do")
	if err != nil {
		return nil, err
	}
	return &ast.Fori{Base: ast.Base{P: path}, Var: vr, From: from, To: to, Step: step, Do: do}, nil
}

// parseMatch parses a multi-way value-equality dispatch (§14.2):
//
//	{"match": {
//	   "value": <expr>,
//	   "cases": [
//	      {"when": <expr>, "do": [<stmt>...]},
//	      ...
//	   ],
//	   "default": [<stmt>...]    // optional
//	}}
func parseMatch(body any, path string) (ast.Node, error) {
	m, ok := body.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"match body must be object").WithPath(path)
	}
	rawV, ok := m["value"]
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"match.value is required").WithPath(path)
	}
	val, err := parseExpr(rawV, path+"/value")
	if err != nil {
		return nil, err
	}
	rawCases, _ := m["cases"].([]any)
	if len(rawCases) == 0 {
		return nil, werr.Newf(werr.CodeASTShape,
			"match.cases must be non-empty").WithPath(path)
	}
	cases := make([]ast.MatchCase, 0, len(rawCases))
	for i, rc := range rawCases {
		csub := fmt.Sprintf("%s/cases/%d", path, i)
		cm, ok := rc.(map[string]any)
		if !ok {
			return nil, werr.Newf(werr.CodeASTShape,
				"match case must be object").WithPath(csub)
		}
		rawWhen, ok := cm["when"]
		if !ok {
			return nil, werr.Newf(werr.CodeASTShape,
				"match case requires `when`").WithPath(csub)
		}
		when, err := parseExpr(rawWhen, csub+"/when")
		if err != nil {
			return nil, err
		}
		do, err := parseStatements(toArr(cm["do"]), csub+"/do")
		if err != nil {
			return nil, err
		}
		cases = append(cases, ast.MatchCase{When: when, Do: do})
	}
	var def []ast.Node
	if rd, ok := m["default"]; ok && rd != nil {
		def, err = parseStatements(toArr(rd), path+"/default")
		if err != nil {
			return nil, err
		}
	}
	return &ast.Match{
		Base:    ast.Base{P: path},
		Value:   val,
		Cases:   cases,
		Default: def,
	}, nil
}

func parseRoutine(body any, path string) (ast.Node, error) {
	m, ok := body.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"routine body must be a single host call or {do:[stmts]} block").WithPath(path)
	}
	// Body form: {"routine": {"do": [stmts...]}}.
	if rawDo, ok := m["do"]; ok {
		if len(m) != 1 {
			return nil, werr.Newf(werr.CodeASTShape,
				"routine {do} form must not have sibling keys").WithPath(path)
		}
		stmts, err := parseStatements(toArr(rawDo), path+"/do")
		if err != nil {
			return nil, err
		}
		return &ast.Routine{Base: ast.Base{P: path}, Body: stmts}, nil
	}
	// Legacy call form: single-key object that is a host call.
	keys := sortedKeys(m)
	if len(keys) != 1 {
		return nil, werr.Newf(werr.CodeASTShape,
			"routine body must have exactly one key, got %d", len(keys)).WithPath(path)
	}
	node, err := parseNodeWithKey(keys[0], m[keys[0]], path+"/"+keys[0])
	if err != nil {
		return nil, err
	}
	call, ok := node.(*ast.Call)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"routine must wrap a host call, got %T", node).WithPath(path)
	}
	return &ast.Routine{Base: ast.Base{P: path}, Call: call}, nil
}

// parseDefer parses {"defer": <hostCall>} (LANGUAGE.md §3.7).
func parseDefer(body any, path string) (ast.Node, error) {
	m, ok := body.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"defer body must be a single host call object").WithPath(path)
	}
	keys := sortedKeys(m)
	if len(keys) != 1 {
		return nil, werr.Newf(werr.CodeASTShape,
			"defer body must have exactly one key, got %d", len(keys)).WithPath(path)
	}
	node, err := parseNodeWithKey(keys[0], m[keys[0]], path+"/"+keys[0])
	if err != nil {
		return nil, err
	}
	call, ok := node.(*ast.Call)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"defer requires a host call expression, got %T", node).WithPath(path)
	}
	return &ast.Defer{Base: ast.Base{P: path}, Call: call}, nil
}

// parseMapLit parses {"map":{"type":["K","V"],"value":{...}}} and the
// alternative entries form {"map":{"type":["K","V"],"entries":[[k,v],...]}}.
func parseMapLit(body any, path string) (ast.Node, error) {
	m, ok := body.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"map body must be object").WithPath(path)
	}
	rawType, _ := m["type"].([]any)
	if len(rawType) != 2 {
		return nil, werr.Newf(werr.CodeASTShape,
			"map.type must be [K, V]").WithPath(path)
	}
	kType, _ := rawType[0].(string)
	vType, _ := rawType[1].(string)
	if kType == "" || vType == "" {
		return nil, werr.Newf(werr.CodeASTShape,
			"map.type must be [K,V] strings").WithPath(path)
	}
	if !isMapKeyType(kType) {
		return nil, werr.Newf(werr.CodeType,
			"map key type %q not allowed (use string or intN/uintN)", kType).WithPath(path)
	}
	out := &ast.MapLit{Base: ast.Base{P: path}, KeyType: kType, ValType: vType}
	// Entries form first.
	if rawEntries, ok := m["entries"]; ok {
		arr, ok := rawEntries.([]any)
		if !ok {
			return nil, werr.Newf(werr.CodeASTShape,
				"map.entries must be an array").WithPath(path)
		}
		for i, raw := range arr {
			pair, ok := raw.([]any)
			if !ok || len(pair) != 2 {
				return nil, werr.Newf(werr.CodeASTShape,
					"map.entries[%d] must be [k,v]", i).WithPath(path)
			}
			sub := fmt.Sprintf("%s/entries/%d", path, i)
			k, err := parseExpr(pair[0], sub+"/0")
			if err != nil {
				return nil, err
			}
			v, err := parseExpr(pair[1], sub+"/1")
			if err != nil {
				return nil, err
			}
			out.Entries = append(out.Entries, ast.MapEntry{Key: k, Val: v})
		}
		return out, nil
	}
	// value form: requires string keys (the JSON object is the literal map).
	rawValue, _ := m["value"].(map[string]any)
	if rawValue != nil {
		if kType != types.TString {
			return nil, werr.Newf(werr.CodeASTShape,
				"map.value object form requires K=string, got K=%s; use map.entries for non-string keys",
				kType).WithPath(path)
		}
		// Deterministic order so JSON map non-determinism does not affect runs.
		keys := sortedKeys(rawValue)
		for _, k := range keys {
			sub := fmt.Sprintf("%s/value/%s", path, k)
			v, err := parseExpr(rawValue[k], sub)
			if err != nil {
				return nil, err
			}
			keyLit := &ast.Literal{
				Base:  ast.Base{P: sub + "/_key"},
				Value: types.NewValue(types.TString, k),
			}
			out.Entries = append(out.Entries, ast.MapEntry{Key: keyLit, Val: v})
		}
		return out, nil
	}
	// No entries/value → empty map literal.
	return out, nil
}

func isMapKeyType(k string) bool {
	switch k {
	case types.TString,
		types.TInt8, types.TInt16, types.TInt32, types.TInt64,
		types.TUint8, types.TUint16, types.TUint32, types.TUint64:
		return true
	}
	return false
}

// parseStructLit parses {"struct": ["TypeName", {field:expr,...}]}.
func parseStructLit(body any, path string) (ast.Node, error) {
	arr, ok := body.([]any)
	if !ok || len(arr) != 2 {
		return nil, werr.Newf(werr.CodeASTShape,
			"struct body must be [typeName, fields-object]").WithPath(path)
	}
	typName, _ := arr[0].(string)
	if typName == "" {
		return nil, werr.Newf(werr.CodeASTShape,
			"struct[0] must be a non-empty type name").WithPath(path)
	}
	fm, ok := arr[1].(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"struct[1] must be {field:expr,...}").WithPath(path)
	}
	out := &ast.StructLit{Base: ast.Base{P: path}, TypeName: typName}
	for _, name := range sortedKeys(fm) {
		sub := fmt.Sprintf("%s/%s", path, name)
		expr, err := parseExpr(fm[name], sub)
		if err != nil {
			return nil, err
		}
		out.Fields = append(out.Fields, ast.StructField{Name: name, Expr: expr})
	}
	return out, nil
}

// parseChanLit parses {"chan": ["T", bufExpr?]} where bufExpr defaults to 0.
func parseChanLit(body any, path string) (ast.Node, error) {
	arr, ok := body.([]any)
	if !ok || len(arr) < 1 || len(arr) > 2 {
		return nil, werr.Newf(werr.CodeASTShape,
			"chan body must be [elemType] or [elemType, bufferExpr]").WithPath(path)
	}
	elem, _ := arr[0].(string)
	if elem == "" {
		return nil, werr.Newf(werr.CodeASTShape,
			"chan[0] must be element type name").WithPath(path)
	}
	out := &ast.ChanLit{Base: ast.Base{P: path}, ElemType: elem}
	if len(arr) == 2 {
		buf, err := parseExpr(arr[1], path+"/1")
		if err != nil {
			return nil, err
		}
		out.Buffer = buf
	}
	return out, nil
}

// parseSelect parses {"select": [{"case":{...}}, ..., {"default":[stmts]}]}.
//
// Each non-default case body shape:
//   - recv: {"case":{"recv":[chExpr], "bind":["v","ok"], "do":[stmts]}}
//   - send: {"case":{"send":[chExpr, valExpr], "do":[stmts]}}
//
// The bind array elements can be "" or "_" to discard. Both elements are
// optional individually.
func parseSelect(body any, path string) (ast.Node, error) {
	arr, ok := body.([]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"select body must be an array of cases").WithPath(path)
	}
	out := &ast.SelectStmt{Base: ast.Base{P: path}}
	for i, raw := range arr {
		csub := fmt.Sprintf("%s/%d", path, i)
		cm, ok := raw.(map[string]any)
		if !ok || len(cm) != 1 {
			return nil, werr.Newf(werr.CodeASTShape,
				"select case must have exactly one key (case|default)").WithPath(csub)
		}
		for k, v := range cm {
			switch k {
			case "default":
				if out.Default != nil {
					return nil, werr.Newf(werr.CodeASTShape,
						"select has multiple default branches").WithPath(csub)
				}
				stmts, err := parseStatements(toArr(v), csub+"/default")
				if err != nil {
					return nil, err
				}
				out.Default = stmts
			case "case":
				inner, ok := v.(map[string]any)
				if !ok {
					return nil, werr.Newf(werr.CodeASTShape,
						"select case body must be object").WithPath(csub)
				}
				sc := ast.SelectCase{}
				switch {
				case inner["recv"] != nil:
					sc.Kind = ast.SelectCaseRecv
					rec, ok := inner["recv"].([]any)
					if !ok || len(rec) != 1 {
						return nil, werr.Newf(werr.CodeASTShape,
							"select recv must be [chanExpr]").WithPath(csub + "/recv")
					}
					ch, err := parseExpr(rec[0], csub+"/recv/0")
					if err != nil {
						return nil, err
					}
					sc.Chan = ch
					if rawBind, ok := inner["bind"]; ok {
						bindArr, ok := rawBind.([]any)
						if !ok || len(bindArr) > 2 {
							return nil, werr.Newf(werr.CodeASTShape,
								"select recv bind must be 0-2 names").WithPath(csub + "/bind")
						}
						if len(bindArr) >= 1 {
							sc.BindVal, _ = bindArr[0].(string)
						}
						if len(bindArr) == 2 {
							sc.BindOK, _ = bindArr[1].(string)
						}
					}
				case inner["send"] != nil:
					sc.Kind = ast.SelectCaseSend
					snd, ok := inner["send"].([]any)
					if !ok || len(snd) != 2 {
						return nil, werr.Newf(werr.CodeASTShape,
							"select send must be [chanExpr, valExpr]").WithPath(csub + "/send")
					}
					ch, err := parseExpr(snd[0], csub+"/send/0")
					if err != nil {
						return nil, err
					}
					val, err := parseExpr(snd[1], csub+"/send/1")
					if err != nil {
						return nil, err
					}
					sc.Chan = ch
					sc.SendExpr = val
				default:
					return nil, werr.Newf(werr.CodeASTShape,
						"select case requires recv or send").WithPath(csub)
				}
				do, err := parseStatements(toArr(inner["do"]), csub+"/do")
				if err != nil {
					return nil, err
				}
				sc.Do = do
				out.Cases = append(out.Cases, sc)
			default:
				return nil, werr.Newf(werr.CodeASTShape,
					"unknown select branch key %q (want case|default)", k).WithPath(csub)
			}
		}
	}
	return out, nil
}

func parseAwait(body any, path string) (ast.Node, error) {
	if arr, ok := body.([]any); ok {
		items := make([]ast.Node, 0, len(arr))
		for i, it := range arr {
			n, err := parseNode(it, fmt.Sprintf("%s/%d", path, i))
			if err != nil {
				return nil, err
			}
			items = append(items, n)
		}
		return &ast.Call{Base: ast.Base{P: path}, Op: "await", Args: items}, nil
	}
	n, err := parseNode(body, path+"/0")
	if err != nil {
		return nil, err
	}
	return &ast.Call{Base: ast.Base{P: path}, Op: "await", Args: []ast.Node{n}}, nil
}

func parseArrayLit(body any, path string) (ast.Node, error) {
	m, ok := body.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"array body must be object {elem,items}").WithPath(path)
	}
	elem, _ := m["elem"].(string)
	if elem == "" {
		return nil, werr.Newf(werr.CodeASTShape,
			"array.elem must be a non-empty type name").WithPath(path)
	}
	itemsRaw, _ := m["items"].([]any)
	items := make([]ast.Node, 0, len(itemsRaw))
	for i, it := range itemsRaw {
		sub := fmt.Sprintf("%s/items/%d", path, i)
		n, err := parseNode(it, sub)
		if err != nil {
			return nil, err
		}
		items = append(items, n)
	}
	return &ast.Array{Base: ast.Base{P: path}, Elem: elem, Items: items}, nil
}

// ---------- helpers ----------

func toArr(v any) []any {
	if v == nil {
		return nil
	}
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
