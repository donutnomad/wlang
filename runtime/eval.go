package runtime

import (
	"fmt"
	"math/big"
	"reflect"
	"strings"

	"github.com/wflang/wflang/ast"
	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/types"
)

// Eval evaluates an expression node.
func (e *Executor) Eval(n ast.Node) (types.Value, error) {
	if err := e.tickStep(); err != nil {
		return types.Value{}, err
	}
	switch x := n.(type) {
	case *ast.Literal:
		return x.Value, nil
	case *ast.Var:
		return e.evalVar(x)
	case *ast.Pkg:
		return e.evalPkg(x)
	case *ast.Array:
		return e.evalArray(x)
	case *ast.IfStmt:
		return e.evalIfExpr(x)
	case *ast.Call:
		return e.evalCall(x)
	case *ast.Try:
		// try used in expression position: fold return/last-value, propagate panic.
		v, _, err := e.execTry(x)
		return v, err
	}
	return types.Value{}, werr.Newf(werr.CodeASTShape,
		"cannot evaluate %T as expression", n).WithPath(nodePath(n))
}

func (e *Executor) evalVar(v *ast.Var) (types.Value, error) {
	if got, ok := e.scope.LookupPath(v.Name); ok {
		return got, nil
	}
	if v.Default != nil {
		return e.Eval(v.Default)
	}
	return types.Value{}, werr.Newf(werr.CodeSymbol,
		"variable path %q not found and no default", v.Name).WithPath(v.Path())
}

func (e *Executor) evalPkg(p *ast.Pkg) (types.Value, error) {
	// Packages are carried in the package registry.
	if e.pkgs == nil {
		return types.Value{}, werr.Newf(werr.CodeSymbol,
			"unknown package %q", p.Name).WithPath(p.Path())
	}
	if _, ok := e.pkgs[p.Name]; !ok {
		return types.Value{}, werr.Newf(werr.CodeSymbol,
			"unknown package %q", p.Name).WithPath(p.Path())
	}
	// Package receivers carry a sentinel type name.
	return types.NewValue(pkgTypeName, pkgRef{Name: p.Name}), nil
}

// pkgRef is a runtime marker for package receivers.
const pkgTypeName = "__pkg__"

type pkgRef struct{ Name string }

func (e *Executor) evalArray(a *ast.Array) (types.Value, error) {
	vals := make([]types.Value, 0, len(a.Items))
	for _, item := range a.Items {
		v, err := e.Eval(item)
		if err != nil {
			return types.Value{}, err
		}
		vals = append(vals, v)
	}
	v, err := types.NewArray(a.Elem, vals)
	if err != nil {
		if le, ok := err.(*werr.LangError); ok {
			return types.Value{}, le.WithPath(a.Path())
		}
		return types.Value{}, err
	}
	return v, nil
}

// evalIfExpr: if used as an expression, the matching branch is executed as a
// statement block and its implicit "return" becomes the expression value.
func (e *Executor) evalIfExpr(x *ast.IfStmt) (types.Value, error) {
	cond, err := e.Eval(x.Cond)
	if err != nil {
		return types.Value{}, err
	}
	b, ok := cond.Go().(bool)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeType,
			"if.cond must be boolean, got %s", cond.TypeName()).
			WithPath(x.Cond.Path())
	}
	var branch []ast.Node
	if b {
		branch = x.Then
	} else {
		branch = x.Else
	}
	return e.evalBlock(branch)
}

// evalBlock runs statements within a new scope and returns the result of the
// last expression-yielding statement (return / last return-like).
func (e *Executor) evalBlock(stmts []ast.Node) (types.Value, error) {
	e.scope = e.scope.Push()
	defer func() { e.scope = e.scope.Pop() }()
	var last types.Value
	last, _ = types.NewNull()
	for _, s := range stmts {
		v, sig, err := e.Exec(s)
		if err != nil {
			return types.Value{}, err
		}
		if sig == sigReturn {
			return v, errReturnSig{v: v}
		}
		if sig == sigBreak || sig == sigContinue {
			return v, sigToErr(sig)
		}
		last = v
	}
	return last, nil
}

// ---------- Call dispatch ----------

func (e *Executor) evalCall(c *ast.Call) (types.Value, error) {
	// Arguments are evaluated left-to-right.
	if handler, ok := builtinOps[c.Op]; ok {
		return handler(e, c)
	}
	// Host call: first argument decides receiver kind.
	if len(c.Args) == 0 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"call %q requires at least a receiver argument", c.Op).WithPath(c.Path())
	}
	recv, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	if e.registry == nil {
		return types.Value{}, werr.Newf(werr.CodeSymbol,
			"no registry available for call %q", c.Op).WithPath(c.Path())
	}
	// Evaluate remaining arguments.
	args := make([]types.Value, 0, len(c.Args)-1)
	for _, a := range c.Args[1:] {
		av, err := e.Eval(a)
		if err != nil {
			return types.Value{}, err
		}
		args = append(args, av)
	}
	return e.registry.Invoke(e.ctx, c.Op, recv, args, c.Path())
}

// ---------- Built-in operators ----------

type opHandler func(e *Executor, c *ast.Call) (types.Value, error)

var builtinOps map[string]opHandler

func init() {
	builtinOps = map[string]opHandler{
		"+":          arithmeticOp("+"),
		"-":          arithmeticOp("-"),
		"*":          arithmeticOp("*"),
		"/":          arithmeticOp("/"),
		">":          compareOp(">"),
		">=":         compareOp(">="),
		"<":          compareOp("<"),
		"<=":         compareOp("<="),
		"==":         equalityOp(true),
		"!=":         equalityOp(false),
		"and":        logicalAnd,
		"or":         logicalOr,
		"!":          logicalNot,
		"contains":   stringBinOp("contains"),
		"startsWith": stringBinOp("startsWith"),
		"endsWith":   stringBinOp("endsWith"),
	}
}

func evalArgs(e *Executor, args []ast.Node) ([]types.Value, error) {
	out := make([]types.Value, 0, len(args))
	for _, a := range args {
		v, err := e.Eval(a)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func arithmeticOp(op string) opHandler {
	return func(e *Executor, c *ast.Call) (types.Value, error) {
		vals, err := evalArgs(e, c.Args)
		if err != nil {
			return types.Value{}, err
		}
		if len(vals) < 2 {
			return types.Value{}, werr.Newf(werr.CodeType,
				"operator %q requires at least 2 args, got %d", op, len(vals)).
				WithPath(c.Path())
		}
		res := vals[0]
		for _, rhs := range vals[1:] {
			r, err := arithStep(op, res, rhs)
			if err != nil {
				if le, ok := err.(*werr.LangError); ok {
					return types.Value{}, le.WithPath(c.Path())
				}
				return types.Value{}, err
			}
			res = r
		}
		return res, nil
	}
}

func arithStep(op string, a, b types.Value) (types.Value, error) {
	// Same typename -> direct
	if a.TypeName() == b.TypeName() {
		return arithSame(op, a, b)
	}
	// string concat for "+" when both are string is already covered above.
	// Otherwise, promote numeric types: int* <-> float* / bigInt / bigDecimal.
	// For this MVP we only handle exact same typename; mismatched types are an error.
	return types.Value{}, werr.Newf(werr.CodeType,
		"operator %q requires matching types, got %s and %s", op, a.TypeName(), b.TypeName())
}

func arithSame(op string, a, b types.Value) (types.Value, error) {
	switch a.TypeName() {
	case types.TInt8:
		return numI8(op, a.Go().(int8), b.Go().(int8))
	case types.TInt16:
		return numI16(op, a.Go().(int16), b.Go().(int16))
	case types.TInt32:
		return numI32(op, a.Go().(int32), b.Go().(int32))
	case types.TInt64:
		return numI64(op, a.Go().(int64), b.Go().(int64))
	case types.TUint8:
		return numU8(op, a.Go().(uint8), b.Go().(uint8))
	case types.TUint16:
		return numU16(op, a.Go().(uint16), b.Go().(uint16))
	case types.TUint32:
		return numU32(op, a.Go().(uint32), b.Go().(uint32))
	case types.TUint64:
		return numU64(op, a.Go().(uint64), b.Go().(uint64))
	case types.TFloat32:
		return numF32(op, a.Go().(float32), b.Go().(float32))
	case types.TFloat64:
		return numF64(op, a.Go().(float64), b.Go().(float64))
	case types.TString:
		if op != "+" {
			return types.Value{}, werr.Newf(werr.CodeType,
				"operator %q not defined for string", op)
		}
		return types.NewValue(types.TString, a.Go().(string)+b.Go().(string)), nil
	case types.TBigInt:
		return bigIntOp(op, a.Go().(*big.Int), b.Go().(*big.Int))
	case types.TBigDecimal:
		return bigRatOp(op, a.Go().(*big.Rat), b.Go().(*big.Rat))
	}
	return types.Value{}, werr.Newf(werr.CodeType,
		"operator %q not defined for %s", op, a.TypeName())
}

func numI8(op string, a, b int8) (types.Value, error) {
	var r int8
	switch op {
	case "+":
		r = a + b
	case "-":
		r = a - b
	case "*":
		r = a * b
	case "/":
		if b == 0 {
			return types.Value{}, werr.New(werr.CodeRuntime, "division by zero")
		}
		r = a / b
	default:
		return types.Value{}, werr.Newf(werr.CodeType, "bad int8 op %q", op)
	}
	return types.NewValue(types.TInt8, r), nil
}
func numI16(op string, a, b int16) (types.Value, error) {
	var r int16
	switch op {
	case "+":
		r = a + b
	case "-":
		r = a - b
	case "*":
		r = a * b
	case "/":
		if b == 0 {
			return types.Value{}, werr.New(werr.CodeRuntime, "division by zero")
		}
		r = a / b
	default:
		return types.Value{}, werr.Newf(werr.CodeType, "bad int16 op %q", op)
	}
	return types.NewValue(types.TInt16, r), nil
}
func numI32(op string, a, b int32) (types.Value, error) {
	var r int32
	switch op {
	case "+":
		r = a + b
	case "-":
		r = a - b
	case "*":
		r = a * b
	case "/":
		if b == 0 {
			return types.Value{}, werr.New(werr.CodeRuntime, "division by zero")
		}
		r = a / b
	default:
		return types.Value{}, werr.Newf(werr.CodeType, "bad int32 op %q", op)
	}
	return types.NewValue(types.TInt32, r), nil
}
func numI64(op string, a, b int64) (types.Value, error) {
	var r int64
	switch op {
	case "+":
		r = a + b
	case "-":
		r = a - b
	case "*":
		r = a * b
	case "/":
		if b == 0 {
			return types.Value{}, werr.New(werr.CodeRuntime, "division by zero")
		}
		r = a / b
	default:
		return types.Value{}, werr.Newf(werr.CodeType, "bad int64 op %q", op)
	}
	return types.NewValue(types.TInt64, r), nil
}
func numU8(op string, a, b uint8) (types.Value, error) {
	var r uint8
	switch op {
	case "+":
		r = a + b
	case "-":
		r = a - b
	case "*":
		r = a * b
	case "/":
		if b == 0 {
			return types.Value{}, werr.New(werr.CodeRuntime, "division by zero")
		}
		r = a / b
	default:
		return types.Value{}, werr.Newf(werr.CodeType, "bad uint8 op %q", op)
	}
	return types.NewValue(types.TUint8, r), nil
}
func numU16(op string, a, b uint16) (types.Value, error) {
	var r uint16
	switch op {
	case "+":
		r = a + b
	case "-":
		r = a - b
	case "*":
		r = a * b
	case "/":
		if b == 0 {
			return types.Value{}, werr.New(werr.CodeRuntime, "division by zero")
		}
		r = a / b
	default:
		return types.Value{}, werr.Newf(werr.CodeType, "bad uint16 op %q", op)
	}
	return types.NewValue(types.TUint16, r), nil
}
func numU32(op string, a, b uint32) (types.Value, error) {
	var r uint32
	switch op {
	case "+":
		r = a + b
	case "-":
		r = a - b
	case "*":
		r = a * b
	case "/":
		if b == 0 {
			return types.Value{}, werr.New(werr.CodeRuntime, "division by zero")
		}
		r = a / b
	default:
		return types.Value{}, werr.Newf(werr.CodeType, "bad uint32 op %q", op)
	}
	return types.NewValue(types.TUint32, r), nil
}
func numU64(op string, a, b uint64) (types.Value, error) {
	var r uint64
	switch op {
	case "+":
		r = a + b
	case "-":
		r = a - b
	case "*":
		r = a * b
	case "/":
		if b == 0 {
			return types.Value{}, werr.New(werr.CodeRuntime, "division by zero")
		}
		r = a / b
	default:
		return types.Value{}, werr.Newf(werr.CodeType, "bad uint64 op %q", op)
	}
	return types.NewValue(types.TUint64, r), nil
}
func numF32(op string, a, b float32) (types.Value, error) {
	var r float32
	switch op {
	case "+":
		r = a + b
	case "-":
		r = a - b
	case "*":
		r = a * b
	case "/":
		if b == 0 {
			return types.Value{}, werr.New(werr.CodeRuntime, "division by zero")
		}
		r = a / b
	default:
		return types.Value{}, werr.Newf(werr.CodeType, "bad float32 op %q", op)
	}
	return types.NewValue(types.TFloat32, r), nil
}
func numF64(op string, a, b float64) (types.Value, error) {
	var r float64
	switch op {
	case "+":
		r = a + b
	case "-":
		r = a - b
	case "*":
		r = a * b
	case "/":
		if b == 0 {
			return types.Value{}, werr.New(werr.CodeRuntime, "division by zero")
		}
		r = a / b
	default:
		return types.Value{}, werr.Newf(werr.CodeType, "bad float64 op %q", op)
	}
	return types.NewValue(types.TFloat64, r), nil
}

func bigIntOp(op string, a, b *big.Int) (types.Value, error) {
	r := new(big.Int)
	switch op {
	case "+":
		r.Add(a, b)
	case "-":
		r.Sub(a, b)
	case "*":
		r.Mul(a, b)
	case "/":
		if b.Sign() == 0 {
			return types.Value{}, werr.New(werr.CodeRuntime, "division by zero")
		}
		r.Quo(a, b)
	default:
		return types.Value{}, werr.Newf(werr.CodeType, "bad bigInt op %q", op)
	}
	return types.NewValue(types.TBigInt, r), nil
}

func bigRatOp(op string, a, b *big.Rat) (types.Value, error) {
	r := new(big.Rat)
	switch op {
	case "+":
		r.Add(a, b)
	case "-":
		r.Sub(a, b)
	case "*":
		r.Mul(a, b)
	case "/":
		if b.Sign() == 0 {
			return types.Value{}, werr.New(werr.CodeRuntime, "division by zero")
		}
		r.Quo(a, b)
	default:
		return types.Value{}, werr.Newf(werr.CodeType, "bad bigDecimal op %q", op)
	}
	return types.NewValue(types.TBigDecimal, r), nil
}

func compareOp(op string) opHandler {
	return func(e *Executor, c *ast.Call) (types.Value, error) {
		vals, err := evalArgs(e, c.Args)
		if err != nil {
			return types.Value{}, err
		}
		if len(vals) != 2 {
			return types.Value{}, werr.Newf(werr.CodeType,
				"operator %q expects 2 args", op).WithPath(c.Path())
		}
		a, b := vals[0], vals[1]
		if a.TypeName() != b.TypeName() {
			return types.Value{}, werr.Newf(werr.CodeType,
				"operator %q requires matching types, got %s and %s",
				op, a.TypeName(), b.TypeName()).WithPath(c.Path())
		}
		cmp, err := compareValues(a, b)
		if err != nil {
			return types.Value{}, err
		}
		result := false
		switch op {
		case "<":
			result = cmp < 0
		case "<=":
			result = cmp <= 0
		case ">":
			result = cmp > 0
		case ">=":
			result = cmp >= 0
		}
		return types.NewValue(types.TBoolean, result), nil
	}
}

func compareValues(a, b types.Value) (int, error) {
	switch a.TypeName() {
	case types.TInt8:
		return cmpInt64(int64(a.Go().(int8)), int64(b.Go().(int8))), nil
	case types.TInt16:
		return cmpInt64(int64(a.Go().(int16)), int64(b.Go().(int16))), nil
	case types.TInt32:
		return cmpInt64(int64(a.Go().(int32)), int64(b.Go().(int32))), nil
	case types.TInt64:
		return cmpInt64(a.Go().(int64), b.Go().(int64)), nil
	case types.TUint8:
		return cmpUint64(uint64(a.Go().(uint8)), uint64(b.Go().(uint8))), nil
	case types.TUint16:
		return cmpUint64(uint64(a.Go().(uint16)), uint64(b.Go().(uint16))), nil
	case types.TUint32:
		return cmpUint64(uint64(a.Go().(uint32)), uint64(b.Go().(uint32))), nil
	case types.TUint64:
		return cmpUint64(a.Go().(uint64), b.Go().(uint64)), nil
	case types.TFloat32:
		af, bf := a.Go().(float32), b.Go().(float32)
		switch {
		case af < bf:
			return -1, nil
		case af > bf:
			return 1, nil
		}
		return 0, nil
	case types.TFloat64:
		af, bf := a.Go().(float64), b.Go().(float64)
		switch {
		case af < bf:
			return -1, nil
		case af > bf:
			return 1, nil
		}
		return 0, nil
	case types.TString:
		return strings.Compare(a.Go().(string), b.Go().(string)), nil
	case types.TBigInt:
		return a.Go().(*big.Int).Cmp(b.Go().(*big.Int)), nil
	case types.TBigDecimal:
		return a.Go().(*big.Rat).Cmp(b.Go().(*big.Rat)), nil
	}
	return 0, werr.Newf(werr.CodeType, "cannot compare %s", a.TypeName())
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
func cmpUint64(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func equalityOp(want bool) opHandler {
	return func(e *Executor, c *ast.Call) (types.Value, error) {
		vals, err := evalArgs(e, c.Args)
		if err != nil {
			return types.Value{}, err
		}
		if len(vals) != 2 {
			return types.Value{}, werr.Newf(werr.CodeType,
				"equality expects 2 args").WithPath(c.Path())
		}
		eq := equalValues(vals[0], vals[1])
		return types.NewValue(types.TBoolean, eq == want), nil
	}
}

func equalValues(a, b types.Value) bool {
	if a.IsNull() && b.IsNull() {
		return true
	}
	if a.TypeName() != b.TypeName() {
		return false
	}
	switch a.TypeName() {
	case types.TBigInt:
		return a.Go().(*big.Int).Cmp(b.Go().(*big.Int)) == 0
	case types.TBigDecimal:
		return a.Go().(*big.Rat).Cmp(b.Go().(*big.Rat)) == 0
	}
	return reflect.DeepEqual(a.Go(), b.Go())
}

func logicalAnd(e *Executor, c *ast.Call) (types.Value, error) {
	for _, n := range c.Args {
		v, err := e.Eval(n)
		if err != nil {
			return types.Value{}, err
		}
		b, ok := v.Go().(bool)
		if !ok {
			return types.Value{}, werr.Newf(werr.CodeType,
				"and requires boolean, got %s", v.TypeName()).WithPath(c.Path())
		}
		if !b {
			return types.NewValue(types.TBoolean, false), nil
		}
	}
	return types.NewValue(types.TBoolean, true), nil
}

func logicalOr(e *Executor, c *ast.Call) (types.Value, error) {
	for _, n := range c.Args {
		v, err := e.Eval(n)
		if err != nil {
			return types.Value{}, err
		}
		b, ok := v.Go().(bool)
		if !ok {
			return types.Value{}, werr.Newf(werr.CodeType,
				"or requires boolean, got %s", v.TypeName()).WithPath(c.Path())
		}
		if b {
			return types.NewValue(types.TBoolean, true), nil
		}
	}
	return types.NewValue(types.TBoolean, false), nil
}

func logicalNot(e *Executor, c *ast.Call) (types.Value, error) {
	if len(c.Args) != 1 {
		return types.Value{}, werr.Newf(werr.CodeType,
			"! expects exactly 1 arg, got %d", len(c.Args)).WithPath(c.Path())
	}
	v, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	b, ok := v.Go().(bool)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeType,
			"! requires boolean, got %s", v.TypeName()).WithPath(c.Path())
	}
	return types.NewValue(types.TBoolean, !b), nil
}

func stringBinOp(op string) opHandler {
	return func(e *Executor, c *ast.Call) (types.Value, error) {
		vals, err := evalArgs(e, c.Args)
		if err != nil {
			return types.Value{}, err
		}
		if len(vals) != 2 {
			return types.Value{}, werr.Newf(werr.CodeType,
				"%s expects 2 args", op).WithPath(c.Path())
		}
		s, ok := vals[0].Go().(string)
		t, ok2 := vals[1].Go().(string)
		if !ok || !ok2 {
			return types.Value{}, werr.Newf(werr.CodeType,
				"%s requires string args", op).WithPath(c.Path())
		}
		var r bool
		switch op {
		case "contains":
			r = strings.Contains(s, t)
		case "startsWith":
			r = strings.HasPrefix(s, t)
		case "endsWith":
			r = strings.HasSuffix(s, t)
		}
		return types.NewValue(types.TBoolean, r), nil
	}
}

// nodePath extracts the Path() of any node (nil-safe).
func nodePath(n ast.Node) string {
	if n == nil {
		return ""
	}
	return n.Path()
}

// ensure fmt import isn't dropped
var _ = fmt.Sprintf
