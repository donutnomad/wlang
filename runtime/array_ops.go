package runtime

import (
	"strings"

	"github.com/donutnomad/wlang/ast"
	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/types"
)

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
	items, elem, ok := extractArrayItems(arrV)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.push target must be array, got %s", arrV.TypeName()).WithPath(c.Args[0].Path())
	}
	if e.budget.MaxArrayLength > 0 && len(items)+1 > e.budget.MaxArrayLength {
		return types.Value{}, werr.Newf(werr.CodeBudget,
			"MaxArrayLength exceeded (%d)", e.budget.MaxArrayLength).WithPath(c.Path())
	}
	item, err := e.Eval(c.Args[1])
	if err != nil {
		return types.Value{}, err
	}
	if !valueAssignableTo(item, elem) {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.push element expects %s, got %s", elem, item.TypeName()).WithPath(c.Args[1].Path())
	}
	next := make([]types.Value, 0, len(items)+1)
	for _, raw := range items {
		next = append(next, types.NewValue(elem, raw))
	}
	next = append(next, item)
	newArr, err := types.NewArray(elem, next)
	if err != nil {
		if le, ok := err.(*werr.LangError); ok {
			return types.Value{}, le.WithPath(c.Path())
		}
		return types.Value{}, err
	}
	if err := e.scope.Set(target.Name, newArr); err != nil {
		if le, ok := err.(*werr.LangError); ok {
			return types.Value{}, le.WithPath(c.Args[0].Path())
		}
		return types.Value{}, err
	}
	null, _ := types.NewNull()
	return null, nil
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
	items, elem, ok := extractArrayItems(arrV)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.get target must be array, got %s", arrV.TypeName()).WithPath(c.Args[0].Path())
	}
	idxV, err := e.Eval(c.Args[1])
	if err != nil {
		return types.Value{}, err
	}
	if idxV.TypeName() != types.TInt64 {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.get index must be int64, got %s", idxV.TypeName()).WithPath(c.Args[1].Path())
	}
	idx := idxV.Go().(int64)
	if idx < 0 || idx >= int64(len(items)) {
		return types.Value{}, werr.Newf(werr.CodeRuntime,
			"arr.get index %d out of range [0,%d)", idx, len(items)).WithPath(c.Args[1].Path())
	}
	return types.NewValue(elem, items[idx]), nil
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
	items, elem, ok := extractArrayItems(arrV)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.set target must be array, got %s", arrV.TypeName()).WithPath(c.Args[0].Path())
	}
	idxV, err := e.Eval(c.Args[1])
	if err != nil {
		return types.Value{}, err
	}
	if idxV.TypeName() != types.TInt64 {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.set index must be int64, got %s", idxV.TypeName()).WithPath(c.Args[1].Path())
	}
	idx := idxV.Go().(int64)
	if idx < 0 || idx >= int64(len(items)) {
		return types.Value{}, werr.Newf(werr.CodeRuntime,
			"arr.set index %d out of range [0,%d)", idx, len(items)).WithPath(c.Args[1].Path())
	}
	item, err := e.Eval(c.Args[2])
	if err != nil {
		return types.Value{}, err
	}
	if !valueAssignableTo(item, elem) {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.set element expects %s, got %s", elem, item.TypeName()).WithPath(c.Args[2].Path())
	}
	next := make([]types.Value, 0, len(items))
	for _, raw := range items {
		next = append(next, types.NewValue(elem, raw))
	}
	next[idx] = item
	newArr, err := types.NewArray(elem, next)
	if err != nil {
		if le, ok := err.(*werr.LangError); ok {
			return types.Value{}, le.WithPath(c.Path())
		}
		return types.Value{}, err
	}
	if err := e.scope.Set(target.Name, newArr); err != nil {
		if le, ok := err.(*werr.LangError); ok {
			return types.Value{}, le.WithPath(c.Args[0].Path())
		}
		return types.Value{}, err
	}
	null, _ := types.NewNull()
	return null, nil
}

func arraySlice(e *Executor, c *ast.Call) (types.Value, error) {
	if len(c.Args) != 3 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"arr.slice expects (array, low, high), got %d args", len(c.Args)).WithPath(c.Path())
	}
	arrV, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	items, elem, ok := extractArrayItems(arrV)
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
	if low < 0 || high < low || high > int64(len(items)) {
		return types.Value{}, werr.Newf(werr.CodeRuntime,
			"arr.slice indexes [%d:%d] out of range [0:%d]", low, high, len(items)).WithPath(c.Path())
	}
	next := make([]types.Value, 0, high-low)
	for _, raw := range items[low:high] {
		next = append(next, types.NewValue(elem, raw))
	}
	return types.NewArray(elem, next)
}

func arrayLen(e *Executor, c *ast.Call) (types.Value, error) {
	if len(c.Args) != 1 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"arr.len expects (array), got %d args", len(c.Args)).WithPath(c.Path())
	}
	arrV, err := e.Eval(c.Args[0])
	if err != nil {
		return types.Value{}, err
	}
	items, _, ok := extractArrayItems(arrV)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeType,
			"arr.len target must be array, got %s", arrV.TypeName()).WithPath(c.Args[0].Path())
	}
	return types.NewValue(types.TInt64, int64(len(items))), nil
}
