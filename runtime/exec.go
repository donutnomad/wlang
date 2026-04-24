package runtime

import (
	"context"
	"errors"

	"github.com/wflang/wflang/ast"
	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/types"
)

// yieldLike is the duck-typed interface the runtime uses to detect yield errors
// without importing the wflang facade package. Any error that exposes Token()
// and Payload() is treated as a yield (§8.1).
type yieldLike interface {
	error
	Token() string
	Payload() any
}

// asYield inspects an error chain and returns the first yieldLike encountered.
func asYield(err error) (yieldLike, bool) {
	if err == nil {
		return nil, false
	}
	var y yieldLike
	if errors.As(err, &y) {
		return y, true
	}
	// errors.As uses target type; yieldLike is an interface, so As works through
	// wrappers already. Nothing else to do.
	return nil, false
}

// HostRegistry is the interface the executor needs from the host registry.
// The concrete implementation lives in the registry package.
type HostRegistry interface {
	Invoke(ctx context.Context, op string, recv types.Value,
		args []types.Value, path string) (types.Value, error)
}

// Signal is an out-of-band control-flow code.
type Signal int

const (
	sigNone Signal = iota
	sigReturn
	sigBreak
	sigContinue
)

// errReturnSig / errBreakSig / errContinueSig implement control flow via errors
// so cross-function unwinding is trivial. Exec unwraps them.
type errReturnSig struct{ v types.Value }

func (errReturnSig) Error() string { return "__signal:return" }

type errBreakSig struct{}

func (errBreakSig) Error() string { return "__signal:break" }

type errContinueSig struct{}

func (errContinueSig) Error() string { return "__signal:continue" }

func sigToErr(s Signal) error {
	switch s {
	case sigReturn:
		return errReturnSig{}
	case sigBreak:
		return errBreakSig{}
	case sigContinue:
		return errContinueSig{}
	}
	return nil
}

// Budget defines runtime limits (§10.1).
type Budget struct {
	MaxSteps          int64
	MaxCallDepth      int
	MaxLoopIterations int64
	MaxRoutines       int
	MaxArrayLength    int
	MaxObjectKeys     int
	MaxStringBytes    int
	MaxAllocBytes     int64
	// Timeout is enforced via ctx deadline; no direct field needed here.
}

// RoutineErrorHandler processes non-yield errors raised inside a routine.
type RoutineErrorHandler func(ctx context.Context, err error)

// RoutineYieldReport is the suspend descriptor handed to a yield handler.
type RoutineYieldReport struct {
	Token       string
	Path        string
	ReturnTypes []string
}

// RoutineYieldHandler receives yield reports produced by a routine call.
type RoutineYieldHandler func(ctx context.Context, y RoutineYieldReport)

// Executor runs statements within a scope chain.
type Executor struct {
	ctx          context.Context
	scope        *Scope
	registry     HostRegistry
	pkgs         map[string]any
	budget       Budget
	stepCount    int64
	loopCount    int64
	callDepth    int
	maxLoopCtx   int // current loop depth for break/continue validity
	onRoutineErr RoutineErrorHandler
	onYield      RoutineYieldHandler
}

// SetRoutineHandlers wires the handlers used by execRoutine (§3.3 / §8).
func (e *Executor) SetRoutineHandlers(onErr RoutineErrorHandler, onYield RoutineYieldHandler) {
	e.onRoutineErr = onErr
	e.onYield = onYield
}

// NewExecutor constructs a fresh executor.
func NewExecutor(ctx context.Context, scope *Scope, reg HostRegistry,
	pkgs map[string]any, budget Budget) *Executor {
	if scope == nil {
		scope = NewScope()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return &Executor{ctx: ctx, scope: scope, registry: reg, pkgs: pkgs, budget: budget}
}

// Scope returns the current scope.
func (e *Executor) Scope() *Scope { return e.scope }

// Ctx returns the executor's context.
func (e *Executor) Ctx() context.Context { return e.ctx }

// Pkgs returns the package map.
func (e *Executor) Pkgs() map[string]any { return e.pkgs }

func (e *Executor) tickStep() error {
	e.stepCount++
	if e.budget.MaxSteps > 0 && e.stepCount > e.budget.MaxSteps {
		return werr.Newf(werr.CodeBudget, "MaxSteps exceeded (%d)", e.budget.MaxSteps)
	}
	// Cooperative ctx cancellation check at step boundaries (§5.5 / TC-445).
	if err := e.ctx.Err(); err != nil {
		return werr.Newf(werr.CodeRuntime, "ctx: %v", err)
	}
	return nil
}

// Exec runs a single statement. It returns:
//
//	v   – last value (optional)
//	sig – control-flow signal (return/break/continue) – never returned directly,
//	      instead surfaced via err
//	err – either a real diagnostic error or an internal signal wrapper
func (e *Executor) Exec(n ast.Node) (types.Value, Signal, error) {
	if err := e.tickStep(); err != nil {
		return types.Value{}, sigNone, err
	}
	switch x := n.(type) {
	case *ast.Let:
		v, err := e.Eval(x.Expr)
		if err != nil {
			return types.Value{}, sigNone, err
		}
		if x.Type != "" && x.Type != v.TypeName() {
			return types.Value{}, sigNone, werr.Newf(werr.CodeType,
				"let %s: declared %s, got %s", x.Name, x.Type, v.TypeName()).WithPath(x.Path())
		}
		e.scope.Let(x.Name, v, x.Type)
		return v, sigNone, nil
	case *ast.Set:
		v, err := e.Eval(x.Expr)
		if err != nil {
			return types.Value{}, sigNone, err
		}
		if err := e.scope.Set(x.Name, v); err != nil {
			if le, ok := err.(*werr.LangError); ok {
				return types.Value{}, sigNone, le.WithPath(x.Path())
			}
			return types.Value{}, sigNone, err
		}
		return v, sigNone, nil
	case *ast.Return:
		v, err := e.Eval(x.Expr)
		if err != nil {
			return types.Value{}, sigNone, err
		}
		return v, sigReturn, errReturnSig{v: v}
	case *ast.IfStmt:
		cond, err := e.Eval(x.Cond)
		if err != nil {
			return types.Value{}, sigNone, err
		}
		b, ok := cond.Go().(bool)
		if !ok {
			return types.Value{}, sigNone, werr.Newf(werr.CodeType,
				"if.cond must be boolean, got %s", cond.TypeName()).WithPath(x.Cond.Path())
		}
		var branch []ast.Node
		if b {
			branch = x.Then
		} else {
			branch = x.Else
		}
		return e.execBlock(branch)
	case *ast.Foreach:
		return e.execForeach(x)
	case *ast.Fori:
		return e.execFori(x)
	case *ast.Break:
		if e.maxLoopCtx == 0 {
			return types.Value{}, sigNone, werr.New(werr.CodeInvalidControlFlow,
				"break outside of loop").WithPath(x.Path())
		}
		return types.Value{}, sigBreak, errBreakSig{}
	case *ast.Continue:
		if e.maxLoopCtx == 0 {
			return types.Value{}, sigNone, werr.New(werr.CodeInvalidControlFlow,
				"continue outside of loop").WithPath(x.Path())
		}
		return types.Value{}, sigContinue, errContinueSig{}
	case *ast.Panic:
		v, err := e.Eval(x.Expr)
		if err != nil {
			return types.Value{}, sigNone, err
		}
		msg, _ := v.Go().(string)
		return types.Value{}, sigNone, werr.Newf(werr.CodePanic,
			"panic: %v", msg).WithPath(x.Path())
	case *ast.ExprStmt:
		if _, err := e.Eval(x.Expr); err != nil {
			return types.Value{}, sigNone, err
		}
		nullV, _ := types.NewNull()
		return nullV, sigNone, nil
	case *ast.Routine:
		return e.execRoutine(x)
	case *ast.Try:
		return e.execTry(x)
	}
	// Fallback: statement is actually an expression-producing node.
	v, err := e.Eval(n)
	return v, sigNone, err
}

// execBlock runs a sequence of statements within a new scope.
func (e *Executor) execBlock(stmts []ast.Node) (types.Value, Signal, error) {
	e.scope = e.scope.Push()
	defer func() { e.scope = e.scope.Pop() }()
	var last types.Value
	last, _ = types.NewNull()
	for _, s := range stmts {
		v, sig, err := e.Exec(s)
		if err != nil {
			return v, sig, err
		}
		if sig != sigNone {
			return v, sig, nil
		}
		last = v
	}
	return last, sigNone, nil
}

// execTry runs the try-Do block; on a non-signal, non-panic error it captures
// the error as a typed `error` value bound to x.Bind in the Catch block.
func (e *Executor) execTry(x *ast.Try) (types.Value, Signal, error) {
	v, sig, err := e.execBlock(x.Do)
	if err == nil {
		return v, sig, nil
	}
	// Don't catch control flow signals.
	if _, isReturn := err.(errReturnSig); isReturn {
		return v, sig, err
	}
	if _, isBreak := err.(errBreakSig); isBreak {
		return v, sig, err
	}
	if _, isCont := err.(errContinueSig); isCont {
		return v, sig, err
	}
	// Don't catch E_PANIC; panic terminates the program.
	if le, ok := err.(*werr.LangError); ok && le.Code == werr.CodePanic {
		return v, sig, err
	}
	// Capture as a typed `error` value. Unwrap to underlying Go error when
	// the diagnostic wraps one (CodeHost etc.), so `err.Error()` returns the
	// original message rather than the diagnostic envelope.
	underlying := err
	if le, ok := err.(*werr.LangError); ok && le.Cause != nil {
		underlying = le.Cause
	}
	errVal := types.NewValue(types.TError, underlying)
	e.scope = e.scope.Push()
	defer func() { e.scope = e.scope.Pop() }()
	e.scope.Let(x.Bind, errVal, types.TError)
	return e.execBlock(x.Catch)
}

func (e *Executor) execForeach(x *ast.Foreach) (types.Value, Signal, error) {
	tv, err := e.Eval(x.Target)
	if err != nil {
		return types.Value{}, sigNone, err
	}
	// Only array<T> types are iterable for now.
	items, elemName, ok := extractArrayItems(tv)
	if !ok {
		return types.Value{}, sigNone, werr.Newf(werr.CodeType,
			"foreach target must be array, got %s", tv.TypeName()).WithPath(x.Target.Path())
	}
	e.maxLoopCtx++
	defer func() { e.maxLoopCtx-- }()
	var last types.Value
	last, _ = types.NewNull()
	for i, item := range items {
		e.loopCount++
		if e.budget.MaxLoopIterations > 0 && e.loopCount > e.budget.MaxLoopIterations {
			return types.Value{}, sigNone, werr.Newf(werr.CodeBudget,
				"MaxLoopIterations exceeded").WithPath(x.Path())
		}
		e.scope = e.scope.Push()
		if x.As != "" {
			e.scope.Let(x.As, types.NewValue(elemName, item), "")
		}
		if x.Index != "" {
			e.scope.Let(x.Index, types.NewValue(types.TInt64, int64(i)), "")
		}
		v, sig, err := runStatements(e, x.Do)
		e.scope = e.scope.Pop()
		if err != nil {
			if _, isBreak := err.(errBreakSig); isBreak {
				break
			}
			if _, isCont := err.(errContinueSig); isCont {
				continue
			}
			return v, sigNone, err
		}
		if sig == sigReturn {
			return v, sigReturn, errReturnSig{v: v}
		}
		last = v
	}
	return last, sigNone, nil
}

func (e *Executor) execFori(x *ast.Fori) (types.Value, Signal, error) {
	fromV, err := e.Eval(x.From)
	if err != nil {
		return types.Value{}, sigNone, err
	}
	toV, err := e.Eval(x.To)
	if err != nil {
		return types.Value{}, sigNone, err
	}
	if fromV.TypeName() != toV.TypeName() {
		return types.Value{}, sigNone, werr.Newf(werr.CodeType,
			"fori from/to type mismatch %s vs %s",
			fromV.TypeName(), toV.TypeName()).WithPath(x.Path())
	}
	if fromV.TypeName() != types.TInt64 {
		return types.Value{}, sigNone, werr.Newf(werr.CodeType,
			"fori currently requires int64 bounds, got %s", fromV.TypeName()).WithPath(x.Path())
	}
	from := fromV.Go().(int64)
	to := toV.Go().(int64)
	var step int64 = 1
	if x.Step != nil {
		stV, err := e.Eval(x.Step)
		if err != nil {
			return types.Value{}, sigNone, err
		}
		if stV.TypeName() != types.TInt64 {
			return types.Value{}, sigNone, werr.Newf(werr.CodeType,
				"fori step must be int64, got %s", stV.TypeName()).WithPath(x.Step.Path())
		}
		step = stV.Go().(int64)
	}
	if step == 0 {
		return types.Value{}, sigNone, werr.New(werr.CodeRuntime,
			"fori step cannot be 0").WithPath(x.Path())
	}
	e.maxLoopCtx++
	defer func() { e.maxLoopCtx-- }()
	var last types.Value
	last, _ = types.NewNull()
	cond := func(i int64) bool {
		if step > 0 {
			return i < to
		}
		return i > to
	}
	for i := from; cond(i); i += step {
		e.loopCount++
		if e.budget.MaxLoopIterations > 0 && e.loopCount > e.budget.MaxLoopIterations {
			return types.Value{}, sigNone, werr.New(werr.CodeBudget,
				"MaxLoopIterations exceeded").WithPath(x.Path())
		}
		e.scope = e.scope.Push()
		if x.Var != "" {
			e.scope.Let(x.Var, types.NewValue(types.TInt64, i), types.TInt64)
		}
		v, sig, err := runStatements(e, x.Do)
		e.scope = e.scope.Pop()
		if err != nil {
			if _, isBreak := err.(errBreakSig); isBreak {
				break
			}
			if _, isCont := err.(errContinueSig); isCont {
				continue
			}
			return v, sigNone, err
		}
		if sig == sigReturn {
			return v, sigReturn, errReturnSig{v: v}
		}
		last = v
	}
	return last, sigNone, nil
}

// runStatements runs stmts within the current scope (no push). Break/continue
// are surfaced via err so the calling loop can react.
func runStatements(e *Executor, stmts []ast.Node) (types.Value, Signal, error) {
	var last types.Value
	last, _ = types.NewNull()
	for _, s := range stmts {
		v, sig, err := e.Exec(s)
		if err != nil {
			return v, sig, err
		}
		if sig != sigNone {
			return v, sig, nil
		}
		last = v
	}
	return last, sigNone, nil
}

func extractArrayItems(v types.Value) ([]any, string, bool) {
	name := v.TypeName()
	if len(name) < 7 || name[:6] != "array<" || name[len(name)-1] != '>' {
		return nil, "", false
	}
	elem := name[6 : len(name)-1]
	switch arr := v.Go().(type) {
	case []int8:
		out := make([]any, len(arr))
		for i, x := range arr {
			out[i] = x
		}
		return out, elem, true
	case []int16:
		out := make([]any, len(arr))
		for i, x := range arr {
			out[i] = x
		}
		return out, elem, true
	case []int32:
		out := make([]any, len(arr))
		for i, x := range arr {
			out[i] = x
		}
		return out, elem, true
	case []int64:
		out := make([]any, len(arr))
		for i, x := range arr {
			out[i] = x
		}
		return out, elem, true
	case []uint8:
		out := make([]any, len(arr))
		for i, x := range arr {
			out[i] = x
		}
		return out, elem, true
	case []uint16:
		out := make([]any, len(arr))
		for i, x := range arr {
			out[i] = x
		}
		return out, elem, true
	case []uint32:
		out := make([]any, len(arr))
		for i, x := range arr {
			out[i] = x
		}
		return out, elem, true
	case []uint64:
		out := make([]any, len(arr))
		for i, x := range arr {
			out[i] = x
		}
		return out, elem, true
	case []float32:
		out := make([]any, len(arr))
		for i, x := range arr {
			out[i] = x
		}
		return out, elem, true
	case []float64:
		out := make([]any, len(arr))
		for i, x := range arr {
			out[i] = x
		}
		return out, elem, true
	case []bool:
		out := make([]any, len(arr))
		for i, x := range arr {
			out[i] = x
		}
		return out, elem, true
	case []string:
		out := make([]any, len(arr))
		for i, x := range arr {
			out[i] = x
		}
		return out, elem, true
	case []any:
		return arr, elem, true
	}
	return nil, "", false
}

// ProgramContainsReturn reports whether the program's top-level body has any
// `return` statement (used by Session to decide session completion).
func ProgramContainsReturn(p *ast.Program) bool {
	for _, s := range p.Body {
		if containsReturn(s) {
			return true
		}
	}
	return false
}

func containsReturn(n ast.Node) bool {
	switch x := n.(type) {
	case *ast.Return:
		return true
	case *ast.IfStmt:
		for _, s := range x.Then {
			if containsReturn(s) {
				return true
			}
		}
		for _, s := range x.Else {
			if containsReturn(s) {
				return true
			}
		}
	}
	return false
}

// RunProgram executes a full program and returns the program's result value.
func (e *Executor) RunProgram(p *ast.Program) (types.Value, error) {
	var last types.Value
	last, _ = types.NewNull()
	for _, s := range p.Body {
		v, sig, err := e.Exec(s)
		if err != nil {
			// Unwrap return signal.
			if rs, ok := err.(errReturnSig); ok {
				return rs.v, nil
			}
			if _, ok := err.(errBreakSig); ok {
				return types.Value{}, werr.New(werr.CodeInvalidControlFlow,
					"break at top level")
			}
			if _, ok := err.(errContinueSig); ok {
				return types.Value{}, werr.New(werr.CodeInvalidControlFlow,
					"continue at top level")
			}
			return types.Value{}, err
		}
		if sig == sigReturn {
			return v, nil
		}
		last = v
	}
	return last, nil
}

// execRoutine launches a routine (§3.3 routine / §5.2 / §8).
// The host call runs in a goroutine; errors are dispatched to handlers:
//   - yield errors (implement yieldLike) → RoutineYieldHandler
//   - any other error                    → RoutineErrorHandler
//
// The statement itself returns null immediately (fire-and-forget, §3.3).
func (e *Executor) execRoutine(x *ast.Routine) (types.Value, Signal, error) {
	call := x.Call
	ctx := e.ctx
	onErr := e.onRoutineErr
	onYield := e.onYield
	go func() {
		_, err := e.evalCall(call)
		if err == nil {
			return
		}
		if y, ok := asYield(err); ok {
			if onYield != nil {
				onYield(ctx, RoutineYieldReport{
					Token: y.Token(),
					Path:  call.Path(),
				})
			}
			return
		}
		if onErr != nil {
			onErr(ctx, err)
		}
	}()
	v, _ := types.NewNull()
	return v, sigNone, nil
}
