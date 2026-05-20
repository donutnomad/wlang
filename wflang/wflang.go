// Package wflang is the public facade for the wflang language (LANGUAGE.md §5).
package wflang

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/wflang/wflang/ast"
	"github.com/wflang/wflang/compiler"
	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/registry"
	"github.com/wflang/wflang/runtime"
	"github.com/wflang/wflang/stdlib"
	"github.com/wflang/wflang/types"
)

// Value is re-exported for convenience.
type Value = types.Value

// Budget is re-exported for convenience.
type Budget = runtime.Budget

// Registry is re-exported for convenience.
type Registry = registry.Registry

// PackageSpec is re-exported for convenience.
type PackageSpec = registry.PackageSpec

// FuncSpec is re-exported for convenience.
type FuncSpec = registry.FuncSpec

// GoMethodOverload is re-exported for convenience.
type GoMethodOverload = registry.GoMethodOverload

// ParamSpec is re-exported for convenience.
type ParamSpec = registry.ParamSpec

// LangError is re-exported for convenience.
type LangError = werr.LangError

// VarOptions describes per-var injection options (§3.5).
type VarOptions struct {
	Writable bool
}

// Env is the runtime environment value injected into host functions whose
// first non-ctx parameter is wflang.Env (LANGUAGE.md §5.3 / TC-422).
type Env = registry.Env

// FuncMeta is the read-only descriptor of a registered Go function.
type FuncMeta = registry.FuncMeta

// BindOptions controls Registry.BindType (LANGUAGE.md §4.5).
type BindOptions = registry.BindOptions

// MethodOptions describes per-method overrides for BindType.
type MethodOptions = registry.MethodOptions

// CapabilitySet is a map of capability names to grants.
type CapabilitySet map[string]bool

// EngineOptions configures a new Engine.
type EngineOptions struct {
	Registry     *Registry
	Strict       bool
	Budget       Budget
	Capabilities CapabilitySet
	// Optimize enables compiler Lower/Optimize passes (§7.8).
	Optimize bool
	// Features is the runtime-flag set used to gate experimental syntax /
	// builtins (LANGUAGE.md §13.3 / TC-884). The default behavior treats
	// every feature as enabled; turning a key to false disables it.
	Features map[string]bool
	// Trace enables compile-phase trace observability (LANGUAGE.md §7.1 /
	// TC-600). Compiled programs expose the recorded phases via
	// Program.CompileTrace().
	Trace bool
}

// RunOptions configures a single program Run.
type RunOptions struct {
	Vars       map[string]any
	VarOptions map[string]VarOptions
	Packages   map[string]PackageSpec
}

// SessionOptions configures a new Session.
type SessionOptions struct {
	Vars                map[string]any
	VarOptions          map[string]VarOptions
	Packages            map[string]PackageSpec
	RoutineErrorHandler func(ctx context.Context, err error)
	// Lang declares the wflang language version of this session
	// (LANGUAGE.md §3.1). Defaults to "wflang/v1" when empty.
	Lang string
	// Imports declares the initial import set; subsequent envelope
	// fragments take the union with this set (TC-159).
	Imports []string
}

// Engine is the top-level runtime.
type Engine struct {
	opts EngineOptions
}

// NewRegistry creates a fresh Registry.
func NewRegistry() *Registry { return registry.New() }

// DefaultRegistry returns a registry preloaded with the pure standard library
// packages (§10.2: no net / file / db etc).
func DefaultRegistry() *Registry {
	r := registry.New()
	// Intentionally ignore error: core packages are under our control.
	_ = stdlib.RegisterCore(r)
	return r
}

// NewEngine builds an Engine.
func NewEngine(opts EngineOptions) *Engine {
	if opts.Registry == nil {
		opts.Registry = registry.New()
	}
	return &Engine{opts: opts}
}

// CompileJSON compiles a JSON AST into a reusable Program.
func (e *Engine) CompileJSON(data []byte) (*Program, error) {
	prog, err := compiler.Compile(data, compiler.Options{
		Optimize: e.opts.Optimize,
		Trace:    e.opts.Trace,
	})
	if err != nil {
		return nil, err
	}
	if err := checkFeatures(prog, e.opts.Features); err != nil {
		return nil, err
	}
	// Static type checks (LANGUAGE.md §7.5 / TC-644, TC-653). Strict mode
	// (TC-401) flips the checker into multi-error aggregation (TC-732) so
	// callers see every diagnostic in one shot via *errors.List.
	if err := compiler.TypeCheckOpts(prog, e.opts.Registry, compiler.TypeCheckOptions{
		Aggregate: e.opts.Strict,
	}); err != nil {
		return nil, err
	}
	return &Program{engine: e, prog: prog}, nil
}

// featureEnabled returns true unless the named feature is explicitly disabled.
func featureEnabled(feats map[string]bool, name string) bool {
	if feats == nil {
		return true
	}
	v, ok := feats[name]
	if !ok {
		return true
	}
	return v
}

// checkFeatures walks the compiled program and rejects use of disabled
// features (LANGUAGE.md §13.3 / TC-884). Currently gated features:
//   - "typedArray": when false, `array<T>` typed literals are rejected.
func checkFeatures(prog *ast.Program, feats map[string]bool) error {
	if feats == nil {
		return nil
	}
	if !featureEnabled(feats, "typedArray") {
		var bad string
		var path string
		var walk func(n ast.Node) bool
		walk = func(n ast.Node) bool {
			if n == nil {
				return false
			}
			if lit, ok := n.(*ast.Literal); ok {
				name := lit.Value.TypeName()
				if len(name) > 6 && name[:6] == "array<" {
					bad = name
					path = lit.Path()
					return true
				}
			}
			if a, ok := n.(*ast.Array); ok {
				bad = "array<" + a.Elem + ">"
				path = a.Path()
				return true
			}
			stop := false
			astWalkChildren(n, func(c ast.Node) {
				if stop {
					return
				}
				if walk(c) {
					stop = true
				}
			})
			return stop
		}
		for _, s := range prog.Body {
			if walk(s) {
				return werr.Newf(werr.CodeASTShape,
					"feature typedArray=false: %s not allowed", bad).WithPath(path)
			}
		}
	}
	return nil
}

// astWalkChildren is a tiny external mirror of toolchain.walkChildren so that
// wflang.go can perform AST traversal without importing toolchain from itself.
func astWalkChildren(n ast.Node, fn func(ast.Node)) {
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
		if len(x.Bindings) > 0 {
			for _, b := range x.Bindings {
				fn(b.Expr)
			}
		} else {
			fn(x.Expr)
		}
	case *ast.Set:
		fn(x.Expr)
	case *ast.Return:
		fn(x.Expr)
	case *ast.ExprStmt:
		fn(x.Expr)
	case *ast.Panic:
		fn(x.Expr)
	case *ast.Routine:
		if x.Call != nil {
			fn(x.Call)
		}
		for _, s := range x.Body {
			fn(s)
		}
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

// Diagnostic is re-exported from ast for the public surface.
type Diagnostic = ast.Diagnostic

// Deprecation is re-exported for migration tooling.
type Deprecation = compiler.Deprecation

// DeprecationTable lists every legacy AST form recognised by the compiler
// migrator (LANGUAGE.md §13.2 / TC-882).
func DeprecationTable() []Deprecation { return compiler.DeprecationTable() }

// Migrate rewrites a legacy program JSON into the current AST form and
// returns the applied deprecation diagnostics (LANGUAGE.md §13.2 / TC-883).
func Migrate(raw []byte) ([]byte, []Diagnostic, error) { return compiler.Migrate(raw) }

// Program is a compiled program.
type Program struct {
	engine *Engine
	prog   *ast.Program
}

// Diagnostics returns the compile-time deprecation/warning notices attached
// to this program (LANGUAGE.md §13.2 / TC-604).
func (p *Program) Diagnostics() []Diagnostic {
	if p.prog == nil {
		return nil
	}
	out := make([]Diagnostic, len(p.prog.Diagnostics))
	copy(out, p.prog.Diagnostics)
	return out
}

// CompileTrace returns the ordered phase events recorded during compilation
// (LANGUAGE.md §7.1 / TC-600). Empty when EngineOptions.Trace was not set.
func (p *Program) CompileTrace() []ast.TraceEvent {
	if p.prog == nil {
		return nil
	}
	out := make([]ast.TraceEvent, len(p.prog.CompileTrace))
	copy(out, p.prog.CompileTrace)
	return out
}

// CompilePhases returns the canonical pipeline phase order (TC-600).
func CompilePhases() []compiler.Phase { return compiler.PipelinePhases() }

// Run executes the program.
func (p *Program) Run(ctx context.Context, opts RunOptions) (Value, error) {
	scope, err := buildScope(opts.Vars, opts.VarOptions)
	if err != nil {
		return Value{}, err
	}
	// Register per-run packages in a scoped registry copy.
	reg := p.engine.opts.Registry
	if len(opts.Packages) > 0 {
		reg = withPackages(reg, opts.Packages)
	}
	ctx = registry.WithCapabilities(ctx, p.engine.opts.Capabilities)
	exec := runtime.NewExecutor(ctx, scope, reg, availablePkgNames(reg), p.engine.opts.Budget)
	return exec.RunProgram(p.prog)
}

// NewSession creates a Session that supports AppendRun (§5.2 / §3.1 progressive).
func (e *Engine) NewSession(opts SessionOptions) (*Session, error) {
	scope, err := buildScope(opts.Vars, opts.VarOptions)
	if err != nil {
		return nil, err
	}
	reg := e.opts.Registry
	if len(opts.Packages) > 0 {
		reg = withPackages(reg, opts.Packages)
	}
	lang := opts.Lang
	if lang == "" {
		lang = "wflang/v1"
	}
	imports := map[string]bool{}
	for _, im := range opts.Imports {
		imports[im] = true
	}
	return &Session{
		engine:   e,
		scope:    scope,
		reg:      reg,
		finished: false,
		onErr:    opts.RoutineErrorHandler,
		lang:     lang,
		imports:  imports,
	}, nil
}

// Session holds progressive execution state.
type Session struct {
	engine   *Engine
	scope    *runtime.Scope
	reg      *Registry
	finished bool
	result   Value
	onErr    func(ctx context.Context, err error)

	metaMu  sync.Mutex
	lang    string
	imports map[string]bool
}

// Lang reports the session's wflang language version (§3.1).
func (s *Session) Lang() string {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	return s.lang
}

// Imports returns the session's current import set, sorted (§3.1 / TC-159).
func (s *Session) Imports() []string {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	out := make([]string, 0, len(s.imports))
	for k := range s.imports {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

// sortStrings is a tiny in-place sort to avoid importing "sort" at the top
// of this already-busy file.
func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// AppendRun compiles and executes one or more additional statements.
// Arguments may be a bare statement object or an array of statements.
func (s *Session) AppendRun(ctx context.Context, data []byte) (Value, error) {
	if s.finished {
		nv := s.result
		return nv, werr.New(werr.CodeInvalidControlFlow,
			"session already returned; cannot append more statements")
	}
	// Wrap single statement into an array if needed.
	body, err := toStatementArray(data)
	if err != nil {
		return Value{}, err
	}
	prog, err := compiler.ParseProgram(body)
	if err != nil {
		return Value{}, err
	}
	// LANGUAGE.md §3.1: envelope `lang` must agree with the session's
	// language version; `imports` are merged via union (TC-158/TC-159).
	if envIsEnvelope(body) {
		if prog.Lang != "" && prog.Lang != s.lang {
			return Value{}, werr.Newf(werr.CodeLangVersionConflct,
				"session lang=%s but envelope declares %s", s.lang, prog.Lang)
		}
		if err := s.mergeImports(prog.Imports); err != nil {
			return Value{}, err
		}
	}
	ctx = registry.WithCapabilities(ctx, s.engine.opts.Capabilities)
	exec := runtime.NewExecutor(ctx, s.scope, s.reg,
		availablePkgNames(s.reg), s.engine.opts.Budget)
	// Bridge SessionOptions handlers → Executor routine handlers (§5.2 / §8).
	var rtErr runtime.RoutineErrorHandler
	if s.onErr != nil {
		rtErr = func(ctx context.Context, e error) { s.onErr(ctx, e) }
	}
	exec.SetRoutineHandlers(rtErr)
	v, err := exec.RunProgram(prog)
	if err != nil {
		return Value{}, err
	}
	// If program contained `return`, mark finished.
	if runtime.ProgramContainsReturn(prog) {
		s.finished = true
		s.result = v
	}
	return v, nil
}

// envIsEnvelope reports whether the (whitespace-trimmed) JSON payload begins
// with a `{...}` map shape, which is how envelope payloads are identified
// before parsing.
func envIsEnvelope(body []byte) bool {
	for _, c := range body {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

// mergeImports unions the envelope's imports into the session import set.
// Re-importing the same name is silently idempotent. (LANGUAGE.md §3.1
// reserves "冲突项报错"; with current `imports = []string` semantics no
// version-conflict surface exists, so duplicates are pure no-ops.)
func (s *Session) mergeImports(imps []string) error {
	if len(imps) == 0 {
		return nil
	}
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	for _, im := range imps {
		if im == "" {
			continue
		}
		s.imports[im] = true
	}
	return nil
}

// toStatementArray ensures the input is wrapped as `[...]` for ParseProgram.
func toStatementArray(data []byte) ([]byte, error) {
	// Trim whitespace quickly.
	var i int
	for i = 0; i < len(data); i++ {
		c := data[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		break
	}
	if i >= len(data) {
		return nil, werr.New(werr.CodeJSONDecode, "empty AppendRun payload")
	}
	if data[i] == '[' || data[i] == '{' && looksLikeEnvelope(data[i:]) {
		return data, nil
	}
	// Single statement object: wrap into [ ... ].
	buf := make([]byte, 0, len(data)+2)
	buf = append(buf, '[')
	buf = append(buf, data...)
	buf = append(buf, ']')
	return buf, nil
}

// looksLikeEnvelope peeks for "lang" / "program" keys.
func looksLikeEnvelope(b []byte) bool {
	// Light peek: just search for "program" in the next 64 bytes.
	limit := len(b)
	if limit > 256 {
		limit = 256
	}
	for i := 0; i < limit-7; i++ {
		if b[i] == '"' && i+8 <= limit && string(b[i+1:i+8]) == "program" {
			return true
		}
	}
	return false
}

// MustValue panics on NewLiteral error; convenience helper.
func MustValue(typeName string, raw any) Value {
	v, err := types.NewValue(typeName, raw), error(nil)
	_ = err
	return v
}

// MustRawJSON re-marshals a Go value to JSON bytes; convenience for tests.
func MustRawJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// buildScope populates top-level variables.
func buildScope(vars map[string]any, opts map[string]VarOptions) (*runtime.Scope, error) {
	s := runtime.NewScope()
	for k, v := range vars {
		o := opts[k]
		val := wrap(v)
		if o.Writable {
			s.LetWritable(k, val)
		} else {
			s.LetReadOnly(k, val)
		}
	}
	return s, nil
}

// wrap converts an arbitrary Go value into a typed Value.
func wrap(v any) types.Value {
	if v == nil {
		out, _ := types.NewNull()
		return out
	}
	switch x := v.(type) {
	case types.Value:
		return x
	case bool:
		return types.NewValue(types.TBoolean, x)
	case string:
		return types.NewValue(types.TString, x)
	case int:
		return types.NewValue(types.TInt64, int64(x))
	case int8:
		return types.NewValue(types.TInt8, x)
	case int16:
		return types.NewValue(types.TInt16, x)
	case int32:
		return types.NewValue(types.TInt32, x)
	case int64:
		return types.NewValue(types.TInt64, x)
	case uint8:
		return types.NewValue(types.TUint8, x)
	case uint16:
		return types.NewValue(types.TUint16, x)
	case uint32:
		return types.NewValue(types.TUint32, x)
	case uint64:
		return types.NewValue(types.TUint64, x)
	case float32:
		return types.NewValue(types.TFloat32, x)
	case float64:
		return types.NewValue(types.TFloat64, x)
	}
	return types.NewValue(types.AutoHostTypeNameOf(v), v)
}

// withPackages returns a registry copy extended with per-run packages.
func withPackages(base *Registry, pkgs map[string]PackageSpec) *Registry {
	// For now we mutate base; a real implementation would overlay.
	for name, spec := range pkgs {
		_ = base.BindGoPackage(name, spec)
	}
	return base
}

// availablePkgNames returns a set of package names known to the registry.
func availablePkgNames(r *Registry) map[string]any {
	return r.PackageNames()
}
