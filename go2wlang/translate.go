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
		prefix = append(prefix, map[string]any{"let": map[string]any{name: t.zeroLiteral(typ)}})
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
		return t.ifStmt(x)
	case *ast.SwitchStmt:
		return t.switchStmt(x)
	case *ast.TypeSwitchStmt:
		return t.typeSwitchStmt(x)
	case *ast.RangeStmt:
		return t.rangeStmt(x)
	case *ast.ForStmt:
		return t.forStmt(x)
	case *ast.BranchStmt:
		switch x.Tok {
		case token.BREAK:
			return []any{map[string]any{"break": true}}, nil
		case token.CONTINUE:
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
		return t.stmt(x.Stmt)
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
				expr = t.zeroLiteral(vs.Type)
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

func (t *translator) ifStmt(x *ast.IfStmt) ([]any, error) {
	var out []any
	if err := t.locals.withFrame(func() error {
		if x.Init != nil {
			items, err := t.stmt(x.Init)
			if err != nil {
				return err
			}
			out = append(out, items...)
		}
		cond, err := t.expr(x.Cond)
		if err != nil {
			return err
		}
		var thenBody []any
		if err := t.locals.withFrame(func() error {
			var err error
			thenBody, err = t.stmtList(x.Body.List)
			return err
		}); err != nil {
			return err
		}
		elseBody, err := t.elseBody(x.Else)
		if err != nil {
			return err
		}
		body := map[string]any{"cond": cond, "then": thenBody}
		if elseBody != nil {
			body["else"] = elseBody
		}
		out = append(out, map[string]any{"if": body})
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (t *translator) switchStmt(x *ast.SwitchStmt) ([]any, error) {
	var out []any
	if err := t.locals.withFrame(func() error {
		if x.Init != nil {
			items, err := t.stmt(x.Init)
			if err != nil {
				return err
			}
			out = append(out, items...)
		}
		var tag any
		var err error
		if x.Tag != nil {
			tag, err = t.expr(x.Tag)
			if err != nil {
				return err
			}
		}
		chain, err := t.switchCaseChain(x.Body.List, tag)
		if err != nil {
			return err
		}
		out = append(out, chain...)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (t *translator) switchCaseChain(stmts []ast.Stmt, tag any) ([]any, error) {
	type branch struct {
		cond any
		body []any
	}
	var branches []branch
	var defaultBody []any
	for _, stmt := range stmts {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			return nil, t.unsupported(stmt, "switch body must contain case clauses", "")
		}
		var body []any
		if err := t.locals.withFrame(func() error {
			var err error
			body, err = t.stmtList(cc.Body)
			return err
		}); err != nil {
			return nil, err
		}
		if len(cc.List) == 0 {
			defaultBody = body
			continue
		}
		cond, err := t.caseCond(tag, cc.List)
		if err != nil {
			return nil, err
		}
		branches = append(branches, branch{cond: cond, body: body})
	}
	var elseBody []any
	if defaultBody != nil {
		elseBody = defaultBody
	}
	for i := len(branches) - 1; i >= 0; i-- {
		body := map[string]any{"cond": branches[i].cond, "then": branches[i].body}
		if elseBody != nil {
			body["else"] = elseBody
		}
		elseBody = []any{map[string]any{"if": body}}
	}
	return elseBody, nil
}

func (t *translator) caseCond(tag any, exprs []ast.Expr) (any, error) {
	var out any
	for _, raw := range exprs {
		ex, err := t.expr(raw)
		if err != nil {
			return nil, err
		}
		cond := ex
		if tag != nil {
			cond = map[string]any{"==": []any{tag, ex}}
		}
		if out == nil {
			out = cond
		} else {
			out = map[string]any{"or": []any{out, cond}}
		}
	}
	if out == nil {
		return lit("boolean", "false"), nil
	}
	return out, nil
}

func (t *translator) typeSwitchStmt(x *ast.TypeSwitchStmt) ([]any, error) {
	var out []any
	if err := t.locals.withFrame(func() error {
		if x.Init != nil {
			items, err := t.stmt(x.Init)
			if err != nil {
				return err
			}
			out = append(out, items...)
		}
		subject, bindName, err := t.typeSwitchSubject(x.Assign)
		if err != nil {
			return err
		}
		chain, err := t.typeSwitchCaseChain(x.Body.List, subject, bindName)
		if err != nil {
			return err
		}
		out = append(out, chain...)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (t *translator) typeSwitchSubject(stmt ast.Stmt) (any, string, error) {
	switch x := stmt.(type) {
	case *ast.ExprStmt:
		ta, ok := x.X.(*ast.TypeAssertExpr)
		if !ok || ta.Type != nil {
			return nil, "", t.unsupported(x, "type switch guard must be x.(type)", "")
		}
		subject, err := t.expr(ta.X)
		return subject, "", err
	case *ast.AssignStmt:
		if len(x.Lhs) != 1 || len(x.Rhs) != 1 {
			return nil, "", t.unsupported(x, "type switch assignment guard must have one target and one value", "")
		}
		ta, ok := x.Rhs[0].(*ast.TypeAssertExpr)
		if !ok || ta.Type != nil {
			return nil, "", t.unsupported(x, "type switch guard must be x.(type)", "")
		}
		id, ok := x.Lhs[0].(*ast.Ident)
		if !ok {
			return nil, "", t.unsupported(x.Lhs[0], "type switch target must be identifier", "")
		}
		subject, err := t.expr(ta.X)
		return subject, id.Name, err
	default:
		return nil, "", t.unsupported(stmt, "type switch guard is not supported", "")
	}
}

func (t *translator) typeSwitchCaseChain(stmts []ast.Stmt, subject any, bindName string) ([]any, error) {
	type branch struct {
		cond any
		body []any
	}
	var branches []branch
	var defaultBody []any
	for _, stmt := range stmts {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			return nil, t.unsupported(stmt, "type switch body must contain case clauses", "")
		}
		bindExpr := subject
		if len(cc.List) == 1 {
			if typ := t.caseTypeName(cc.List[0]); typ != "" {
				bindExpr = map[string]any{"type.assert": []any{subject, lit("string", typ)}}
			}
		}
		var body []any
		if err := t.locals.withFrame(func() error {
			if bindName != "" {
				t.locals.add(bindName)
			}
			var err error
			body, err = t.stmtList(cc.Body)
			if err != nil {
				return err
			}
			if bindName != "" {
				body = append([]any{map[string]any{"let": map[string]any{bindName: bindExpr}}}, body...)
			}
			return nil
		}); err != nil {
			return nil, err
		}
		if len(cc.List) == 0 {
			defaultBody = body
			continue
		}
		cond, err := t.typeCaseCond(subject, cc.List)
		if err != nil {
			return nil, err
		}
		branches = append(branches, branch{cond: cond, body: body})
	}
	var elseBody []any
	if defaultBody != nil {
		elseBody = defaultBody
	}
	for i := len(branches) - 1; i >= 0; i-- {
		body := map[string]any{"cond": branches[i].cond, "then": branches[i].body}
		if elseBody != nil {
			body["else"] = elseBody
		}
		elseBody = []any{map[string]any{"if": body}}
	}
	return elseBody, nil
}

func (t *translator) typeCaseCond(subject any, exprs []ast.Expr) (any, error) {
	var out any
	for _, raw := range exprs {
		typ := t.caseTypeName(raw)
		if typ == "" {
			return nil, t.unsupported(raw, "type switch case type is not supported", "")
		}
		cond := map[string]any{"type.is": []any{subject, lit("string", typ)}}
		if out == nil {
			out = cond
		} else {
			out = map[string]any{"or": []any{out, cond}}
		}
	}
	return out, nil
}

func (t *translator) caseTypeName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok && id.Name == "nil" {
		return typesNullName()
	}
	return t.typeName(e)
}

func typesNullName() string {
	return "null"
}

func (t *translator) assign(x *ast.AssignStmt) ([]any, error) {
	if len(x.Lhs) == 1 && len(x.Rhs) == 1 {
		if idx, ok := x.Lhs[0].(*ast.IndexExpr); ok {
			return t.indexAssign(idx, x.Tok, x.Rhs[0])
		}
		if sel, ok := x.Lhs[0].(*ast.SelectorExpr); ok {
			return t.pathAssign(selectorPath(sel), x.Tok, x.Rhs[0], sel)
		}
		id, ok := x.Lhs[0].(*ast.Ident)
		if !ok {
			return nil, t.unsupported(x.Lhs[0], "assignment target is not supported", "keep complex writes in Go")
		}
		name := id.Name
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
		if ta, ok := x.Rhs[0].(*ast.TypeAssertExpr); ok && len(x.Lhs) == 2 {
			rhs, err := t.typeAssertExpr(ta, true)
			if err != nil {
				return nil, err
			}
			return t.tupleAssign(x, rhs)
		}
		if idx, ok := x.Rhs[0].(*ast.IndexExpr); ok && len(x.Lhs) == 2 && t.isMapExpr(idx.X) {
			target, err := t.expr(idx.X)
			if err != nil {
				return nil, err
			}
			key, err := t.expr(idx.Index)
			if err != nil {
				return nil, err
			}
			return t.tupleAssign(x, map[string]any{"m.get": []any{target, key}})
		}
		return t.tupleAssign(x, nil)
	}
	return nil, t.unsupported(x, "multi-value assignment requires one RHS expression", "use a host call returning a tuple")
}

func (t *translator) tupleAssign(x *ast.AssignStmt, rhs any) ([]any, error) {
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
	var err error
	if rhs == nil {
		rhs, err = t.expr(x.Rhs[0])
		if err != nil {
			return nil, err
		}
	}
	return []any{map[string]any{"let": []any{names, rhs}}}, nil
}

func (t *translator) pathAssign(name string, tok token.Token, rhs ast.Expr, src ast.Node) ([]any, error) {
	if name == "" {
		return nil, t.unsupported(src, "selector assignment target is not supported", "")
	}
	val, err := t.expr(rhs)
	if err != nil {
		return nil, err
	}
	if tok == token.ASSIGN {
		return []any{map[string]any{"set": map[string]any{name: val}}}, nil
	}
	if op, ok := compoundOp(tok); ok {
		ex := map[string]any{op: []any{varNode(name), val}}
		return []any{map[string]any{"set": map[string]any{name: ex}}}, nil
	}
	return nil, t.unsupported(src, "assignment operator is not supported", "use = or a compound arithmetic assignment")
}

func (t *translator) indexAssign(idx *ast.IndexExpr, tok token.Token, rhs ast.Expr) ([]any, error) {
	target, err := t.expr(idx.X)
	if err != nil {
		return nil, err
	}
	key, err := t.expr(idx.Index)
	if err != nil {
		return nil, err
	}
	val, err := t.expr(rhs)
	if err != nil {
		return nil, err
	}
	setOp := "arr.set"
	getOp := "arr.get"
	if t.isMapExpr(idx.X) {
		setOp = "m.set"
		getOp = "m.value"
	}
	if tok == token.ASSIGN {
		return []any{map[string]any{"expr": map[string]any{setOp: []any{target, key, val}}}}, nil
	}
	if op, ok := compoundOp(tok); ok {
		cur := map[string]any{getOp: []any{target, key}}
		next := map[string]any{op: []any{cur, val}}
		return []any{map[string]any{"expr": map[string]any{setOp: []any{target, key, next}}}}, nil
	}
	return nil, t.unsupported(idx, "assignment operator is not supported", "use = or a compound arithmetic assignment")
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
		var body []any
		prefix := []any{}
		params := funcLitParamNames(fun)
		if len(params) != len(x.Call.Args) {
			return nil, t.unsupported(x.Call, "go function literal argument count does not match parameters", "")
		}
		for i, arg := range x.Call.Args {
			ex, err := t.expr(arg)
			if err != nil {
				return nil, err
			}
			prefix = append(prefix, map[string]any{"let": map[string]any{params[i]: ex}})
		}
		if err := t.locals.withFrame(func() error {
			for _, name := range params {
				t.locals.add(name)
			}
			var err error
			body, err = t.stmtList(fun.Body.List)
			return err
		}); err != nil {
			return nil, err
		}
		body = append(prefix, body...)
		return []any{map[string]any{"routine": map[string]any{"do": body}}}, nil
	}
	call, err := t.hostCall(x.Call)
	if err != nil {
		return nil, err
	}
	return []any{map[string]any{"routine": call}}, nil
}

func funcLitParamNames(fun *ast.FuncLit) []string {
	var out []string
	if fun == nil || fun.Type.Params == nil {
		return out
	}
	for _, field := range fun.Type.Params.List {
		for _, name := range field.Names {
			out = append(out, name.Name)
		}
	}
	return out
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
			if t.locals.has(x.Name) {
				return varNode(x.Name), nil
			}
			if sym, ok := t.staticIdentSymbol(x); ok {
				return symbolNode(sym), nil
			}
			return varNode(x.Name), nil
		}
	case *ast.SelectorExpr:
		root := selectorRoot(x)
		if root != "" && t.isPackageSelector(x) {
			t.markImport(root)
			return symbolNode(selectorPath(x)), nil
		}
		if t.isMethodValue(x) {
			recv, err := t.expr(x.X)
			if err != nil {
				return nil, err
			}
			return methodNode(recv, x.Sel.Name), nil
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
		if x.Op == token.AND {
			return t.outExpr(x.X)
		}
		if x.Op == token.ADD {
			return t.expr(x.X)
		}
		if x.Op == token.SUB {
			v, err := t.expr(x.X)
			if err != nil {
				return nil, err
			}
			return map[string]any{"-": []any{lit("int64", "0"), v}}, nil
		}
		if x.Op == token.XOR {
			v, err := t.expr(x.X)
			if err != nil {
				return nil, err
			}
			return map[string]any{"bit.not": []any{v}}, nil
		}
		if x.Op == token.MUL {
			v, err := t.expr(x.X)
			if err != nil {
				return nil, err
			}
			return map[string]any{"ptr.deref": []any{v}}, nil
		}
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
		return t.typeAssertExpr(x, false)
	case *ast.StarExpr:
		v, err := t.expr(x.X)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ptr.deref": []any{v}}, nil
	case *ast.IndexExpr:
		target, err := t.expr(x.X)
		if err != nil {
			return nil, err
		}
		idx, err := t.expr(x.Index)
		if err != nil {
			return nil, err
		}
		if t.isMapExpr(x.X) {
			return map[string]any{"m.value": []any{target, idx}}, nil
		}
		return map[string]any{"arr.get": []any{target, idx}}, nil
	case *ast.SliceExpr:
		target, err := t.expr(x.X)
		if err != nil {
			return nil, err
		}
		args := []any{target}
		if x.Low == nil {
			args = append(args, lit("int64", "0"))
		} else {
			low, err := t.expr(x.Low)
			if err != nil {
				return nil, err
			}
			args = append(args, low)
		}
		if x.High == nil {
			args = append(args, map[string]any{"arr.len": []any{target}})
		} else {
			high, err := t.expr(x.High)
			if err != nil {
				return nil, err
			}
			args = append(args, high)
		}
		return map[string]any{"arr.slice": args}, nil
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
	fun := t.stripCallTypeArgs(x.Fun)
	if id, ok := fun.(*ast.Ident); ok {
		switch id.Name {
		case "int64":
			if len(x.Args) != 1 {
				return nil, t.unsupported(x, "int64 conversion requires one argument", "")
			}
			return t.expr(x.Args[0])
		case "copy":
			return t.builtinCall("copy", x.Args)
		case "delete":
			if len(x.Args) != 2 {
				return nil, t.unsupported(x, "delete requires two arguments", "")
			}
			target, err := t.expr(x.Args[0])
			if err != nil {
				return nil, err
			}
			key, err := t.expr(x.Args[1])
			if err != nil {
				return nil, err
			}
			return map[string]any{"m.del": []any{target, key}}, nil
		case "cap":
			if len(x.Args) != 1 {
				return nil, t.unsupported(x, "cap requires one argument", "")
			}
			arg, err := t.expr(x.Args[0])
			if err != nil {
				return nil, err
			}
			if t.isChanExpr(x.Args[0]) {
				return map[string]any{"ch.cap": []any{arg}}, nil
			}
			return map[string]any{"arr.len": []any{arg}}, nil
		case "new":
			if len(x.Args) != 1 {
				return nil, t.unsupported(x, "new requires one type argument", "")
			}
			typ := t.typeName(x.Args[0])
			if typ == "" {
				return nil, t.unsupported(x.Args[0], "new type is not supported", "")
			}
			return map[string]any{"ptr.new": []any{lit("string", typ)}}, nil
		case "complex", "real", "imag":
			return t.builtinCall(id.Name, x.Args)
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
			if sym, ok := t.staticIdentSymbol(id); ok {
				return t.dynamicCall(symbolNode(sym), x.Args)
			}
			return t.localPackageCall(id.Name, x.Args)
		}
	}
	if _, ok := fun.(*ast.SelectorExpr); ok {
		orig := *x
		orig.Fun = fun
		return t.hostCall(&orig)
	}
	if fun != x.Fun {
		orig := *x
		orig.Fun = fun
		x = &orig
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

func (t *translator) stripCallTypeArgs(e ast.Expr) ast.Expr {
	switch x := e.(type) {
	case *ast.IndexExpr:
		if t.isTypeExpr(x.Index) {
			return x.X
		}
	case *ast.IndexListExpr:
		allTypes := true
		for _, idx := range x.Indices {
			if !t.isTypeExpr(idx) {
				allTypes = false
				break
			}
		}
		if allTypes {
			return x.X
		}
	}
	return e
}

func (t *translator) isTypeExpr(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Ident:
		if t.info == nil {
			return isPredeclaredTypeName(x.Name)
		}
		_, ok := t.info.Uses[x].(*gotypes.TypeName)
		return ok || isPredeclaredTypeName(x.Name)
	case *ast.SelectorExpr:
		if t.info == nil {
			return true
		}
		_, ok := t.info.Uses[x.Sel].(*gotypes.TypeName)
		return ok
	case *ast.StarExpr:
		return t.isTypeExpr(x.X)
	case *ast.ArrayType, *ast.MapType, *ast.ChanType, *ast.FuncType, *ast.InterfaceType, *ast.StructType:
		return true
	}
	return false
}

func (t *translator) builtinCall(name string, args []ast.Expr) (any, error) {
	out := make([]any, 0, len(args))
	for _, arg := range args {
		ex, err := t.expr(arg)
		if err != nil {
			return nil, err
		}
		out = append(out, ex)
	}
	return map[string]any{name: out}, nil
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

func (t *translator) typeAssertExpr(x *ast.TypeAssertExpr, withOK bool) (any, error) {
	target, err := t.expr(x.X)
	if err != nil {
		return nil, err
	}
	typ := t.typeName(x.Type)
	if typ == "" {
		return nil, t.unsupported(x.Type, "type assertion target is not supported", "")
	}
	op := "type.assert"
	if withOK {
		op = "type.assert.ok"
	}
	return map[string]any{op: []any{target, lit("string", typ)}}, nil
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
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				idx, ok := intLiteralIndex(kv.Key)
				if !ok {
					return nil, t.unsupported(kv.Key, "array literal key must be an integer literal", "")
				}
				for len(items) <= idx {
					items = append(items, zeroForTypeName(elem))
				}
				v, err := t.expr(kv.Value)
				if err != nil {
					return nil, err
				}
				items[idx] = v
				continue
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
	names := t.structFieldNames(x)
	for i, el := range x.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			if i >= len(names) {
				return nil, t.unsupported(el, "struct literal has more values than fields", "")
			}
			val, err := t.expr(el)
			if err != nil {
				return nil, err
			}
			fields[names[i]] = val
			continue
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

func (t *translator) structFieldNames(x *ast.CompositeLit) []string {
	if t.info != nil {
		if tv, ok := t.info.Types[x]; ok && tv.Type != nil {
			if st, ok := tv.Type.Underlying().(*gotypes.Struct); ok {
				out := make([]string, 0, st.NumFields())
				for i := 0; i < st.NumFields(); i++ {
					out = append(out, st.Field(i).Name())
				}
				return out
			}
		}
	}
	return nil
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

func (t *translator) isMethodValue(sel *ast.SelectorExpr) bool {
	if t.info == nil {
		return false
	}
	selection := t.info.Selections[sel]
	return selection != nil && selection.Kind() == gotypes.MethodVal
}

func (t *translator) isMapExpr(e ast.Expr) bool {
	if t.info == nil {
		return false
	}
	tv, ok := t.info.Types[e]
	if !ok || tv.Type == nil {
		return false
	}
	_, ok = tv.Type.Underlying().(*gotypes.Map)
	return ok
}

func (t *translator) isChanExpr(e ast.Expr) bool {
	if t.info == nil {
		return false
	}
	tv, ok := t.info.Types[e]
	if !ok || tv.Type == nil {
		return false
	}
	_, ok = tv.Type.Underlying().(*gotypes.Chan)
	return ok
}

func (t *translator) staticIdentSymbol(id *ast.Ident) (string, bool) {
	if id == nil || t.locals.has(id.Name) || t.info == nil {
		return "", false
	}
	obj := t.info.Uses[id]
	fn, ok := obj.(*gotypes.Func)
	if !ok {
		return "", false
	}
	pkgName := t.localPackageName
	if fn.Pkg() != nil && (t.file == nil || fn.Pkg().Name() != t.file.Name.Name) {
		pkgName = fn.Pkg().Name()
	}
	if pkgName == "" {
		return fn.Name(), true
	}
	return pkgName + "." + fn.Name(), true
}

func (t *translator) outExpr(e ast.Expr) (any, error) {
	switch x := e.(type) {
	case *ast.Ident:
		return outNode(x.Name), nil
	case *ast.SelectorExpr:
		path := selectorPath(x)
		if path == "" {
			return nil, t.unsupported(x, "address-of selector target is not supported", "")
		}
		return outNode(path), nil
	case *ast.IndexExpr:
		return nil, t.unsupported(x, "address-of index is not supported", "store the value in a local variable and pass &local")
	default:
		return nil, t.unsupported(e, "address-of expression is not supported", "store the value in a local variable and pass &local")
	}
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

func symbolNode(name string) any {
	return map[string]any{"symbol": name}
}

func methodNode(receiver any, name string) any {
	return map[string]any{"method": []any{receiver, name}}
}

func outNode(name string) any {
	return map[string]any{"out": name}
}

func zeroNode(typeName string) any {
	return map[string]any{"zero": typeName}
}

func intLiteralIndex(e ast.Expr) (int, bool) {
	litNode, ok := e.(*ast.BasicLit)
	if !ok || litNode.Kind != token.INT {
		return 0, false
	}
	n, err := strconv.Atoi(litNode.Value)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func zeroForTypeName(typ string) any {
	switch typ {
	case "string":
		return lit("string", "")
	case "boolean":
		return lit("boolean", "false")
	case "float64", "float32":
		return lit(typ, "0")
	case "int64", "int32", "int16", "int8", "uint64", "uint32", "uint16", "uint8":
		return lit(typ, "0")
	case "error", "any", "":
		return lit("null", nil)
	default:
		return zeroNode(typ)
	}
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
	case *ast.StarExpr:
		elem := typeName(x.X)
		if elem == "" {
			return ""
		}
		return "*" + elem
	case *ast.FuncType:
		params, returns, ok := funcTypeParts(x)
		if !ok {
			return ""
		}
		return "func<(" + strings.Join(params, ",") + ")->" + strings.Join(returns, ",") + ">"
	}
	return ""
}

func (t *translator) typeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		if isPredeclaredTypeName(x.Name) {
			return goTypeToWlang(x.Name)
		}
		if t.localPackageName != "" {
			return t.localPackageName + "." + x.Name
		}
		return x.Name
	case *ast.SelectorExpr:
		return selectorPath(x)
	case *ast.ArrayType:
		elem := t.typeName(x.Elt)
		if elem == "" {
			return ""
		}
		return "array<" + elem + ">"
	case *ast.StarExpr:
		elem := t.typeName(x.X)
		if elem == "" {
			return ""
		}
		return "*" + elem
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
	case "error", "any":
		return name
	}
	return name
}

func isPredeclaredTypeName(name string) bool {
	switch name {
	case "string", "bool", "int", "int64", "int32", "int16", "int8",
		"uint64", "uint32", "uint16", "uint8", "float64", "float32",
		"error", "any":
		return true
	}
	return false
}

func (t *translator) zeroLiteral(e ast.Expr) any {
	typ := t.typeName(e)
	if strings.HasPrefix(typ, "array<") && strings.HasSuffix(typ, ">") {
		return map[string]any{"array": map[string]any{"elem": typ[6 : len(typ)-1], "items": []any{}}}
	}
	switch typ {
	case "string":
		return lit("string", "")
	case "boolean":
		return lit("boolean", "false")
	case "float64", "float32":
		return lit(typ, "0")
	case "int64", "int32", "int16", "int8", "uint64", "uint32", "uint16", "uint8":
		return lit(typ, "0")
	case "error", "any", "":
		return lit("null", nil)
	default:
		return zeroNode(typ)
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
