package go2wlang

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	gotypes "go/types"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/donutnomad/wlang/wflang"
)

// Options configures Go to wlang translation.
type Options struct {
	FuncName         string
	PackageAliases   map[string]string
	Lang             string
	Imports          []string
	Filename         string
	Dir              string
	EmbedImportMap   bool
	LocalPackageName string
}

// Result contains generated wlang JSON and metadata useful for storage.
type Result struct {
	JSON     []byte
	Imports  map[string]string
	FuncName string
	Source   string
}

// TranslateFilePath reads a Go source file and translates one named top-level
// function into a wlang JSON program envelope. The source file directory is
// used as the type-checking context so project-local imports can be resolved.
func TranslateFilePath(filename string, opts Options) ([]byte, error) {
	result, err := TranslateFilePathDetailed(filename, opts)
	if err != nil {
		return nil, err
	}
	return result.JSON, nil
}

// TranslateFilePathDetailed reads a Go source file and translates one named
// top-level function into a wlang JSON program envelope plus metadata.
func TranslateFilePathDetailed(filename string, opts Options) (*Result, error) {
	src, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	opts.Filename = filename
	if opts.Dir == "" {
		opts.Dir = filepath.Dir(filename)
	}
	return TranslateFileDetailed(src, opts)
}

// TranslateFile translates one named top-level Go function into a wlang JSON
// program envelope.
func TranslateFile(src []byte, opts Options) ([]byte, error) {
	result, err := TranslateFileDetailed(src, opts)
	if err != nil {
		return nil, err
	}
	return result.JSON, nil
}

// TranslateFileDetailed translates one named top-level Go function into a
// wlang JSON program envelope plus metadata.
func TranslateFileDetailed(src []byte, opts Options) (*Result, error) {
	if opts.FuncName == "" {
		return nil, fmt.Errorf("FuncName is required")
	}
	fset := token.NewFileSet()
	filename := opts.Filename
	if filename == "" {
		filename = "input.go"
	}
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	fn := findFunc(file, opts.FuncName)
	if fn == nil {
		return nil, fmt.Errorf("function %s not found", opts.FuncName)
	}
	t := &translator{
		fset:             fset,
		file:             file,
		imports:          importAliases(file, opts.Dir),
		locals:           newLocalScopes(),
		localPackageName: localPackageName(file, opts),
	}
	t.info = typeInfoForFile(fset, file, opts.Dir)
	for k, v := range opts.PackageAliases {
		t.imports[k] = v
	}
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			for _, name := range field.Names {
				t.locals.add(name.Name)
			}
		}
	}
	var prefix []any
	if name, typ, ok := namedReturn(fn.Type.Results); ok {
		t.namedReturn = name
		t.locals.add(name)
		prefix = append(prefix, map[string]any{"let": map[string]any{name: zeroLiteral(typ)}})
	}
	body, err := t.stmtList(fn.Body.List)
	if err != nil {
		return nil, err
	}
	if len(prefix) > 0 {
		body = append(prefix, body...)
	}
	lang := opts.Lang
	if lang == "" {
		lang = "wflang/v1"
	}
	imports := opts.Imports
	if len(imports) == 0 {
		imports = sortedImportNames(t.usedImports)
	}
	env := map[string]any{
		"lang":    lang,
		"program": body,
	}
	if len(imports) > 0 {
		env["imports"] = imports
	}
	importMap := usedImportMap(imports, t.imports)
	if opts.EmbedImportMap && len(importMap) > 0 {
		env["importMap"] = importMap
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	formatted, err := wflang.FormatJSON(raw)
	if err != nil {
		return nil, err
	}
	return &Result{
		JSON:     formatted,
		Imports:  importMap,
		FuncName: opts.FuncName,
		Source:   filename,
	}, nil
}

type translator struct {
	fset             *token.FileSet
	file             *ast.File
	info             *gotypes.Info
	imports          map[string]string
	usedImports      map[string]bool
	locals           *localScopes
	localPackageName string
	namedReturn      string
}

type localScopes struct {
	frames []map[string]bool
}

func newLocalScopes() *localScopes {
	return &localScopes{frames: []map[string]bool{{}}}
}

func (s *localScopes) push() {
	s.frames = append(s.frames, map[string]bool{})
}

func (s *localScopes) pop() {
	if len(s.frames) > 1 {
		s.frames = s.frames[:len(s.frames)-1]
	}
}

func (s *localScopes) add(name string) {
	if name == "" || name == "_" {
		return
	}
	s.frames[len(s.frames)-1][name] = true
}

func (s *localScopes) has(name string) bool {
	for i := len(s.frames) - 1; i >= 0; i-- {
		if s.frames[i][name] {
			return true
		}
	}
	return false
}

func (s *localScopes) withFrame(fn func() error) error {
	s.push()
	defer s.pop()
	return fn()
}

func typeInfoForFile(fset *token.FileSet, file *ast.File, dir string) *gotypes.Info {
	info := &gotypes.Info{
		Types:      map[ast.Expr]gotypes.TypeAndValue{},
		Defs:       map[*ast.Ident]gotypes.Object{},
		Uses:       map[*ast.Ident]gotypes.Object{},
		Selections: map[*ast.SelectorExpr]*gotypes.Selection{},
	}
	conf := gotypes.Config{
		Importer: importer.Default(),
		Error:    func(error) {},
	}
	if dir != "" {
		cwd, err := os.Getwd()
		if err == nil {
			if chErr := os.Chdir(dir); chErr == nil {
				_, _ = conf.Check(file.Name.Name, fset, []*ast.File{file}, info)
				_ = os.Chdir(cwd)
				return info
			}
		}
	}
	_, _ = conf.Check(file.Name.Name, fset, []*ast.File{file}, info)
	return info
}

func findFunc(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name && fn.Recv == nil {
			return fn
		}
	}
	return nil
}

func importAliases(file *ast.File, dir string) map[string]string {
	out := map[string]string{}
	resolver := newPackageNameResolver(dir)
	for _, spec := range file.Imports {
		raw, _ := strconv.Unquote(spec.Path.Value)
		name := path.Base(raw)
		if spec.Name != nil {
			name = spec.Name.Name
		} else if resolved, err := resolver.packageName(raw); err == nil && resolved != "" {
			name = resolved
		}
		if name != "." && name != "_" && name != "" {
			out[name] = raw
		}
	}
	return out
}

func localPackageName(file *ast.File, opts Options) string {
	if opts.LocalPackageName != "" {
		return opts.LocalPackageName
	}
	if file.Name != nil {
		return file.Name.Name
	}
	return ""
}

func sortedImportNames(used map[string]bool) []string {
	if len(used) == 0 {
		return nil
	}
	out := make([]string, 0, len(used))
	for name := range used {
		out = append(out, name)
	}
	sortStrings(out)
	return out
}

func usedImportMap(names []string, all map[string]string) map[string]string {
	if len(names) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, name := range names {
		if path := all[name]; path != "" {
			out[name] = path
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (t *translator) stmtList(stmts []ast.Stmt) ([]any, error) {
	out := make([]any, 0, len(stmts))
	for _, stmt := range stmts {
		items, err := t.stmt(stmt)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

func (t *translator) stmt(s ast.Stmt) ([]any, error) {
	switch x := s.(type) {
	case *ast.DeclStmt:
		decl, ok := x.Decl.(*ast.GenDecl)
		if !ok || decl.Tok != token.VAR {
			return nil, t.unsupported(s, "only var declarations are supported", "move declarations into simple var statements")
		}
		return t.varDecl(decl)
	case *ast.AssignStmt:
		return t.assign(x)
	case *ast.ReturnStmt:
		if len(x.Results) == 0 {
			if t.namedReturn != "" {
				return []any{map[string]any{"return": map[string]any{"named": t.namedReturn}}}, nil
			}
			return []any{map[string]any{"return": lit("null", nil)}}, nil
		}
		if len(x.Results) != 1 {
			return nil, t.unsupported(s, "return supports zero or one value", "return a struct or tuple-producing host value")
		}
		ex, err := t.expr(x.Results[0])
		if err != nil {
			return nil, err
		}
		if t.namedReturn != "" {
			return []any{
				map[string]any{"set": map[string]any{t.namedReturn: ex}},
				map[string]any{"return": map[string]any{"named": t.namedReturn}},
			}, nil
		}
		return []any{map[string]any{"return": ex}}, nil
	case *ast.IfStmt:
		if x.Init != nil {
			return nil, t.unsupported(x.Init, "if init statements are not supported", "split the init into the previous statement")
		}
		cond, err := t.expr(x.Cond)
		if err != nil {
			return nil, err
		}
		var thenBody []any
		if err := t.locals.withFrame(func() error {
			var err error
			thenBody, err = t.stmtList(x.Body.List)
			return err
		}); err != nil {
			return nil, err
		}
		elseBody, err := t.elseBody(x.Else)
		if err != nil {
			return nil, err
		}
		body := map[string]any{"cond": cond, "then": thenBody}
		if elseBody != nil {
			body["else"] = elseBody
		}
		return []any{map[string]any{"if": body}}, nil
	case *ast.RangeStmt:
		return t.rangeStmt(x)
	case *ast.ForStmt:
		return t.forStmt(x)
	case *ast.BranchStmt:
		switch x.Tok {
		case token.BREAK:
			if x.Label != nil {
				return nil, t.unsupported(s, "labeled break is not supported", "remove labels or keep this code in Go")
			}
			return []any{map[string]any{"break": true}}, nil
		case token.CONTINUE:
			if x.Label != nil {
				return nil, t.unsupported(s, "labeled continue is not supported", "remove labels or keep this code in Go")
			}
			return []any{map[string]any{"continue": true}}, nil
		}
		return nil, t.unsupported(s, "branch statement is not supported", "use break or continue")
	case *ast.ExprStmt:
		if call, ok := x.X.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "panic" {
				if len(call.Args) != 1 {
					return nil, t.unsupported(call, "panic requires exactly one argument", "")
				}
				arg, err := t.expr(call.Args[0])
				if err != nil {
					return nil, err
				}
				return []any{map[string]any{"panic": arg}}, nil
			}
		}
		ex, err := t.expr(x.X)
		if err != nil {
			return nil, err
		}
		return []any{map[string]any{"expr": ex}}, nil
	case *ast.DeferStmt:
		c, err := t.expr(x.Call)
		if err != nil {
			return nil, err
		}
		return []any{map[string]any{"defer": c}}, nil
	case *ast.GoStmt:
		return t.goStmt(x)
	case *ast.SendStmt:
		ch, err := t.expr(x.Chan)
		if err != nil {
			return nil, err
		}
		val, err := t.expr(x.Value)
		if err != nil {
			return nil, err
		}
		return []any{map[string]any{"expr": map[string]any{"ch.send": []any{ch, val}}}}, nil
	case *ast.SelectStmt:
		return t.selectStmt(x)
	case *ast.BlockStmt:
		var body []any
		if err := t.locals.withFrame(func() error {
			var err error
			body, err = t.stmtList(x.List)
			return err
		}); err != nil {
			return nil, err
		}
		return body, nil
	case *ast.LabeledStmt:
		return nil, t.unsupported(s, "labels are not supported", "remove labels or keep this code in Go")
	case *ast.IncDecStmt:
		return nil, t.unsupported(s, "standalone inc/dec is only supported as for post", "use x = x + 1")
	default:
		return nil, t.unsupported(s, "statement is not supported", "keep this code in a Go host function")
	}
}

func (t *translator) varDecl(decl *ast.GenDecl) ([]any, error) {
	out := []any{}
	for _, spec := range decl.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			return nil, t.unsupported(spec, "only value specs are supported", "")
		}
		if len(vs.Values) != 0 && len(vs.Values) != len(vs.Names) && len(vs.Values) != 1 {
			return nil, t.unsupported(vs, "var declaration value count is unsupported", "use one value per name")
		}
		for i, name := range vs.Names {
			var expr any
			var err error
			if len(vs.Values) == 0 {
				expr = zeroLiteral(vs.Type)
			} else if len(vs.Values) == 1 {
				expr, err = t.expr(vs.Values[0])
			} else {
				expr, err = t.expr(vs.Values[i])
			}
			if err != nil {
				return nil, err
			}
			t.locals.add(name.Name)
			out = append(out, map[string]any{"let": map[string]any{name.Name: expr}})
		}
	}
	return out, nil
}

func (t *translator) assign(x *ast.AssignStmt) ([]any, error) {
	if len(x.Lhs) == 1 && len(x.Rhs) == 1 {
		if _, ok := x.Lhs[0].(*ast.Ident); !ok {
			return nil, t.unsupported(x.Lhs[0], "only identifier assignment is supported", "keep complex writes in Go")
		}
		name := x.Lhs[0].(*ast.Ident).Name
		if name == "_" {
			return nil, nil
		}
		if call, ok := x.Rhs[0].(*ast.CallExpr); ok && isIdentCall(call, "append") {
			if x.Tok != token.ASSIGN {
				return nil, t.unsupported(x, "append assignment requires =", "declare the slice before appending")
			}
			return t.appendStatements(name, call)
		}
		rhs, err := t.expr(x.Rhs[0])
		if err != nil {
			return nil, err
		}
		if x.Tok == token.DEFINE {
			t.locals.add(name)
			return []any{map[string]any{"let": map[string]any{name: rhs}}}, nil
		}
		if x.Tok == token.ASSIGN {
			return []any{map[string]any{"set": map[string]any{name: rhs}}}, nil
		}
		if op, ok := compoundOp(x.Tok); ok {
			ex := map[string]any{op: []any{varNode(name), rhs}}
			return []any{map[string]any{"set": map[string]any{name: ex}}}, nil
		}
		return nil, t.unsupported(x, "assignment operator is not supported", "use = or :=")
	}
	if len(x.Rhs) == 1 {
		names := []any{}
		for _, lhs := range x.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok {
				return nil, t.unsupported(lhs, "tuple assignment targets must be identifiers", "")
			}
			names = append(names, id.Name)
			if x.Tok == token.DEFINE {
				t.locals.add(id.Name)
			}
		}
		rhs, err := t.expr(x.Rhs[0])
		if err != nil {
			return nil, err
		}
		return []any{map[string]any{"let": []any{names, rhs}}}, nil
	}
	return nil, t.unsupported(x, "multi-value assignment requires one RHS expression", "use a host call returning a tuple")
}

func (t *translator) appendStatements(target string, call *ast.CallExpr) ([]any, error) {
	if len(call.Args) < 2 {
		return nil, t.unsupported(call, "append requires a slice and at least one value", "")
	}
	base, ok := call.Args[0].(*ast.Ident)
	if !ok || base.Name != target {
		return nil, t.unsupported(call.Args[0], "append target must match assignment target", "")
	}
	out := make([]any, 0, len(call.Args)-1)
	for _, raw := range call.Args[1:] {
		item, err := t.expr(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"expr": map[string]any{"arr.push": []any{varNode(target), item}}})
	}
	return out, nil
}

func (t *translator) elseBody(s ast.Stmt) ([]any, error) {
	if s == nil {
		return nil, nil
	}
	switch x := s.(type) {
	case *ast.BlockStmt:
		var body []any
		if err := t.locals.withFrame(func() error {
			var err error
			body, err = t.stmtList(x.List)
			return err
		}); err != nil {
			return nil, err
		}
		return body, nil
	case *ast.IfStmt:
		items, err := t.stmt(x)
		if err != nil {
			return nil, err
		}
		return items, nil
	default:
		return nil, t.unsupported(s, "else branch must be block or if", "")
	}
}

func (t *translator) rangeStmt(x *ast.RangeStmt) ([]any, error) {
	target, err := t.expr(x.X)
	if err != nil {
		return nil, err
	}
	as := "_"
	idx := ""
	if id, ok := x.Value.(*ast.Ident); ok && id.Name != "_" {
		as = id.Name
	}
	if id, ok := x.Key.(*ast.Ident); ok && id.Name != "_" {
		idx = id.Name
	}
	if as == "_" && idx != "" {
		as = idx
		idx = ""
	}
	var body []any
	if err := t.locals.withFrame(func() error {
		t.locals.add(as)
		t.locals.add(idx)
		var err error
		body, err = t.stmtList(x.Body.List)
		return err
	}); err != nil {
		return nil, err
	}
	loop := map[string]any{"target": target, "as": as, "do": body}
	if idx != "" {
		loop["index"] = idx
	}
	return []any{map[string]any{"foreach": loop}}, nil
}

func (t *translator) forStmt(x *ast.ForStmt) ([]any, error) {
	if x.Init == nil || x.Cond == nil || x.Post == nil {
		return nil, t.unsupported(x, "for requires init, condition, and post", "use a simple counted for loop")
	}
	init, ok := x.Init.(*ast.AssignStmt)
	if !ok || len(init.Lhs) != 1 || len(init.Rhs) != 1 || init.Tok != token.DEFINE {
		return nil, t.unsupported(x.Init, "for init must be i := from", "")
	}
	id, ok := init.Lhs[0].(*ast.Ident)
	if !ok {
		return nil, t.unsupported(init.Lhs[0], "for variable must be identifier", "")
	}
	from, err := t.expr(init.Rhs[0])
	if err != nil {
		return nil, err
	}
	cond, ok := x.Cond.(*ast.BinaryExpr)
	if !ok || (cond.Op != token.LSS && cond.Op != token.GEQ) {
		return nil, t.unsupported(x.Cond, "for condition must be i < to or i >= to", "")
	}
	left, ok := cond.X.(*ast.Ident)
	if !ok || left.Name != id.Name {
		return nil, t.unsupported(x.Cond, "for condition must use loop variable", "")
	}
	to, err := t.expr(cond.Y)
	if err != nil {
		return nil, err
	}
	if cond.Op == token.GEQ {
		to = map[string]any{"-": []any{to, lit("int64", "1")}}
	}
	step, err := t.forStep(x.Post, id.Name)
	if err != nil {
		return nil, err
	}
	var body []any
	if err := t.locals.withFrame(func() error {
		t.locals.add(id.Name)
		var err error
		body, err = t.stmtList(x.Body.List)
		return err
	}); err != nil {
		return nil, err
	}
	return []any{map[string]any{"fori": map[string]any{
		"var":  id.Name,
		"from": from,
		"to":   to,
		"step": step,
		"do":   body,
	}}}, nil
}

func (t *translator) forStep(post ast.Stmt, name string) (any, error) {
	switch p := post.(type) {
	case *ast.IncDecStmt:
		id, ok := p.X.(*ast.Ident)
		if !ok || id.Name != name {
			return nil, t.unsupported(post, "for post must use loop variable", "")
		}
		switch p.Tok {
		case token.INC:
			return lit("int64", "1"), nil
		case token.DEC:
			return lit("int64", "-1"), nil
		default:
			return nil, t.unsupported(post, "for post must be i++ or i--", "")
		}
	case *ast.AssignStmt:
		if len(p.Lhs) != 1 || len(p.Rhs) != 1 || (p.Tok != token.ADD_ASSIGN && p.Tok != token.SUB_ASSIGN) {
			return nil, t.unsupported(post, "for post must be i += step or i -= step", "")
		}
		id, ok := p.Lhs[0].(*ast.Ident)
		if !ok || id.Name != name {
			return nil, t.unsupported(post, "for post must update loop variable", "")
		}
		step, err := t.expr(p.Rhs[0])
		if err != nil {
			return nil, err
		}
		if p.Tok == token.SUB_ASSIGN {
			return map[string]any{"-": []any{lit("int64", "0"), step}}, nil
		}
		return step, nil
	default:
		return nil, t.unsupported(post, "for post must be i++/i-- or compound step", "")
	}
}

func (t *translator) goStmt(x *ast.GoStmt) ([]any, error) {
	if fun, ok := x.Call.Fun.(*ast.FuncLit); ok {
		if len(x.Call.Args) != 0 {
			return nil, t.unsupported(x.Call, "go func literal call with args is not supported", "capture values from outer scope or use a host function")
		}
		var body []any
		if err := t.locals.withFrame(func() error {
			var err error
			body, err = t.stmtList(fun.Body.List)
			return err
		}); err != nil {
			return nil, err
		}
		return []any{map[string]any{"routine": map[string]any{"do": body}}}, nil
	}
	call, err := t.hostCall(x.Call)
	if err != nil {
		return nil, err
	}
	return []any{map[string]any{"routine": call}}, nil
}

func (t *translator) selectStmt(x *ast.SelectStmt) ([]any, error) {
	cases := []any{}
	for _, stmt := range x.Body.List {
		cc, ok := stmt.(*ast.CommClause)
		if !ok {
			return nil, t.unsupported(stmt, "select body must contain communication clauses", "")
		}
		var do []any
		if cc.Comm == nil {
			if err := t.locals.withFrame(func() error {
				var err error
				do, err = t.stmtList(cc.Body)
				return err
			}); err != nil {
				return nil, err
			}
			cases = append(cases, map[string]any{"default": do})
			continue
		}
		inner := map[string]any{"do": do}
		switch comm := cc.Comm.(type) {
		case *ast.AssignStmt:
			if len(comm.Rhs) != 1 || len(comm.Lhs) == 0 || len(comm.Lhs) > 2 {
				return nil, t.unsupported(comm, "select receive assignment must have one or two targets", "")
			}
			ue, ok := comm.Rhs[0].(*ast.UnaryExpr)
			if !ok || ue.Op != token.ARROW {
				return nil, t.unsupported(comm.Rhs[0], "select assignment must receive from channel", "")
			}
			ch, err := t.expr(ue.X)
			if err != nil {
				return nil, err
			}
			bind := []any{}
			for _, lhs := range comm.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok {
					return nil, t.unsupported(lhs, "select receive targets must be identifiers", "")
				}
				bind = append(bind, id.Name)
			}
			inner["recv"] = []any{ch}
			inner["bind"] = bind
		case *ast.ExprStmt:
			ue, ok := comm.X.(*ast.UnaryExpr)
			if !ok || ue.Op != token.ARROW {
				return nil, t.unsupported(comm.X, "select expression case must receive from channel", "")
			}
			ch, err := t.expr(ue.X)
			if err != nil {
				return nil, err
			}
			inner["recv"] = []any{ch}
		case *ast.SendStmt:
			ch, err := t.expr(comm.Chan)
			if err != nil {
				return nil, err
			}
			val, err := t.expr(comm.Value)
			if err != nil {
				return nil, err
			}
			inner["send"] = []any{ch, val}
		default:
			return nil, t.unsupported(comm, "select case is not supported", "")
		}
		if err := t.locals.withFrame(func() error {
			if comm, ok := cc.Comm.(*ast.AssignStmt); ok && comm.Tok == token.DEFINE {
				for _, lhs := range comm.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						t.locals.add(id.Name)
					}
				}
			}
			var err error
			do, err = t.stmtList(cc.Body)
			return err
		}); err != nil {
			return nil, err
		}
		inner["do"] = do
		cases = append(cases, map[string]any{"case": inner})
	}
	return []any{map[string]any{"select": cases}}, nil
}

func (t *translator) expr(e ast.Expr) (any, error) {
	switch x := e.(type) {
	case *ast.BasicLit:
		return t.basicLit(x)
	case *ast.Ident:
		switch x.Name {
		case "nil":
			return lit("null", nil), nil
		case "true", "false":
			return lit("boolean", x.Name), nil
		default:
			return varNode(x.Name), nil
		}
	case *ast.SelectorExpr:
		root := selectorRoot(x)
		if root != "" && t.isPackageSelector(x) {
			return nil, t.unsupported(x, "package selector cannot be used as a value", "call the package function or expose a host value")
		}
		return varNode(selectorPath(x)), nil
	case *ast.BinaryExpr:
		op, ok := binaryOp(x.Op)
		if !ok {
			return nil, t.unsupported(x, "binary operator is not supported", "")
		}
		l, err := t.expr(x.X)
		if err != nil {
			return nil, err
		}
		r, err := t.expr(x.Y)
		if err != nil {
			return nil, err
		}
		return map[string]any{op: []any{l, r}}, nil
	case *ast.UnaryExpr:
		if x.Op == token.NOT {
			v, err := t.expr(x.X)
			if err != nil {
				return nil, err
			}
			return map[string]any{"!": []any{v}}, nil
		}
		if x.Op == token.ARROW {
			ch, err := t.expr(x.X)
			if err != nil {
				return nil, err
			}
			return map[string]any{"ch.recv": []any{ch}}, nil
		}
		return nil, t.unsupported(x, "unary operator is not supported", "keep pointer and arithmetic unary code in Go")
	case *ast.CallExpr:
		return t.callExpr(x)
	case *ast.CompositeLit:
		return t.compositeLit(x)
	case *ast.ParenExpr:
		return t.expr(x.X)
	case *ast.TypeAssertExpr:
		return nil, t.unsupported(x, "type assertions are not supported", "perform type-specific logic in a Go host function")
	case *ast.IndexExpr:
		target, err := t.expr(x.X)
		if err != nil {
			return nil, err
		}
		idx, err := t.expr(x.Index)
		if err != nil {
			return nil, err
		}
		return map[string]any{"arr.get": []any{target, idx}}, nil
	case *ast.FuncLit:
		return t.funcLit(x)
	default:
		return nil, t.unsupported(x, "expression is not supported", "keep this code in a Go host function")
	}
}

func (t *translator) basicLit(x *ast.BasicLit) (any, error) {
	switch x.Kind {
	case token.STRING:
		s, err := strconv.Unquote(x.Value)
		if err != nil {
			return nil, err
		}
		return lit("string", s), nil
	case token.INT:
		return lit("int64", x.Value), nil
	case token.FLOAT:
		return lit("float64", x.Value), nil
	default:
		return nil, t.unsupported(x, "literal kind is not supported", "")
	}
}

func (t *translator) callExpr(x *ast.CallExpr) (any, error) {
	if id, ok := x.Fun.(*ast.Ident); ok {
		switch id.Name {
		case "int64":
			if len(x.Args) != 1 {
				return nil, t.unsupported(x, "int64 conversion requires one argument", "")
			}
			return t.expr(x.Args[0])
		case "make":
			return t.makeExpr(x)
		case "close":
			if len(x.Args) != 1 {
				return nil, t.unsupported(x, "close requires one argument", "")
			}
			arg, err := t.expr(x.Args[0])
			if err != nil {
				return nil, err
			}
			return map[string]any{"ch.close": []any{arg}}, nil
		case "len":
			if len(x.Args) != 1 {
				return nil, t.unsupported(x, "len requires one argument", "")
			}
			arg, err := t.expr(x.Args[0])
			if err != nil {
				return nil, err
			}
			return map[string]any{"arr.len": []any{arg}}, nil
		case "append":
			if len(x.Args) != 2 {
				return nil, t.unsupported(x, "append expression supports exactly two arguments", "")
			}
			target, ok := x.Args[0].(*ast.Ident)
			if !ok {
				return nil, t.unsupported(x.Args[0], "append target must be an identifier", "")
			}
			item, err := t.expr(x.Args[1])
			if err != nil {
				return nil, err
			}
			return map[string]any{"arr.push": []any{varNode(target.Name), item}}, nil
		default:
			if t.locals.has(id.Name) {
				return t.dynamicCall(varNode(id.Name), x.Args)
			}
			return t.localPackageCall(id.Name, x.Args)
		}
	}
	if _, ok := x.Fun.(*ast.SelectorExpr); ok {
		return t.hostCall(x)
	}
	fn, err := t.expr(x.Fun)
	if err != nil {
		return nil, err
	}
	return t.dynamicCall(fn, x.Args)
}

func (t *translator) dynamicCall(fn any, args []ast.Expr) (any, error) {
	out := make([]any, 0, len(args))
	for _, arg := range args {
		ex, err := t.expr(arg)
		if err != nil {
			return nil, err
		}
		out = append(out, ex)
	}
	return map[string]any{"call": map[string]any{"fn": fn, "args": out}}, nil
}

func (t *translator) localPackageCall(name string, args []ast.Expr) (any, error) {
	callArgs := []any{map[string]any{"pkg": t.localPackageName}}
	for _, arg := range args {
		ex, err := t.expr(arg)
		if err != nil {
			return nil, err
		}
		callArgs = append(callArgs, ex)
	}
	return map[string]any{name: callArgs}, nil
}

func (t *translator) funcLit(x *ast.FuncLit) (any, error) {
	params := []any{}
	if x.Type.Params != nil {
		for _, field := range x.Type.Params.List {
			typ := typeName(field.Type)
			if typ == "" {
				return nil, t.unsupported(field.Type, "function parameter type is not supported", "")
			}
			if len(field.Names) == 0 {
				return nil, t.unsupported(field, "function literal parameters must be named", "")
			}
			for _, name := range field.Names {
				params = append(params, []any{name.Name, typ})
			}
		}
	}
	returns := []any{}
	if x.Type.Results != nil {
		for _, field := range x.Type.Results.List {
			typ := typeName(field.Type)
			if typ == "" {
				return nil, t.unsupported(field.Type, "function result type is not supported", "")
			}
			count := 1
			if len(field.Names) > 0 {
				count = len(field.Names)
			}
			for i := 0; i < count; i++ {
				returns = append(returns, typ)
			}
		}
	}
	var body []any
	prevNamed := t.namedReturn
	t.namedReturn = ""
	err := t.locals.withFrame(func() error {
		if x.Type.Params != nil {
			for _, field := range x.Type.Params.List {
				for _, name := range field.Names {
					t.locals.add(name.Name)
				}
			}
		}
		var err error
		body, err = t.stmtList(x.Body.List)
		return err
	})
	t.namedReturn = prevNamed
	if err != nil {
		return nil, err
	}
	return map[string]any{"fn": map[string]any{"params": params, "returns": returns, "do": body}}, nil
}

func (t *translator) makeExpr(x *ast.CallExpr) (any, error) {
	if len(x.Args) == 0 || len(x.Args) > 2 {
		return nil, t.unsupported(x, "make supports only make(chan T[, n])", "")
	}
	chType, ok := x.Args[0].(*ast.ChanType)
	if !ok {
		return nil, t.unsupported(x.Args[0], "make supports only channel types", "")
	}
	elem := typeName(chType.Value)
	if elem == "" {
		return nil, t.unsupported(chType.Value, "channel element type is not supported", "")
	}
	args := []any{elem}
	if len(x.Args) == 2 {
		buf, err := t.expr(x.Args[1])
		if err != nil {
			return nil, err
		}
		args = append(args, buf)
	}
	return map[string]any{"chan": args}, nil
}

func (t *translator) hostCall(x *ast.CallExpr) (any, error) {
	sel, ok := x.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, t.unsupported(x.Fun, "only selector calls are supported", "call a package function or receiver method")
	}
	args := []any{}
	root := selectorRoot(sel)
	if root != "" && t.isPackageSelector(sel) {
		t.markImport(root)
		args = append(args, map[string]any{"pkg": root})
	} else {
		recv, err := t.expr(sel.X)
		if err != nil {
			return nil, err
		}
		args = append(args, recv)
	}
	for _, arg := range x.Args {
		ex, err := t.expr(arg)
		if err != nil {
			return nil, err
		}
		args = append(args, ex)
	}
	return map[string]any{sel.Sel.Name: args}, nil
}

func (t *translator) compositeLit(x *ast.CompositeLit) (any, error) {
	switch typ := x.Type.(type) {
	case *ast.ArrayType:
		elem := typeName(typ.Elt)
		if elem == "" {
			return nil, t.unsupported(typ.Elt, "array element type is not supported", "")
		}
		items := []any{}
		for _, el := range x.Elts {
			if _, ok := el.(*ast.KeyValueExpr); ok {
				return nil, t.unsupported(el, "keyed array elements are not supported", "")
			}
			v, err := t.expr(el)
			if err != nil {
				return nil, err
			}
			items = append(items, v)
		}
		return map[string]any{"array": map[string]any{"elem": elem, "items": items}}, nil
	case *ast.MapType:
		k := typeName(typ.Key)
		v := typeName(typ.Value)
		if k == "" || v == "" {
			return nil, t.unsupported(x.Type, "map key/value type is not supported", "")
		}
		entries := []any{}
		for _, el := range x.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				return nil, t.unsupported(el, "map literal requires key/value elements", "")
			}
			key, err := t.expr(kv.Key)
			if err != nil {
				return nil, err
			}
			val, err := t.expr(kv.Value)
			if err != nil {
				return nil, err
			}
			entries = append(entries, []any{key, val})
		}
		return map[string]any{"map": map[string]any{"type": []any{k, v}, "entries": entries}}, nil
	case *ast.SelectorExpr:
		typeName := selectorPath(typ)
		root := selectorRoot(typ)
		if root != "" && t.isPackageSelector(typ) {
			t.markImport(root)
		}
		return t.structLiteral(x, typeName)
	case *ast.Ident:
		if t.localPackageName == "" {
			return nil, t.unsupported(x, "local struct literal requires a package name", "set Options.LocalPackageName or use pkg.Type")
		}
		return t.structLiteral(x, t.localPackageName+"."+typ.Name)
	default:
		return nil, t.unsupported(x.Type, "composite literal type is not supported", "")
	}
}

func (t *translator) structLiteral(x *ast.CompositeLit, typeName string) (any, error) {
	fields := map[string]any{}
	for _, el := range x.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			return nil, t.unsupported(el, "struct literal requires keyed fields", "")
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			return nil, t.unsupported(kv.Key, "struct literal field must be identifier", "")
		}
		val, err := t.expr(kv.Value)
		if err != nil {
			return nil, err
		}
		fields[key.Name] = val
	}
	return map[string]any{"struct": []any{typeName, fields}}, nil
}

func (t *translator) markImport(name string) {
	if t.usedImports == nil {
		t.usedImports = map[string]bool{}
	}
	t.usedImports[name] = true
}

func (t *translator) isPackageSelector(sel *ast.SelectorExpr) bool {
	id, ok := selectorRootIdent(sel.X)
	if !ok {
		return false
	}
	if t.info != nil {
		if _, ok := t.info.Uses[id].(*gotypes.PkgName); ok {
			return true
		}
		if obj, ok := t.info.Uses[id]; ok && obj != nil {
			return false
		}
		if obj, ok := t.info.Defs[id]; ok && obj != nil {
			return false
		}
	}
	return t.imports[id.Name] != "" && !t.locals.has(id.Name)
}

func (t *translator) unsupported(n ast.Node, reason, hint string) error {
	return diagnostic(t.fset, n, reason, hint)
}

func lit(typ string, value any) any {
	return map[string]any{"literal": map[string]any{"type": typ, "value": value}}
}

func varNode(name string) any {
	return map[string]any{"var": name}
}

func binaryOp(op token.Token) (string, bool) {
	switch op {
	case token.ADD:
		return "+", true
	case token.SUB:
		return "-", true
	case token.MUL:
		return "*", true
	case token.QUO:
		return "/", true
	case token.GTR:
		return ">", true
	case token.GEQ:
		return ">=", true
	case token.LSS:
		return "<", true
	case token.LEQ:
		return "<=", true
	case token.EQL:
		return "==", true
	case token.NEQ:
		return "!=", true
	case token.LAND:
		return "and", true
	case token.LOR:
		return "or", true
	}
	return "", false
}

func compoundOp(op token.Token) (string, bool) {
	switch op {
	case token.ADD_ASSIGN:
		return "+", true
	case token.SUB_ASSIGN:
		return "-", true
	case token.MUL_ASSIGN:
		return "*", true
	case token.QUO_ASSIGN:
		return "/", true
	}
	return "", false
}

func selectorPath(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		base := selectorPath(x.X)
		if base == "" {
			return x.Sel.Name
		}
		return base + "." + x.Sel.Name
	}
	return ""
}

func selectorRoot(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return selectorRoot(x.X)
	}
	return ""
}

func selectorRootIdent(e ast.Expr) (*ast.Ident, bool) {
	switch x := e.(type) {
	case *ast.Ident:
		return x, true
	case *ast.SelectorExpr:
		return selectorRootIdent(x.X)
	}
	return nil, false
}

func typeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return goTypeToWlang(x.Name)
	case *ast.SelectorExpr:
		return selectorPath(x)
	case *ast.ArrayType:
		elem := typeName(x.Elt)
		if elem == "" {
			return ""
		}
		return "array<" + elem + ">"
	case *ast.FuncType:
		params, returns, ok := funcTypeParts(x)
		if !ok {
			return ""
		}
		return "func<(" + strings.Join(params, ",") + ")->" + strings.Join(returns, ",") + ">"
	}
	return ""
}

func funcTypeParts(x *ast.FuncType) ([]string, []string, bool) {
	params := []string{}
	if x.Params != nil {
		for _, field := range x.Params.List {
			typ := typeName(field.Type)
			if typ == "" {
				return nil, nil, false
			}
			count := 1
			if len(field.Names) > 0 {
				count = len(field.Names)
			}
			for i := 0; i < count; i++ {
				params = append(params, typ)
			}
		}
	}
	returns := []string{}
	if x.Results != nil {
		for _, field := range x.Results.List {
			typ := typeName(field.Type)
			if typ == "" {
				return nil, nil, false
			}
			count := 1
			if len(field.Names) > 0 {
				count = len(field.Names)
			}
			for i := 0; i < count; i++ {
				returns = append(returns, typ)
			}
		}
	}
	return params, returns, true
}

func goTypeToWlang(name string) string {
	switch name {
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "int64", "int32", "int16", "int8", "uint64", "uint32", "uint16", "uint8", "float64", "float32":
		return name
	case "int":
		return "int64"
	}
	return name
}

func zeroLiteral(e ast.Expr) any {
	typ := typeName(e)
	if strings.HasPrefix(typ, "array<") && strings.HasSuffix(typ, ">") {
		return map[string]any{"array": map[string]any{"elem": typ[6 : len(typ)-1], "items": []any{}}}
	}
	switch typ {
	case "string":
		return lit("string", "")
	case "boolean":
		return lit("boolean", "false")
	case "float64", "float32":
		return lit(typeName(e), "0")
	case "int64", "int32", "int16", "int8", "uint64", "uint32", "uint16", "uint8":
		return lit(typeName(e), "0")
	default:
		return lit("null", nil)
	}
}

func namedReturn(results *ast.FieldList) (string, ast.Expr, bool) {
	if results == nil || len(results.List) != 1 {
		return "", nil, false
	}
	field := results.List[0]
	if len(field.Names) != 1 {
		return "", nil, false
	}
	return field.Names[0].Name, field.Type, true
}

func isIdentCall(call *ast.CallExpr, name string) bool {
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == name
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && strings.Compare(s[j-1], s[j]) > 0; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
