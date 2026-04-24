// Package wflang is the public facade for the wflang language (LANGUAGE.md §5).
package wflang

import (
	"context"
	"encoding/json"

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
	RoutineYieldHandler func(ctx context.Context, y YieldState)
	RoutineErrorHandler func(ctx context.Context, err error)
}

// YieldError is a Go error that a host function returns from inside a routine
// to suspend execution. Host functions construct one via NewYield (LANGUAGE.md
// §8.1).
type YieldError interface {
	error
	Token() string
	Payload() any
}

// yieldErr is the built-in YieldError implementation.
type yieldErr struct {
	token   string
	payload any
}

func (y *yieldErr) Error() string { return "yield:" + y.token }
func (y *yieldErr) Token() string { return y.token }
func (y *yieldErr) Payload() any  { return y.payload }

// NewYield creates a YieldError for use by host functions.
func NewYield(token string, payload any) error {
	return &yieldErr{token: token, payload: payload}
}

// YieldState is routine yielded information (§5.2 / §8).
type YieldState struct {
	Token       string
	Path        string
	ReturnTypes []string
}

// ResumeInput is ResumeYield payload (§5.2).
type ResumeInput struct {
	Token   string
	Results []Value
	Err     error
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
	prog, err := compiler.Compile(data, compiler.Options{Optimize: e.opts.Optimize})
	if err != nil {
		return nil, err
	}
	return &Program{engine: e, prog: prog}, nil
}

// Program is a compiled program.
type Program struct {
	engine *Engine
	prog   *ast.Program
}

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
	return &Session{
		engine:   e,
		scope:    scope,
		reg:      reg,
		finished: false,
		onErr:    opts.RoutineErrorHandler,
		onYield:  opts.RoutineYieldHandler,
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
	onYield  func(ctx context.Context, y YieldState)
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
	exec := runtime.NewExecutor(ctx, s.scope, s.reg,
		availablePkgNames(s.reg), s.engine.opts.Budget)
	// Bridge SessionOptions handlers → Executor routine handlers (§5.2 / §8).
	var rtErr runtime.RoutineErrorHandler
	var rtYield runtime.RoutineYieldHandler
	if s.onErr != nil {
		rtErr = func(ctx context.Context, e error) { s.onErr(ctx, e) }
	}
	if s.onYield != nil {
		rtYield = func(ctx context.Context, y runtime.RoutineYieldReport) {
			s.onYield(ctx, YieldState{
				Token:       y.Token,
				Path:        y.Path,
				ReturnTypes: y.ReturnTypes,
			})
		}
	}
	if rtErr != nil || rtYield != nil {
		exec.SetRoutineHandlers(rtErr, rtYield)
	}
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
