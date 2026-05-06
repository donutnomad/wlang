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

// Env is the runtime environment injected into host functions whose first
// (or first-after-ctx) parameter is of this type (LANGUAGE.md §5.3). The
// language argument list does NOT include env — the runtime fills it in.
type Env struct {
	// Caps is the set of granted capability names for the current execution.
	Caps map[string]bool
	// Path is the JSON Pointer of the call site, useful for diagnostics.
	Path string
}

var envType = reflect.TypeOf((*Env)(nil)).Elem()

// FuncSpec describes a host-callable function entry.
type FuncSpec struct {
	GoName        string
	Params        []ParamSpec
	ReturnTypes   []string
	Pure          bool
	Deterministic bool
	Capabilities  []string
	// YieldAware marks the function as one that may return a YieldError; the
	// compiler propagates this to CallPlan.YieldAware (LANGUAGE.md §7.7 / §8.1).
	YieldAware bool
	// Deprecated, when non-empty, signals the lint pass to emit L_DEPRECATED
	// (LANGUAGE.md §13.2). The string is the migration guidance shown to users.
	Deprecated string
	Impl       any
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
	wantsEnv   bool
	variadic   bool
	pure       bool
	capability []string
	yieldAware bool
	deprecated string
}

type boundPackage struct {
	name  string
	funcs map[string]*boundFunc
}

type boundType struct {
	name      string
	methods   map[string]*boundFunc
	overloads map[string]*overloadSet
	// excluded records exported method names that were filtered out by
	// BindOptions Include/Exclude. Dispatch consults this set to surface a
	// targeted E_SYMBOL instead of E_OPERATOR_NOT_FOUND (TC-361).
	excluded map[string]bool
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
	// Constructor coercion: arg is string and target has a registered literal
	// constructor producing the target type (TC-648).
	if gt.Kind() == reflect.String {
		if _, ok := types.LookupConstructorForGoType(tgt); ok {
			return 30, true
		}
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

// HasOverloads reports whether op has more than one candidate registered
// (across all bound types and packages). Used by the compile-time type
// checker to flag any-typed arguments in overloaded calls (TC-653).
func (r *Registry) HasOverloads(op string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, pkg := range r.packages {
		if _, ok := pkg.funcs[op]; ok {
			count++
			if count > 1 {
				return true
			}
		}
	}
	for _, bt := range r.types {
		if set, ok := bt.overloads[op]; ok && len(set.cands) > 1 {
			return true
		}
		if _, ok := bt.methods[op]; ok {
			count++
			if count > 1 {
				return true
			}
		}
	}
	return false
}

// HasOperator reports whether op is registered anywhere in this Registry.
// It returns true if op matches a function in any package, a method on any
// bound type, or an overload entry. Used by the builder to validate symbols
// before they reach CompileJSON (LANGUAGE.md §5.6 / TC-464).
func (r *Registry) HasOperator(op string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, pkg := range r.packages {
		if _, ok := pkg.funcs[op]; ok {
			return true
		}
	}
	for _, bt := range r.types {
		if _, ok := bt.methods[op]; ok {
			return true
		}
		if _, ok := bt.overloads[op]; ok {
			return true
		}
	}
	return false
}

// HasPackageFunc reports whether package pkg defines function op.
func (r *Registry) HasPackageFunc(pkg, op string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bp, ok := r.packages[pkg]
	if !ok {
		return false
	}
	_, ok = bp.funcs[op]
	return ok
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
		bf.yieldAware = fs.YieldAware
		bf.deprecated = fs.Deprecated
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

// BindOptions is the public knob set for BindType (LANGUAGE.md §4.5).
type BindOptions struct {
	// Constructor decodes a typed-literal raw string into the bound Go type.
	// Must be a func(string) (T, error) where T matches the Go type. Optional;
	// only required when the type appears in JSON typed literals (TC-360).
	Constructor any
	// Include filters method binding to this whitelist (TC-361).
	Include []string
	// Exclude filters method binding by name (TC-361). When both are set,
	// Include applies first, then Exclude.
	Exclude []string
	// Capabilities is the type-level required-capability set; merged into
	// each bound method's capability list (TC-363).
	Capabilities []string
	// Deprecated marks every bound method as deprecated with this guidance.
	Deprecated string
	// MethodOverrides allows fine-grained per-method overrides.
	MethodOverrides map[string]MethodOptions
}

// MethodOptions specifies per-method overrides for BindType.
type MethodOptions struct {
	Capabilities []string
	Deprecated   string
}

// BindType is the named-binding API specified in LANGUAGE.md §4.5: binds a Go
// type under both its short language-level name and its auto host-type name,
// optionally registering a typed-literal Constructor and applying include/
// exclude filters and metadata. Unlike AutoBindType, BindType collects all
// signature-mapping errors and returns them as a single *errors.List
// (TC-362). Existing AutoBindType remains for backward compatibility.
func (r *Registry) BindType(name string, rt reflect.Type, opts BindOptions) error {
	if rt == nil {
		return werr.New(werr.CodeASTShape, "BindType: nil reflect.Type")
	}
	if name == "" {
		return werr.New(werr.CodeASTShape, "BindType: empty type name")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	includeSet := map[string]bool{}
	for _, n := range opts.Include {
		includeSet[n] = true
	}
	excludeSet := map[string]bool{}
	for _, n := range opts.Exclude {
		excludeSet[n] = true
	}

	bt := r.types[name]
	if bt == nil {
		bt = &boundType{name: name, methods: map[string]*boundFunc{}}
		r.types[name] = bt
	}
	// Also register under the auto host-type name so Go-derived values still
	// resolve methods by either name (LANGUAGE.md §2.1 / §4.2.2).
	autoName := types.AutoHostTypeName(rt)
	if _, ok := r.types[autoName]; !ok {
		r.types[autoName] = bt
	}

	var diags werr.List
	for i := 0; i < rt.NumMethod(); i++ {
		m := rt.Method(i)
		if !m.IsExported() {
			continue
		}
		if len(includeSet) > 0 && !includeSet[m.Name] {
			if bt.excluded == nil {
				bt.excluded = map[string]bool{}
			}
			bt.excluded[m.Name] = true
			continue
		}
		if excludeSet[m.Name] {
			if bt.excluded == nil {
				bt.excluded = map[string]bool{}
			}
			bt.excluded[m.Name] = true
			continue
		}
		bf, err := bindMethod(m)
		if err != nil {
			if le, ok := err.(*werr.LangError); ok {
				diags.Add(le)
			} else {
				diags.Add(werr.Newf(werr.CodeASTShape,
					"%s.%s: %v", name, m.Name, err))
			}
			continue
		}
		bf.capability = mergeCaps(opts.Capabilities, bf.capability)
		if opts.Deprecated != "" && bf.deprecated == "" {
			bf.deprecated = opts.Deprecated
		}
		if mo, ok := opts.MethodOverrides[m.Name]; ok {
			bf.capability = mergeCaps(mo.Capabilities, bf.capability)
			if mo.Deprecated != "" {
				bf.deprecated = mo.Deprecated
			}
		}
		bt.methods[m.Name] = bf
	}

	if opts.Constructor != nil {
		if err := registerLiteralCtor(name, opts.Constructor); err != nil {
			diags.Add(werr.Newf(werr.CodeASTShape,
				"BindType(%q): Constructor invalid: %v", name, err))
		}
	}

	return diags.Err()
}

// mergeCaps merges two capability slices, deduplicating while preserving the
// first-seen order so deterministic behavior is preserved.
func mergeCaps(a, b []string) []string {
	if len(a) == 0 {
		return append([]string(nil), b...)
	}
	if len(b) == 0 {
		return append([]string(nil), a...)
	}
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, c := range a {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	for _, c := range b {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// registerLiteralCtor adapts a user-supplied constructor (`func(string) (T, error)`)
// into a types.LiteralConstructor and registers it under the given short name.
func registerLiteralCtor(name string, ctor any) error {
	cv := reflect.ValueOf(ctor)
	ct := cv.Type()
	if ct.Kind() != reflect.Func {
		return werr.Newf(werr.CodeASTShape,
			"Constructor must be a func, got %s", ct.Kind())
	}
	if ct.NumIn() != 1 || ct.In(0).Kind() != reflect.String {
		return werr.Newf(werr.CodeASTShape,
			"Constructor must accept exactly one string argument, got %s", ct.String())
	}
	if ct.NumOut() != 2 {
		return werr.Newf(werr.CodeASTShape,
			"Constructor must return (T, error), got %d outs", ct.NumOut())
	}
	if !isErrorType(ct.Out(1)) {
		return werr.Newf(werr.CodeASTShape,
			"Constructor: second return must be error, got %s", ct.Out(1))
	}
	fn := func(raw string) (any, error) {
		out := cv.Call([]reflect.Value{reflect.ValueOf(raw)})
		if !out[1].IsNil() {
			return nil, out[1].Interface().(error)
		}
		return out[0].Interface(), nil
	}
	// Index by Go output type so the overload picker can coerce string args
	// at call time (TC-648).
	return types.RegisterLiteralConstructorTyped(name, fn, ct.Out(0))
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
		if !bf.wantsCtx && i == 0 && p.Implements(ctxT) {
			bf.wantsCtx = true
			continue
		}
		// Env injection: an Env-typed parameter at the first non-ctx position.
		if !bf.wantsEnv && p == envType {
			bf.wantsEnv = true
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
		if !bf.wantsCtx && i == 1 && p.Implements(ctxT) {
			bf.wantsCtx = true
			continue
		}
		if !bf.wantsEnv && p == envType {
			bf.wantsEnv = true
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

// capCtxKey is the context key holding the granted capability set.
type capCtxKey struct{}

// WithCapabilities returns a derived context that carries the granted
// capability set. The runtime injects capabilities here before invoking
// host functions; bound functions whose Capabilities slice contains a name
// not present in the set are rejected with E_CAPABILITY (LANGUAGE.md §5.5
// / §10.2 / TC-440-441).
func WithCapabilities(ctx context.Context, caps map[string]bool) context.Context {
	return context.WithValue(ctx, capCtxKey{}, caps)
}

func capsFromCtx(ctx context.Context) map[string]bool {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(capCtxKey{}).(map[string]bool)
	return v
}

// checkCaps returns nil when every required capability is granted.
func checkCaps(ctx context.Context, name string, required []string, path string) error {
	if len(required) == 0 {
		return nil
	}
	granted := capsFromCtx(ctx)
	for _, c := range required {
		if !granted[c] {
			return werr.Newf(werr.CodeCapability,
				"capability %q not granted for %q", c, name).WithPath(path)
		}
	}
	return nil
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
		if err := checkCaps(ctx, fn.name, fn.capability, path); err != nil {
			return types.Value{}, err
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
			if err := checkCaps(ctx, fn.name, fn.capability, path); err != nil {
				return types.Value{}, err
			}
			return fn.call(ctx, &recv, args, path)
		}
		if fn, ok := bt.methods[op]; ok {
			// Nil receiver check per §9.1.2.
			if recv.IsNull() {
				return types.Value{}, werr.Newf(werr.CodeNilReceiver,
					"receiver is null for method %q on %s", op, typName).WithPath(path)
			}
			if err := checkCaps(ctx, fn.name, fn.capability, path); err != nil {
				return types.Value{}, err
			}
			return fn.call(ctx, &recv, args, path)
		}
		// Method was explicitly filtered out by BindOptions Include/Exclude
		// (TC-361): fire E_SYMBOL to mark the name as unbound.
		if bt.excluded[op] {
			return types.Value{}, werr.Newf(werr.CodeSymbol,
				"method %q is not bound on %s (excluded by BindOptions)",
				op, typName).WithPath(path)
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

// yieldLike duck-types a runtime yield error without importing the wflang facade.
type yieldLike interface {
	error
	Token() string
	Payload() any
}

// yieldEnvelope wraps a host yield with the host function's business
// ReturnTypes so the runtime can report them in RoutineYieldReport
// (LANGUAGE.md §8 / TC-704).
type yieldEnvelope struct {
	inner       yieldLike
	returnTypes []string
	path        string
}

func (y *yieldEnvelope) Error() string         { return y.inner.Error() }
func (y *yieldEnvelope) Token() string         { return y.inner.Token() }
func (y *yieldEnvelope) Payload() any          { return y.inner.Payload() }
func (y *yieldEnvelope) ReturnTypes() []string { return y.returnTypes }
func (y *yieldEnvelope) Path() string          { return y.path }
func (y *yieldEnvelope) Unwrap() error         { return y.inner }

// reflectToTypeName mirrors wrapResult's primitive table for derivation of
// business return types from a host function signature.
func reflectToTypeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Int8:
		return types.TInt8
	case reflect.Int16:
		return types.TInt16
	case reflect.Int32:
		return types.TInt32
	case reflect.Int64, reflect.Int:
		return types.TInt64
	case reflect.Uint8:
		return types.TUint8
	case reflect.Uint16:
		return types.TUint16
	case reflect.Uint32:
		return types.TUint32
	case reflect.Uint64:
		return types.TUint64
	case reflect.Float32:
		return types.TFloat32
	case reflect.Float64:
		return types.TFloat64
	case reflect.Bool:
		return types.TBoolean
	case reflect.String:
		return types.TString
	}
	return types.AutoHostTypeName(t)
}

// businessReturnTypes returns the function's return types with the trailing
// error type omitted (the same shape ResumeYield Results must match).
func (bf *boundFunc) businessReturnTypes() []string {
	out := []string{}
	for _, t := range bf.outTypes {
		if isErrorType(t) {
			continue
		}
		out = append(out, reflectToTypeName(t))
	}
	return out
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
	if bf.wantsEnv {
		in = append(in, reflect.ValueOf(Env{Caps: capsFromCtx(ctx), Path: path}))
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
	// Result shapes:
	//   ()                        → null
	//   (error)                   → null on nil, host err otherwise
	//   (T)                       → wrapResult(T)
	//   (T, error)                → wrapResult(T) or host err / yield envelope
	//   (T1,...,Tn, error)        → tuple<T1,...,Tn> or host err / yield envelope
	var resVal reflect.Value
	var resErr error
	switch {
	case len(out) == 0:
		v, _ := types.NewNull()
		return v, nil
	case len(out) == 1:
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
	case len(out) == 2:
		resVal = out[0]
		if !out[1].IsNil() {
			resErr, _ = out[1].Interface().(error)
		}
	default:
		// (T1,...,Tn, error) shape — last out must be error.
		last := len(out) - 1
		if !isErrorType(bf.outTypes[last]) {
			return types.Value{}, werr.Newf(werr.CodeHost,
				"%q unsupported return arity %d (no trailing error)",
				bf.name, len(out)).WithPath(path)
		}
		if !out[last].IsNil() {
			resErr, _ = out[last].Interface().(error)
		}
		if resErr == nil {
			// Build tuple<T1,...,Tn> from the prefix returns.
			names := make([]string, last)
			vals := make([]any, last)
			for i := 0; i < last; i++ {
				wv := wrapResult(out[i])
				names[i] = wv.TypeName()
				vals[i] = wv.Go()
			}
			return types.NewValue(types.TupleType(names), vals), nil
		}
		// Fall through to error handling below.
	}
	if resErr != nil {
		// If the host returned a yield error, surface it through a typed
		// envelope so the routine machinery can attach ReturnTypes.
		if y, ok := resErr.(yieldLike); ok {
			return types.Value{}, &yieldEnvelope{
				inner:       y,
				returnTypes: bf.businessReturnTypes(),
				path:        path,
			}
		}
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
	// Constructor coercion: string → T via registered literal constructor
	// (LANGUAGE.md §7.4 / TC-648).
	if gv.Type().Kind() == reflect.String {
		if ctor, ok := types.LookupConstructorForGoType(target); ok {
			out, err := ctor(gv.String())
			if err != nil {
				return reflect.Value{}, werr.Newf(werr.CodeType,
					"coerce string to %s: %v", target, err)
			}
			ov := reflect.ValueOf(out)
			if ov.Type().AssignableTo(target) {
				return ov, nil
			}
			if ov.Type().ConvertibleTo(target) {
				return ov.Convert(target), nil
			}
		}
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

// RequiredCapabilities returns the capability list declared on the named
// package function (LANGUAGE.md §5.5 / §10.2). Returns nil if the package or
// function is unknown, or if no capabilities are required.
func (r *Registry) RequiredCapabilities(pkg, op string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bp, ok := r.packages[pkg]
	if !ok {
		return nil
	}
	fn, ok := bp.funcs[op]
	if !ok {
		return nil
	}
	return append([]string(nil), fn.capability...)
}

// PackageFunctions returns a snapshot of the function-name set of the named
// package. The map values are placeholders; callers should treat the keys as
// the authoritative set. Returns nil if the package is unknown.
func (r *Registry) PackageFunctions(pkg string) map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bp, ok := r.packages[pkg]
	if !ok {
		return nil
	}
	out := make(map[string]any, len(bp.funcs))
	for k := range bp.funcs {
		out[k] = struct{}{}
	}
	return out
}

// FuncMeta is a read-only descriptor for a registered Go function.
// Used by the compiler to build CallPlan and by lint/explain passes.
// LANGUAGE.md §5.8 / §7.7 / §13.2.
type FuncMeta struct {
	Name         string
	GoFunc       any      // for package functions
	GoMethod     any      // for methods
	ReceiverKind string   // "package" | "method" | "auto"
	PackageName  string   // for package functions
	TypeName     string   // for methods
	ParamTypes   []string // language-visible param types (after stripping ctx/env)
	ReturnTypes  []string // business return types (excluding error)
	HasError     bool
	WantsEnv     bool
	WantsCtx     bool
	YieldAware   bool
	Capabilities []string
	Deprecated   string
}

// LookupPackageFunc returns the metadata of a package function or nil if absent.
func (r *Registry) LookupPackageFunc(pkg, op string) *FuncMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bp, ok := r.packages[pkg]
	if !ok {
		return nil
	}
	bf, ok := bp.funcs[op]
	if !ok {
		return nil
	}
	return bf.toMeta("package", pkg, "")
}

// LookupTypeMethod returns the metadata of a type method or nil if absent.
// typeName is the auto host-type name (e.g. "*pkg.T").
func (r *Registry) LookupTypeMethod(typeName, op string) *FuncMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bt, ok := r.types[typeName]
	if !ok {
		return nil
	}
	bf, ok := bt.methods[op]
	if !ok {
		return nil
	}
	return bf.toMeta("method", "", typeName)
}

// IsYieldAware reports whether the function is annotated YieldAware.
func (r *Registry) IsYieldAware(pkg, op string) bool {
	if m := r.LookupPackageFunc(pkg, op); m != nil {
		return m.YieldAware
	}
	return false
}

// DeprecationOf returns the deprecation guidance string, or "" when none.
func (r *Registry) DeprecationOf(pkg, op string) string {
	if m := r.LookupPackageFunc(pkg, op); m != nil {
		return m.Deprecated
	}
	return ""
}

// toMeta projects a boundFunc into a FuncMeta. caller-side types are derived
// via reflectToTypeName so the language-visible type names match wrapResult.
func (bf *boundFunc) toMeta(kind, pkgName, typeName string) *FuncMeta {
	pt := make([]string, 0, len(bf.inTypes))
	for _, t := range bf.inTypes {
		pt = append(pt, reflectToTypeName(t))
	}
	rt := bf.businessReturnTypes()
	hasErr := false
	for _, t := range bf.outTypes {
		if isErrorType(t) {
			hasErr = true
			break
		}
	}
	m := &FuncMeta{
		Name:         bf.name,
		ReceiverKind: kind,
		PackageName:  pkgName,
		TypeName:     typeName,
		ParamTypes:   pt,
		ReturnTypes:  rt,
		HasError:     hasErr,
		WantsCtx:     bf.wantsCtx,
		WantsEnv:     bf.wantsEnv,
		YieldAware:   bf.yieldAware,
		Capabilities: append([]string(nil), bf.capability...),
		Deprecated:   bf.deprecated,
	}
	if kind == "method" {
		m.GoMethod = bf.impl.Interface()
	} else {
		m.GoFunc = bf.impl.Interface()
	}
	return m
}
