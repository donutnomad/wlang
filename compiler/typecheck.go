// Compile-time type checks (LANGUAGE.md §7.5 / TC-644, TC-653).
//
// This pass runs after Parse and before runtime evaluation. It catches a
// well-defined set of statically provable errors:
//
//   - TC-644 receiver = null literal             → E_NIL_RECEIVER
//   - TC-653 var declared type=any in overload   → E_AMBIGUOUS_OVERLOAD
//
// Detection here is conservative: every flagged case is statically certain.
// Anything that requires runtime inference is intentionally left to the
// existing executor checks.
package compiler

import (
	"github.com/donutnomad/wlang/ast"
	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/types"
)

// OverloadProbe lets the type checker ask whether an operator has multiple
// candidates registered. The wflang facade plugs in a registry-backed
// implementation; tests can supply a stub. A nil probe disables TC-653.
type OverloadProbe interface {
	HasOverloads(op string) bool
}

// TypeCheck runs the static checks against prog. Errors are returned eagerly
// at the first violation so the path information stays precise. A nil probe
// disables overload-based checks but still runs receiver-null detection.
func TypeCheck(prog *ast.Program, probe OverloadProbe) error {
	return TypeCheckOpts(prog, probe, TypeCheckOptions{})
}

// TypeCheckOptions configures the type-check pass.
type TypeCheckOptions struct {
	// Aggregate causes the checker to keep walking after the first violation,
	// collecting every error into a single *errors.List (LANGUAGE.md §9.4 /
	// TC-732). When false (default) the first error is returned eagerly.
	Aggregate bool
}

// TypeCheckOpts is the parameterised entry point for TypeCheck (TC-732).
func TypeCheckOpts(prog *ast.Program, probe OverloadProbe, opts TypeCheckOptions) error {
	if prog == nil {
		return nil
	}
	tc := &typeChecker{
		probe:     probe,
		declared:  map[string]string{},
		aggregate: opts.Aggregate,
	}
	for _, s := range prog.Body {
		if err := tc.walk(s); err != nil {
			if !tc.aggregate {
				return err
			}
			// In aggregate mode walk() returns nil and stashes errors
			// internally; this branch should be unreachable but keeps
			// the contract robust.
			tc.collect(err)
		}
	}
	return tc.errs.Err()
}

type typeChecker struct {
	probe OverloadProbe
	// declared tracks the most-recently declared type annotation for each
	// let-bound variable name (TC-653 requires "any" tracking).
	declared map[string]string
	// aggregate enables multi-error collection (TC-732). When true, walk()
	// captures errors via collect() and continues, instead of returning at
	// the first violation.
	aggregate bool
	errs      werr.List
}

// collect appends err to the aggregator. err must be either a *werr.LangError
// or a *werr.List (the latter is flattened).
func (t *typeChecker) collect(err error) {
	if err == nil {
		return
	}
	if le, ok := err.(*werr.LangError); ok {
		t.errs.Add(le)
		return
	}
	if list, ok := err.(*werr.List); ok {
		for _, e := range list.Errors {
			t.errs.Add(e)
		}
		return
	}
	// Wrap unknown errors so the list stays homogeneous.
	t.errs.Add(werr.Newf(werr.CodeASTShape, "%v", err))
}

func (t *typeChecker) walk(n ast.Node) error {
	switch x := n.(type) {
	case nil:
		return nil
	case *ast.Let:
		bindings := x.Bindings
		if len(bindings) == 0 {
			bindings = []ast.LetBinding{{Name: x.Name, Type: x.Type, Expr: x.Expr}}
		}
		for _, b := range bindings {
			if b.Type != "" {
				t.declared[b.Name] = b.Type
			}
			if err := t.walk(b.Expr); err != nil {
				return err
			}
		}
		return nil
	case *ast.Set:
		return t.walk(x.Expr)
	case *ast.Return:
		return t.walk(x.Expr)
	case *ast.ExprStmt:
		return t.walk(x.Expr)
	case *ast.Panic:
		return t.walk(x.Expr)
	case *ast.IfStmt:
		if err := t.walk(x.Cond); err != nil {
			return err
		}
		if err := t.walkBlock(x.Then); err != nil {
			return err
		}
		return t.walkBlock(x.Else)
	case *ast.IfExpr:
		if err := t.walk(x.Cond); err != nil {
			return err
		}
		if err := t.walkBlock(x.Then); err != nil {
			return err
		}
		return t.walkBlock(x.Else)
	case *ast.Foreach:
		if err := t.walk(x.Target); err != nil {
			return err
		}
		return t.walkBlock(x.Do)
	case *ast.Fori:
		if err := t.walk(x.From); err != nil {
			return err
		}
		if err := t.walk(x.To); err != nil {
			return err
		}
		if x.Step != nil {
			if err := t.walk(x.Step); err != nil {
				return err
			}
		}
		return t.walkBlock(x.Do)
	case *ast.Match:
		if err := t.walk(x.Value); err != nil {
			return err
		}
		for _, c := range x.Cases {
			if err := t.walk(c.When); err != nil {
				return err
			}
			if err := t.walkBlock(c.Do); err != nil {
				return err
			}
		}
		return t.walkBlock(x.Default)
	case *ast.Routine:
		if x.Call != nil {
			return t.walk(x.Call)
		}
		return t.walkBlock(x.Body)
	case *ast.Defer:
		if x.Expr != nil {
			return t.walk(x.Expr)
		}
		return t.walk(x.Call)
	case *ast.Array:
		for _, it := range x.Items {
			if err := t.walk(it); err != nil {
				return err
			}
		}
		return nil
	case *ast.FuncLit:
		return t.walkBlock(x.Body)
	case *ast.FuncCall:
		if err := t.walk(x.Fn); err != nil {
			return err
		}
		for _, a := range x.Args {
			if err := t.walk(a); err != nil {
				return err
			}
		}
		return nil
	case *ast.Call:
		return t.walkCall(x)
	}
	return nil
}

func (t *typeChecker) walkBlock(stmts []ast.Node) error {
	for _, s := range stmts {
		if err := t.walk(s); err != nil {
			return err
		}
	}
	return nil
}

// walkCall enforces TC-644 (null literal receiver) and TC-653 (any-typed
// variable arg under an overloaded operator). In aggregate mode (TC-732)
// detected violations are collected and traversal continues so siblings are
// still inspected.
func (t *typeChecker) walkCall(c *ast.Call) error {
	if len(c.Args) == 0 {
		return nil
	}
	// TC-644: null literal receiver is rejected at compile time. We exempt the
	// common builtin operators that legitimately take literal first args (+, -,
	// etc.) by only flagging when the receiver is the *exact* literal null.
	if lit, ok := c.Args[0].(*ast.Literal); ok && lit.Value.IsNull() {
		err := werr.Newf(werr.CodeNilReceiver,
			"static null receiver for %q", c.Op).WithPath(c.Path())
		if !t.aggregate {
			return err
		}
		t.collect(err)
	}
	// TC-653: any-typed var argument with overloaded operator → ambiguous.
	if t.probe != nil && t.probe.HasOverloads(c.Op) {
		for _, a := range c.Args {
			if v, ok := a.(*ast.Var); ok {
				if t.declared[v.Name] == types.TAny {
					err := werr.Newf(werr.CodeAmbiguousOverload,
						"argument %q is statically any in overloaded call %q; "+
							"narrow the let type to a concrete type",
						v.Name, c.Op).WithPath(c.Path())
					if !t.aggregate {
						return err
					}
					t.collect(err)
				}
			}
		}
	}
	for _, a := range c.Args {
		if err := t.walk(a); err != nil {
			if !t.aggregate {
				return err
			}
			t.collect(err)
		}
	}
	return nil
}
