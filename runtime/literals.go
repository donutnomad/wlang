package runtime

import (
	"reflect"

	"github.com/donutnomad/wlang/ast"
	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/types"
)

// evalStructLit instantiates a registered Go struct (LANGUAGE.md §3.9).
// The TypeName must have been bound via Registry.BindType / AutoBindType.
// Field names use Go's case-sensitive field names; unset fields keep their
// Go zero value. The resulting value carries the registered type name.
func (e *Executor) evalStructLit(x *ast.StructLit) (types.Value, error) {
	if e.registry == nil {
		return types.Value{}, werr.New(werr.CodeSymbol,
			"no registry available for struct literal").WithPath(x.Path())
	}
	rt, ok := e.registry.StructType(x.TypeName)
	if !ok {
		return types.Value{}, werr.Newf(werr.CodeSymbol,
			"struct type %q not registered", x.TypeName).WithPath(x.Path())
	}
	if e.budget.MaxObjectKeys > 0 && len(x.Fields) > e.budget.MaxObjectKeys {
		return types.Value{}, werr.Newf(werr.CodeBudget,
			"MaxObjectKeys exceeded (%d)", e.budget.MaxObjectKeys).WithPath(x.Path())
	}
	inst := reflect.New(rt).Elem()
	for _, f := range x.Fields {
		fv, err := e.Eval(f.Expr)
		if err != nil {
			return types.Value{}, err
		}
		fld := inst.FieldByName(f.Name)
		if !fld.IsValid() {
			return types.Value{}, werr.Newf(werr.CodeSymbol,
				"struct %q has no field %q", x.TypeName, f.Name).WithPath(x.Path())
		}
		if !fld.CanSet() {
			return types.Value{}, werr.Newf(werr.CodeSymbol,
				"struct %q field %q is not settable (unexported)",
				x.TypeName, f.Name).WithPath(x.Path())
		}
		gv := reflect.ValueOf(fv.Go())
		if !gv.IsValid() {
			continue
		}
		if !gv.Type().AssignableTo(fld.Type()) {
			if gv.Type().ConvertibleTo(fld.Type()) {
				gv = gv.Convert(fld.Type())
			} else {
				return types.Value{}, werr.Newf(werr.CodeType,
					"struct %q field %q: cannot assign %s to %s",
					x.TypeName, f.Name, fv.TypeName(), fld.Type().String()).
					WithPath(x.Path())
			}
		}
		fld.Set(gv)
	}
	return types.NewValue(x.TypeName, inst.Interface()), nil
}

// evalMapLit constructs a map<K,V> value (LANGUAGE.md §3.8). Keys and values
// are evaluated left-to-right; the resulting Go carrier is a Go map with
// reflect-derived key/value types.
func (e *Executor) evalMapLit(x *ast.MapLit) (types.Value, error) {
	if e.budget.MaxObjectKeys > 0 && len(x.Entries) > e.budget.MaxObjectKeys {
		return types.Value{}, werr.Newf(werr.CodeBudget,
			"MaxObjectKeys exceeded (%d)", e.budget.MaxObjectKeys).WithPath(x.Path())
	}
	kt, err := goTypeForName(x.KeyType)
	if err != nil {
		return types.Value{}, werr.Newf(werr.CodeType,
			"map<%s,%s>: bad key type: %v", x.KeyType, x.ValType, err).
			WithPath(x.Path())
	}
	if !isValidMapKeyType(x.KeyType) {
		return types.Value{}, werr.Newf(werr.CodeType,
			"map key type %q not supported (use string or integer)", x.KeyType).
			WithPath(x.Path())
	}
	vt, err := goTypeForName(x.ValType)
	if err != nil {
		return types.Value{}, werr.Newf(werr.CodeType,
			"map<%s,%s>: bad value type: %v", x.KeyType, x.ValType, err).
			WithPath(x.Path())
	}
	m := reflect.MakeMapWithSize(reflect.MapOf(kt, vt), len(x.Entries))
	for _, ent := range x.Entries {
		kv, err := e.Eval(ent.Key)
		if err != nil {
			return types.Value{}, err
		}
		vv, err := e.Eval(ent.Val)
		if err != nil {
			return types.Value{}, err
		}
		rk, err := coerceToReflect(kv, kt)
		if err != nil {
			return types.Value{}, werr.Newf(werr.CodeType,
				"map key: %v", err).WithPath(x.Path())
		}
		rv, err := coerceToReflect(vv, vt)
		if err != nil {
			return types.Value{}, werr.Newf(werr.CodeType,
				"map value: %v", err).WithPath(x.Path())
		}
		m.SetMapIndex(rk, rv)
	}
	return types.NewValue(types.MapType(x.KeyType, x.ValType), m.Interface()), nil
}

// evalChanLit constructs a chan<T> value (LANGUAGE.md §3.10).
func (e *Executor) evalChanLit(x *ast.ChanLit) (types.Value, error) {
	et, err := goTypeForName(x.ElemType)
	if err != nil {
		return types.Value{}, werr.Newf(werr.CodeType,
			"chan<%s>: bad element type: %v", x.ElemType, err).WithPath(x.Path())
	}
	var bufSize int
	if x.Buffer != nil {
		bv, err := e.Eval(x.Buffer)
		if err != nil {
			return types.Value{}, err
		}
		n, ok := bv.Go().(int64)
		if !ok {
			return types.Value{}, werr.Newf(werr.CodeType,
				"chan buffer must be int64, got %s", bv.TypeName()).WithPath(x.Path())
		}
		if n < 0 {
			return types.Value{}, werr.Newf(werr.CodeRuntime,
				"chan buffer must be non-negative, got %d", n).WithPath(x.Path())
		}
		bufSize = int(n)
	}
	ch := reflect.MakeChan(reflect.ChanOf(reflect.BothDir, et), bufSize)
	return types.NewValue(types.ChanType(x.ElemType), ch.Interface()), nil
}

// goTypeForName maps a wflang primitive type name to a reflect.Type. It only
// handles map key / chan element types; richer composite types are handled
// elsewhere.
func goTypeForName(name string) (reflect.Type, error) {
	switch name {
	case types.TString:
		return reflect.TypeOf(""), nil
	case types.TInt8:
		return reflect.TypeOf(int8(0)), nil
	case types.TInt16:
		return reflect.TypeOf(int16(0)), nil
	case types.TInt32:
		return reflect.TypeOf(int32(0)), nil
	case types.TInt64:
		return reflect.TypeOf(int64(0)), nil
	case types.TUint8:
		return reflect.TypeOf(uint8(0)), nil
	case types.TUint16:
		return reflect.TypeOf(uint16(0)), nil
	case types.TUint32:
		return reflect.TypeOf(uint32(0)), nil
	case types.TUint64:
		return reflect.TypeOf(uint64(0)), nil
	case types.TFloat32:
		return reflect.TypeOf(float32(0)), nil
	case types.TFloat64:
		return reflect.TypeOf(float64(0)), nil
	case types.TBoolean:
		return reflect.TypeOf(false), nil
	case types.TAny, "":
		var anyT any
		return reflect.TypeOf(&anyT).Elem(), nil
	}
	return nil, werr.Newf(werr.CodeType, "unsupported type %q", name)
}

// isValidMapKeyType restricts map keys to strings or fixed-width integers
// (LANGUAGE.md §3.8 design decision).
func isValidMapKeyType(name string) bool {
	switch name {
	case types.TString,
		types.TInt8, types.TInt16, types.TInt32, types.TInt64,
		types.TUint8, types.TUint16, types.TUint32, types.TUint64:
		return true
	}
	return false
}

// ---------- map<K,V> built-ins ----------

func argMap(c *ast.Call, e *Executor) (reflect.Value, string, string, error) {
	if len(c.Args) == 0 {
		return reflect.Value{}, "", "", werr.New(werr.CodeASTShape,
			"map op requires a map receiver").WithPath(c.Path())
	}
	mv, err := e.Eval(c.Args[0])
	if err != nil {
		return reflect.Value{}, "", "", err
	}
	k, v, ok := splitMapTypeName(mv.TypeName())
	if !ok {
		return reflect.Value{}, "", "", werr.Newf(werr.CodeType,
			"map op requires map<K,V> receiver, got %s", mv.TypeName()).WithPath(c.Path())
	}
	rv := reflect.ValueOf(mv.Go())
	if !rv.IsValid() || rv.Kind() != reflect.Map {
		return reflect.Value{}, "", "", werr.Newf(werr.CodeType,
			"map op: receiver carrier is not a Go map (%T)", mv.Go()).WithPath(c.Path())
	}
	return rv, k, v, nil
}

// splitMapTypeName parses "map<K,V>" into K, V; returns false if the name is
// not a map type literal.
func splitMapTypeName(name string) (string, string, bool) {
	const prefix = "map<"
	if len(name) < len(prefix)+3 || name[:len(prefix)] != prefix || name[len(name)-1] != '>' {
		return "", "", false
	}
	inner := name[len(prefix) : len(name)-1]
	for i := 0; i < len(inner); i++ {
		if inner[i] == ',' {
			return inner[:i], inner[i+1:], true
		}
	}
	return "", "", false
}

func mapGet(e *Executor, c *ast.Call) (types.Value, error) {
	rv, _, valType, err := argMap(c, e)
	if err != nil {
		return types.Value{}, err
	}
	if len(c.Args) != 2 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"map.get expects (map, key), got %d args", len(c.Args)).WithPath(c.Path())
	}
	kv, err := e.Eval(c.Args[1])
	if err != nil {
		return types.Value{}, err
	}
	rk, err := coerceToReflect(kv, rv.Type().Key())
	if err != nil {
		return types.Value{}, werr.Newf(werr.CodeType, "map.get key: %v", err).WithPath(c.Path())
	}
	got := rv.MapIndex(rk)
	if !got.IsValid() {
		// Return tuple<V, boolean> with zero/false to mirror Go's `v, ok := m[k]`.
		zero := reflect.Zero(rv.Type().Elem()).Interface()
		return types.NewValue(types.TupleType([]string{valType, types.TBoolean}),
			[]any{zero, false}), nil
	}
	return types.NewValue(types.TupleType([]string{valType, types.TBoolean}),
		[]any{got.Interface(), true}), nil
}

func mapValue(e *Executor, c *ast.Call) (types.Value, error) {
	rv, _, valType, err := argMap(c, e)
	if err != nil {
		return types.Value{}, err
	}
	if len(c.Args) != 2 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"map.value expects (map, key), got %d args", len(c.Args)).WithPath(c.Path())
	}
	kv, err := e.Eval(c.Args[1])
	if err != nil {
		return types.Value{}, err
	}
	rk, err := coerceToReflect(kv, rv.Type().Key())
	if err != nil {
		return types.Value{}, werr.Newf(werr.CodeType, "map.value key: %v", err).WithPath(c.Path())
	}
	got := rv.MapIndex(rk)
	if !got.IsValid() {
		return types.NewValue(valType, reflect.Zero(rv.Type().Elem()).Interface()), nil
	}
	return types.NewValue(valType, got.Interface()), nil
}

func mapSet(e *Executor, c *ast.Call) (types.Value, error) {
	rv, _, _, err := argMap(c, e)
	if err != nil {
		return types.Value{}, err
	}
	if len(c.Args) != 3 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"map.set expects (map, key, value), got %d args", len(c.Args)).WithPath(c.Path())
	}
	kv, err := e.Eval(c.Args[1])
	if err != nil {
		return types.Value{}, err
	}
	vv, err := e.Eval(c.Args[2])
	if err != nil {
		return types.Value{}, err
	}
	rk, err := coerceToReflect(kv, rv.Type().Key())
	if err != nil {
		return types.Value{}, werr.Newf(werr.CodeType, "map.set key: %v", err).WithPath(c.Path())
	}
	rvalue, err := coerceToReflect(vv, rv.Type().Elem())
	if err != nil {
		return types.Value{}, werr.Newf(werr.CodeType, "map.set value: %v", err).WithPath(c.Path())
	}
	rv.SetMapIndex(rk, rvalue)
	null, _ := types.NewNull()
	return null, nil
}

func mapDel(e *Executor, c *ast.Call) (types.Value, error) {
	rv, _, _, err := argMap(c, e)
	if err != nil {
		return types.Value{}, err
	}
	if len(c.Args) != 2 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"map.del expects (map, key), got %d args", len(c.Args)).WithPath(c.Path())
	}
	kv, err := e.Eval(c.Args[1])
	if err != nil {
		return types.Value{}, err
	}
	rk, err := coerceToReflect(kv, rv.Type().Key())
	if err != nil {
		return types.Value{}, werr.Newf(werr.CodeType, "map.del key: %v", err).WithPath(c.Path())
	}
	rv.SetMapIndex(rk, reflect.Value{})
	null, _ := types.NewNull()
	return null, nil
}

func mapHas(e *Executor, c *ast.Call) (types.Value, error) {
	rv, _, _, err := argMap(c, e)
	if err != nil {
		return types.Value{}, err
	}
	if len(c.Args) != 2 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"map.has expects (map, key), got %d args", len(c.Args)).WithPath(c.Path())
	}
	kv, err := e.Eval(c.Args[1])
	if err != nil {
		return types.Value{}, err
	}
	rk, err := coerceToReflect(kv, rv.Type().Key())
	if err != nil {
		return types.Value{}, werr.Newf(werr.CodeType, "map.has key: %v", err).WithPath(c.Path())
	}
	return types.NewValue(types.TBoolean, rv.MapIndex(rk).IsValid()), nil
}

func mapLen(e *Executor, c *ast.Call) (types.Value, error) {
	rv, _, _, err := argMap(c, e)
	if err != nil {
		return types.Value{}, err
	}
	if len(c.Args) != 1 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"map.len expects (map), got %d args", len(c.Args)).WithPath(c.Path())
	}
	return types.NewValue(types.TInt64, int64(rv.Len())), nil
}

func mapKeys(e *Executor, c *ast.Call) (types.Value, error) {
	rv, keyType, _, err := argMap(c, e)
	if err != nil {
		return types.Value{}, err
	}
	if len(c.Args) != 1 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"map.keys expects (map), got %d args", len(c.Args)).WithPath(c.Path())
	}
	keys := rv.MapKeys()
	out := make([]types.Value, 0, len(keys))
	for _, k := range keys {
		out = append(out, types.NewValue(keyType, k.Interface()))
	}
	return types.NewArray(keyType, out)
}

func mapValues(e *Executor, c *ast.Call) (types.Value, error) {
	rv, _, valType, err := argMap(c, e)
	if err != nil {
		return types.Value{}, err
	}
	if len(c.Args) != 1 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"map.values expects (map), got %d args", len(c.Args)).WithPath(c.Path())
	}
	iter := rv.MapRange()
	out := make([]types.Value, 0, rv.Len())
	for iter.Next() {
		out = append(out, types.NewValue(valType, iter.Value().Interface()))
	}
	return types.NewArray(valType, out)
}

// ---------- select statement ----------

// execSelect implements Go-style select (LANGUAGE.md §3.10.2). Each case is
// either a send, a recv (optionally binding the received value and ok flag),
// or a default arm. ctx cancellation is wired into the select so abort
// terminates the call.
func (e *Executor) execSelect(x *ast.SelectStmt) (types.Value, Signal, error) {
	cases := make([]reflect.SelectCase, 0, len(x.Cases)+2)
	// Per-case prepared data; index aligned with cases slice. -1 means "ctx"
	// or "default" sentinel arm.
	type prep struct {
		idx     int  // index in x.Cases, or -1 for ctx/default
		isCtx   bool // ctx done arm
		isDef   bool // default sentinel arm
		elemTyp string
	}
	preps := make([]prep, 0, len(x.Cases)+2)

	for i, sc := range x.Cases {
		chV, err := e.Eval(sc.Chan)
		if err != nil {
			return types.Value{}, sigNone, err
		}
		elem, ok := splitChanTypeName(chV.TypeName())
		if !ok {
			return types.Value{}, sigNone, werr.Newf(werr.CodeType,
				"select case %d: not a chan, got %s", i, chV.TypeName()).
				WithPath(x.Path())
		}
		chRV := reflect.ValueOf(chV.Go())
		if !chRV.IsValid() || chRV.Kind() != reflect.Chan {
			return types.Value{}, sigNone, werr.Newf(werr.CodeType,
				"select case %d: chan carrier invalid (%T)", i, chV.Go()).
				WithPath(x.Path())
		}
		if sc.Kind == ast.SelectCaseSend {
			sendV, err := e.Eval(sc.SendExpr)
			if err != nil {
				return types.Value{}, sigNone, err
			}
			rsend, err := coerceToReflect(sendV, chRV.Type().Elem())
			if err != nil {
				return types.Value{}, sigNone, werr.Newf(werr.CodeType,
					"select case %d send: %v", i, err).WithPath(x.Path())
			}
			cases = append(cases, reflect.SelectCase{
				Dir: reflect.SelectSend, Chan: chRV, Send: rsend,
			})
		} else {
			cases = append(cases, reflect.SelectCase{
				Dir: reflect.SelectRecv, Chan: chRV,
			})
		}
		preps = append(preps, prep{idx: i, elemTyp: elem})
	}

	// ctx.Done arm so cancellation unblocks the select.
	cases = append(cases, reflect.SelectCase{
		Dir: reflect.SelectRecv, Chan: reflect.ValueOf(e.ctx.Done()),
	})
	preps = append(preps, prep{idx: -1, isCtx: true})

	if x.Default != nil {
		// Default arm: signaled via SelectDefault.
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectDefault})
		preps = append(preps, prep{idx: -1, isDef: true})
	}

	chosen, recv, ok := reflect.Select(cases)
	p := preps[chosen]
	if p.isCtx {
		return types.Value{}, sigNone, werr.Newf(werr.CodeRuntime,
			"ctx: %v", e.ctx.Err()).WithPath(x.Path())
	}
	if p.isDef {
		return e.execBlock(x.Default)
	}
	sc := x.Cases[p.idx]
	if sc.Kind == ast.SelectCaseRecv {
		// Bind recv value / ok flag into a fresh child scope for the case body.
		e.scope = e.scope.Push()
		bodyScope := e.scope
		cleanup := func(v types.Value, sig Signal, err error) (types.Value, Signal, error) {
			defErr := e.runDeferred(bodyScope)
			e.scope = bodyScope.Pop()
			if err == nil && defErr != nil {
				return v, sigNone, defErr
			}
			return v, sig, err
		}
		if sc.BindVal != "" && sc.BindVal != "_" {
			var carrier any
			if recv.IsValid() {
				carrier = recv.Interface()
			}
			e.scope.Let(sc.BindVal, types.NewValue(p.elemTyp, carrier), "")
		}
		if sc.BindOK != "" && sc.BindOK != "_" {
			e.scope.Let(sc.BindOK, types.NewValue(types.TBoolean, ok), "")
		}
		v, sig, err := runStatements(e, sc.Do)
		return cleanup(v, sig, err)
	}
	return e.execBlock(sc.Do)
}

// ---------- chan<T> built-ins ----------

func argChan(c *ast.Call, e *Executor) (reflect.Value, string, error) {
	if len(c.Args) == 0 {
		return reflect.Value{}, "", werr.New(werr.CodeASTShape,
			"chan op requires a chan receiver").WithPath(c.Path())
	}
	cv, err := e.Eval(c.Args[0])
	if err != nil {
		return reflect.Value{}, "", err
	}
	elem, ok := splitChanTypeName(cv.TypeName())
	if !ok {
		return reflect.Value{}, "", werr.Newf(werr.CodeType,
			"chan op requires chan<T> receiver, got %s", cv.TypeName()).WithPath(c.Path())
	}
	rv := reflect.ValueOf(cv.Go())
	if !rv.IsValid() || rv.Kind() != reflect.Chan {
		return reflect.Value{}, "", werr.Newf(werr.CodeType,
			"chan op: carrier is not a Go chan (%T)", cv.Go()).WithPath(c.Path())
	}
	return rv, elem, nil
}

func splitChanTypeName(name string) (string, bool) {
	const prefix = "chan<"
	if len(name) < len(prefix)+2 || name[:len(prefix)] != prefix || name[len(name)-1] != '>' {
		return "", false
	}
	return name[len(prefix) : len(name)-1], true
}

func chanSend(e *Executor, c *ast.Call) (types.Value, error) {
	rv, _, err := argChan(c, e)
	if err != nil {
		return types.Value{}, err
	}
	if len(c.Args) != 2 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"ch.send expects (chan, value), got %d args", len(c.Args)).WithPath(c.Path())
	}
	vv, err := e.Eval(c.Args[1])
	if err != nil {
		return types.Value{}, err
	}
	rvalue, err := coerceToReflect(vv, rv.Type().Elem())
	if err != nil {
		return types.Value{}, werr.Newf(werr.CodeType, "ch.send: %v", err).WithPath(c.Path())
	}
	// Use a cancellable select so an aborted ctx unblocks the send.
	ctxDone := reflect.ValueOf(e.ctx.Done())
	chosen, _, _ := reflect.Select([]reflect.SelectCase{
		{Dir: reflect.SelectSend, Chan: rv, Send: rvalue},
		{Dir: reflect.SelectRecv, Chan: ctxDone},
	})
	if chosen == 1 {
		return types.Value{}, werr.Newf(werr.CodeRuntime,
			"ctx: %v", e.ctx.Err()).WithPath(c.Path())
	}
	null, _ := types.NewNull()
	return null, nil
}

func chanRecv(e *Executor, c *ast.Call) (types.Value, error) {
	rv, elem, err := argChan(c, e)
	if err != nil {
		return types.Value{}, err
	}
	if len(c.Args) != 1 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"ch.recv expects (chan), got %d args", len(c.Args)).WithPath(c.Path())
	}
	ctxDone := reflect.ValueOf(e.ctx.Done())
	chosen, recv, ok := reflect.Select([]reflect.SelectCase{
		{Dir: reflect.SelectRecv, Chan: rv},
		{Dir: reflect.SelectRecv, Chan: ctxDone},
	})
	if chosen == 1 {
		return types.Value{}, werr.Newf(werr.CodeRuntime,
			"ctx: %v", e.ctx.Err()).WithPath(c.Path())
	}
	var carrier any
	if recv.IsValid() {
		carrier = recv.Interface()
	}
	// Returns tuple<T, boolean> mirroring Go's `v, ok := <-ch`.
	return types.NewValue(types.TupleType([]string{elem, types.TBoolean}),
		[]any{carrier, ok}), nil
}

func chanClose(e *Executor, c *ast.Call) (types.Value, error) {
	rv, _, err := argChan(c, e)
	if err != nil {
		return types.Value{}, err
	}
	if len(c.Args) != 1 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"ch.close expects (chan), got %d args", len(c.Args)).WithPath(c.Path())
	}
	rv.Close()
	null, _ := types.NewNull()
	return null, nil
}

func chanLen(e *Executor, c *ast.Call) (types.Value, error) {
	rv, _, err := argChan(c, e)
	if err != nil {
		return types.Value{}, err
	}
	if len(c.Args) != 1 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"ch.len expects (chan), got %d args", len(c.Args)).WithPath(c.Path())
	}
	return types.NewValue(types.TInt64, int64(rv.Len())), nil
}

func chanCap(e *Executor, c *ast.Call) (types.Value, error) {
	rv, _, err := argChan(c, e)
	if err != nil {
		return types.Value{}, err
	}
	if len(c.Args) != 1 {
		return types.Value{}, werr.Newf(werr.CodeASTShape,
			"ch.cap expects (chan), got %d args", len(c.Args)).WithPath(c.Path())
	}
	return types.NewValue(types.TInt64, int64(rv.Cap())), nil
}

// coerceToReflect converts a wflang Value to a reflect.Value of the given Go
// type. Assignable values pass through; convertible values are converted.
func coerceToReflect(v types.Value, target reflect.Type) (reflect.Value, error) {
	g := v.Go()
	if g == nil {
		return reflect.Zero(target), nil
	}
	rv := reflect.ValueOf(g)
	if rv.Type().AssignableTo(target) {
		return rv, nil
	}
	if rv.Type().ConvertibleTo(target) {
		return rv.Convert(target), nil
	}
	return reflect.Value{}, werr.Newf(werr.CodeType,
		"cannot assign %s to %s", v.TypeName(), target.String())
}
