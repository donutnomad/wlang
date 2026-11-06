package runtime

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/donutnomad/wlang/ast"
	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/types"
)

// atomicInt32 is sync/atomic.Int32 imported under a local name so the executor
// struct stays compact.
type atomicInt32 = atomic.Int32

// routineInstanceSeq hands every launched routine instance a unique increasing
// ID (from 1; 0 is reserved for the main line). It is process-global so IDs stay
// unique across concurrently running programs, at the cost of not restarting at
// 1 per program: treat the value as an opaque identity, not an index.
var routineInstanceSeq atomic.Uint64

const RoutineHandleType = "routineHandle"

// HostRegistry is the interface the executor needs from the host registry.
// The concrete implementation lives in the registry package.
type HostRegistry interface {
	Invoke(ctx context.Context, op string, recv types.Value,
		args []types.Value, path string) (types.Value, error)
	CallValue(ctx context.Context, fn types.Value, args []types.Value,
		path string) (types.Value, error)
	BindMethodValue(ctx context.Context, recv types.Value, name, path string) (types.Value, error)
	ResolveSymbol(name, path string) (types.Value, error)
	ZeroValue(typeName, path string) (types.Value, error)
	Descriptor(name string) (types.TypeDescriptor, bool)
	ValidateValue(typeName string, value types.Value, path string) error
	// StructType returns the reflect.Type for a registered struct type used by
	// struct-literal evaluation (LANGUAGE.md §3.9). Implementations that do not
	// expose struct types may always return false.
	StructType(name string) (reflect.Type, bool)
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
type errReturnSig struct {
	v       types.Value
	named   string
	results []string
	frame   *Scope
	path    string
}

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

// RoutineErrorHandler observes errors raised inside a fire-and-forget routine.
type RoutineErrorHandler func(ctx context.Context, err error)

// StepInfo describes the statement a StepHook is about to observe.
type StepInfo struct {
	// Node is the statement about to execute.
	Node ast.Node
	// Scope is the scope the statement executes in.
	Scope *Scope
	// RoutineID identifies the routine instance running this statement.
	// 0 means the main line (not inside any routine). Each `routine {do: ...}`
	// launch gets a distinct non-zero ID, so concurrent instances of the same
	// body are distinguishable. IDs are opaque and unique within the process.
	RoutineID uint64
	// Frames lists the active function frame first, followed by its callers.
	Frames []DebugFrame
}

// StepHook observes a statement before it executes. Returning an error stops
// execution and returns that error to the caller.
type StepHook func(info StepInfo) error

// Executor runs statements within a scope chain.
type Executor struct {
	ctx        context.Context
	scope      *Scope
	registry   HostRegistry
	pkgs       map[string]any
	budget     Budget
	stepCount  int64
	loopCount  int64
	callDepth  int
	maxLoopCtx int // current loop depth for break/continue validity
	// routineCount is shared by pointer with every child Executor so
	// Budget.MaxRoutines is enforced across nested routine launches.
	routineCount *atomicInt32
	// routineID is the instance ID of the routine this Executor runs
	// (0 = main line). Routine instances map 1:1 onto child Executors.
	routineID    uint64
	onRoutineErr RoutineErrorHandler
	OnStep       StepHook
}

// RoutineHandle is a reusable future-like value returned by routine.
type RoutineHandle struct {
	once sync.Once
	done chan struct{}
	val  types.Value
	err  error
	path string
}

func newRoutineHandle(path string) *RoutineHandle {
	return &RoutineHandle{done: make(chan struct{}), path: path}
}

func (h *RoutineHandle) complete(v types.Value, err error) {
	h.once.Do(func() {
		h.val = v
		h.err = err
		close(h.done)
	})
}

func (h *RoutineHandle) Wait(ctx context.Context) (types.Value, error) {
	if h == nil {
		return types.Value{}, werr.New(werr.CodeType, "await target is nil routine handle")
	}
	select {
	case <-h.done:
		return h.val, h.err
	case <-ctx.Done():
		return types.Value{}, werr.Newf(werr.CodeRuntime, "ctx: %v", ctx.Err()).WithPath(h.path)
	}
}

// SetRoutineHandlers wires the handlers used by execRoutine (§3.3).
func (e *Executor) SetRoutineHandlers(onErr RoutineErrorHandler) {
	e.onRoutineErr = onErr
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
	scope.setValueValidator(reg)
	return &Executor{
		ctx: ctx, scope: scope, registry: reg, pkgs: pkgs, budget: budget,
		routineCount: new(atomicInt32),
	}
}

// Scope returns the current scope.
func (e *Executor) Scope() *Scope { return e.scope }

// RoutineID returns the routine instance ID this Executor runs (0 = main line).
func (e *Executor) RoutineID() uint64 { return e.routineID }

// SetScope replaces the current scope. Host coroutine schedulers use the
// Scope/SetScope pair to save/restore the shared executor scope across
// suspension points: a parked closure freezes its stack mid-InvokeClosure,
// so the deferred scope restoration does not run until resume, and any
// interleaved coroutine would otherwise observe the parked closure's scope.
func (e *Executor) SetScope(s *Scope) { e.scope = s }

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
	if e.OnStep != nil {
		if err := e.OnStep(StepInfo{
			Node: n, Scope: e.scope, RoutineID: e.routineID,
			Frames: e.scope.DebugFrames(n.Path()),
		}); err != nil {
			return types.Value{}, sigNone, err
		}
	}
	switch x := n.(type) {
	case *ast.Let:
		// Tuple destructure form (LANGUAGE.md §3.4.1): the right-hand
		// expression must evaluate to a tuple<T1,...,Tn> whose arity matches
		// the names list; each non-"_" name becomes a new variable.
		if x.Destructure != nil {
			v, err := e.Eval(x.Expr)
			if err != nil {
				return types.Value{}, sigNone, err
			}
			tupleValues, ok := types.UnpackTuple(v)
			if !ok {
				return types.Value{}, sigNone, werr.Newf(werr.CodeType,
					"let destructure requires tuple value, got %s", v.TypeName()).
					WithPath(x.Path())
			}
			if len(tupleValues) != len(x.Destructure.Names) {
				return types.Value{}, sigNone, werr.Newf(werr.CodeType,
					"let destructure arity mismatch: %d names vs tuple<%d>",
					len(x.Destructure.Names), len(tupleValues)).WithPath(x.Path())
			}
			for i, name := range x.Destructure.Names {
				if name == "_" {
					continue
				}
				elemV := tupleValues[i]
				declType := ""
				if i < len(x.Destructure.Types) {
					declType = x.Destructure.Types[i]
				}
				if declType != "" && declType != elemV.TypeName() {
					return types.Value{}, sigNone, werr.Newf(werr.CodeType,
						"let %s: declared %s, got %s",
						name, declType, elemV.TypeName()).WithPath(x.Path())
				}
				e.scope.Let(name, elemV, declType)
			}
			return v, sigNone, nil
		}
		// Multi-binding (destructuring) is the canonical form (TC-231); a
		// single-binding let still populates Bindings[0] in addition to the
		// legacy Name/Type/Expr fields.
		bindings := x.Bindings
		if len(bindings) == 0 {
			bindings = []ast.LetBinding{{Name: x.Name, Type: x.Type, Expr: x.Expr}}
		}
		var last types.Value
		for _, b := range bindings {
			v, err := e.Eval(b.Expr)
			if err != nil {
				return types.Value{}, sigNone, err
			}
			if b.Type != "" && b.Type != v.TypeName() {
				return types.Value{}, sigNone, werr.Newf(werr.CodeType,
					"let %s: declared %s, got %s", b.Name, b.Type, v.TypeName()).WithPath(x.Path())
			}
			e.scope.Let(b.Name, v, b.Type)
			last = v
		}
		return last, sigNone, nil
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
		if len(x.Results) > 0 {
			frame := e.scope.deferFrame()
			if x.Expr != nil {
				v, err := e.Eval(x.Expr)
				if err != nil {
					return types.Value{}, sigNone, err
				}
				if err := assignFrameResults(frame, x.Results, v, x.Path()); err != nil {
					return types.Value{}, sigNone, err
				}
			}
			return types.Value{}, sigReturn, errReturnSig{
				results: x.Results, frame: frame, path: x.Path(),
			}
		}
		if x.Named != "" {
			return types.Value{}, sigReturn, errReturnSig{named: x.Named, path: x.Path()}
		}
		v, err := e.Eval(x.Expr)
		if err != nil {
			return types.Value{}, sigNone, err
		}
		return v, sigReturn, errReturnSig{v: v, path: x.Path()}
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
	case *ast.Defer:
		return e.execDefer(x)
	case *ast.SelectStmt:
		return e.execSelect(x)
	case *ast.Match:
		v, err := e.evalMatch(x)
		return v, sigNone, err
	}
	// Fallback: statement is actually an expression-producing node.
	v, err := e.Eval(n)
	return v, sigNone, err
}

// execDefer evaluates the deferred host call's receiver and arguments now and
// records them on the nearest function frame. The actual invocation happens
// when that function returns (LANGUAGE.md §3.3).
func (e *Executor) execDefer(x *ast.Defer) (types.Value, Signal, error) {
	if x.Expr == nil && x.Call != nil {
		x.Expr = x.Call
	}
	if x.Expr == nil {
		return types.Value{}, sigNone, werr.New(werr.CodeASTShape,
			"defer requires a call").WithPath(x.Path())
	}
	switch call := x.Expr.(type) {
	case *ast.Call:
		if _, isBuiltin := builtinOps[call.Op]; isBuiltin {
			return types.Value{}, sigNone, werr.Newf(werr.CodeASTShape,
				"defer body must be a host call, got builtin %q", call.Op).WithPath(x.Path())
		}
		prepared, err := e.prepareHostCall(call)
		if err != nil {
			return types.Value{}, sigNone, err
		}
		e.scope.PushDeferred(prepared.op, prepared.recv, prepared.args, prepared.path)
	case *ast.FuncCall:
		prepared, err := e.prepareFuncCall(call)
		if err != nil {
			return types.Value{}, sigNone, err
		}
		e.scope.PushDeferredFunc(prepared.fn, prepared.args, prepared.path)
	default:
		return types.Value{}, sigNone, werr.Newf(werr.CodeASTShape,
			"defer requires a call expression, got %T", x.Expr).WithPath(x.Path())
	}
	nullV, _ := types.NewNull()
	return nullV, sigNone, nil
}

// runDeferred executes every deferred call recorded on the given function
// frame in LIFO order. A failing deferred call does not stop the remaining
// ones; the first error in drain order becomes the frame's deferred error.
// Control-flow signals from deferred bodies are not produced because deferred
// bodies are single calls.
func (e *Executor) runDeferred(s *Scope) error {
	if s == nil {
		return nil
	}
	calls := s.PopDeferred()
	var firstErr error
	for _, d := range calls {
		var err error
		switch d.kind {
		case deferredHostCall:
			_, err = e.registry.Invoke(e.ctx, d.op, d.recv, d.args, d.path)
		case deferredFuncCall:
			_, err = e.invokePreparedFuncCall(preparedFuncCall{
				fn: d.fn, args: d.fnArgs, path: d.path,
			})
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// assignFrameResults stores an explicit return expression into the named
// result parameters of the current function frame. Go evaluates the whole
// expression first and then assigns; callers pass the already-evaluated value.
func assignFrameResults(frame *Scope, names []string, v types.Value, path string) error {
	if len(names) == 1 {
		return frame.setLocal(names[0], v, path)
	}
	values, ok := types.UnpackTuple(v)
	if !ok || len(values) != len(names) {
		return werr.Newf(werr.CodeType,
			"named return expects tuple arity %d, got %s", len(names), v.TypeName()).
			WithPath(path)
	}
	for i, name := range names {
		if err := frame.setLocal(name, values[i], path); err != nil {
			return err
		}
	}
	return nil
}

// resolveReturnValues reads the final return value after the function frame's
// deferred calls have run. Named result lists are read from the frame root, so
// deferred writes (including writes through captured closures) are visible;
// blank "_" slots use the zero placeholder the translator declared there.
func (e *Executor) resolveReturnValues(s *Scope, rs errReturnSig) (errReturnSig, error) {
	if len(rs.results) > 0 {
		frame := rs.frame
		if frame == nil {
			frame = s
		}
		values := make([]types.Value, len(rs.results))
		for i, name := range rs.results {
			v, ok := frame.localValue(name)
			if !ok {
				return rs, werr.Newf(werr.CodeSymbol,
					"named return variable %q not found", name).WithPath(rs.path)
			}
			values[i] = v
		}
		if len(values) == 1 {
			rs.v = values[0]
		} else {
			rs.v = types.NewTuple(values...)
		}
		return rs, nil
	}
	if rs.named == "" {
		return rs, nil
	}
	v, ok := s.LookupPath(rs.named)
	if !ok {
		return rs, werr.Newf(werr.CodeSymbol,
			"named return variable %q not found", rs.named).WithPath(rs.path)
	}
	rs.v = v
	return rs, nil
}

// execBlock runs a sequence of statements within a new lexical scope. The
// scope carries bindings only: defer registrations belong to the nearest
// function frame and run when that function returns. Leaving the block via
// fall-through, break/continue, return, or error does not run them
// (LANGUAGE.md §3.3). Named returns are resolved at the function boundary
// after the deferred calls run.
func (e *Executor) execBlock(stmts []ast.Node) (types.Value, Signal, error) {
	e.scope = e.scope.Push()
	blockScope := e.scope
	var last types.Value
	last, _ = types.NewNull()
	leave := func(v types.Value, sig Signal, err error) (types.Value, Signal, error) {
		e.scope = blockScope.Pop()
		return v, sig, err
	}
	for _, s := range stmts {
		v, sig, err := e.Exec(s)
		if err != nil {
			return leave(v, sig, err)
		}
		if sig != sigNone {
			return leave(v, sig, nil)
		}
		last = v
	}
	return leave(last, sigNone, nil)
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
		iterScope := e.scope
		if x.As != "" {
			e.scope.Let(x.As, types.NewValue(elemName, item), "")
		}
		if x.Index != "" {
			e.scope.Let(x.Index, types.NewValue(types.TInt64, int64(i)), "")
		}
		v, sig, err := runStatements(e, x.Do)
		e.scope = iterScope.Pop()
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
		iterScope := e.scope
		if x.Var != "" {
			e.scope.Let(x.Var, types.NewValue(types.TInt64, i), types.TInt64)
		}
		v, sig, err := runStatements(e, x.Do)
		e.scope = iterScope.Pop()
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
	return containsReturnList(p.Body)
}

// containsReturn recursively walks a wflang AST node looking for a `return`
// statement reachable from the program's own evaluation (i.e. not behind a
// child-routine boundary). Routine.Body is intentionally skipped because a
// return inside a `routine.do` block resolves that routine's handle and does
// not end the enclosing program (LANGUAGE.md §3.3 / runtime/exec.go
// execRoutine body form).
func containsReturn(n ast.Node) bool {
	switch x := n.(type) {
	case nil:
		return false
	case *ast.Return:
		return true
	case *ast.IfStmt:
		return containsReturnList(x.Then) || containsReturnList(x.Else)
	case *ast.IfExpr:
		return containsReturnList(x.Then) || containsReturnList(x.Else)
	case *ast.Foreach:
		return containsReturnList(x.Do)
	case *ast.Fori:
		return containsReturnList(x.Do)
	case *ast.Match:
		if slices.ContainsFunc(x.Cases, func(c ast.MatchCase) bool {
			return containsReturnList(c.Do)
		}) {
			return true
		}
		return containsReturnList(x.Default)
	case *ast.SelectStmt:
		if slices.ContainsFunc(x.Cases, func(c ast.SelectCase) bool {
			return containsReturnList(c.Do)
		}) {
			return true
		}
		return containsReturnList(x.Default)
	case *ast.Let:
		for _, b := range x.Bindings {
			if containsReturn(b.Expr) {
				return true
			}
		}
		return containsReturn(x.Expr)
	case *ast.Set:
		return containsReturn(x.Expr)
	case *ast.ExprStmt:
		return containsReturn(x.Expr)
	case *ast.Panic:
		return containsReturn(x.Expr)
	case *ast.Call:
		return containsReturnList(x.Args)
	case *ast.FuncCall:
		return containsReturn(x.Fn) || containsReturnList(x.Args)
	case *ast.FuncLit:
		return false
	case *ast.Array:
		return containsReturnList(x.Items)
	case *ast.MapLit:
		for _, e := range x.Entries {
			if containsReturn(e.Key) || containsReturn(e.Val) {
				return true
			}
		}
	case *ast.StructLit:
		for _, f := range x.Fields {
			if containsReturn(f.Expr) {
				return true
			}
		}
	case *ast.ChanLit:
		return containsReturn(x.Buffer)
	case *ast.Var:
		return containsReturn(x.Default)
	case *ast.MethodValue:
		return containsReturn(x.Receiver)
	case *ast.Routine:
		// Routine.Body is a child-routine boundary: a `return` there resolves
		// the routine handle, it does not end the enclosing program. Only the
		// Call form's argument expressions can carry a program-level return.
		if x.Call != nil {
			return containsReturn(x.Call)
		}
	case *ast.Defer:
		return containsReturn(x.Expr)
	}
	return false
}

func containsReturnList(ns []ast.Node) bool {
	return slices.ContainsFunc(ns, containsReturn)
}

// RunProgram executes a full program and returns the program's result value.
// Deferred calls registered at the program top level fire when the program
// ends (LANGUAGE.md §3.3).
func (e *Executor) RunProgram(p *ast.Program) (types.Value, error) {
	topScope := e.scope
	// The program body is an implicit function frame (LANGUAGE.md §3.3). A
	// host may hand NewExecutor any scope; promote a non-root scope for the
	// duration of the run so top-level defers attach to and drain from it.
	restoredFrameRoot := false
	oldFrameName := ""
	if topScope != nil && !topScope.frameRoot {
		oldFrameName = topScope.frameName
		topScope.frameRoot = true
		topScope.frameName = "main"
		restoredFrameRoot = true
	}
	if restoredFrameRoot {
		defer func() {
			topScope.frameRoot = false
			topScope.frameName = oldFrameName
		}()
	}
	finish := func(v types.Value, err error) (types.Value, error) {
		defErr := e.runDeferred(topScope)
		if err == nil && defErr != nil {
			return v, defErr
		}
		return v, err
	}
	finishReturn := func(rs errReturnSig) (types.Value, error) {
		defErr := e.runDeferred(topScope)
		if defErr != nil {
			// Resolve the named value best-effort for diagnostics, but the
			// deferred error keeps priority over a resolution failure.
			if rs.named != "" || len(rs.results) > 0 {
				if resolved, rerr := e.resolveReturnValues(topScope, rs); rerr == nil {
					rs = resolved
				}
			}
			return rs.v, defErr
		}
		if rs.named != "" || len(rs.results) > 0 {
			resolved, err := e.resolveReturnValues(topScope, rs)
			if err != nil {
				return types.Value{}, err
			}
			rs = resolved
		}
		return rs.v, nil
	}
	var last types.Value
	last, _ = types.NewNull()
	for _, s := range p.Body {
		v, sig, err := e.Exec(s)
		if err != nil {
			if rs, ok := err.(errReturnSig); ok {
				return finishReturn(rs)
			}
			if _, ok := err.(errBreakSig); ok {
				return finish(types.Value{}, werr.New(werr.CodeInvalidControlFlow,
					"break at top level"))
			}
			if _, ok := err.(errContinueSig); ok {
				return finish(types.Value{}, werr.New(werr.CodeInvalidControlFlow,
					"continue at top level"))
			}
			return finish(types.Value{}, err)
		}
		if sig == sigReturn {
			return finishReturn(errReturnSig{v: v})
		}
		last = v
	}
	return finish(last, nil)
}

// execRoutine launches a routine (§3.3 routine / await).
// The host call runs in a goroutine; its result is stored on the returned
// handle. Errors are also dispatched to RoutineErrorHandler for fire-and-forget
// callers.
//
// The statement itself returns a routineHandle immediately.
// Concurrent routines are capped by Budget.MaxRoutines (§10.1 / TC-803).
//
// The goroutine runs against an isolated child Executor so per-step counters
// (stepCount, loopCount, callDepth) do not race with the parent goroutine.
// The routineCount counter is shared by pointer with the child so MaxRoutines
// is enforced across nested routine launches too.
func (e *Executor) execRoutine(x *ast.Routine) (types.Value, Signal, error) {
	v, err := e.evalRoutine(x)
	return v, sigNone, err
}

func (e *Executor) evalRoutine(x *ast.Routine) (types.Value, error) {
	if e.budget.MaxRoutines > 0 {
		current := e.routineCount.Add(1)
		if int(current) > e.budget.MaxRoutines {
			e.routineCount.Add(-1)
			return types.Value{}, werr.Newf(werr.CodeBudget,
				"MaxRoutines exceeded (%d)", e.budget.MaxRoutines).
				WithPath(x.Path())
		}
	}
	ctx := e.ctx
	onErr := e.onRoutineErr
	handle := newRoutineHandle(x.Path())

	// Call form: prepare the host call eagerly on the parent goroutine so
	// receiver/argument evaluation never races with the caller's scope.
	if x.Call != nil {
		exitCall, err := e.enterHostCall(x.Call.Path())
		if err != nil {
			if e.budget.MaxRoutines > 0 {
				e.routineCount.Add(-1)
			}
			return types.Value{}, err
		}
		prepared, err := e.prepareHostCall(x.Call)
		exitCall()
		if err != nil {
			if e.budget.MaxRoutines > 0 {
				e.routineCount.Add(-1)
			}
			return types.Value{}, err
		}
		// invokePreparedHostCall goes straight to the registry: it never runs a
		// statement, so this child needs neither a scope nor a routineID.
		child := &Executor{
			ctx:          ctx,
			registry:     e.registry,
			pkgs:         e.pkgs,
			budget:       e.budget,
			routineCount: e.routineCount,
			OnStep:       e.OnStep,
		}
		go func() {
			defer func() {
				if e.budget.MaxRoutines > 0 {
					e.routineCount.Add(-1)
				}
			}()
			v, err := child.invokePreparedHostCall(prepared)
			if err == nil {
				handle.complete(v, nil)
				return
			}
			if onErr != nil {
				onErr(ctx, err)
			}
			handle.complete(types.Value{}, err)
		}()
		return types.NewValue(RoutineHandleType, handle), nil
	}

	// Body form (LANGUAGE.md §3.3): statements execute in a child Executor
	// with a fresh root scope. The body cannot read mutable parent state since
	// the scope chain is not shared, mirroring Go's `go func() { ... }()`.
	// The last expression's value (or an explicit return) becomes the handle
	// result; top-level defers in the body fire before the handle resolves.
	//
	// The child carries a fresh routineID: every launch is a distinct instance,
	// so an OnStep hook can tell concurrent instances of the same body apart.
	body := x.Body
	child := &Executor{
		ctx:          ctx,
		registry:     e.registry,
		pkgs:         e.pkgs,
		budget:       e.budget,
		routineCount: e.routineCount,
		routineID:    routineInstanceSeq.Add(1),
		OnStep:       e.OnStep,
	}
	child.scope = NewScope()
	child.scope.setValueValidator(e.registry)
	go func() {
		defer func() {
			if e.budget.MaxRoutines > 0 {
				e.routineCount.Add(-1)
			}
		}()
		topScope := child.scope
		finish := func(v types.Value, err error) (types.Value, error) {
			defErr := child.runDeferred(topScope)
			if err == nil && defErr != nil {
				return v, defErr
			}
			return v, err
		}
		last, _ := types.NewNull()
		for _, s := range body {
			v, sig, err := child.Exec(s)
			if err != nil {
				if rs, ok := err.(errReturnSig); ok {
					v2, ferr := finish(rs.v, nil)
					if ferr != nil {
						if onErr != nil {
							onErr(ctx, ferr)
						}
						handle.complete(types.Value{}, ferr)
						return
					}
					if rs.named != "" || len(rs.results) > 0 {
						resolved, rerr := child.resolveReturnValues(topScope, rs)
						if rerr != nil {
							if onErr != nil {
								onErr(ctx, rerr)
							}
							handle.complete(types.Value{}, rerr)
							return
						}
						v2 = resolved.v
					}
					handle.complete(v2, nil)
					return
				}
				v2, ferr := finish(types.Value{}, err)
				_ = v2
				if onErr != nil {
					onErr(ctx, ferr)
				}
				handle.complete(types.Value{}, ferr)
				return
			}
			if sig == sigReturn {
				v2, ferr := finish(v, nil)
				if ferr != nil {
					if onErr != nil {
						onErr(ctx, ferr)
					}
					handle.complete(types.Value{}, ferr)
					return
				}
				handle.complete(v2, nil)
				return
			}
			last = v
		}
		v2, ferr := finish(last, nil)
		if ferr != nil {
			if onErr != nil {
				onErr(ctx, ferr)
			}
			handle.complete(types.Value{}, ferr)
			return
		}
		handle.complete(v2, nil)
	}()
	return types.NewValue(RoutineHandleType, handle), nil
}
