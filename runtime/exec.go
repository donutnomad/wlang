package runtime

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/wflang/wflang/ast"
	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/types"
)

// atomicInt32 is sync/atomic.Int32 imported under a local name so the executor
// struct stays compact.
type atomicInt32 = atomic.Int32

const RoutineHandleType = "routineHandle"

// HostRegistry is the interface the executor needs from the host registry.
// The concrete implementation lives in the registry package.
type HostRegistry interface {
	Invoke(ctx context.Context, op string, recv types.Value,
		args []types.Value, path string) (types.Value, error)
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

// RoutineErrorHandler observes errors raised inside a fire-and-forget routine.
type RoutineErrorHandler func(ctx context.Context, err error)

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
	routineCount atomicInt32
	onRoutineErr RoutineErrorHandler
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
		// Tuple destructure form (LANGUAGE.md §3.4.1): the right-hand
		// expression must evaluate to a tuple<T1,...,Tn> whose arity matches
		// the names list; each non-"_" name becomes a new variable.
		if x.Destructure != nil {
			v, err := e.Eval(x.Expr)
			if err != nil {
				return types.Value{}, sigNone, err
			}
			elems, elemNames, ok := extractTupleParts(v)
			if !ok {
				return types.Value{}, sigNone, werr.Newf(werr.CodeType,
					"let destructure requires tuple value, got %s", v.TypeName()).
					WithPath(x.Path())
			}
			if len(elems) != len(x.Destructure.Names) {
				return types.Value{}, sigNone, werr.Newf(werr.CodeType,
					"let destructure arity mismatch: %d names vs tuple<%d>",
					len(x.Destructure.Names), len(elems)).WithPath(x.Path())
			}
			for i, name := range x.Destructure.Names {
				if name == "_" {
					continue
				}
				elemV := types.NewValue(elemNames[i], elems[i])
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
// records them on the current scope. The actual invocation happens when the
// enclosing block scope exits (LANGUAGE.md §3.7).
func (e *Executor) execDefer(x *ast.Defer) (types.Value, Signal, error) {
	if x.Call == nil {
		return types.Value{}, sigNone, werr.New(werr.CodeASTShape,
			"defer requires a host call").WithPath(x.Path())
	}
	if _, isBuiltin := builtinOps[x.Call.Op]; isBuiltin {
		return types.Value{}, sigNone, werr.Newf(werr.CodeASTShape,
			"defer body must be a host call, got builtin %q", x.Call.Op).WithPath(x.Path())
	}
	prepared, err := e.prepareHostCall(x.Call)
	if err != nil {
		return types.Value{}, sigNone, err
	}
	e.scope.PushDeferred(prepared.op, prepared.recv, prepared.args, prepared.path)
	nullV, _ := types.NewNull()
	return nullV, sigNone, nil
}

// runDeferred executes any deferred calls recorded on the given scope in LIFO
// order. Errors from deferred calls surface to the caller; control-flow
// signals from deferred calls are not produced because deferred bodies are
// single host calls.
func (e *Executor) runDeferred(s *Scope) error {
	if s == nil {
		return nil
	}
	calls := s.PopDeferred()
	var firstErr error
	for _, d := range calls {
		_, err := e.registry.Invoke(e.ctx, d.op, d.recv, d.args, d.path)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// execBlock runs a sequence of statements within a new scope. Deferred calls
// registered inside the block always run when the block exits, regardless of
// whether the exit is via fall-through, return/break/continue signal, or
// error propagation (LANGUAGE.md §3.7).
func (e *Executor) execBlock(stmts []ast.Node) (types.Value, Signal, error) {
	e.scope = e.scope.Push()
	blockScope := e.scope
	var last types.Value
	last, _ = types.NewNull()
	cleanup := func(v types.Value, sig Signal, err error) (types.Value, Signal, error) {
		defErr := e.runDeferred(blockScope)
		e.scope = blockScope.Pop()
		if err == nil && defErr != nil {
			return v, sigNone, defErr
		}
		return v, sig, err
	}
	for _, s := range stmts {
		v, sig, err := e.Exec(s)
		if err != nil {
			return cleanup(v, sig, err)
		}
		if sig != sigNone {
			return cleanup(v, sig, nil)
		}
		last = v
	}
	return cleanup(last, sigNone, nil)
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
		defErr := e.runDeferred(iterScope)
		e.scope = iterScope.Pop()
		if err != nil {
			if _, isBreak := err.(errBreakSig); isBreak {
				if defErr != nil {
					return v, sigNone, defErr
				}
				break
			}
			if _, isCont := err.(errContinueSig); isCont {
				if defErr != nil {
					return v, sigNone, defErr
				}
				continue
			}
			return v, sigNone, err
		}
		if defErr != nil {
			return v, sigNone, defErr
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
		defErr := e.runDeferred(iterScope)
		e.scope = iterScope.Pop()
		if err != nil {
			if _, isBreak := err.(errBreakSig); isBreak {
				if defErr != nil {
					return v, sigNone, defErr
				}
				break
			}
			if _, isCont := err.(errContinueSig); isCont {
				if defErr != nil {
					return v, sigNone, defErr
				}
				continue
			}
			return v, sigNone, err
		}
		if defErr != nil {
			return v, sigNone, defErr
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

// extractTupleParts pulls the element values and element type names out of a
// tuple<T1,...,Tn> Value. Returns ok=false for non-tuple inputs.
func extractTupleParts(v types.Value) ([]any, []string, bool) {
	name := v.TypeName()
	const prefix = "tuple<"
	if len(name) < len(prefix)+2 || name[:len(prefix)] != prefix || name[len(name)-1] != '>' {
		return nil, nil, false
	}
	inner := name[len(prefix) : len(name)-1]
	var names []string
	if inner != "" {
		// Type names cannot themselves contain commas outside of nested
		// generics. We do a balance-aware split on top-level commas so that
		// tuple<map<string,int64>,error> stays parseable.
		depth := 0
		start := 0
		for i := 0; i < len(inner); i++ {
			switch inner[i] {
			case '<':
				depth++
			case '>':
				depth--
			case ',':
				if depth == 0 {
					names = append(names, inner[start:i])
					start = i + 1
				}
			}
		}
		names = append(names, inner[start:])
	}
	parts, ok := v.Go().([]any)
	if !ok {
		return nil, nil, false
	}
	return parts, names, true
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
	case *ast.Routine:
		// Routine.Body is a child-routine boundary: a `return` there resolves
		// the routine handle, it does not end the enclosing program. Only the
		// Call form's argument expressions can carry a program-level return.
		if x.Call != nil {
			return containsReturn(x.Call)
		}
	case *ast.Defer:
		if x.Call != nil {
			return containsReturn(x.Call)
		}
	}
	return false
}

func containsReturnList(ns []ast.Node) bool {
	return slices.ContainsFunc(ns, containsReturn)
}

// RunProgram executes a full program and returns the program's result value.
// Deferred calls registered at the program top level fire when the program
// ends (LANGUAGE.md §3.7).
func (e *Executor) RunProgram(p *ast.Program) (types.Value, error) {
	topScope := e.scope
	finish := func(v types.Value, err error) (types.Value, error) {
		defErr := e.runDeferred(topScope)
		if err == nil && defErr != nil {
			return v, defErr
		}
		return v, err
	}
	var last types.Value
	last, _ = types.NewNull()
	for _, s := range p.Body {
		v, sig, err := e.Exec(s)
		if err != nil {
			if rs, ok := err.(errReturnSig); ok {
				return finish(rs.v, nil)
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
			return finish(v, nil)
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
// The routineCount counter remains shared (it's atomic).
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
		child := &Executor{
			ctx:      ctx,
			registry: e.registry,
			pkgs:     e.pkgs,
			budget:   e.budget,
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
	body := x.Body
	child := &Executor{
		ctx:      ctx,
		registry: e.registry,
		pkgs:     e.pkgs,
		budget:   e.budget,
	}
	child.scope = NewScope()
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
