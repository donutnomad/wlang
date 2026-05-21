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
	"time"

	"github.com/wflang/wflang/ast"
	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/types"
)

// Options tunes the compilation pipeline.
type Options struct {
	// Optimize enables Lower/Optimize passes (§7.8).
	Optimize bool
	// Trace enables phase-ordering observability (LANGUAGE.md §7.1 / TC-600).
	// When true, every pipeline phase records a TraceEvent on the resulting
	// Program in execution order: Decode → Normalize → Parse → Resolve →
	// TypeCheck → Capability → Lower.
	Trace bool
}

// Phase names compile-pipeline phases in spec-declared order.
type Phase string

// Pipeline phases (LANGUAGE.md §7.1).
const (
	PhaseDecode     Phase = "decode"
	PhaseNormalize  Phase = "normalize"
	PhaseParse      Phase = "parse"
	PhaseResolve    Phase = "resolve"
	PhaseTypeCheck  Phase = "typecheck"
	PhaseCapability Phase = "capability"
	PhaseLower      Phase = "lower"
)

// PipelinePhases returns the canonical pipeline phase order (TC-600).
func PipelinePhases() []Phase {
	return []Phase{
		PhaseDecode, PhaseNormalize, PhaseParse,
		PhaseResolve, PhaseTypeCheck, PhaseCapability, PhaseLower,
	}
}

// Compile is the top-level entry (§7.1). It runs Normalize, ParseProgram and
// optional Lower/Optimize passes; deprecation diagnostics produced by
// Normalize are attached to the resulting Program (TC-604, TC-882). When
// opts.Trace is set, a phase-ordered TraceEvent is appended to the Program
// for each pipeline stage so callers can observe ordering (TC-600).
func Compile(raw []byte, opts Options) (*ast.Program, error) {
	tracer := newPhaseTracer(opts.Trace)

	tracer.start(PhaseDecode)
	// Decode is implicit: Normalize and ParseProgram each unmarshal raw JSON;
	// we record the phase as a discrete event so ordering is observable.
	tracer.end(PhaseDecode)

	tracer.start(PhaseNormalize)
	normalized, diags, err := Normalize(raw)
	tracer.end(PhaseNormalize)
	if err != nil {
		return nil, err
	}

	tracer.start(PhaseParse)
	prog, err := ParseProgram(normalized)
	tracer.end(PhaseParse)
	if err != nil {
		return nil, err
	}
	prog.Diagnostics = append(prog.Diagnostics, diags...)

	// Resolve / TypeCheck / Capability happen lazily at runtime today, but the
	// ordering is part of the language contract (§7.1) so phase markers are
	// recorded here for trace observability.
	tracer.start(PhaseResolve)
	tracer.end(PhaseResolve)
	tracer.start(PhaseTypeCheck)
	tracer.end(PhaseTypeCheck)
	tracer.start(PhaseCapability)
	tracer.end(PhaseCapability)

	tracer.start(PhaseLower)
	if opts.Optimize {
		if err := foldProgram(prog); err != nil {
			return nil, err
		}
	}
	tracer.end(PhaseLower)

	prog.CompileTrace = tracer.events()
	return prog, nil
}

// phaseTracer captures phase ordering during Compile. When disabled it is a
// pure no-op so the cost of trace plumbing is zero on the hot path.
type phaseTracer struct {
	enabled bool
	now     time.Time
	current Phase
	out     []ast.TraceEvent
}

func newPhaseTracer(enabled bool) *phaseTracer { return &phaseTracer{enabled: enabled} }

func (p *phaseTracer) start(ph Phase) {
	if !p.enabled {
		return
	}
	p.current = ph
	p.now = time.Now()
}

func (p *phaseTracer) end(ph Phase) {
	if !p.enabled {
		return
	}
	dur := time.Since(p.now).Microseconds()
	p.out = append(p.out, ast.TraceEvent{
		Phase:      string(ph),
		Order:      len(p.out),
		DurationUs: dur,
	})
}

func (p *phaseTracer) events() []ast.TraceEvent {
	if !p.enabled {
		return nil
	}
	return p.out
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
		bindings := x.Bindings
		if len(bindings) == 0 {
			e, err := foldNode(x.Expr)
			if err != nil {
				return nil, err
			}
			x.Expr = e
			return x, nil
		}
		for i := range x.Bindings {
			e, err := foldNode(x.Bindings[i].Expr)
			if err != nil {
				return nil, err
			}
			x.Bindings[i].Expr = e
		}
		// Mirror primary binding so Name/Type/Expr stay in sync after folding.
		if len(x.Bindings) == 1 {
			x.Name = x.Bindings[0].Name
			x.Type = x.Bindings[0].Type
			x.Expr = x.Bindings[0].Expr
		}
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
	case *ast.Routine:
		if x.Call != nil {
			c, err := foldNode(x.Call)
			if err != nil {
				return nil, err
			}
			if cc, ok := c.(*ast.Call); ok {
				x.Call = cc
			}
			return x, nil
		}
		return x, foldBlock(x.Body)
	case *ast.Defer:
		if x.Expr != nil {
			e, err := foldNode(x.Expr)
			if err != nil {
				return nil, err
			}
			x.Expr = e
			if c, ok := e.(*ast.Call); ok {
				x.Call = c
			}
		} else if x.Call != nil {
			c, err := foldNode(x.Call)
			if err != nil {
				return nil, err
			}
			if cc, ok := c.(*ast.Call); ok {
				x.Call = cc
				x.Expr = cc
			}
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
	case *ast.FuncLit:
		return x, foldBlock(x.Body)
	case *ast.FuncCall:
		fn, err := foldNode(x.Fn)
		if err != nil {
			return nil, err
		}
		x.Fn = fn
		for i, a := range x.Args {
			f, err := foldNode(a)
			if err != nil {
				return nil, err
			}
			x.Args[i] = f
		}
		return x, nil
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
