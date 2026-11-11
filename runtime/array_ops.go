package runtime

import (
	"reflect"
	"strings"

	"github.com/donutnomad/wlang/ast"
	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/types"
)

// Arrays follow Go slice semantics: the carrier is a Go slice, element writes
// and sub-slices share its backing array, and arr.push appends like Go's
// append (reusing spare capacity).

// arrayCarrier returns the Go slice behind an array<T> value and its element
// type name.
func arrayCarrier(v types.Value) (reflect.Value, string, bool) {
	name := v.TypeName()
	if len(name) < 7 || name[:6] != "array<" || name[len(name)-1] != '>' {
		return reflect.Value{}, "", false
	}
	elem := name[6 : len(name)-1]
	rv := reflect.ValueOf(v.Go())
	if rv.Kind() != reflect.Slice || rv.Type() != reflect.TypeOf(types.NewNilArray(elem).Go()) {
		return reflect.Value{}, "", false
	}
	return rv, elem, true
}

// carrierElement converts item into a value storable in a slice of type t.
func carrierElement(item types.Value, t reflect.Type) (reflect.Value, bool) {
	raw := item.Go()
	if raw == nil {
		return reflect.Zero(t), true
	}
	rv := reflect.ValueOf(raw)
	if rv.Type().AssignableTo(t) {
		return rv, true
	}
	return reflect.Value{}, false
}

func arrayPush(e *Executor, c *ast.Call) (types.Value, error) {
	if len(c.Args) != 2 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"arr.push expects (arrayVar, value), got %d args", len(c.Args)).WithPath(c.Path())
	}
	target, ok := c.Args[0].(*ast.Var)
	if !ok || target.Name == "" || strings.Contains(target.Name, ".") {
		return types.Value{}, werr.New(werr.CodeASTShape,
			"arr.push first argument must be a variable").WithPath(c.Args[0].Path())
	}
	arrV, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	items, elem, ok := arrayCarrier(arrV)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.push target must be array, got %s", arrV.TypeName()).WithPath(c.Args[0].Path())
	}
	if e.budget.MaxArrayLength > 0 && items.Len()+1 > e.budget.MaxArrayLength {
		return types.Value{}, werr.Newf(werr.CodeBudget,
			"MaxArrayLength exceeded (%d)", e.budget.MaxArrayLength).WithPath(c.Path())
	}
	item, err := e.Eval(c.Args[1])
	if err != nil {
		return types.Value{}, err
	}
	stored, ok := carrierElement(item, items.Type().Elem())
	if !ok || !e.valueAssignableTo(item, elem) {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.push element expects %s, got %s", elem, item.TypeName()).WithPath(c.Args[1].Path())
	}
	newArr := types.NewValue(arrV.TypeName(), reflect.Append(items, stored).Interface())
	if err := e.scope.Set(target.Name, newArr); err != nil {
		if le, ok := err.(*werr.LangError); ok {
			return types.Value{}, le.WithPath(c.Args[0].Path())
		}
		return types.Value{}, err
	}
	null, _ := types.NewNull()
	return null, nil
}

func arrayIndex(e *Executor, c *ast.Call, op string, items reflect.Value) (int, error) {
	idxV, err := e.Eval(c.Args[1])
	if err != nil {
		return 0, err
	}
	if idxV.TypeName() != types.TInt64 {
		return 0, werr.Newf(werr.CodeType,
			"%s index must be int64, got %s", op, idxV.TypeName()).WithPath(c.Args[1].Path())
	}
	idx := idxV.Go().(int64)
	if idx < 0 || idx >= int64(items.Len()) {
		return 0, werr.Newf(werr.CodeRuntime,
			"%s index %d out of range [0,%d)", op, idx, items.Len()).WithPath(c.Args[1].Path())
	}
	return int(idx), nil
}

func arrayGet(e *Executor, c *ast.Call) (types.Value, error) {
	if len(c.Args) != 2 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"arr.get expects (array, index), got %d args", len(c.Args)).WithPath(c.Path())
	}
	arrV, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	items, elem, ok := arrayCarrier(arrV)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.get target must be array, got %s", arrV.TypeName()).WithPath(c.Args[0].Path())
	}
	idx, err := arrayIndex(e, c, "arr.get", items)
	if err != nil {
		return types.Value{}, err
	}
	return types.NewValue(elem, items.Index(idx).Interface()), nil
}

func arraySet(e *Executor, c *ast.Call) (types.Value, error) {
	if len(c.Args) != 3 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"arr.set expects (arrayVar, index, value), got %d args", len(c.Args)).WithPath(c.Path())
	}
	target, ok := c.Args[0].(*ast.Var)
	if !ok || target.Name == "" || strings.Contains(target.Name, ".") {
		return types.Value{}, werr.New(werr.CodeASTShape,
			"arr.set first argument must be a variable").WithPath(c.Args[0].Path())
	}
	arrV, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	items, elem, ok := arrayCarrier(arrV)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.set target must be array, got %s", arrV.TypeName()).WithPath(c.Args[0].Path())
	}
	idx, err := arrayIndex(e, c, "arr.set", items)
	if err != nil {
		return types.Value{}, err
	}
	item, err := e.Eval(c.Args[2])
	if err != nil {
		return types.Value{}, err
	}
	stored, ok := carrierElement(item, items.Type().Elem())
	if !ok || !e.valueAssignableTo(item, elem) {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.set element expects %s, got %s", elem, item.TypeName()).WithPath(c.Args[2].Path())
	}
	items.Index(idx).Set(stored)
	null, _ := types.NewNull()
	return null, nil
}

// arraySlice is Go's xs[low:high]: the result shares xs's backing array and
// high may reach into spare capacity.
func arraySlice(e *Executor, c *ast.Call) (types.Value, error) {
	if len(c.Args) != 3 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"arr.slice expects (array, low, high), got %d args", len(c.Args)).WithPath(c.Path())
	}
	arrV, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	items, _, ok := arrayCarrier(arrV)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.slice target must be array, got %s", arrV.TypeName()).WithPath(c.Args[0].Path())
	}
	lowV, err := e.Eval(c.Args[1])
	if err != nil {
		return types.Value{}, err
	}
	highV, err := e.Eval(c.Args[2])
	if err != nil {
		return types.Value{}, err
	}
	if lowV.TypeName() != types.TInt64 || highV.TypeName() != types.TInt64 {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.slice indexes must be int64, got %s and %s", lowV.TypeName(), highV.TypeName()).WithPath(c.Path())
	}
	low := lowV.Go().(int64)
	high := highV.Go().(int64)
	if low < 0 || high < low || high > int64(items.Cap()) {
		return types.Value{}, werr.Newf(werr.CodeRuntime,
			"arr.slice indexes [%d:%d] out of range [0:%d]", low, high, items.Cap()).WithPath(c.Path())
	}
	return types.NewValue(arrV.TypeName(), items.Slice(int(low), int(high)).Interface()), nil
}

// arrayLen is Go's len for arrays and strings (bytes).
func arrayLen(e *Executor, c *ast.Call) (types.Value, error) {
	if len(c.Args) != 1 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"arr.len expects (array), got %d args", len(c.Args)).WithPath(c.Path())
	}
	arrV, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	if s, ok := arrV.Go().(string); ok && arrV.TypeName() == types.TString {
		return types.NewValue(types.TInt64, int64(len(s))), nil
	}
	items, _, ok := arrayCarrier(arrV)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.len target must be array, got %s", arrV.TypeName()).WithPath(c.Args[0].Path())
	}
	return types.NewValue(types.TInt64, int64(items.Len())), nil
}

// arrayCap is Go's cap for arrays: the capacity of the backing slice.
func arrayCap(e *Executor, c *ast.Call) (types.Value, error) {
	if len(c.Args) != 1 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"arr.cap expects (array), got %d args", len(c.Args)).WithPath(c.Path())
	}
	arrV, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	items, _, ok := arrayCarrier(arrV)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.cap target must be array, got %s", arrV.TypeName()).WithPath(c.Args[0].Path())
	}
	return types.NewValue(types.TInt64, int64(items.Cap())), nil
}
