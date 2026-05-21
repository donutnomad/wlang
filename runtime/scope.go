// Package runtime implements program execution (LANGUAGE.md §2.3, §2.4, §3).
package runtime

import (
	"reflect"
	"strconv"
	"strings"

	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/types"
)

// varBinding records a runtime variable.
type varBinding struct {
	val      types.Value
	writable bool
	// declaredType is "" when free, otherwise the declared static type.
	declaredType string
}

// deferredCall captures a host call with its receiver/arguments snapshot, to
// be executed in LIFO order when the enclosing scope unwinds (LANGUAGE.md §3.7).
type deferredCall struct {
	op     string
	recv   types.Value
	args   []types.Value
	fn     types.Value
	fnArgs []types.Value
	path   string
	kind   deferredCallKind
}

type deferredCallKind int

const (
	deferredHostCall deferredCallKind = iota
	deferredFuncCall
)

// Scope is a single lexical scope frame (§2.3).
type Scope struct {
	parent   *Scope
	vars     map[string]*varBinding
	deferred []deferredCall
}

// NewScope creates an empty root scope.
func NewScope() *Scope { return &Scope{vars: map[string]*varBinding{}} }

// Push returns a child scope.
func (s *Scope) Push() *Scope { return &Scope{parent: s, vars: map[string]*varBinding{}} }

// PushDeferred records a deferred host call on this scope frame.
func (s *Scope) PushDeferred(op string, recv types.Value, args []types.Value, path string) {
	s.deferred = append(s.deferred, deferredCall{
		op: op, recv: recv, args: args, path: path, kind: deferredHostCall,
	})
}

// PushDeferredFunc records a deferred function call on this scope frame.
func (s *Scope) PushDeferredFunc(fn types.Value, args []types.Value, path string) {
	s.deferred = append(s.deferred, deferredCall{
		fn: fn, fnArgs: args, path: path, kind: deferredFuncCall,
	})
}

// PopDeferred returns the deferred calls slice in LIFO order and clears it.
func (s *Scope) PopDeferred() []deferredCall {
	if len(s.deferred) == 0 {
		return nil
	}
	out := make([]deferredCall, len(s.deferred))
	for i, d := range s.deferred {
		out[len(s.deferred)-1-i] = d
	}
	s.deferred = nil
	return out
}

// Pop returns the parent scope (used when leaving a block). The current scope
// is discarded, so any variables created here do not leak upward (TC-196).
func (s *Scope) Pop() *Scope { return s.parent }

// Let defines a new variable in the current scope (always writable locally).
func (s *Scope) Let(name string, v types.Value, declaredType string) {
	s.vars[name] = &varBinding{val: v, writable: true, declaredType: declaredType}
}

// LetReadOnly defines a read-only variable (used for top-level injection).
func (s *Scope) LetReadOnly(name string, v types.Value) {
	s.vars[name] = &varBinding{val: v, writable: false}
}

// LetWritable defines a top-level writable variable.
func (s *Scope) LetWritable(name string, v types.Value) {
	s.vars[name] = &varBinding{val: v, writable: true}
}

// Set searches outward and updates the binding. Returns E_READONLY_VAR / E_SYMBOL.
func (s *Scope) Set(name string, v types.Value) error {
	for cur := s; cur != nil; cur = cur.parent {
		if b, ok := cur.vars[name]; ok {
			if !b.writable {
				return werr.Newf(werr.CodeReadonlyVar,
					"cannot assign to read-only variable %q", name)
			}
			if b.declaredType != "" && b.declaredType != v.TypeName() {
				return werr.Newf(werr.CodeType,
					"variable %q declared as %s, got %s",
					name, b.declaredType, v.TypeName())
			}
			b.val = v
			return nil
		}
	}
	return werr.Newf(werr.CodeSymbol, "variable %q not defined", name)
}

// Lookup looks up a variable (without descending into paths).
func (s *Scope) Lookup(name string) (types.Value, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if b, ok := cur.vars[name]; ok {
			return b.val, true
		}
	}
	return types.Value{}, false
}

// LookupPath evaluates a dotted path against the current scope (§2.3).
// Returns found=false when any segment is missing; caller may apply defaults.
func (s *Scope) LookupPath(path string) (types.Value, bool) {
	if path == "" {
		return types.Value{}, false
	}
	segs := strings.Split(path, ".")
	v, ok := s.Lookup(segs[0])
	if !ok {
		return types.Value{}, false
	}
	// Single-segment path: return the stored value directly so its language
	// type name (e.g. `error`) is preserved.
	if len(segs) == 1 {
		return v, true
	}
	cur := v.Go()
	for _, seg := range segs[1:] {
		nxt, ok := accessSegment(cur, seg)
		if !ok {
			return types.Value{}, false
		}
		cur = nxt
	}
	return wrapGo(cur), true
}

// accessSegment applies one path segment to a Go value.
func accessSegment(cur any, seg string) (any, bool) {
	if cur == nil {
		return nil, false
	}
	rv := reflect.ValueOf(cur)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Map:
		// Map keys are always strings (TC-272).
		if rv.Type().Key().Kind() != reflect.String {
			return nil, false
		}
		v := rv.MapIndex(reflect.ValueOf(seg))
		if !v.IsValid() {
			return nil, false
		}
		return v.Interface(), true
	case reflect.Slice, reflect.Array:
		idx, err := strconv.Atoi(seg)
		if err != nil || idx < 0 || idx >= rv.Len() {
			return nil, false
		}
		return rv.Index(idx).Interface(), true
	case reflect.Struct:
		// Match by json tag first, then exported field name (case-sensitive, TC-275).
		rt := rv.Type()
		for i := 0; i < rt.NumField(); i++ {
			sf := rt.Field(i)
			if !sf.IsExported() {
				continue
			}
			tag := sf.Tag.Get("json")
			if tag == "-" {
				continue
			}
			tagName := strings.SplitN(tag, ",", 2)[0]
			if tagName == seg {
				return rv.Field(i).Interface(), true
			}
		}
		for i := 0; i < rt.NumField(); i++ {
			sf := rt.Field(i)
			if !sf.IsExported() {
				continue
			}
			if sf.Name == seg {
				// Check json:"-" blocking
				if strings.SplitN(sf.Tag.Get("json"), ",", 2)[0] == "-" {
					return nil, false
				}
				return rv.Field(i).Interface(), true
			}
		}
	}
	return nil, false
}

// wrapGo attempts to wrap a Go value in a typed Value.
func wrapGo(g any) types.Value {
	if g == nil {
		v, _ := types.NewNull()
		return v
	}
	switch x := g.(type) {
	case types.Value:
		return x
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
	case int:
		// Host side ints collapse to int64 for safety.
		return types.NewValue(types.TInt64, int64(x))
	}
	// Auto host type: use reflect to derive name
	rt := reflect.TypeOf(g)
	return types.NewValue(types.AutoHostTypeName(rt), g)
}
