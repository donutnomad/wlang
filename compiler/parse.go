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
	"array":    true,
	"try":      true,
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
	case "array":
		return parseArrayLit(body, nodePath)
	case "try":
		return parseTry(body, nodePath)
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

func parseLet(body any, path string) (ast.Node, error) {
	m, ok := body.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"let body must be object").WithPath(path)
	}
	if len(m) != 1 {
		return nil, werr.Newf(werr.CodeASTShape,
			"let accepts exactly one binding, got %d", len(m)).WithPath(path)
	}
	for name, raw := range m {
		expr, err := parseExpr(raw, path+"/"+name)
		if err != nil {
			return nil, err
		}
		return &ast.Let{Base: ast.Base{P: path}, Name: name, Expr: expr}, nil
	}
	return nil, werr.New(werr.CodeASTShape, "let empty").WithPath(path)
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

// parseTry parses a try/catch statement: {"try": {"do":[...], "bind":"err", "catch":[...]}}.
func parseTry(body any, path string) (ast.Node, error) {
	m, ok := body.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"try body must be object").WithPath(path)
	}
	do, err := parseStatements(toArr(m["do"]), path+"/do")
	if err != nil {
		return nil, err
	}
	bind, _ := m["bind"].(string)
	if bind == "" {
		bind = "err"
	}
	catch, err := parseStatements(toArr(m["catch"]), path+"/catch")
	if err != nil {
		return nil, err
	}
	return &ast.Try{Base: ast.Base{P: path}, Do: do, Bind: bind, Catch: catch}, nil
}

func parseRoutine(body any, path string) (ast.Node, error) {
	// routine body must be a single host call object.
	m, ok := body.(map[string]any)
	if !ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"routine body must be a single host call object").WithPath(path)
	}
	if _, ok := m["do"]; ok {
		return nil, werr.Newf(werr.CodeASTShape,
			"routine body must be a single host call object, not a do-block").WithPath(path)
	}
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
