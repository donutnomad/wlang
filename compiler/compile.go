// Package compiler's Compile entry point wires the pipeline stages declared
// in LANGUAGE.md §7: Decode → Normalize → Parse → (Resolve/TypeCheck/Capability
// happen lazily at runtime today) → Lower/Optimize.
//
// The Optimize pass currently implements a single, safe transformation:
//   - constant folding for arithmetic/comparison operators whose operands are
//     all literal values of compatible numeric type.
//
// More aggressive passes (dead-branch elimination, pure-call pre-evaluation)
// can be layered here without changing the executor.
package compiler

import (
	"github.com/wflang/wflang/ast"
	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/types"
)

// Options tunes the compilation pipeline.
type Options struct {
	// Optimize enables Lower/Optimize passes (§7.8).
	Optimize bool
}

// Compile is the top-level entry (§7.1). It wraps ParseProgram and then applies
// optional Lower/Optimize passes.
func Compile(raw []byte, opts Options) (*ast.Program, error) {
	prog, err := ParseProgram(raw)
	if err != nil {
		return nil, err
	}
	if opts.Optimize {
		if err := foldProgram(prog); err != nil {
			return nil, err
		}
	}
	return prog, nil
}

// foldProgram walks every statement and folds constant sub-expressions.
func foldProgram(p *ast.Program) error {
	for i, s := range p.Body {
		folded, err := foldNode(s)
		if err != nil {
			return err
		}
		p.Body[i] = folded
	}
	return nil
}

// foldNode recursively folds constant expressions inside n and returns the
// (possibly new) node. Statements are returned unchanged except for their
// inner expression slots being rewritten in place.
func foldNode(n ast.Node) (ast.Node, error) {
	switch x := n.(type) {
	case nil:
		return nil, nil
	case *ast.Literal, *ast.Var, *ast.Pkg, *ast.Break, *ast.Continue:
		return n, nil
	case *ast.Return:
		e, err := foldNode(x.Expr)
		if err != nil {
			return nil, err
		}
		x.Expr = e
		return x, nil
	case *ast.Let:
		e, err := foldNode(x.Expr)
		if err != nil {
			return nil, err
		}
		x.Expr = e
		return x, nil
	case *ast.Set:
		e, err := foldNode(x.Expr)
		if err != nil {
			return nil, err
		}
		x.Expr = e
		return x, nil
	case *ast.ExprStmt:
		e, err := foldNode(x.Expr)
		if err != nil {
			return nil, err
		}
		x.Expr = e
		return x, nil
	case *ast.Panic:
		e, err := foldNode(x.Expr)
		if err != nil {
			return nil, err
		}
		x.Expr = e
		return x, nil
	case *ast.IfStmt:
		c, err := foldNode(x.Cond)
		if err != nil {
			return nil, err
		}
		x.Cond = c
		if err := foldBlock(x.Then); err != nil {
			return nil, err
		}
		if err := foldBlock(x.Else); err != nil {
			return nil, err
		}
		return x, nil
	case *ast.IfExpr:
		c, err := foldNode(x.Cond)
		if err != nil {
			return nil, err
		}
		x.Cond = c
		if err := foldBlock(x.Then); err != nil {
			return nil, err
		}
		if err := foldBlock(x.Else); err != nil {
			return nil, err
		}
		return x, nil
	case *ast.Foreach:
		tgt, err := foldNode(x.Target)
		if err != nil {
			return nil, err
		}
		x.Target = tgt
		return x, foldBlock(x.Do)
	case *ast.Fori:
		from, err := foldNode(x.From)
		if err != nil {
			return nil, err
		}
		x.From = from
		to, err := foldNode(x.To)
		if err != nil {
			return nil, err
		}
		x.To = to
		if x.Step != nil {
			step, err := foldNode(x.Step)
			if err != nil {
				return nil, err
			}
			x.Step = step
		}
		return x, foldBlock(x.Do)
	case *ast.Try:
		if err := foldBlock(x.Do); err != nil {
			return nil, err
		}
		return x, foldBlock(x.Catch)
	case *ast.Routine:
		c, err := foldNode(x.Call)
		if err != nil {
			return nil, err
		}
		if cc, ok := c.(*ast.Call); ok {
			x.Call = cc
		}
		return x, nil
	case *ast.Array:
		for i, item := range x.Items {
			f, err := foldNode(item)
			if err != nil {
				return nil, err
			}
			x.Items[i] = f
		}
		return x, nil
	case *ast.Call:
		for i, a := range x.Args {
			f, err := foldNode(a)
			if err != nil {
				return nil, err
			}
			x.Args[i] = f
		}
		return foldCall(x), nil
	}
	return n, nil
}

func foldBlock(stmts []ast.Node) error {
	for i, s := range stmts {
		f, err := foldNode(s)
		if err != nil {
			return err
		}
		stmts[i] = f
	}
	return nil
}

// foldCall attempts constant folding for binary arithmetic when both operands
// are int64 literals. Returns the original Call when folding does not apply.
func foldCall(c *ast.Call) ast.Node {
	if len(c.Args) != 2 {
		return c
	}
	a, aok := c.Args[0].(*ast.Literal)
	b, bok := c.Args[1].(*ast.Literal)
	if !aok || !bok {
		return c
	}
	if a.Value.TypeName() != types.TInt64 || b.Value.TypeName() != types.TInt64 {
		return c
	}
	av, _ := a.Value.Go().(int64)
	bv, _ := b.Value.Go().(int64)
	var (
		res any
		typ string
		ok  = true
	)
	switch c.Op {
	case "+":
		res, typ = av+bv, types.TInt64
	case "-":
		res, typ = av-bv, types.TInt64
	case "*":
		res, typ = av*bv, types.TInt64
	case "/":
		if bv == 0 {
			return c
		}
		res, typ = av/bv, types.TInt64
	case "==":
		res, typ = av == bv, types.TBoolean
	case "!=":
		res, typ = av != bv, types.TBoolean
	case "<":
		res, typ = av < bv, types.TBoolean
	case "<=":
		res, typ = av <= bv, types.TBoolean
	case ">":
		res, typ = av > bv, types.TBoolean
	case ">=":
		res, typ = av >= bv, types.TBoolean
	default:
		ok = false
	}
	if !ok {
		return c
	}
	v := types.NewValue(typ, res)
	return &ast.Literal{Base: ast.Base{P: c.Path()}, Value: v}
}

// unused import guard
var _ = werr.CodeRuntime
