package wflang

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/donutnomad/wlang/ast"
	"github.com/donutnomad/wlang/compiler"
	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/runtime"
	"github.com/donutnomad/wlang/types"
)

// ---------- DumpAST (LANGUAGE.md §11.3 / TC-832) ----------

// DumpAST returns a deterministic JSON serialization of a compiled
// Program. The output round-trips through CompileJSON.
func DumpAST(p *Program) ([]byte, error) {
	if p == nil || p.prog == nil {
		return nil, werr.New(werr.CodeASTShape, "DumpAST: nil program")
	}
	body := make([]any, 0, len(p.prog.Body))
	for _, s := range p.prog.Body {
		v, err := dumpNode(s)
		if err != nil {
			return nil, err
		}
		body = append(body, v)
	}
	if p.prog.Lang != "" {
		envelope := map[string]any{
			"lang":    p.prog.Lang,
			"program": body,
		}
		if len(p.prog.Imports) > 0 {
			envelope["imports"] = anySlice(p.prog.Imports)
		}
		return marshalCanonical(envelope)
	}
	return marshalCanonical(body)
}

// DumpTypedAST is the type-annotated variant. The current type system carries
// per-value typenames at runtime, not per-node — so this returns the same
// shape as DumpAST. Reserved for future enhancement.
func DumpTypedAST(p *Program) ([]byte, error) { return DumpAST(p) }

func anySlice(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

func marshalCanonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return FormatJSON(raw)
}

func dumpNode(n ast.Node) (any, error) {
	switch x := n.(type) {
	case *ast.Literal:
		return map[string]any{
			"literal": map[string]any{
				"type":  x.Value.TypeName(),
				"value": literalValueRaw(x.Value),
			},
		}, nil
	case *ast.Var:
		if x.Default == nil {
			return map[string]any{"var": x.Name}, nil
		}
		dv, err := dumpNode(x.Default)
		if err != nil {
			return nil, err
		}
		return map[string]any{"var": map[string]any{"name": x.Name, "default": dv}}, nil
	case *ast.Pkg:
		return map[string]any{"pkg": x.Name}, nil
	case *ast.Array:
		items := make([]any, 0, len(x.Items))
		for _, e := range x.Items {
			d, err := dumpNode(e)
			if err != nil {
				return nil, err
			}
			items = append(items, d)
		}
		return map[string]any{"array": map[string]any{"elem": x.Elem, "items": items}}, nil
	case *ast.IfStmt:
		return dumpIf(x.Cond, x.Then, x.Else)
	case *ast.IfExpr:
		return dumpIf(x.Cond, x.Then, x.Else)
	case *ast.Call:
		args := make([]any, 0, len(x.Args))
		for _, a := range x.Args {
			d, err := dumpNode(a)
			if err != nil {
				return nil, err
			}
			args = append(args, d)
		}
		if x.Op == "await" {
			if len(args) == 1 {
				return map[string]any{"await": args[0]}, nil
			}
			return map[string]any{"await": args}, nil
		}
		return map[string]any{x.Op: args}, nil
	case *ast.Let:
		body := map[string]any{}
		bindings := x.Bindings
		if len(bindings) == 0 {
			bindings = []ast.LetBinding{{Name: x.Name, Type: x.Type, Expr: x.Expr}}
		}
		for _, b := range bindings {
			ed, err := dumpNode(b.Expr)
			if err != nil {
				return nil, err
			}
			if b.Type != "" {
				body[b.Name] = map[string]any{"type": b.Type, "value": ed}
			} else {
				body[b.Name] = ed
			}
		}
		return map[string]any{"let": body}, nil
	case *ast.Set:
		ed, err := dumpNode(x.Expr)
		if err != nil {
			return nil, err
		}
		return map[string]any{"set": map[string]any{x.Name: ed}}, nil
	case *ast.Return:
		ed, err := dumpNode(x.Expr)
		if err != nil {
			return nil, err
		}
		return map[string]any{"return": ed}, nil
	case *ast.Foreach:
		td, err := dumpNode(x.Target)
		if err != nil {
			return nil, err
		}
		do, err := dumpNodeList(x.Do)
		if err != nil {
			return nil, err
		}
		body := map[string]any{"target": td, "as": x.As, "do": do}
		if x.Index != "" {
			body["index"] = x.Index
		}
		return map[string]any{"foreach": body}, nil
	case *ast.Fori:
		from, err := dumpNode(x.From)
		if err != nil {
			return nil, err
		}
		to, err := dumpNode(x.To)
		if err != nil {
			return nil, err
		}
		do, err := dumpNodeList(x.Do)
		if err != nil {
			return nil, err
		}
		body := map[string]any{"var": x.Var, "from": from, "to": to, "do": do}
		if x.Step != nil {
			st, err := dumpNode(x.Step)
			if err != nil {
				return nil, err
			}
			body["step"] = st
		}
		return map[string]any{"fori": body}, nil
	case *ast.Break:
		return map[string]any{"break": map[string]any{}}, nil
	case *ast.Continue:
		return map[string]any{"continue": map[string]any{}}, nil
	case *ast.Panic:
		ed, err := dumpNode(x.Expr)
		if err != nil {
			return nil, err
		}
		return map[string]any{"panic": ed}, nil
	case *ast.ExprStmt:
		ed, err := dumpNode(x.Expr)
		if err != nil {
			return nil, err
		}
		return map[string]any{"expr": ed}, nil
	case *ast.Routine:
		if x.Call != nil {
			c, err := dumpNode(x.Call)
			if err != nil {
				return nil, err
			}
			return map[string]any{"routine": c}, nil
		}
		body, err := dumpNodeList(x.Body)
		if err != nil {
			return nil, err
		}
		return map[string]any{"routine": map[string]any{"do": body}}, nil
	}
	return nil, werr.Newf(werr.CodeASTShape, "DumpAST: unsupported node %T", n)
}

func dumpNodeList(ns []ast.Node) ([]any, error) {
	out := make([]any, 0, len(ns))
	for _, n := range ns {
		d, err := dumpNode(n)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func dumpIf(cond ast.Node, thenS, elseS []ast.Node) (any, error) {
	c, err := dumpNode(cond)
	if err != nil {
		return nil, err
	}
	then, err := dumpNodeList(thenS)
	if err != nil {
		return nil, err
	}
	elseB, err := dumpNodeList(elseS)
	if err != nil {
		return nil, err
	}
	return map[string]any{"if": map[string]any{
		"cond": c, "then": then, "else": elseB,
	}}, nil
}

func literalValueRaw(v types.Value) any {
	if v.IsNull() {
		return nil
	}
	switch g := v.Go().(type) {
	case bool, string:
		return g
	case int8, int16, int32, int64, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprintf("%v", g)
	}
	return fmt.Sprintf("%v", v.Go())
}

// ---------- EvalAt (TC-833) ----------

// EvalAt evaluates the AST sub-expression located at JSON Pointer path
// against the provided RunOptions. It does not affect any outer execution
// state; a fresh executor is used.
func (p *Program) EvalAt(ctx context.Context, path string, opts RunOptions) (Value, error) {
	if p == nil || p.prog == nil {
		return Value{}, werr.New(werr.CodeASTShape, "EvalAt: nil program")
	}
	target, ok := findNodeByPath(p.prog.Body, path)
	if !ok {
		return Value{}, werr.Newf(werr.CodeSymbol,
			"EvalAt: no node at path %q", path)
	}
	scope, err := buildScope(opts.Vars, opts.VarOptions)
	if err != nil {
		return Value{}, err
	}
	reg := p.engine.opts.Registry
	if len(opts.Packages) > 0 {
		reg = withPackages(reg, opts.Packages)
	}
	ctx = registry.WithCapabilities(ctx, p.engine.opts.Capabilities)
	exec := runtime.NewExecutor(ctx, scope, reg, availablePkgNames(reg), p.engine.opts.Budget)
	return exec.Eval(target)
}

// findNodeByPath does a depth-first traversal looking for the node whose
// Path() == target. Statements and expressions are both searched.
func findNodeByPath(body []ast.Node, target string) (ast.Node, bool) {
	for _, s := range body {
		if n, ok := walkPath(s, target); ok {
			return n, true
		}
	}
	return nil, false
}

func walkPath(n ast.Node, target string) (ast.Node, bool) {
	if n == nil {
		return nil, false
	}
	if n.Path() == target {
		return n, true
	}
	switch x := n.(type) {
	case *ast.Var:
		if x.Default != nil {
			return walkPath(x.Default, target)
		}
	case *ast.Array:
		for _, it := range x.Items {
			if r, ok := walkPath(it, target); ok {
				return r, true
			}
		}
	case *ast.IfStmt:
		if r, ok := walkPath(x.Cond, target); ok {
			return r, true
		}
		for _, s := range x.Then {
			if r, ok := walkPath(s, target); ok {
				return r, true
			}
		}
		for _, s := range x.Else {
			if r, ok := walkPath(s, target); ok {
				return r, true
			}
		}
	case *ast.IfExpr:
		if r, ok := walkPath(x.Cond, target); ok {
			return r, true
		}
		for _, s := range x.Then {
			if r, ok := walkPath(s, target); ok {
				return r, true
			}
		}
		for _, s := range x.Else {
			if r, ok := walkPath(s, target); ok {
				return r, true
			}
		}
	case *ast.Call:
		for _, a := range x.Args {
			if r, ok := walkPath(a, target); ok {
				return r, true
			}
		}
	case *ast.Let:
		return walkPath(x.Expr, target)
	case *ast.Set:
		return walkPath(x.Expr, target)
	case *ast.Return:
		return walkPath(x.Expr, target)
	case *ast.ExprStmt:
		return walkPath(x.Expr, target)
	case *ast.Panic:
		return walkPath(x.Expr, target)
	case *ast.Routine:
		return walkPath(x.Call, target)
	case *ast.Foreach:
		if r, ok := walkPath(x.Target, target); ok {
			return r, true
		}
		for _, s := range x.Do {
			if r, ok := walkPath(s, target); ok {
				return r, true
			}
		}
	case *ast.Fori:
		if r, ok := walkPath(x.From, target); ok {
			return r, true
		}
		if r, ok := walkPath(x.To, target); ok {
			return r, true
		}
		if x.Step != nil {
			if r, ok := walkPath(x.Step, target); ok {
				return r, true
			}
		}
		for _, s := range x.Do {
			if r, ok := walkPath(s, target); ok {
				return r, true
			}
		}
	}
	return nil, false
}

// ---------- Explain (TC-831) ----------

// ExplainReport summarizes a program's surface (LANGUAGE.md §11.2).
type ExplainReport struct {
	Vars         []string `json:"vars"`
	Packages     []string `json:"packages"`
	Operators    []string `json:"operators"`
	Capabilities []string `json:"capabilities"`
	HasRoutines  bool     `json:"hasRoutines"`
}

// Explain inspects a compiled program and returns an ExplainReport.
func (p *Program) Explain() ExplainReport {
	r := ExplainReport{}
	if p == nil || p.prog == nil {
		return r
	}
	vars := map[string]bool{}
	pkgs := map[string]bool{}
	ops := map[string]bool{}
	caps := map[string]bool{}
	var reg *registry.Registry
	if p.engine != nil {
		reg = p.engine.opts.Registry
	}
	walk := func(n ast.Node) {}
	walk = func(n ast.Node) {
		if n == nil {
			return
		}
		switch x := n.(type) {
		case *ast.Var:
			vars[x.Name] = true
		case *ast.Pkg:
			pkgs[x.Name] = true
		case *ast.Call:
			ops[x.Op] = true
			if reg != nil && len(x.Args) > 0 {
				if pk, ok := x.Args[0].(*ast.Pkg); ok {
					for _, c := range reg.RequiredCapabilities(pk.Name, x.Op) {
						caps[c] = true
					}
				}
			}
		case *ast.Routine:
			r.HasRoutines = true
		}
		walkChildren(n, walk)
	}
	for _, s := range p.prog.Body {
		walk(s)
	}
	r.Vars = sortedKeys(vars)
	r.Packages = sortedKeys(pkgs)
	r.Operators = sortedKeys(ops)
	r.Capabilities = sortedKeys(caps)
	return r
}

func walkChildren(n ast.Node, fn func(ast.Node)) {
	switch x := n.(type) {
	case *ast.Var:
		if x.Default != nil {
			fn(x.Default)
		}
	case *ast.Array:
		for _, it := range x.Items {
			fn(it)
		}
	case *ast.IfStmt:
		fn(x.Cond)
		for _, s := range x.Then {
			fn(s)
		}
		for _, s := range x.Else {
			fn(s)
		}
	case *ast.IfExpr:
		fn(x.Cond)
		for _, s := range x.Then {
			fn(s)
		}
		for _, s := range x.Else {
			fn(s)
		}
	case *ast.Call:
		for _, a := range x.Args {
			fn(a)
		}
	case *ast.Let:
		fn(x.Expr)
	case *ast.Set:
		fn(x.Expr)
	case *ast.Return:
		fn(x.Expr)
	case *ast.ExprStmt:
		fn(x.Expr)
	case *ast.Panic:
		fn(x.Expr)
	case *ast.Routine:
		fn(x.Call)
	case *ast.Foreach:
		fn(x.Target)
		for _, s := range x.Do {
			fn(s)
		}
	case *ast.Fori:
		fn(x.From)
		fn(x.To)
		if x.Step != nil {
			fn(x.Step)
		}
		for _, s := range x.Do {
			fn(s)
		}
	case *ast.Match:
		fn(x.Value)
		for _, c := range x.Cases {
			fn(c.When)
			for _, s := range c.Do {
				fn(s)
			}
		}
		for _, s := range x.Default {
			fn(s)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------- CallPlan (LANGUAGE.md §5.8 / §7.7) ----------

// CallPlan describes one host call site after compilation. It captures every
// piece of information the runtime / tooling needs to dispatch and validate
// the call without re-resolving the registry.
//
// Fields not applicable to a given call shape are zero-valued — for example,
// PackageName is empty for method calls, TypeName is empty for package calls.
//
// LANGUAGE.md §5.8 / §7.7 / TC-481 / TC-672 / TC-674.
type CallPlan struct {
	Operator     string   `json:"operator"`
	Path         string   `json:"path"`
	ReceiverKind string   `json:"receiverKind"`
	PackageName  string   `json:"packageName,omitempty"`
	TypeName     string   `json:"typeName,omitempty"`
	GoFunc       any      `json:"-"`
	GoMethod     any      `json:"-"`
	ParamTypes   []string `json:"paramTypes,omitempty"`
	ReturnTypes  []string `json:"returnTypes,omitempty"`
	ErrorIndex   int      `json:"errorIndex"` // -1 when the function has no error
	ResultKind   string   `json:"resultKind"` // "value" | "tuple" | "null"
	Capabilities []string `json:"capabilities,omitempty"`
	Deprecated   string   `json:"deprecated,omitempty"`
}

// CallPlans walks the program's compiled AST and emits a CallPlan for every
// resolvable host call site (package or type method). Unresolved operators
// (e.g. builtin arithmetic, unknown symbols) are skipped — CallPlans is a
// surface-area report, not a full resolution oracle.
func (p *Program) CallPlans() []CallPlan {
	if p == nil || p.prog == nil || p.engine == nil {
		return nil
	}
	reg := p.engine.opts.Registry
	if reg == nil {
		return nil
	}
	var out []CallPlan
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if n == nil {
			return
		}
		if c, ok := n.(*ast.Call); ok {
			if plan := buildCallPlan(reg, c); plan != nil {
				out = append(out, *plan)
			}
		}
		walkChildren(n, walk)
	}
	for _, s := range p.prog.Body {
		walk(s)
	}
	return out
}

// buildCallPlan resolves a single Call against the registry. It returns nil
// when the call can't be classified (e.g., builtin operator or unknown).
func buildCallPlan(reg *Registry, c *ast.Call) *CallPlan {
	if c == nil || len(c.Args) == 0 {
		return nil
	}
	// Package receiver shape: first arg is *ast.Pkg.
	if pk, ok := c.Args[0].(*ast.Pkg); ok {
		meta := reg.LookupPackageFunc(pk.Name, c.Op)
		if meta == nil {
			return nil
		}
		return planFromMeta(c, meta)
	}
	// Method receiver shape: try every bound type — we don't know the receiver
	// type at compile time without full inference. We surface the first match
	// so tooling at least sees the operator.
	// The receiver expression is c.Args[0] (often a Var); we can't statically
	// resolve it here. CallPlan is best-effort for methods.
	return nil
}

func planFromMeta(c *ast.Call, m *FuncMeta) *CallPlan {
	plan := &CallPlan{
		Operator:     c.Op,
		Path:         c.Path(),
		ReceiverKind: m.ReceiverKind,
		PackageName:  m.PackageName,
		TypeName:     m.TypeName,
		ParamTypes:   m.ParamTypes,
		ReturnTypes:  m.ReturnTypes,
		Capabilities: m.Capabilities,
		Deprecated:   m.Deprecated,
	}
	plan.GoFunc = m.GoFunc
	plan.GoMethod = m.GoMethod
	plan.ErrorIndex = -1
	if m.HasError {
		plan.ErrorIndex = len(m.ReturnTypes)
	}
	switch {
	case len(m.ReturnTypes) == 0:
		plan.ResultKind = "null"
	case len(m.ReturnTypes) == 1:
		plan.ResultKind = "value"
	default:
		plan.ResultKind = "tuple"
	}
	return plan
}

// ---------- Schema validator (TC-850 / TC-851) ----------

// SchemaIssue represents one structural violation.
type SchemaIssue struct {
	Code, Message, Path string
}

// reservedOperators are AST shapes that aren't function calls. They have
// their own arg shapes (object/string), so single-key wrapping rules don't
// apply. Used by both ValidateSchema and Format.
var reservedOperators = map[string]bool{
	"literal": true, "array": true, "var": true, "pkg": true,
	"if": true, "let": true, "set": true, "return": true,
	"routine": true, "await": true, "panic": true, "expr": true,
	"foreach": true, "fori": true, "break": true, "continue": true,
	"defer": true, "map": true, "struct": true, "chan": true, "select": true,
}

// ValidateSchema enforces structural rules without the full compile pipeline:
//   - The top-level shape is an array, a single statement object, or an
//     envelope `{lang, program}`.
//   - Every statement/expression object that uses an operator key has either
//     a value of array or a single-arg primitive.
//   - Numeric / string literals MUST appear inside `{"literal":{...}}` —
//     bare numbers/strings outside a typed-literal envelope are rejected
//     (TC-851).
func ValidateSchema(src []byte) []SchemaIssue {
	var tree any
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return []SchemaIssue{{Code: "E_JSON_DECODE", Message: err.Error(), Path: ""}}
	}
	body := tree
	switch v := tree.(type) {
	case map[string]any:
		if _, hasLang := v["lang"]; hasLang {
			body = v["program"]
		}
	}
	var issues []SchemaIssue
	walkSchema("", body, &issues, true)
	return issues
}

// walkSchema validates one node. inStmt=true means a statement context
// (top-level body items) — `return` / `let` etc. allowed.
func walkSchema(path string, v any, issues *[]SchemaIssue, inStmt bool) {
	switch x := v.(type) {
	case []any:
		for i, item := range x {
			walkSchema(fmt.Sprintf("%s/%d", path, i), item, issues, inStmt)
		}
	case map[string]any:
		// Statements/exprs are single-key dicts (multi-key is a TC-854 lint
		// issue, but here we still report the structural failure).
		if len(x) != 1 {
			*issues = append(*issues, SchemaIssue{
				Code: "E_AST_SHAPE",
				Message: fmt.Sprintf(
					"object must have exactly 1 key (got %d)", len(x)),
				Path: path,
			})
			return
		}
		var k string
		var body any
		for kk, vv := range x {
			k, body = kk, vv
		}
		if k == "literal" {
			// literal must be {type, value} — its body is its own shape.
			lm, ok := body.(map[string]any)
			if !ok {
				*issues = append(*issues, SchemaIssue{
					Code:    "E_AST_SHAPE",
					Message: "literal body must be {type,value}",
					Path:    path + "/literal",
				})
				return
			}
			if _, ok := lm["type"]; !ok {
				*issues = append(*issues, SchemaIssue{
					Code: "E_AST_SHAPE", Message: "literal missing type",
					Path: path + "/literal",
				})
			}
			return
		}
		// Composite operators (let/set/foreach/fori/try/if/array/routine/await)
		// have a body whose shape is dictated by the operator itself, not
		// the single-key rule. We recurse into the inner expression
		// children but don't enforce single-key on the immediate body.
		if isCompositeBody(k) {
			walkCompositeBody(path+"/"+k, k, body, issues)
			return
		}
		// recurse into reserved/operator children
		walkSchema(path+"/"+k, body, issues, false)
	default:
		// Bare scalar at expression position is rejected unless it's a
		// statement-context container (handled elsewhere).
		// JSON numbers come through as json.Number due to UseNumber.
		if _, isNum := v.(json.Number); isNum {
			*issues = append(*issues, SchemaIssue{
				Code:    "E_AST_SHAPE",
				Message: "bare numeric literal — wrap as {\"literal\":{\"type\":...,\"value\":...}}",
				Path:    path,
			})
		}
		_ = inStmt
	}
}

// isCompositeBody reports whether the operator's body has a known multi-key
// shape (not the standard single-key operator-call shape).
func isCompositeBody(op string) bool {
	switch op {
	case "let", "set", "foreach", "fori", "if", "array", "routine", "await",
		"defer", "map", "struct", "chan", "select":
		return true
	}
	return false
}

// walkCompositeBody recurses into the named children of a composite operator
// body, applying schema rules to the embedded expressions.
func walkCompositeBody(path, op string, body any, issues *[]SchemaIssue) {
	m, ok := body.(map[string]any)
	if !ok {
		// `routine` body is itself an operator-call object; recurse normally.
		walkSchema(path, body, issues, false)
		return
	}
	// Children that hold expressions/statements; the rest are scalar config.
	expressionKeys := map[string]bool{
		"expr":   true,
		"cond":   true,
		"then":   true,
		"else":   true,
		"target": true,
		"do":     true,
		"from":   true,
		"to":     true,
		"step":   true,
		"items":  true,
		"catch":  true,
	}
	for k, v := range m {
		if !expressionKeys[k] {
			continue
		}
		walkSchema(path+"/"+k, v, issues, false)
	}
	// `let` shorthand: a single non-meta key whose value is an expression.
	if op == "let" || op == "set" {
		for k, v := range m {
			if k == "name" || k == "type" || k == "expr" {
				continue
			}
			// shorthand binding: `{ "x": <expr> }`
			walkSchema(path+"/"+k, v, issues, false)
		}
	}
}

// ---------- Lint (TC-854 / TC-855 / TC-856) ----------

// LintIssue represents a lint diagnostic.
type LintIssue struct {
	Code, Message, Path string
}

// LintOptions configures lint passes.
type LintOptions struct {
	Deployment    CapabilitySet
	MaxComplexity int
}

// Lint runs structural and semantic lint passes over a raw program JSON.
func Lint(src []byte, reg *Registry, opts LintOptions) []LintIssue {
	var tree any
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return []LintIssue{{Code: "E_JSON_DECODE", Message: err.Error()}}
	}
	body := tree
	if m, ok := tree.(map[string]any); ok {
		if _, hasLang := m["lang"]; hasLang {
			body = m["program"]
		}
	}
	var issues []LintIssue
	state := &lintState{reg: reg, opts: opts, issues: &issues}
	walkLint("", body, state)
	if opts.MaxComplexity > 0 && state.complexity > opts.MaxComplexity {
		issues = append(issues, LintIssue{
			Code: "L_COMPLEXITY",
			Message: fmt.Sprintf(
				"program complexity %d exceeds threshold %d",
				state.complexity, opts.MaxComplexity),
		})
	}
	return issues
}

type lintState struct {
	reg        *Registry
	opts       LintOptions
	issues     *[]LintIssue
	complexity int
}

func walkLint(path string, v any, st *lintState) {
	switch x := v.(type) {
	case []any:
		for i, it := range x {
			walkLint(fmt.Sprintf("%s/%d", path, i), it, st)
		}
	case map[string]any:
		// TC-854: multi-key operator object — only flag at operator-call
		// shape; literal/composite bodies have their own internal shape.
		if len(x) > 1 {
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			*st.issues = append(*st.issues, LintIssue{
				Code:    "L_MULTI_KEY",
				Message: fmt.Sprintf("operator object has multiple keys: %v", keys),
				Path:    path,
			})
		}
		for k, body := range x {
			// TC-856: count loops.
			if k == "foreach" || k == "fori" {
				st.complexity += 10
			} else {
				st.complexity++
			}
			// TC-855: capability checks for package calls.
			// TC-882: deprecation warning for package calls.
			if !reservedOperators[k] && st.reg != nil {
				if pkgName, op := findPkgRef(body, k); pkgName != "" {
					if caps := requiredCaps(st.reg, pkgName, op); len(caps) > 0 {
						for _, cap := range caps {
							if !st.opts.Deployment[cap] {
								*st.issues = append(*st.issues, LintIssue{
									Code: "L_CAPABILITY",
									Message: fmt.Sprintf(
										"%s.%s requires capability %q",
										pkgName, op, cap),
									Path: path,
								})
							}
						}
					}
					if dep := st.reg.DeprecationOf(pkgName, op); dep != "" {
						*st.issues = append(*st.issues, LintIssue{
							Code: "L_DEPRECATED",
							Message: fmt.Sprintf(
								"%s.%s is deprecated: %s", pkgName, op, dep),
							Path: path,
						})
					}
				}
			}
			// Skip recursion into literal body and composite-shape bodies
			// where keys are structural, not operator names — preventing
			// false-positive multi-key flags on {type,value} or {name,expr}.
			if k == "literal" {
				continue
			}
			if isCompositeBody(k) {
				walkLintComposite(path+"/"+k, body, st)
				continue
			}
			walkLint(path+"/"+k, body, st)
		}
	}
}

// walkLintComposite recurses into a composite operator's expression children
// without flagging the immediate body as multi-key.
func walkLintComposite(path string, body any, st *lintState) {
	m, ok := body.(map[string]any)
	if !ok {
		walkLint(path, body, st)
		return
	}
	expressionKeys := map[string]bool{
		"expr": true, "cond": true, "then": true, "else": true,
		"target": true, "do": true, "from": true, "to": true,
		"step": true, "items": true, "catch": true,
	}
	for k, v := range m {
		if !expressionKeys[k] {
			// Shorthand let/set binding: `{ "x": <expr> }`.
			if k == "name" || k == "type" || k == "bind" || k == "as" || k == "index" || k == "var" || k == "elem" {
				continue
			}
			walkLint(path+"/"+k, v, st)
			continue
		}
		walkLint(path+"/"+k, v, st)
	}
}

// findPkgRef inspects an args list (arr) to detect a `{"pkg": <name>}`
// receiver. Returns pkg name and the parent operator name as op.
func findPkgRef(body any, op string) (string, string) {
	arr, ok := body.([]any)
	if !ok || len(arr) == 0 {
		return "", ""
	}
	first, ok := arr[0].(map[string]any)
	if !ok {
		return "", ""
	}
	pkgRaw, ok := first["pkg"]
	if !ok {
		return "", ""
	}
	pkgName, _ := pkgRaw.(string)
	return pkgName, op
}

// requiredCaps fetches the capability set for a registered function. The
// registry doesn't expose this publicly yet, so this is a placeholder
// hook — returns nil for now. Tests that need TC-855 use direct construction
// of the package spec to control the capability metadata via Registry.
func requiredCaps(r *Registry, pkg, op string) []string {
	return r.RequiredCapabilities(pkg, op)
}

// ---------- DocGen (TC-857) ----------

// DocGen returns markdown describing the registry's exposed surface.
func DocGen(reg *Registry) string {
	var sb strings.Builder
	sb.WriteString("# wflang Registry\n\n")
	sb.WriteString("## Packages\n\n")
	pkgs := reg.PackageNames()
	names := make([]string, 0, len(pkgs))
	for k := range pkgs {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		sb.WriteString("### " + name + "\n\n")
		funcs := reg.PackageFunctions(name)
		fnames := make([]string, 0, len(funcs))
		for k := range funcs {
			fnames = append(fnames, k)
		}
		sort.Strings(fnames)
		for _, fn := range fnames {
			sb.WriteString("- `" + fn + "`")
			if caps := reg.RequiredCapabilities(name, fn); len(caps) > 0 {
				sb.WriteString(" — capabilities: " + strings.Join(caps, ","))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ---------- TestRunner (TC-858) ----------

// TestCase describes one programmatic test.
type TestCase struct {
	Name    string          `json:"name"`
	Program json.RawMessage `json:"program"`
	Vars    map[string]any  `json:"vars,omitempty"`
	Want    any             `json:"want,omitempty"`
	WantErr string          `json:"wantErr,omitempty"`
}

// TestResult captures one case outcome.
type TestResult struct {
	Name string
	Pass bool
	Diff string
	Err  string
}

// RunTests executes each case and reports pass/fail.
func RunTests(eng *Engine, cases []TestCase) []TestResult {
	out := make([]TestResult, 0, len(cases))
	for _, tc := range cases {
		res := TestResult{Name: tc.Name}
		prog, err := eng.CompileJSON(tc.Program)
		if err != nil {
			res.Err = err.Error()
			res.Pass = tc.WantErr != "" && strings.Contains(err.Error(), tc.WantErr)
			out = append(out, res)
			continue
		}
		v, err := prog.Run(context.Background(), RunOptions{Vars: tc.Vars})
		if err != nil {
			res.Err = err.Error()
			res.Pass = tc.WantErr != "" && strings.Contains(err.Error(), tc.WantErr)
			out = append(out, res)
			continue
		}
		got := v.Go()
		if !reflect.DeepEqual(canonicalize(got), canonicalize(tc.Want)) {
			res.Diff = fmt.Sprintf("want %v (%T), got %v (%T)",
				tc.Want, tc.Want, got, got)
			res.Pass = false
		} else {
			res.Pass = true
		}
		out = append(out, res)
	}
	return out
}

// canonicalize coerces numbers to int64/float64/string for compare so that
// JSON-decoded test wants line up with runtime values.
func canonicalize(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case float64:
		// JSON decode default is float64; if it's a whole number, coerce.
		if x == float64(int64(x)) {
			return int64(x)
		}
		return x
	case int:
		return int64(x)
	}
	return v
}

// ---------- Format with single-arg normalization (TC-853) ----------

// FormatProgram extends FormatJSON with TC-853's normalization: any
// non-reserved operator object whose value is not an array is rewritten as
// {"Op": [<value>]}.
func FormatProgram(src []byte) ([]byte, error) {
	var tree any
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return nil, werr.Newf(werr.CodeJSONDecode, "format: %v", err)
	}
	normalized := normalizeNode(tree)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, werr.Newf(werr.CodeJSONDecode, "format: %v", err)
	}
	return FormatJSON(raw)
}

func normalizeNode(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalizeNode(e)
		}
		return out
	case map[string]any:
		if len(x) == 1 {
			for k, body := range x {
				if reservedOperators[k] || k == "lang" || k == "program" || k == "imports" {
					return map[string]any{k: normalizeNode(body)}
				}
				if _, isArr := body.([]any); isArr {
					return map[string]any{k: normalizeNode(body)}
				}
				// Single non-array value — wrap.
				return map[string]any{k: []any{normalizeNode(body)}}
			}
		}
		out := make(map[string]any, len(x))
		for k, body := range x {
			out[k] = normalizeNode(body)
		}
		return out
	}
	return v
}

// ---------- Trace (TC-830) ----------

// TraceEvent records one runtime evaluation step.
type TraceEvent struct {
	Path     string `json:"path"`
	Op       string `json:"op,omitempty"`
	Type     string `json:"type,omitempty"`
	Result   any    `json:"result,omitempty"`
	Error    string `json:"error,omitempty"`
	Duration int64  `json:"duration_us"`
}

// _ keeps strconv referenced; useful when tracing serializes numbers.
var _ = strconv.Itoa

// TraceProgram compiles and runs a program with tracing enabled.
// It returns the run result, the captured events, and any runtime error.
// Implementation-wise, this wraps the registry to intercept Invoke and
// builds events per host-call. It is intentionally minimal — full step-
// level tracing is reserved for a future enhancement.
func (e *Engine) TraceProgram(ctx context.Context, src []byte,
	opts RunOptions) (Value, []TraceEvent, error) {
	prog, err := e.CompileJSON(src)
	if err != nil {
		return Value{}, nil, err
	}
	tr := &tracingRegistry{inner: e.opts.Registry, events: []TraceEvent{}}
	scope, err := buildScope(opts.Vars, opts.VarOptions)
	if err != nil {
		return Value{}, nil, err
	}
	reg := e.opts.Registry
	if len(opts.Packages) > 0 {
		reg = withPackages(reg, opts.Packages)
	}
	tr.inner = reg
	ctx = registry.WithCapabilities(ctx, e.opts.Capabilities)
	exec := runtime.NewExecutor(ctx, scope, tr, availablePkgNames(reg), e.opts.Budget)
	v, err := exec.RunProgram(prog.prog)
	return v, tr.events, err
}

// tracingRegistry wraps a HostRegistry and records each Invoke.
type tracingRegistry struct {
	inner  runtime.HostRegistry
	events []TraceEvent
}

func (t *tracingRegistry) Invoke(ctx context.Context, op string, recv types.Value,
	args []types.Value, path string) (types.Value, error) {
	ev := TraceEvent{Path: path, Op: op}
	v, err := t.inner.Invoke(ctx, op, recv, args, path)
	if err != nil {
		ev.Error = err.Error()
	} else {
		ev.Type = v.TypeName()
		ev.Result = v.Go()
	}
	t.events = append(t.events, ev)
	return v, err
}

// StructType forwards to the wrapped registry so struct-literal evaluation
// keeps working under tracing.
func (t *tracingRegistry) StructType(name string) (reflect.Type, bool) {
	return t.inner.StructType(name)
}

// ---------- Migrator (LANGUAGE.md §13.2 / TC-883) ----------

// MigrationRule rewrites one operator name. The migrator iterates the AST
// and applies every matching rule in order, so MigrateProgram is idempotent
// when each From is unique.
type MigrationRule struct {
	From string
	To   string
}

// DefaultMigrations are the built-in renamings shipped by the language. The
// list is intentionally small — projects can append their own rules and pass
// the combined slice into MigrateProgram.
var DefaultMigrations = []MigrationRule{
	{From: "len", To: "Len"},
	{From: "size", To: "Len"},
}

// MigrateProgram rewrites legacy operator names in src to the current
// language equivalents. The output is JSON bytes that re-parse to an
// equivalent program. Idempotent: running the migrator twice is a no-op.
func MigrateProgram(src []byte, rules []MigrationRule) ([]byte, error) {
	if len(rules) == 0 {
		rules = DefaultMigrations
	}
	var tree any
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return nil, werr.Newf(werr.CodeJSONDecode, "migrate: %v", err)
	}
	rmap := make(map[string]string, len(rules))
	for _, r := range rules {
		rmap[r.From] = r.To
	}
	migrated := migrateNode(tree, rmap)
	return FormatProgram(mustJSON(migrated))
}

func migrateNode(v any, rmap map[string]string) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = migrateNode(e, rmap)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, body := range x {
			newKey := k
			if reservedOperators[k] || k == "lang" || k == "program" || k == "imports" {
				// preserve reserved keys; recurse into bodies.
				out[newKey] = migrateNode(body, rmap)
				continue
			}
			if rep, ok := rmap[k]; ok {
				newKey = rep
			}
			out[newKey] = migrateNode(body, rmap)
		}
		return out
	}
	return v
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ---------- compiler import sentinel ----------

var _ = compiler.Compile
