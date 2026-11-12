package types

import (
	"context"
	"reflect"

	werr "github.com/donutnomad/wlang/errors"
)

// Limits are the per-value size limits of one execution, taken from its
// Budget (MaxStringBytes, MaxArrayLength, MaxObjectKeys). The runtime attaches
// them to the context it hands to host calls, so a bound function can refuse
// a result before allocating it and the registry can check every host result.
// A zero field means unlimited.
type Limits struct {
	MaxStringBytes int
	MaxArrayLength int
	MaxObjectKeys  int
}

type limitsKey struct{}

// WithLimits returns a context carrying l.
func WithLimits(ctx context.Context, l Limits) context.Context {
	return context.WithValue(ctx, limitsKey{}, l)
}

// LimitsFrom returns the limits carried by ctx; without any, every field is
// zero (unlimited).
func LimitsFrom(ctx context.Context) Limits {
	if ctx == nil {
		return Limits{}
	}
	l, _ := ctx.Value(limitsKey{}).(Limits)
	return l
}

// CheckString fails with E_BUDGET when a string (or a big number's magnitude)
// of n bytes exceeds MaxStringBytes.
func (l Limits) CheckString(n int64) error {
	if l.MaxStringBytes > 0 && n > int64(l.MaxStringBytes) {
		return werr.Newf(werr.CodeBudget, "MaxStringBytes exceeded: result needs %d bytes, limit %d", n, l.MaxStringBytes)
	}
	return nil
}

// CheckArray fails with E_BUDGET when an array of n elements exceeds
// MaxArrayLength.
func (l Limits) CheckArray(n int64) error {
	if l.MaxArrayLength > 0 && n > int64(l.MaxArrayLength) {
		return werr.Newf(werr.CodeBudget, "MaxArrayLength exceeded: result has %d elements, limit %d", n, l.MaxArrayLength)
	}
	return nil
}

// CheckKeys fails with E_BUDGET when a map of n keys exceeds MaxObjectKeys.
func (l Limits) CheckKeys(n int64) error {
	if l.MaxObjectKeys > 0 && n > int64(l.MaxObjectKeys) {
		return werr.Newf(werr.CodeBudget, "MaxObjectKeys exceeded: result has %d keys, limit %d", n, l.MaxObjectKeys)
	}
	return nil
}

// CheckValue checks the outermost size of a host result: the bytes of a
// string, the length of an array, the keys of a map. Nested values are not
// visited; every operation that builds them is checked on its own.
func (l Limits) CheckValue(v any) error {
	if l == (Limits{}) {
		return nil
	}
	if value, ok := v.(Value); ok {
		v = value.Go()
	}
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return l.CheckString(int64(len(x)))
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return l.CheckString(int64(rv.Len()))
	case reflect.Slice, reflect.Array:
		return l.CheckArray(int64(rv.Len()))
	case reflect.Map:
		return l.CheckKeys(int64(rv.Len()))
	}
	return nil
}
