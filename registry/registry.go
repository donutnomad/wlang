// Package registry is the wflang host registry (LANGUAGE.md §5.3/§7.4).
package registry

import (
	"context"
	"reflect"
	"sync"

	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/types"
)

// ParamSpec describes one parameter of a host function.
type ParamSpec struct {
	Name string
	Type string
}

// FuncSpec describes a host-callable function entry.
type FuncSpec struct {
	GoName        string
	Params        []ParamSpec
	ReturnTypes   []string
	Pure          bool
	Deterministic bool
	Capabilities  []string
	Impl          any
}

// PackageSpec describes a package of functions.
type PackageSpec struct {
	Functions []FuncSpec
}

// Registry owns host bindings.
type Registry struct {
	mu       sync.RWMutex
	packages map[string]*boundPackage
	types    map[string]*boundType
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{
		packages: map[string]*boundPackage{},
		types:    map[string]*boundType{},
	}
}

// boundFunc captures a host function via reflect.
type boundFunc struct {
	name       string
	impl       reflect.Value
	inTypes    []reflect.Type
	outTypes   []reflect.Type
	wantsCtx   bool
	variadic   bool
	pure       bool
	capability []string
}

type boundPackage struct {
	name  string
	funcs map[string]*boundFunc
}

type boundType struct {
	name      string
	methods   map[string]*boundFunc
	overloads map[string]*overloadSet
}

// GoMethodOverload names one Go method participating in an overload set.
type GoMethodOverload struct {
	GoMethod string
}

// BindMethodOverloads maps an operator to multiple Go methods on a type
// (§2.5 overload table).
func (r *Registry) BindMethodOverloads(typeName, operator string,
	overloads []GoMethodOverload) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	bt := r.types[typeName]
	if bt == nil {
		return werr.Newf(werr.CodeSymbol, "type %q has no bound methods", typeName)
	}
	set := &overloadSet{name: operator}
	for _, ov := range overloads {
		fn, ok := bt.methods[ov.GoMethod]
		if !ok {
			return werr.Newf(werr.CodeSymbol,
				"method %q not bound on %q", ov.GoMethod, typeName)
		}
		set.cands = append(set.cands, fn)
	}
	if bt.overloads == nil {
		bt.overloads = map[string]*overloadSet{}
	}
	bt.overloads[operator] = set
	return nil
}

// overloadSet groups candidate functions for an operator.
type overloadSet struct {
	name  string
	cands []*boundFunc
}

// pickOverload selects the best candidate by the §2.5 priority table.
// Returns E_AMBIGUOUS_OVERLOAD when two candidates tie at the top score.
func pickOverload(set *overloadSet, args []types.Value) (*boundFunc, *werr.LangError) {
	type scored struct {
		fn    *boundFunc
		score int
	}
	var best []scored
	bestScore := -1
	for _, c := range set.cands {
		s, ok := scoreCandidate(c, args)
		if !ok {
			continue
		}
		if s > bestScore {
			bestScore = s
			best = []scored{{c, s}}
		} else if s == bestScore {
			best = append(best, scored{c, s})
		}
	}
	if len(best) == 0 {
		return nil, werr.Newf(werr.CodeOperatorNotFound,
			"no matching overload for %q", set.name)
	}
	if len(best) > 1 {
		return nil, werr.Newf(werr.CodeAmbiguousOverload,
			"ambiguous overload for %q (%d candidates at score %d)",
			set.name, len(best), bestScore)
	}
	return best[0].fn, nil
}

// scoreCandidate returns the worst-case score of matching args to candidate
// signature, or (0, false) when unfit.
func scoreCandidate(bf *boundFunc, args []types.Value) (int, bool) {
	expected := len(bf.inTypes)
	if bf.variadic {
		if len(args) < expected-1 {
			return 0, false
		}
	} else if len(args) != expected {
		return 0, false
	}
	worst := 100
	for i, a := range args {
		var tgt reflect.Type
		if bf.variadic && i >= expected-1 {
			tgt = bf.inTypes[expected-1].Elem()
		} else {
			tgt = bf.inTypes[i]
		}
		s, ok := matchScore(a, tgt)
		if !ok {
			return 0, false
		}
		if s < worst {
			worst = s
		}
	}
	return worst, true
}

// matchScore returns the match priority between an arg and a target Go type.
// 100 exact, 80 numeric promotion, 60 precision promotion, 40 convertible, 10 any.
func matchScore(a types.Value, tgt reflect.Type) (int, bool) {
	// any target: priority 10.
	if tgt.Kind() == reflect.Interface && tgt.NumMethod() == 0 {
		return 10, true
	}
	gv := reflect.ValueOf(a.Go())
	if !gv.IsValid() {
		// null only fits pointer/interface/slice/map as zero value.
		switch tgt.Kind() {
		case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
			return 60, true
		}
		return 0, false
	}
	gt := gv.Type()
	if gt == tgt {
		return 100, true
	}
	// Numeric widening
	if isIntKind(gt.Kind()) && isIntKind(tgt.Kind()) && tgt.Size() >= gt.Size() &&
		sameSignedness(gt.Kind(), tgt.Kind()) {
		return 80, true
	}
	if isFloatKind(gt.Kind()) && isFloatKind(tgt.Kind()) && tgt.Size() >= gt.Size() {
		return 80, true
	}
	// int -> float widening
	if isIntKind(gt.Kind()) && isFloatKind(tgt.Kind()) {
		return 60, true
	}
	// Assignable/convertible
	if gt.AssignableTo(tgt) {
		return 100, true
	}
	if gt.ConvertibleTo(tgt) {
		return 40, true
	}
	return 0, false
}

func isIntKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
}

func isFloatKind(k reflect.Kind) bool {
	return k == reflect.Float32 || reflect.Float64 == k
}

func sameSignedness(a, b reflect.Kind) bool {
	isSigned := func(k reflect.Kind) bool {
		switch k {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return true
		}
		return false
	}
	return isSigned(a) == isSigned(b)
}

// PackageNames returns the set of package names as a map (string -> any marker).
// Used by the runtime to resolve {"pkg":"..."} references.
func (r *Registry) PackageNames() map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]any, len(r.packages))
	for k := range r.packages {
		out[k] = struct{}{}
	}
	return out
}

// BindGoPackage registers a package with a spec.
func (r *Registry) BindGoPackage(name string, spec PackageSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.packages[name]; ok {
		return werr.Newf(werr.CodeASTShape, "package %q already registered", name)
	}
	pkg := &boundPackage{name: name, funcs: map[string]*boundFunc{}}
	for _, fs := range spec.Functions {
		bf, err := bindOne(fs.GoName, fs.Impl, fs.Pure, fs.Capabilities)
		if err != nil {
			return err
		}
		pkg.funcs[fs.GoName] = bf
	}
	r.packages[name] = pkg
	return nil
}

// BindGoPackageAuto reflects over a Go struct and binds its exported methods.
func (r *Registry) BindGoPackageAuto(name string, target any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.packages[name]; ok {
		return werr.Newf(werr.CodeASTShape, "package %q already registered", name)
	}
	pkg := &boundPackage{name: name, funcs: map[string]*boundFunc{}}
	rv := reflect.ValueOf(target)
	rt := rv.Type()
	for i := 0; i < rv.NumMethod(); i++ {
		m := rt.Method(i)
		if !m.IsExported() {
			continue
		}
		impl := rv.Method(i)
		bf, err := bindOne(m.Name, impl.Interface(), false, nil)
		if err != nil {
			return err
		}
		pkg.funcs[m.Name] = bf
	}
	r.packages[name] = pkg
	return nil
}

// AutoBindType registers a Go type's exported methods as callable on values of that type.
func (r *Registry) AutoBindType(v any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt := reflect.TypeOf(v)
	// Store methods keyed by the type's AutoHostTypeName.
	name := types.AutoHostTypeName(rt)
	bt := r.types[name]
	if bt == nil {
		bt = &boundType{name: name, methods: map[string]*boundFunc{}}
		r.types[name] = bt
	}
	for i := 0; i < rt.NumMethod(); i++ {
		m := rt.Method(i)
		if !m.IsExported() {
			continue
		}
		bf, err := bindMethod(m)
		if err != nil {
			return err
		}
		bt.methods[m.Name] = bf
	}
	return nil
}

// bindOne validates impl and produces a boundFunc.
func bindOne(name string, impl any, pure bool, caps []string) (*boundFunc, error) {
	if impl == nil {
		return nil, werr.Newf(werr.CodeASTShape, "nil impl for %q", name)
	}
	iv := reflect.ValueOf(impl)
	it := iv.Type()
	if it.Kind() != reflect.Func {
		return nil, werr.Newf(werr.CodeASTShape, "%q impl must be func, got %s", name, it.Kind())
	}
	bf := &boundFunc{
		name:       name,
		impl:       iv,
		variadic:   it.IsVariadic(),
		pure:       pure,
		capability: caps,
	}
	ctxT := reflect.TypeOf((*context.Context)(nil)).Elem()
	for i := 0; i < it.NumIn(); i++ {
		p := it.In(i)
		if i == 0 && p.Implements(ctxT) {
			bf.wantsCtx = true
			continue
		}
		bf.inTypes = append(bf.inTypes, p)
	}
	for i := 0; i < it.NumOut(); i++ {
		bf.outTypes = append(bf.outTypes, it.Out(i))
	}
	return bf, nil
}

// bindMethod binds a reflect.Method.
func bindMethod(m reflect.Method) (*boundFunc, error) {
	iv := m.Func
	it := iv.Type()
	bf := &boundFunc{name: m.Name, impl: iv, variadic: it.IsVariadic()}
	// For method values via reflect.Method, In(0) is receiver; skip it.
	ctxT := reflect.TypeOf((*context.Context)(nil)).Elem()
	for i := 0; i < it.NumIn(); i++ {
		if i == 0 {
			continue
		}
		p := it.In(i)
		if i == 1 && p.Implements(ctxT) {
			bf.wantsCtx = true
			continue
		}
		bf.inTypes = append(bf.inTypes, p)
	}
	for i := 0; i < it.NumOut(); i++ {
		bf.outTypes = append(bf.outTypes, it.Out(i))
	}
	return bf, nil
}

// isPkgReceiver tests whether v is a package marker value.
func isPkgReceiver(v types.Value) (string, bool) {
	if v.TypeName() != "__pkg__" {
		return "", false
	}
	type pkgRef struct{ Name string }
	if pr, ok := v.Go().(pkgRef); ok {
		return pr.Name, true
	}
	// Fallback via reflect (handles unexported type identity cross-package).
	rv := reflect.ValueOf(v.Go())
	if rv.Kind() == reflect.Struct {
		f := rv.FieldByName("Name")
		if f.IsValid() && f.Kind() == reflect.String {
			return f.String(), true
		}
	}
	return "", false
}

// Invoke dispatches op on the receiver. It implements runtime.HostRegistry.
func (r *Registry) Invoke(ctx context.Context, op string, recv types.Value,
	args []types.Value, path string) (types.Value, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Package receiver path.
	if pkgName, ok := isPkgReceiver(recv); ok {
		pkg, ok := r.packages[pkgName]
		if !ok {
			return types.Value{}, werr.Newf(werr.CodeSymbol,
				"package %q not found", pkgName).WithPath(path)
		}
		fn, ok := pkg.funcs[op]
		if !ok {
			return types.Value{}, werr.Newf(werr.CodeSymbol,
				"function %q not found in package %q", op, pkgName).WithPath(path)
		}
		return fn.call(ctx, nil, args, path)
	}
	// Type method path.
	typName := recv.TypeName()
	// Builtin error.Error() method (§2.5 / §9.1).
	if typName == types.TError && op == "Error" && len(args) == 0 {
		if e, ok := recv.Go().(error); ok && e != nil {
			return types.NewValue(types.TString, e.Error()), nil
		}
		return types.NewValue(types.TString, ""), nil
	}
	if bt, ok := r.types[typName]; ok {
		// Overload dispatch first (§2.5).
		if set, ok := bt.overloads[op]; ok {
			fn, err := pickOverload(set, args)
			if err != nil {
				return types.Value{}, err.WithPath(path)
			}
			if recv.IsNull() {
				return types.Value{}, werr.Newf(werr.CodeNilReceiver,
					"receiver is null for method %q on %s", op, typName).WithPath(path)
			}
			return fn.call(ctx, &recv, args, path)
		}
		if fn, ok := bt.methods[op]; ok {
			// Nil receiver check per §9.1.2.
			if recv.IsNull() {
				return types.Value{}, werr.Newf(werr.CodeNilReceiver,
					"receiver is null for method %q on %s", op, typName).WithPath(path)
			}
			return fn.call(ctx, &recv, args, path)
		}
	}
	// Null receiver with unknown type: if any bound type has op as a method,
	// treat it as nil-receiver error instead of operator-not-found (§9.1.2).
	if recv.IsNull() {
		for _, bt := range r.types {
			if _, ok := bt.methods[op]; ok {
				return types.Value{}, werr.Newf(werr.CodeNilReceiver,
					"receiver is null for method %q", op).WithPath(path)
			}
		}
	}
	return types.Value{}, werr.Newf(werr.CodeOperatorNotFound,
		"no binding for operator %q on %s", op, typName).WithPath(path)
}

// call invokes the bound function via reflect, handling ctx and variadic.
func (bf *boundFunc) call(ctx context.Context, recv *types.Value,
	args []types.Value, path string) (types.Value, error) {
	in := []reflect.Value{}
	if recv != nil {
		in = append(in, reflect.ValueOf(recv.Go()))
	}
	if bf.wantsCtx {
		in = append(in, reflect.ValueOf(ctx))
	}
	// Expect len(args) == len(bf.inTypes) (for non-variadic), or >= len-1 for variadic.
	expected := len(bf.inTypes)
	if bf.variadic {
		if len(args) < expected-1 {
			return types.Value{}, werr.Newf(werr.CodeType,
				"%q expects >= %d args, got %d", bf.name, expected-1, len(args)).WithPath(path)
		}
	} else if len(args) != expected {
		return types.Value{}, werr.Newf(werr.CodeType,
			"%q expects %d args, got %d", bf.name, expected, len(args)).WithPath(path)
	}

	for i, a := range args {
		var target reflect.Type
		if bf.variadic && i >= expected-1 {
			target = bf.inTypes[expected-1].Elem()
		} else {
			target = bf.inTypes[i]
		}
		rv, err := coerce(a, target)
		if err != nil {
			return types.Value{}, werr.Newf(werr.CodeType,
				"%q arg %d: %v", bf.name, i, err).WithPath(path)
		}
		in = append(in, rv)
	}

	out, panicErr := safeCall(bf.impl, in, bf.name, path)
	if panicErr != nil {
		return types.Value{}, panicErr
	}
	// Expect result shape (T, error) or (T) or () or (error).
	var resVal reflect.Value
	var resErr error
	switch len(out) {
	case 0:
		v, _ := types.NewNull()
		return v, nil
	case 1:
		if isErrorType(bf.outTypes[0]) {
			if !out[0].IsNil() {
				cause, _ := out[0].Interface().(error)
				le := werr.Newf(werr.CodeHost,
					"%q: %v", bf.name, out[0].Interface()).WithPath(path)
				le.Cause = cause
				return types.Value{}, le
			}
			v, _ := types.NewNull()
			return v, nil
		}
		resVal = out[0]
	case 2:
		resVal = out[0]
		if !out[1].IsNil() {
			resErr, _ = out[1].Interface().(error)
		}
	default:
		return types.Value{}, werr.Newf(werr.CodeHost,
			"%q unsupported return arity %d", bf.name, len(out)).WithPath(path)
	}
	if resErr != nil {
		le := werr.Newf(werr.CodeHost,
			"%q: %v", bf.name, resErr).WithPath(path)
		le.Cause = resErr
		return types.Value{}, le
	}
	return wrapResult(resVal), nil
}

// isErrorType is true for types implementing error.
func isErrorType(t reflect.Type) bool {
	return t.Implements(reflect.TypeOf((*error)(nil)).Elem())
}

// wrapResult produces a typed Value from a reflect.Value result.
func wrapResult(v reflect.Value) types.Value {
	if !v.IsValid() {
		nv, _ := types.NewNull()
		return nv
	}
	g := v.Interface()
	if g == nil {
		nv, _ := types.NewNull()
		return nv
	}
	switch x := g.(type) {
	case types.Value:
		return x
	case int:
		return types.NewValue(types.TInt64, int64(x))
	case int8:
		return types.NewValue(types.TInt8, x)
	case int16:
		return types.NewValue(types.TInt16, x)
	case int32:
		return types.NewValue(types.TInt32, x)
	case int64:
		return types.NewValue(types.TInt64, x)
	case uint8:
		return types.NewValue(types.TUint8, x)
	case uint16:
		return types.NewValue(types.TUint16, x)
	case uint32:
		return types.NewValue(types.TUint32, x)
	case uint64:
		return types.NewValue(types.TUint64, x)
	case float32:
		return types.NewValue(types.TFloat32, x)
	case float64:
		return types.NewValue(types.TFloat64, x)
	case bool:
		return types.NewValue(types.TBoolean, x)
	case string:
		return types.NewValue(types.TString, x)
	}
	// Auto host type.
	return types.NewValue(types.AutoHostTypeName(v.Type()), g)
}

// coerce converts a typed Value into a reflect.Value of target type.
func coerce(v types.Value, target reflect.Type) (reflect.Value, error) {
	// Null -> zero of target if allowed.
	if v.IsNull() {
		if target.Kind() == reflect.Pointer || target.Kind() == reflect.Interface ||
			target.Kind() == reflect.Slice || target.Kind() == reflect.Map {
			return reflect.Zero(target), nil
		}
		return reflect.Value{}, werr.Newf(werr.CodeType,
			"cannot pass null as %s", target)
	}
	gv := reflect.ValueOf(v.Go())
	if gv.Type().AssignableTo(target) {
		return gv, nil
	}
	if gv.Type().ConvertibleTo(target) {
		return gv.Convert(target), nil
	}
	return reflect.Value{}, werr.Newf(werr.CodeType,
		"cannot pass %s as %s", v.TypeName(), target)
}

// safeCall invokes impl.Call(in) with a recover shield so that a host panic is
// converted to an E_PANIC LangError (§10.2). Returning (nil, err) on panic.
func safeCall(impl reflect.Value, in []reflect.Value, name, path string) (out []reflect.Value, panicErr error) {
	defer func() {
		if r := recover(); r != nil {
			le := werr.Newf(werr.CodePanic, "%q panicked: %v", name, r).WithPath(path)
			panicErr = le
		}
	}()
	out = impl.Call(in)
	return out, nil
}
