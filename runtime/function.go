package runtime

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/donutnomad/wlang/ast"
	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/types"
)

// Closure is the runtime carrier for a wflang function value.
type Closure struct {
	params  []ast.FuncParam
	returns []string
	body    []ast.Node
	scope   *Scope
	typ     string
	path    string
}

// FunctionSignature is the immutable public view of a closure's declared
// parameter and return types.
type FunctionSignature struct {
	Params  []string
	Returns []string
}

// Signature returns defensive copies so host ABI validation cannot mutate an
// executable closure.
func (c *Closure) Signature() FunctionSignature {
	if c == nil {
		return FunctionSignature{}
	}
	params := make([]string, len(c.params))
	for i, param := range c.params {
		params[i] = param.Type
	}
	return FunctionSignature{
		Params:  params,
		Returns: append([]string(nil), c.returns...),
	}
}

type preparedFuncCall struct {
	fn   types.Value
	args []types.Value
	path string
}

func (e *Executor) evalFuncLit(x *ast.FuncLit) (types.Value, error) {
	params := make([]string, len(x.Params))
	for i, p := range x.Params {
		params[i] = p.Type
	}
	typ := types.FuncType(params, x.Returns)
	return types.NewValue(typ, &Closure{
		params:  x.Params,
		returns: append([]string(nil), x.Returns...),
		body:    x.Body,
		scope:   e.scope,
		typ:     typ,
		path:    x.Path(),
	}), nil
}

func (e *Executor) evalFuncCall(x *ast.FuncCall) (types.Value, error) {
	exit, err := e.enterHostCall(x.Path())
	if err != nil {
		return types.Value{}, err
	}
	defer exit()
	call, err := e.prepareFuncCall(x)
	if err != nil {
		return types.Value{}, err
	}
	return e.invokePreparedFuncCall(call)
}

func (e *Executor) prepareFuncCall(x *ast.FuncCall) (preparedFuncCall, error) {
	fn, err := e.Eval(x.Fn)
	if err != nil {
		return preparedFuncCall{}, err
	}
	args := make([]types.Value, 0, len(x.Args))
	for _, a := range x.Args {
		v, err := e.prepareCallArg(a)
		if err != nil {
			return preparedFuncCall{}, err
		}
		args = append(args, v)
	}
	return preparedFuncCall{fn: fn, args: args, path: x.Path()}, nil
}

// InvokeClosure lets a Go host call a wflang closure value with a set of
// already-evaluated arguments. It is the dual of CallValue (registry.go, which
// only accepts a reflect.Func): CallValue invokes a Go function, InvokeClosure
// invokes a *Closure. Both share the same Executor from one RunProgram, so the
// outer scope the closure captured stays alive across host invocations.
//
// Like the Executor as a whole, this is not concurrency-safe: callers must
// invoke it serially on a given Executor.
func (e *Executor) InvokeClosure(cl *Closure, args []types.Value) (types.Value, error) {
	if cl == nil {
		return types.Value{}, fmt.Errorf("InvokeClosure: nil closure")
	}
	exit, err := e.enterHostCall("<host-invoke-closure>")
	if err != nil {
		return types.Value{}, err
	}
	defer exit()
	return e.invokePreparedFuncCall(preparedFuncCall{
		fn:   types.NewValue(cl.typ, cl),
		args: args,
		path: cl.path,
	})
}

func (e *Executor) invokePreparedFuncCall(call preparedFuncCall) (types.Value, error) {
	cl, ok := call.fn.Go().(*Closure)
	if !ok || !strings.HasPrefix(call.fn.TypeName(), "func<") {
		if e.registry != nil && isGoFunctionValue(call.fn) {
			return e.registry.CallValue(e.ctx, call.fn, call.args, call.path)
		}
		return types.Value{}, werr.Newf(werr.CodeType,
			"call.fn must be function, got %s", call.fn.TypeName()).WithPath(call.path)
	}
	if len(call.args) != len(cl.params) {
		return types.Value{}, werr.Newf(werr.CodeType,
			"function expects %d args, got %d", len(cl.params), len(call.args)).
			WithPath(call.path)
	}

	prev := e.scope
	callScope := cl.scope.pushFrame("closure", call.path, prev)
	e.scope = callScope
	defer func() { e.scope = prev }()

	for i, p := range cl.params {
		arg := call.args[i]
		if !e.valueAssignableTo(arg, p.Type) {
			return types.Value{}, werr.Newf(werr.CodeType,
				"function arg %q expects %s, got %s", p.Name, p.Type, arg.TypeName()).
				WithPath(call.path)
		}
		e.scope.Let(p.Name, arg, "")
	}

	v, _, err := runStatements(e, cl.body)
	defErr := e.runDeferred(callScope)
	if err != nil {
		if rs, ok := err.(errReturnSig); ok {
			// The deferred error wins over named-return resolution and return
			// type validation, matching the documented frame error priority.
			if defErr != nil {
				return types.Value{}, defErr
			}
			resolved, rerr := e.resolveReturnValues(callScope, rs)
			if rerr != nil {
				return types.Value{}, rerr
			}
			return e.checkFunctionReturn(cl, resolved.v, call.path)
		}
		return types.Value{}, err
	}
	if defErr != nil {
		return types.Value{}, defErr
	}
	return e.checkFunctionReturn(cl, v, call.path)
}

func isGoFunctionValue(v types.Value) bool {
	g := v.Go()
	if g == nil {
		return false
	}
	rv := reflect.ValueOf(g)
	return rv.IsValid() && rv.Kind() == reflect.Func
}

func (e *Executor) checkFunctionReturn(cl *Closure, v types.Value, path string) (types.Value, error) {
	if len(cl.returns) == 0 {
		null, _ := types.NewNull()
		return null, nil
	}
	if len(cl.returns) == 1 {
		if !e.valueAssignableTo(v, cl.returns[0]) {
			return types.Value{}, werr.Newf(werr.CodeType,
				"function return expects %s, got %s", cl.returns[0], v.TypeName()).
				WithPath(path)
		}
		return types.NewValue(cl.returns[0], v.Go()), nil
	}
	values, ok := types.UnpackTuple(v)
	if !ok || len(values) != len(cl.returns) {
		return types.Value{}, werr.Newf(werr.CodeType,
			"function return expects tuple arity %d, got %s", len(cl.returns), v.TypeName()).
			WithPath(path)
	}
	for i, want := range cl.returns {
		if !e.valueAssignableTo(values[i], want) {
			return types.Value{}, werr.Newf(werr.CodeType,
				"function return %d expects %s, got %s", i, want, values[i].TypeName()).
				WithPath(path)
		}
		values[i] = types.NewValue(want, values[i].Go())
	}
	return types.NewTuple(values...), nil
}

func (e *Executor) valueAssignableTo(v types.Value, want string) bool {
	if want == "" || want == types.TAny || v.TypeName() == want {
		return true
	}
	if v.TypeName() != types.TNull {
		return false
	}
	if want == types.TError {
		return true
	}
	if e.registry != nil {
		if descriptor, ok := e.registry.Descriptor(want); ok {
			switch descriptor.Kind {
			case types.TypeKindPointer, types.TypeKindSlice, types.TypeKindMap:
				return true
			}
		}
	}
	return strings.HasPrefix(want, "*") || strings.HasPrefix(want, "array<") || strings.HasPrefix(want, "map<")
}
