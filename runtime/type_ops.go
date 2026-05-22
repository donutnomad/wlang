package runtime

import (
	"reflect"
	"strings"

	"github.com/donutnomad/wlang/ast"
	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/types"
)

func ptrDeref(e *Executor, c *ast.Call) (types.Value, error) {
	if len(c.Args) != 1 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"ptr.deref expects (ptr), got %d args", len(c.Args)).WithPath(c.Path())
	}
	v, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	if v.Go() == nil {
		return types.Value{}, werr.New(werr.CodeNilReceiver,
			"ptr.deref target is nil").WithPath(c.Args[0].Path())
	}
	rv := reflect.ValueOf(v.Go())
	if rv.Kind() != reflect.Pointer {
		return types.Value{}, werr.Newf(werr.CodeType,
			"ptr.deref expects pointer, got %s", rv.Type()).WithPath(c.Args[0].Path())
	}
	if rv.IsNil() {
		return types.Value{}, werr.New(werr.CodeNilReceiver,
			"ptr.deref target is nil").WithPath(c.Args[0].Path())
	}
	return wrapGo(rv.Elem().Interface()), nil
}

func ptrNew(e *Executor, c *ast.Call) (types.Value, error) {
	if len(c.Args) != 1 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"ptr.new expects (typeName), got %d args", len(c.Args)).WithPath(c.Path())
	}
	typeV, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	typeName, ok := typeV.Go().(string)
	if !ok || typeName == "" {
		return types.Value{}, werr.New(werr.CodeType, "ptr.new type name must be string").WithPath(c.Args[0].Path())
	}
	rt, ok := primitiveReflectType(typeName)
	if !ok && e.registry != nil {
		rt, ok = e.registry.StructType(typeName)
	}
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeSymbol, "type %q not registered", typeName).WithPath(c.Args[0].Path())
	}
	ptr := reflect.New(rt)
	return types.NewValue("*"+typeName, ptr.Interface()), nil
}

func primitiveReflectType(typeName string) (reflect.Type, bool) {
	switch typeName {
	case types.TString:
		return reflect.TypeOf(""), true
	case types.TBoolean:
		return reflect.TypeOf(false), true
	case types.TInt8:
		return reflect.TypeOf(int8(0)), true
	case types.TInt16:
		return reflect.TypeOf(int16(0)), true
	case types.TInt32:
		return reflect.TypeOf(int32(0)), true
	case types.TInt64:
		return reflect.TypeOf(int64(0)), true
	case types.TUint8:
		return reflect.TypeOf(uint8(0)), true
	case types.TUint16:
		return reflect.TypeOf(uint16(0)), true
	case types.TUint32:
		return reflect.TypeOf(uint32(0)), true
	case types.TUint64:
		return reflect.TypeOf(uint64(0)), true
	case types.TFloat32:
		return reflect.TypeOf(float32(0)), true
	case types.TFloat64:
		return reflect.TypeOf(float64(0)), true
	}
	return nil, false
}

func typeAssert(e *Executor, c *ast.Call) (types.Value, error) {
	v, target, err := typeAssertArgs(e, c, "type.assert")
	if err != nil {
		return types.Value{}, err
	}
	if !valueMatchesType(v, target) {
		return types.Value{}, werr.Newf(werr.CodeType,
			"type.assert cannot pass %s as %s", v.TypeName(), target).WithPath(c.Path())
	}
	return retagAssertedValue(v, target), nil
}

func typeAssertOK(e *Executor, c *ast.Call) (types.Value, error) {
	v, target, err := typeAssertArgs(e, c, "type.assert.ok")
	if err != nil {
		return types.Value{}, err
	}
	ok := valueMatchesType(v, target)
	var out types.Value
	if ok {
		out = retagAssertedValue(v, target)
	} else {
		out = zeroAssertedValue(target)
	}
	return types.NewValue(types.TupleType([]string{target, types.TBoolean}), []any{out.Go(), ok}), nil
}

func typeIs(e *Executor, c *ast.Call) (types.Value, error) {
	v, target, err := typeAssertArgs(e, c, "type.is")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewValue(types.TBoolean, valueMatchesType(v, target)), nil
}

func typeAssertArgs(e *Executor, c *ast.Call, op string) (types.Value, string, error) {
	if len(c.Args) != 2 {
		return types.Value{}, "", werr.Newf(werr.CodeASTShape,
			"%s expects (value, typeName), got %d args", op, len(c.Args)).WithPath(c.Path())
	}
	v, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, "", err
	}
	targetV, err := e.Eval(c.Args[1])
	if err != nil {
		return types.Value{}, "", err
	}
	target, ok := targetV.Go().(string)
	if !ok || target == "" {
		return types.Value{}, "", werr.Newf(werr.CodeType,
			"%s type name must be string", op).WithPath(c.Args[1].Path())
	}
	return v, target, nil
}

func valueMatchesType(v types.Value, target string) bool {
	if v.TypeName() == target {
		return true
	}
	if v.Go() == nil {
		return target == types.TNull
	}
	rt := reflect.TypeOf(v.Go())
	return reflectTypeName(rt) == target || types.AutoHostTypeName(rt) == target
}

func retagAssertedValue(v types.Value, target string) types.Value {
	if v.TypeName() == target {
		return v
	}
	return types.NewValue(target, v.Go())
}

func zeroAssertedValue(target string) types.Value {
	if v, err := zeroPrimitive(target, ""); err == nil {
		return v
	}
	return types.NewValue(target, nil)
}

func bitNot(e *Executor, c *ast.Call) (types.Value, error) {
	if len(c.Args) != 1 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"bit.not expects (value), got %d args", len(c.Args)).WithPath(c.Path())
	}
	v, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	switch v.TypeName() {
	case types.TInt64:
		return types.NewValue(types.TInt64, ^v.Go().(int64)), nil
	case types.TInt32:
		return types.NewValue(types.TInt32, ^v.Go().(int32)), nil
	case types.TInt16:
		return types.NewValue(types.TInt16, ^v.Go().(int16)), nil
	case types.TInt8:
		return types.NewValue(types.TInt8, ^v.Go().(int8)), nil
	case types.TUint64:
		return types.NewValue(types.TUint64, ^v.Go().(uint64)), nil
	case types.TUint32:
		return types.NewValue(types.TUint32, ^v.Go().(uint32)), nil
	case types.TUint16:
		return types.NewValue(types.TUint16, ^v.Go().(uint16)), nil
	case types.TUint8:
		return types.NewValue(types.TUint8, ^v.Go().(uint8)), nil
	default:
		return types.Value{}, werr.Newf(werr.CodeType,
			"bit.not expects integer, got %s", v.TypeName()).WithPath(c.Args[0].Path())
	}
}

func copyBuiltin(e *Executor, c *ast.Call) (types.Value, error) {
	if len(c.Args) != 2 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"copy expects (dst, src), got %d args", len(c.Args)).WithPath(c.Path())
	}
	target, ok := c.Args[0].(*ast.Var)
	if !ok || target.Name == "" || strings.Contains(target.Name, ".") {
		return types.Value{}, werr.New(werr.CodeASTShape,
			"copy first argument must be an array variable").WithPath(c.Args[0].Path())
	}
	dstV, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	srcV, err := e.Eval(c.Args[1])
	if err != nil {
		return types.Value{}, err
	}
	dst, elem, ok := extractArrayItems(dstV)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeType, "copy dst must be array, got %s", dstV.TypeName()).WithPath(c.Args[0].Path())
	}
	src, srcElem, ok := extractArrayItems(srcV)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeType, "copy src must be array, got %s", srcV.TypeName()).WithPath(c.Args[1].Path())
	}
	if elem != srcElem {
		return types.Value{}, werr.Newf(werr.CodeType, "copy element type mismatch %s vs %s", elem, srcElem).WithPath(c.Path())
	}
	n := len(dst)
	if len(src) < n {
		n = len(src)
	}
	next := make([]types.Value, 0, len(dst))
	for i, raw := range dst {
		if i < n {
			next = append(next, types.NewValue(elem, src[i]))
		} else {
			next = append(next, types.NewValue(elem, raw))
		}
	}
	newArr, err := types.NewArray(elem, next)
	if err != nil {
		return types.Value{}, err
	}
	if err := e.scope.Set(target.Name, newArr); err != nil {
		return types.Value{}, err
	}
	return types.NewValue(types.TInt64, int64(n)), nil
}

func complexBuiltin(e *Executor, c *ast.Call) (types.Value, error) {
	if len(c.Args) != 2 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"complex expects (real, imag), got %d args", len(c.Args)).WithPath(c.Path())
	}
	re, err := numericAsFloat64(e, c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	im, err := numericAsFloat64(e, c.Args[1])
	if err != nil {
		return types.Value{}, err
	}
	return types.NewValue("complex128", complex(re, im)), nil
}

func realBuiltin(e *Executor, c *ast.Call) (types.Value, error) {
	v, err := complexArg(e, c, "real")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewValue(types.TFloat64, real(v)), nil
}

func imagBuiltin(e *Executor, c *ast.Call) (types.Value, error) {
	v, err := complexArg(e, c, "imag")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewValue(types.TFloat64, imag(v)), nil
}

func complexArg(e *Executor, c *ast.Call, op string) (complex128, error) {
	if len(c.Args) != 1 {
		return 0, werr.Newf(werr.CodeASTShape, "%s expects (complex), got %d args", op, len(c.Args)).WithPath(c.Path())
	}
	v, err := e.Eval(c.Args[0])
	if err != nil {
		return 0, err
	}
	switch x := v.Go().(type) {
	case complex128:
		return x, nil
	case complex64:
		return complex128(x), nil
	default:
		return 0, werr.Newf(werr.CodeType, "%s expects complex value, got %s", op, v.TypeName()).WithPath(c.Args[0].Path())
	}
}

func numericAsFloat64(e *Executor, n ast.Node) (float64, error) {
	v, err := e.Eval(n)
	if err != nil {
		return 0, err
	}
	switch x := v.Go().(type) {
	case int8:
		return float64(x), nil
	case int16:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case uint8:
		return float64(x), nil
	case uint16:
		return float64(x), nil
	case uint32:
		return float64(x), nil
	case uint64:
		return float64(x), nil
	case float32:
		return float64(x), nil
	case float64:
		return x, nil
	default:
		return 0, werr.Newf(werr.CodeType, "numeric argument expected, got %s", v.TypeName()).WithPath(n.Path())
	}
}
