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

	"golang.org/x/tools/go/packages"

	wltypes "github.com/donutnomad/wlang/types"
	"github.com/donutnomad/wlang/wflang"
)

// Options configures Go to wlang translation.
type Options struct {
	FuncName       string
	PackageAliases map[string]string
	Lang           string
	Imports        []string
	Filename       string
	// Dir is the type-checking context. When set, the whole package is loaded
	// and the source under Filename is overlaid on it, so package-level symbols
	// declared in sibling files resolve. Package type errors are tolerated on top
	// of the partial type information packages.Load still returns; only a package
	// that cannot be loaded at all (import/vendor resolution failure) degrades to
	// checking Filename on its own, and then sibling-file symbols are
	// unavailable. Leave Dir empty to translate one standalone file.
	Dir              string
	EmbedImportMap   bool
	LocalPackageName string
}

// EntrySignature describes the translated function's payload boundary: which
// variable a host must bind the input to, and the descriptor IDs of the
// business input and output types.
//
// InputVar is the Go parameter name. TranslateFileDetailed seeds every named
// parameter into the local scope, so the translated body refers to the entry
// parameter by that identifier; a host binding a typed value must use this
// exact name rather than inventing one.
//
// Input and Output hold descriptor IDs as produced by addType, which are also
// the keys of Result.Types. Pointer parameters therefore carry a "*"-prefixed ID.
// Either field is empty when the corresponding type is absent or is not
// descriptor backed, which keeps them consistent with what extractDescriptors
// actually collected: error-only results, plain scalar parameters, and
// parameters the body cannot reference (unnamed, or several sharing one type).
// The translated function's own name is not repeated here: Result.FuncName
// already carries it.
type EntrySignature struct {
	InputVar string
	Input    string
	Output   string
}

// Result contains generated wlang JSON and metadata useful for storage.
type Result struct {
	JSON     []byte
	Imports  map[string]string
	FuncName string
	Source   string
	Types    map[string]wltypes.TypeDescriptor
	Entry    EntrySignature
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
	filename := opts.Filename
	if filename == "" {
		filename = "input.go"
	}
	unit, err := loadTranslationUnit(src, opts, filename)
	if err != nil {
		return nil, err
	}
	fset, file := unit.fset, unit.file
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
		packagePath:      unit.packagePath,
	}
	t.info = unit.info
	for k, v := range opts.PackageAliases {
		t.imports[k] = v
	}
	descriptors, entry, err := t.extractDescriptors(fn)
	if err != nil {
		return nil, err
	}
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			for _, name := range field.Names {
				t.locals.add(name.Name)
			}
		}
	}
	results := resultSpecs(fn.Type.Results)
	namedResults := namedResultSpecs(results)
	t.namedResults = namedResults
	for _, result := range namedResults {
		t.locals.add(result.source)
	}
	var prefix []any
	if len(namedResults) > 0 {
		prefix = t.resultZeroBindings(namedResults)
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
	if len(descriptors) > 0 {
		env["types"] = descriptors
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
		Types:    descriptors,
		Entry:    entry,
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
	packagePath      string
	namedResults     []resultSpec
	descriptorNames  map[string]bool
	// temps numbers the hidden variables the translation introduces.
	temps int
	// targets is the stack of statements break/continue can address inside
	// the function being translated; pendingLabel is the label of the
	// labelled statement about to be translated.
	targets      []*branchTarget
	pendingLabel string
}

// branchTarget is an enclosing loop, switch or select. A switch or select
// that a break addresses is wrapped in a single-iteration labelled loop, and
// a continue that has to cross it names its loop explicitly.
type branchTarget struct {
	loop  bool
	label string
	name  string
	used  bool
}

func (t *translator) tempName() string {
	t.temps++
	return fmt.Sprintf("__wlang_tmp_%d", t.temps)
}

// pushTarget opens a break/continue target, taking the pending label.
func (t *translator) pushTarget(loop bool) *branchTarget {
	target := &branchTarget{loop: loop, label: t.pendingLabel}
	t.pendingLabel = ""
	t.targets = append(t.targets, target)
	return target
}

func (t *translator) popTarget() {
	t.targets = t.targets[:len(t.targets)-1]
}

// targetName is the label emitted for a target that a break or continue
// addresses by name.
func (t *translator) targetName(target *branchTarget) string {
	target.used = true
	if target.name == "" {
		target.name = target.label
		if target.name == "" {
			t.temps++
			target.name = fmt.Sprintf("__wlang_label_%d", t.temps)
		}
	}
	return target.name
}

// labelLoop records the emitted label on a loop body map when a branch
// statement addressed the loop by name.
func labelLoop(loop map[string]any, target *branchTarget) {
	if target.used {
		loop["label"] = target.name
	}
}

// branchStmt resolves break/continue against the enclosing targets.
func (t *translator) branchStmt(x *ast.BranchStmt) ([]any, error) {
	switch x.Tok {
	case token.GOTO:
		return nil, t.unsupported(x, "goto is not supported", "use a loop with break or continue")
	case token.FALLTHROUGH:
		return nil, t.unsupported(x, "fallthrough is not supported", "list the values in one case or repeat the statements")
	}
	keyword := "break"
	if x.Tok == token.CONTINUE {
		keyword = "continue"
	}
	index := -1
	for i := len(t.targets) - 1; i >= 0; i-- {
		target := t.targets[i]
		if x.Label != nil {
			if target.label == x.Label.Name {
				index = i
				break
			}
			continue
		}
		if target.loop || x.Tok == token.BREAK {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, t.unsupported(x, keyword+" has no enclosing target", "")
	}
	target := t.targets[index]
	if x.Tok == token.CONTINUE && !target.loop {
		return nil, t.unsupported(x, "continue must name a loop", "")
	}
	if index == len(t.targets)-1 && target.loop {
		return []any{map[string]any{keyword: true}}, nil
	}
	return []any{map[string]any{keyword: t.targetName(target)}}, nil
}

// wrapBreakable wraps a switch or select that a break addressed in a
// single-iteration loop carrying the break's label.
func wrapBreakable(body []any, target *branchTarget) []any {
	if !target.used {
		return body
	}
	return []any{map[string]any{"fori": map[string]any{
		"var":   "",
		"from":  lit("int64", "0"),
		"to":    lit("int64", "1"),
		"label": target.name,
		"do":    body,
	}}}
}

type translationUnit struct {
	fset        *token.FileSet
	file        *ast.File
	info        *gotypes.Info
	packagePath string
}

func loadTranslationUnit(src []byte, opts Options, filename string) (translationUnit, error) {
	if opts.Dir == "" {
		return loadSingleFileUnit(src, filename, "")
	}
	absDir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return translationUnit{}, err
	}
	absFilename := filename
	if !filepath.IsAbs(absFilename) {
		absFilename = filepath.Join(absDir, filename)
	}
	absFilename = filepath.Clean(absFilename)
	fset := token.NewFileSet()
	cfg := &packages.Config{
		Dir:     absDir,
		Fset:    fset,
		Overlay: map[string][]byte{absFilename: src},
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
	}
	loaded, err := packages.Load(cfg, ".")
	if err != nil {
		// Import-level failure (vendor / go list). Keep the long-standing
		// tolerance and translate the file on its own; this is the only path
		// where sibling-file symbols are unavailable.
		return loadSingleFileUnit(src, filename, opts.Dir)
	}
	if len(loaded) != 1 {
		return loadSingleFileUnit(src, filename, opts.Dir)
	}
	pkg := loaded[0]
	// Errors other than syntax errors are deliberately tolerated. A type error
	// in one corner of the package (including the driver-level ListError go list
	// reports for it) must not discard the package-wide type information the
	// rest of the package provides: falling back to a single-file check used to
	// lose every package-level symbol declared in sibling files, so identifiers
	// that are functions (an activity marker, for example) came back as variable
	// references. packages.Load still fills TypesInfo for the parts it checked.
	if hasParseErrors(pkg.Errors) {
		return loadSingleFileUnit(src, filename, opts.Dir)
	}
	for _, candidate := range pkg.Syntax {
		pos := fset.Position(candidate.Pos())
		candidateName, _ := filepath.Abs(pos.Filename)
		if filepath.Clean(candidateName) == absFilename {
			return translationUnit{fset: fset, file: candidate, info: pkg.TypesInfo, packagePath: pkg.PkgPath}, nil
		}
	}
	return loadSingleFileUnit(src, filename, opts.Dir)
}

// hasParseErrors reports whether the package has a syntax error.
// Only a broken syntax tree forces the fallback to checking the translated file
// on its own; type and driver-level errors leave the parsed package (and its
// cross-file type information) usable.
func hasParseErrors(errs []packages.Error) bool {
	for _, e := range errs {
		if e.Kind == packages.ParseError {
			return true
		}
	}
	return false
}

func loadSingleFileUnit(src []byte, filename, dir string) (translationUnit, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return translationUnit{}, err
	}
	return translationUnit{
		fset: fset, file: file,
		info:        typeInfoForFile(fset, file, dir),
		packagePath: file.Name.Name,
	}, nil
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
		if ok && decl.Tok == token.CONST && t.info != nil {
			// Constant uses are folded to literals; the declaration emits nothing.
			return nil, nil
		}
		if !ok || decl.Tok != token.VAR {
			return nil, t.unsupported(s, "only var declarations are supported", "move declarations into simple var statements")
		}
		return t.varDecl(decl)
	case *ast.AssignStmt:
		return t.assign(x)
	case *ast.ReturnStmt:
		if len(x.Results) == 0 {
			if len(t.namedResults) > 0 {
				return []any{map[string]any{"return": map[string]any{"named": t.resultNames()}}}, nil
			}
			return []any{map[string]any{"return": lit("null", nil)}}, nil
		}
		values := make([]any, len(x.Results))
		for i, result := range x.Results {
			ex, err := t.expr(result)
			if err != nil {
				return nil, err
			}
			values[i] = ex
		}
		ex := values[0]
		if len(values) > 1 {
			ex = map[string]any{"tuple": values}
		}
		if len(t.namedResults) > 0 {
			return []any{map[string]any{"return": map[string]any{
				"named": t.resultNames(),
				"expr":  ex,
			}}}, nil
		}
		return []any{map[string]any{"return": ex}}, nil
	case *ast.IfStmt:
		return t.scopeInit(x.Init, func() ([]any, error) { return t.ifStmt(x) })
	case *ast.SwitchStmt:
		return t.scopeInit(x.Init, func() ([]any, error) { return t.switchStmt(x) })
	case *ast.TypeSwitchStmt:
		return t.scopeInit(x.Init, func() ([]any, error) { return t.typeSwitchStmt(x) })
	case *ast.RangeStmt:
		return t.rangeStmt(x)
	case *ast.ForStmt:
		return t.forStmt(x)
	case *ast.BranchStmt:
		return t.branchStmt(x)
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
		return t.exprStmt(x.X)
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
		switch x.Stmt.(type) {
		case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
			t.pendingLabel = x.Label.Name
		}
		return t.stmt(x.Stmt)
	case *ast.IncDecStmt:
		return t.incDec(x)
	default:
		return nil, t.unsupported(s, "statement is not supported", "keep this code in a Go host function")
	}
}

func (t *translator) exprStmt(e ast.Expr) ([]any, error) {
	ex, err := t.expr(e)
	if err != nil {
		return nil, err
	}
	return []any{map[string]any{"expr": ex}}, nil
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
	target := t.pushTarget(false)
	defer t.popTarget()
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
			// The tag is evaluated once, before the cases.
			if _, isIdent := x.Tag.(*ast.Ident); !isIdent && !t.isConstant(x.Tag) {
				name := t.tempName()
				out = append(out, map[string]any{"let": map[string]any{name: tag}})
				tag = varNode(name)
			}
		}
		chain, err := t.switchCaseChain(x.Body.List, tag)
		if err != nil {
			return err
		}
		out = append(out, wrapBreakable(chain, target)...)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// isConstant reports whether e is a compile-time constant.
func (t *translator) isConstant(e ast.Expr) bool {
	if t.info == nil {
		return false
	}
	tv, ok := t.info.Types[e]
	return ok && tv.Value != nil
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
	target := t.pushTarget(false)
	defer t.popTarget()
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
		if node, ok := subject.(map[string]any); !ok || node["var"] == nil {
			name := t.tempName()
			out = append(out, map[string]any{"let": map[string]any{name: subject}})
			subject = varNode(name)
		}
		chain, err := t.typeSwitchCaseChain(x.Body.List, subject, bindName)
		if err != nil {
			return err
		}
		out = append(out, wrapBreakable(chain, target)...)
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
			val, err := t.expr(x.Rhs[0])
			if err != nil {
				return nil, err
			}
			return t.indexAssignValue(idx, x.Tok, val)
		}
		if sel, ok := x.Lhs[0].(*ast.SelectorExpr); ok {
			path, err := t.assignablePath(sel)
			if err != nil {
				return nil, err
			}
			return t.pathAssign(path, x.Tok, x.Rhs[0], sel)
		}
		id, ok := x.Lhs[0].(*ast.Ident)
		if !ok {
			return nil, t.unsupported(x.Lhs[0], "assignment target is not supported", "keep complex writes in Go")
		}
		name := id.Name
		if name == "_" {
			if call, ok := x.Rhs[0].(*ast.CallExpr); ok {
				return t.exprStmt(call)
			}
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
			return t.tupleAssign(x, map[string]any{"map.get": []any{target, key}})
		}
		return t.tupleAssign(x, nil)
	}
	if len(x.Lhs) == len(x.Rhs) {
		return t.multiAssign(x)
	}
	return nil, t.unsupported(x, "assignment count mismatch", "")
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

func (t *translator) indexAssignValue(idx *ast.IndexExpr, tok token.Token, val any) ([]any, error) {
	if _, ok := idx.X.(*ast.Ident); !ok {
		return nil, t.unsupported(idx, "assignment to an element of a computed value is not supported", "assign through a local variable")
	}
	target, err := t.expr(idx.X)
	if err != nil {
		return nil, err
	}
	key, err := t.expr(idx.Index)
	if err != nil {
		return nil, err
	}
	setOp := "arr.set"
	getOp := "arr.get"
	if t.isMapExpr(idx.X) {
		setOp = "map.set"
		getOp = "map.value"
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
	if call.Ellipsis.IsValid() {
		// xs = append(xs, ys...): ys is evaluated once, then pushed element
		// by element.
		items, err := t.expr(call.Args[1])
		if err != nil {
			return nil, err
		}
		source, item := t.tempName(), t.tempName()
		return []any{
			map[string]any{"let": map[string]any{source: items}},
			map[string]any{"foreach": map[string]any{
				"target": varNode(source),
				"as":     item,
				"do":     []any{map[string]any{"expr": map[string]any{"arr.push": []any{varNode(target), varNode(item)}}}},
			}},
		}, nil
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
	if x.Tok == token.ASSIGN {
		return nil, t.unsupported(x, "range with = is not supported", "declare the iteration variables with :=")
	}
	key, value := rangeVarName(x.Key), rangeVarName(x.Value)
	switch kind := t.rangeKind(x.X); kind {
	case "int":
		return t.rangeInt(x, key)
	case "map":
		return t.rangeMap(x, key, value)
	case "slice", "":
	default:
		return nil, t.unsupported(x.X, "range over "+kind+" is not supported", "range over a slice")
	}
	target, err := t.expr(x.X)
	if err != nil {
		return nil, err
	}
	as := "_"
	if value != "" {
		as = value
	}
	loop := map[string]any{"target": target, "as": as}
	if key != "" {
		loop["index"] = key
	}
	if err := t.loopBody(x.Body, loop, key, value); err != nil {
		return nil, err
	}
	return []any{map[string]any{"foreach": loop}}, nil
}

func rangeVarName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok && id.Name != "_" {
		return id.Name
	}
	return ""
}

// rangeKind classifies the range expression by its Go type; "" means the
// type is unknown and the expression is treated as a slice.
func (t *translator) rangeKind(e ast.Expr) string {
	if t.info == nil {
		return ""
	}
	typ := t.info.TypeOf(e)
	if typ == nil {
		return ""
	}
	switch u := typ.Underlying().(type) {
	case *gotypes.Slice, *gotypes.Array:
		return "slice"
	case *gotypes.Map:
		return "map"
	case *gotypes.Chan:
		return "channel"
	case *gotypes.Signature:
		return "function"
	case *gotypes.Pointer:
		return "pointer"
	case *gotypes.Basic:
		switch {
		case u.Info()&gotypes.IsString != 0:
			return "string"
		case u.Info()&gotypes.IsInteger != 0:
			if basicLiteralType(u) != "int64" {
				return "integer type " + u.Name()
			}
			return "int"
		}
	}
	return typ.String()
}

// loopBody translates a loop body into loop["do"] (after any prefix already
// there) with the loop as the innermost break/continue target.
func (t *translator) loopBody(body *ast.BlockStmt, loop map[string]any, vars ...string) error {
	target := t.pushTarget(true)
	defer t.popTarget()
	var out []any
	if err := t.locals.withFrame(func() error {
		for _, name := range vars {
			t.locals.add(name)
		}
		var err error
		out, err = t.stmtList(body.List)
		return err
	}); err != nil {
		return err
	}
	if prefix, ok := loop["do"].([]any); ok {
		out = append(prefix, out...)
	}
	loop["do"] = out
	labelLoop(loop, target)
	return nil
}

// rangeInt is Go's for i := range n: n is evaluated once and each iteration
// gets the next index regardless of writes to i in the body.
func (t *translator) rangeInt(x *ast.RangeStmt, key string) ([]any, error) {
	to, err := t.expr(x.X)
	if err != nil {
		return nil, err
	}
	loop := map[string]any{"var": key, "from": lit("int64", "0"), "to": to}
	if err := t.loopBody(x.Body, loop, key); err != nil {
		return nil, err
	}
	return []any{map[string]any{"fori": loop}}, nil
}

// rangeMap visits the keys in ascending order (map.keys is sorted), a
// deterministic order among those Go's unspecified map iteration permits.
// Like Go, a key deleted before it is reached is skipped.
func (t *translator) rangeMap(x *ast.RangeStmt, key, value string) ([]any, error) {
	target, err := t.expr(x.X)
	if err != nil {
		return nil, err
	}
	mapName := t.tempName()
	if key == "" {
		key = t.tempName()
	}
	prefix := []any{map[string]any{"if": map[string]any{
		"cond": map[string]any{"!": []any{map[string]any{"map.has": []any{varNode(mapName), varNode(key)}}}},
		"then": []any{map[string]any{"continue": true}},
	}}}
	if value != "" {
		prefix = append(prefix, map[string]any{"let": map[string]any{
			value: map[string]any{"map.value": []any{varNode(mapName), varNode(key)}},
		}})
	}
	loop := map[string]any{
		"target": map[string]any{"map.keys": []any{varNode(mapName)}},
		"as":     key,
		"do":     prefix,
	}
	if err := t.loopBody(x.Body, loop, key, value); err != nil {
		return nil, err
	}
	return []any{
		map[string]any{"let": map[string]any{mapName: target}},
		map[string]any{"foreach": loop},
	}, nil
}

// forStmt translates Go's three-clause loop: the condition is evaluated
// before every iteration and the post statement after it, and body writes to
// the loop variable carry into the next iteration.
func (t *translator) forStmt(x *ast.ForStmt) ([]any, error) {
	if x.Init == nil || x.Cond == nil {
		return nil, t.unsupported(x, "for requires an init statement and a condition", "write for i := from; cond; post")
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
	loop := map[string]any{"var": id.Name, "from": from}
	err = t.locals.withFrame(func() error {
		t.locals.add(id.Name)
		cond, err := t.expr(x.Cond)
		if err != nil {
			return err
		}
		loop["cond"] = cond
		post := []any{}
		if x.Post != nil {
			if post, err = t.stmt(x.Post); err != nil {
				return err
			}
		}
		loop["post"] = post
		return t.loopBody(x.Body, loop, id.Name)
	})
	if err != nil {
		return nil, err
	}
	return []any{map[string]any{"fori": loop}}, nil
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
	target := t.pushTarget(false)
	defer t.popTarget()
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
	return wrapBreakable([]any{map[string]any{"select": cases}}, target), nil
}

func (t *translator) expr(e ast.Expr) (any, error) {
	if folded, ok, err := t.constantLiteral(e); ok || err != nil {
		return folded, err
	}
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
		if _, ok := selectorRootIdent(x.X); !ok {
			return nil, t.unsupported(x, "field access on a computed value is not supported", "assign the value to a local variable first, then read the field")
		}
		if t.isPromotedField(x) {
			return nil, t.unsupported(x, "promoted field "+x.Sel.Name+" of an embedded struct is not supported", "name the embedded field explicitly")
		}
		return varNode(selectorPath(x)), nil
	case *ast.BinaryExpr:
		op, ok := binaryOp(x.Op)
		if !ok {
			return nil, t.unsupported(x, "operator "+x.Op.String()+" is not supported", "")
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
			return map[string]any{"-": []any{t.typedZero(x.X), v}}, nil
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
		if t.scalarType(x.X) == "string" {
			return nil, t.unsupported(x, "indexing a string is not supported", "do byte and rune handling in an activity or host function")
		}
		target, err := t.expr(x.X)
		if err != nil {
			return nil, err
		}
		idx, err := t.expr(x.Index)
		if err != nil {
			return nil, err
		}
		if t.isMapExpr(x.X) {
			return map[string]any{"map.value": []any{target, idx}}, nil
		}
		return map[string]any{"arr.get": []any{target, idx}}, nil
	case *ast.SliceExpr:
		if t.scalarType(x.X) == "string" {
			return nil, t.unsupported(x, "slicing a string is not supported", "do substring handling in an activity or host function")
		}
		if x.Slice3 {
			return nil, t.unsupported(x, "full slice expression xs[a:b:c] is not supported", "use xs[a:b]")
		}
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
	if t.info != nil {
		if tv, ok := t.info.Types[x.Fun]; ok && tv.IsType() {
			return t.conversion(x, tv.Type)
		}
	}
	if x.Ellipsis.IsValid() {
		return nil, t.unsupported(x, "variadic argument spread f(xs...) is not supported", "pass the slice as one parameter")
	}
	fun := t.stripCallTypeArgs(x.Fun)
	if id, ok := fun.(*ast.Ident); ok {
		if t.info != nil {
			if builtin, isBuiltin := t.info.Uses[id].(*gotypes.Builtin); isBuiltin && !supportedBuiltins[builtin.Name()] {
				return nil, t.unsupported(x, "builtin "+builtin.Name()+" is not supported", "")
			}
		}
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
			return map[string]any{"map.del": []any{target, key}}, nil
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
			return map[string]any{"arr.cap": []any{arg}}, nil
		case "new":
			if len(x.Args) != 1 {
				return nil, t.unsupported(x, "new requires one type argument", "")
			}
			typ := t.typeName(x.Args[0])
			if typ == "" {
				return nil, t.unsupported(x.Args[0], "new type is not supported", "")
			}
			if t.isLocalStruct(x.Args[0]) {
				return nil, t.unsupported(x, "new of a struct type declared in this package is not supported", "use a struct value")
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
			if t.isMapExpr(x.Args[0]) {
				return map[string]any{"map.len": []any{arg}}, nil
			}
			if t.isChanExpr(x.Args[0]) {
				return map[string]any{"ch.len": []any{arg}}, nil
			}
			return map[string]any{"arr.len": []any{arg}}, nil
		case "append":
			return nil, t.unsupported(x, "append is only supported as the statement xs = append(xs, v...)", "assign the result back to the slice it extends")
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
			typ := t.functionFieldTypeName(field.Type)
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
	results := resultSpecs(x.Type.Results)
	namedResults := namedResultSpecs(results)
	returns := make([]any, len(results))
	for i, result := range results {
		typ := t.functionFieldTypeName(result.typ)
		if typ == "" {
			return nil, t.unsupported(result.typ, "function result type is not supported", "")
		}
		returns[i] = typ
	}
	var body []any
	prevNamed := t.namedResults
	t.namedResults = namedResults
	err := t.locals.withFrame(func() error {
		if x.Type.Params != nil {
			for _, field := range x.Type.Params.List {
				for _, name := range field.Names {
					t.locals.add(name.Name)
				}
			}
		}
		for _, result := range namedResults {
			t.locals.add(result.source)
		}
		var err error
		body, err = t.stmtList(x.Body.List)
		if err != nil {
			return err
		}
		if len(namedResults) > 0 {
			body = append(t.resultZeroBindings(namedResults), body...)
		}
		return nil
	})
	t.namedResults = prevNamed
	if err != nil {
		return nil, err
	}
	return map[string]any{"fn": map[string]any{"params": params, "returns": returns, "do": body}}, nil
}

func (t *translator) functionFieldTypeName(expr ast.Expr) string {
	if t.info != nil {
		if typ := t.info.TypeOf(expr); typ != nil && t.usesDescriptorType(typ) {
			if name := t.languageTypeName(typ); name != "" {
				return name
			}
		}
	}
	return typeName(expr)
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
	if x.Type == nil {
		return t.elidedCompositeLit(x)
	}
	switch typ := x.Type.(type) {
	case *ast.ArrayType:
		elem := t.typeName(typ.Elt)
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
		k := t.typeName(typ.Key)
		v := t.typeName(typ.Value)
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

// elidedCompositeLit translates an element literal whose type the enclosing
// literal implies, such as the {X: 1} in []Point{{X: 1}}.
func (t *translator) elidedCompositeLit(x *ast.CompositeLit) (any, error) {
	var typ gotypes.Type
	if t.info != nil {
		typ = t.info.TypeOf(x)
	}
	if named, ok := typ.(*gotypes.Named); ok {
		if _, isStruct := named.Underlying().(*gotypes.Struct); isStruct {
			if name := t.languageTypeName(named); name != "" {
				return t.structLiteral(x, name)
			}
		}
	}
	return nil, t.unsupported(x, "composite literal with an implied type is not supported here", "write the element type explicitly")
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
	case token.REM:
		return "%", true
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
	case token.REM_ASSIGN:
		return "%", true
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
	case *ast.MapType:
		key := typeName(x.Key)
		val := typeName(x.Value)
		if key == "" || val == "" {
			return ""
		}
		return "map<" + key + "," + val + ">"
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
	if t.info != nil {
		if typ := t.info.TypeOf(e); typ != nil {
			if name := t.languageTypeName(typ); name != "" && t.usesDescriptorType(typ) {
				return name
			}
		}
	}
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
	case *ast.MapType:
		key := t.typeName(x.Key)
		val := t.typeName(x.Value)
		if key == "" || val == "" {
			return ""
		}
		return "map<" + key + "," + val + ">"
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

func (t *translator) usesDescriptorType(typ gotypes.Type) bool {
	if len(t.descriptorNames) == 0 || typ == nil {
		return false
	}
	typ = gotypes.Unalias(typ)
	switch current := typ.(type) {
	case *gotypes.Named:
		return t.descriptorNames[t.languageTypeName(current)]
	case *gotypes.Pointer:
		return t.usesDescriptorType(current.Elem())
	case *gotypes.Slice:
		return t.usesDescriptorType(current.Elem())
	case *gotypes.Array:
		return t.usesDescriptorType(current.Elem())
	case *gotypes.Map:
		return t.usesDescriptorType(current.Key()) || t.usesDescriptorType(current.Elem())
	case *gotypes.Signature:
		for i := 0; i < current.Params().Len(); i++ {
			if t.usesDescriptorType(current.Params().At(i).Type()) {
				return true
			}
		}
		for i := 0; i < current.Results().Len(); i++ {
			if t.usesDescriptorType(current.Results().At(i).Type()) {
				return true
			}
		}
	}
	return false
}

func (t *translator) funcTypeParts(x *ast.FuncType) ([]string, []string, bool) {
	params := []string{}
	if x.Params != nil {
		for _, field := range x.Params.List {
			typ := t.typeName(field.Type)
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
			typ := t.typeName(field.Type)
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
	if scalar := t.scalarType(e); scalar != "" {
		// Named scalar types such as time.Duration are their underlying
		// scalar in WLang.
		e = ast.NewIdent(goScalarName(scalar))
	}
	typ := t.typeName(e)
	if strings.HasPrefix(typ, "array<") && strings.HasSuffix(typ, ">") {
		// A nil slice: length 0, encoded as JSON null like Go's.
		return zeroNode(typ)
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

// resultSpec is one result position in source order. A grouped declaration
// (a, b int) yields one spec per name; an unnamed result keeps an empty name.
// A blank "_" result keeps its position but binds a private placeholder so the
// runtime can assign and read it without exposing a source-visible name.
type resultSpec struct {
	name   string
	source string
	typ    ast.Expr
}

func resultSpecs(results *ast.FieldList) []resultSpec {
	if results == nil {
		return nil
	}
	var specs []resultSpec
	for _, field := range results.List {
		if len(field.Names) == 0 {
			specs = append(specs, resultSpec{typ: field.Type})
			continue
		}
		for _, name := range field.Names {
			spec := resultSpec{name: name.Name, source: name.Name, typ: field.Type}
			if name.Name == "_" {
				spec.name = fmt.Sprintf("__wlang_blank_result_%d", len(specs))
			}
			specs = append(specs, spec)
		}
	}
	return specs
}

// namedResultSpecs returns the results only when the Go source names them. The
// Go parser rejects mixed named/unnamed results, so a non-empty name means the
// whole list is named.
func namedResultSpecs(specs []resultSpec) []resultSpec {
	for _, spec := range specs {
		if spec.name != "" {
			return specs
		}
	}
	return nil
}

// resultNames is the runtime binding order used by return statements.
func (t *translator) resultNames() []any {
	names := make([]any, len(t.namedResults))
	for i, result := range t.namedResults {
		names[i] = result.name
	}
	return names
}

// resultZeroBindings declares the named result parameters at the function
// frame root, matching Go's zero initialization before the body runs.
func (t *translator) resultZeroBindings(results []resultSpec) []any {
	bindings := make([]any, 0, len(results))
	for _, result := range results {
		bindings = append(bindings, map[string]any{"let": map[string]any{
			result.name: t.zeroLiteral(result.typ),
		}})
	}
	return bindings
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
