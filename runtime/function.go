package runtime

import (
	"strings"

	"github.com/wflang/wflang/ast"
	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/types"
)

// Closure is the runtime carrier for a wflang function value.
type Closure struct {
	params  []ast.FuncParam
	returns []string
	body    []ast.Node
	scope   *Scope
	typ     string
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
		v, err := e.Eval(a)
		if err != nil {
			return preparedFuncCall{}, err
		}
		args = append(args, v)
	}
	return preparedFuncCall{fn: fn, args: args, path: x.Path()}, nil
}

func (e *Executor) invokePreparedFuncCall(call preparedFuncCall) (types.Value, error) {
	cl, ok := call.fn.Go().(*Closure)
	if !ok || !strings.HasPrefix(call.fn.TypeName(), "func<") {
		return types.Value{}, werr.Newf(werr.CodeType,
			"call.fn must be function, got %s", call.fn.TypeName()).WithPath(call.path)
	}
	if len(call.args) != len(cl.params) {
		return types.Value{}, werr.Newf(werr.CodeType,
			"function expects %d args, got %d", len(cl.params), len(call.args)).
			WithPath(call.path)
	}

	prev := e.scope
	callScope := cl.scope.Push()
	e.scope = callScope
	defer func() { e.scope = prev }()

	for i, p := range cl.params {
		arg := call.args[i]
		if !valueAssignableTo(arg, p.Type) {
			return types.Value{}, werr.Newf(werr.CodeType,
				"function arg %q expects %s, got %s", p.Name, p.Type, arg.TypeName()).
				WithPath(call.path)
		}
		e.scope.Let(p.Name, arg, "")
	}

	v, sig, err := runStatements(e, cl.body)
	defErr := e.runDeferred(callScope)
	if err != nil {
		if rs, ok := err.(errReturnSig); ok {
			resolved, rerr := e.resolveNamedReturn(callScope, rs)
			if rerr != nil {
				return types.Value{}, rerr
			}
			if defErr != nil {
				return types.Value{}, defErr
			}
			return e.checkFunctionReturn(cl, resolved.v, call.path)
		}
		return types.Value{}, err
	}
	if defErr != nil {
		return types.Value{}, defErr
	}
	if sig == sigReturn {
		return e.checkFunctionReturn(cl, v, call.path)
	}
	return e.checkFunctionReturn(cl, v, call.path)
}

func (e *Executor) checkFunctionReturn(cl *Closure, v types.Value, path string) (types.Value, error) {
	if len(cl.returns) == 0 {
		null, _ := types.NewNull()
		return null, nil
	}
	if len(cl.returns) == 1 {
		if !valueAssignableTo(v, cl.returns[0]) {
			return types.Value{}, werr.Newf(werr.CodeType,
				"function return expects %s, got %s", cl.returns[0], v.TypeName()).
				WithPath(path)
		}
		return v, nil
	}
	_, names, ok := extractTupleParts(v)
	if !ok || len(names) != len(cl.returns) {
		return types.Value{}, werr.Newf(werr.CodeType,
			"function return expects tuple arity %d, got %s", len(cl.returns), v.TypeName()).
			WithPath(path)
	}
	for i, want := range cl.returns {
		if names[i] != want {
			return types.Value{}, werr.Newf(werr.CodeType,
				"function return %d expects %s, got %s", i, want, names[i]).
				WithPath(path)
		}
	}
	return v, nil
}

func valueAssignableTo(v types.Value, want string) bool {
	return want == "" || want == types.TAny || v.TypeName() == want ||
		(want == types.TError && v.TypeName() == types.TNull)
}
