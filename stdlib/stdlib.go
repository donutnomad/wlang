// Package stdlib provides wflang's core standard library (LANGUAGE.md §6).
package stdlib

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/types"
)

// RegisterCore binds all pure standard library packages to r.
// Packages: str, num, arr, val, to, json.
func RegisterCore(r *registry.Registry) error {
	if err := r.BindGoPackage("str", strPackage()); err != nil {
		return err
	}
	if err := r.BindGoPackage("num", numPackage()); err != nil {
		return err
	}
	if err := r.BindGoPackage("arr", arrPackage()); err != nil {
		return err
	}
	if err := r.BindGoPackage("val", valPackage()); err != nil {
		return err
	}
	if err := r.BindGoPackage("to", toPackage()); err != nil {
		return err
	}
	if err := r.BindGoPackage("json", jsonPackage()); err != nil {
		return err
	}
	if err := r.BindGoPackage("path", pathPackage()); err != nil {
		return err
	}
	return nil
}

// ---------- path ----------

func pathPackage() registry.PackageSpec {
	return registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Get", Impl: pathGet, Pure: true},
			{GoName: "Set", Impl: pathSet, Pure: true},
			{GoName: "Has", Impl: pathHas, Pure: true},
			{GoName: "Keys", Impl: pathKeys, Pure: true},
			{GoName: "Values", Impl: pathValues, Pure: true},
		},
	}
}

func pathGet(v any, p string) any {
	parts := splitPath(p)
	cur := v
	for _, seg := range parts {
		next, ok := stepInto(cur, seg)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

// pathSet returns a shallow-copied value with the path overwritten.
// Only map-typed containers are supported; struct writes produce a new map.
func pathSet(v any, p string, newVal any) any {
	parts := splitPath(p)
	if len(parts) == 0 {
		return newVal
	}
	return setInto(v, parts, newVal)
}

func setInto(cur any, parts []string, newVal any) any {
	if len(parts) == 0 {
		return newVal
	}
	head := parts[0]
	rest := parts[1:]
	m, ok := cur.(map[string]any)
	if !ok {
		m = map[string]any{}
	}
	clone := make(map[string]any, len(m)+1)
	maps.Copy(clone, m)
	child, _ := clone[head]
	clone[head] = setInto(child, rest, newVal)
	return clone
}

func pathHas(v any, p string) bool {
	parts := splitPath(p)
	cur := v
	for _, seg := range parts {
		next, ok := stepInto(cur, seg)
		if !ok {
			return false
		}
		cur = next
	}
	return true
}

func pathKeys(v any) []any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil
	}
	if rv.Kind() == reflect.Map {
		keys := rv.MapKeys()
		out := make([]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, k.Interface())
		}
		return out
	}
	if rv.Kind() == reflect.Struct {
		t := rv.Type()
		out := make([]any, 0, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).PkgPath == "" {
				out = append(out, t.Field(i).Name)
			}
		}
		return out
	}
	return nil
}

func pathValues(v any) []any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil
	}
	if rv.Kind() == reflect.Map {
		keys := rv.MapKeys()
		out := make([]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, rv.MapIndex(k).Interface())
		}
		return out
	}
	if rv.Kind() == reflect.Struct {
		t := rv.Type()
		out := make([]any, 0, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).PkgPath == "" {
				out = append(out, rv.Field(i).Interface())
			}
		}
		return out
	}
	return nil
}

func splitPath(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(p, ".")
}

// stepInto resolves one path segment against the current value.
// Returns (child, true) on success, or (nil, false) when not found.
func stepInto(cur any, seg string) (any, bool) {
	if cur == nil {
		return nil, false
	}
	// map[string]any fast path.
	if m, ok := cur.(map[string]any); ok {
		v, ok := m[seg]
		return v, ok
	}
	rv := reflect.ValueOf(cur)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Map:
		key := reflect.ValueOf(seg)
		if !key.Type().AssignableTo(rv.Type().Key()) {
			return nil, false
		}
		mv := rv.MapIndex(key)
		if !mv.IsValid() {
			return nil, false
		}
		return mv.Interface(), true
	case reflect.Struct:
		f := rv.FieldByName(seg)
		if !f.IsValid() {
			return nil, false
		}
		return f.Interface(), true
	case reflect.Slice, reflect.Array:
		// Numeric segment indexes into slice/array.
		idx, err := strconv.Atoi(seg)
		if err != nil || idx < 0 || idx >= rv.Len() {
			return nil, false
		}
		return rv.Index(idx).Interface(), true
	}
	return nil, false
}

// ---------- str ----------

func strPackage() registry.PackageSpec {
	return registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Len", Impl: strLen, Pure: true},
			{GoName: "Trim", Impl: strings.TrimSpace, Pure: true},
			{GoName: "Lower", Impl: strings.ToLower, Pure: true},
			{GoName: "Upper", Impl: strings.ToUpper, Pure: true},
			{GoName: "Contains", Impl: strings.Contains, Pure: true},
			{GoName: "StartsWith", Impl: strings.HasPrefix, Pure: true},
			{GoName: "EndsWith", Impl: strings.HasSuffix, Pure: true},
			{GoName: "Replace", Impl: strReplace, Pure: true},
			{GoName: "Split", Impl: strings.Split, Pure: true},
			{GoName: "Join", Impl: strings.Join, Pure: true},
			{GoName: "Format", Impl: strFormat, Pure: true},
		},
	}
}

func strFormat(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func strLen(s string) int64                { return int64(len(s)) }
func strReplace(s, old, new string) string { return strings.ReplaceAll(s, old, new) }

// ---------- num ----------

func numPackage() registry.PackageSpec {
	return registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Abs", Impl: numAbs, Pure: true},
			{GoName: "Round", Impl: math.Round, Pure: true},
			{GoName: "Floor", Impl: math.Floor, Pure: true},
			{GoName: "Ceil", Impl: math.Ceil, Pure: true},
			{GoName: "Min", Impl: math.Min, Pure: true},
			{GoName: "Max", Impl: math.Max, Pure: true},
			{GoName: "Clamp", Impl: numClamp, Pure: true},
		},
	}
}

func numAbs(v float64) float64 { return math.Abs(v) }
func numClamp(v, lo, hi float64) float64 {
	return math.Min(math.Max(v, lo), hi)
}

// ---------- arr ----------

func arrPackage() registry.PackageSpec {
	return registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Len", Impl: arrLen, Pure: true},
			{GoName: "Contains", Impl: arrContains, Pure: true},
			{GoName: "Sort", Impl: arrSort, Pure: true},
			{GoName: "Distinct", Impl: arrDistinct, Pure: true},
			{GoName: "Flatten", Impl: arrFlatten, Pure: true},
		},
	}
}

// arrLen uses reflect so any slice/array is accepted.
func arrLen(v any) int64 {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return 0
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.String:
		return int64(rv.Len())
	}
	return 0
}

func arrContains(v any, needle any) bool {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return false
	}
	for i := 0; i < rv.Len(); i++ {
		if reflect.DeepEqual(rv.Index(i).Interface(), needle) {
			return true
		}
	}
	return false
}

// arrSort returns a sorted copy of the slice; best-effort for comparable kinds.
func arrSort(v any) []any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil
	}
	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out[i] = rv.Index(i).Interface()
	}
	sort.SliceStable(out, func(i, j int) bool {
		return lessAny(out[i], out[j])
	})
	return out
}

func arrDistinct(v any) []any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]any, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		el := rv.Index(i).Interface()
		key := fmt.Sprintf("%T:%v", el, el)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, el)
	}
	return out
}

func arrFlatten(v any) []any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil
	}
	out := make([]any, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		el := rv.Index(i)
		if el.Kind() == reflect.Interface {
			el = el.Elem()
		}
		if el.IsValid() && (el.Kind() == reflect.Slice || el.Kind() == reflect.Array) {
			for j := 0; j < el.Len(); j++ {
				out = append(out, el.Index(j).Interface())
			}
		} else if el.IsValid() {
			out = append(out, el.Interface())
		} else {
			out = append(out, nil)
		}
	}
	return out
}

// lessAny is a best-effort comparator that orders numbers, strings, and
// falls back to fmt comparison.
func lessAny(a, b any) bool {
	af, aok := asFloat(a)
	bf, bok := asFloat(b)
	if aok && bok {
		return af < bf
	}
	as, aok2 := a.(string)
	bs, bok2 := b.(string)
	if aok2 && bok2 {
		return as < bs
	}
	return fmt.Sprint(a) < fmt.Sprint(b)
}

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// ---------- val ----------

func valPackage() registry.PackageSpec {
	return registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "IsNull", Impl: valIsNull, Pure: true},
			{GoName: "TypeOf", Impl: valTypeOf, Pure: true},
			{GoName: "IsEmpty", Impl: valIsEmpty, Pure: true},
			{GoName: "Coalesce", Impl: valCoalesce, Pure: true},
			{GoName: "IsError", Impl: valIsError, Pure: true},
		},
	}
}

func valIsNull(v any) bool { return v == nil }

func valTypeOf(v any) string {
	if v == nil {
		return types.TNull
	}
	switch v.(type) {
	case bool:
		return types.TBoolean
	case string:
		return types.TString
	case int8:
		return types.TInt8
	case int16:
		return types.TInt16
	case int32:
		return types.TInt32
	case int64:
		return types.TInt64
	case int:
		return types.TInt64
	case uint8:
		return types.TUint8
	case uint16:
		return types.TUint16
	case uint32:
		return types.TUint32
	case uint64:
		return types.TUint64
	case float32:
		return types.TFloat32
	case float64:
		return types.TFloat64
	case *big.Int:
		return types.TBigInt
	case *big.Float:
		return types.TBigDecimal
	case error:
		return types.TError
	}
	return types.AutoHostTypeNameOf(v)
}

func valIsEmpty(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String, reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return rv.IsNil()
	}
	return false
}

func valCoalesce(args ...any) any {
	for _, a := range args {
		if a != nil {
			return a
		}
	}
	return nil
}

func valIsError(v any) bool {
	_, ok := v.(error)
	return ok
}

// ---------- to ----------

func toPackage() registry.PackageSpec {
	return registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "String", Impl: toString, Pure: true},
			{GoName: "Int8", Impl: toInt8, Pure: true},
			{GoName: "Int16", Impl: toInt16, Pure: true},
			{GoName: "Int32", Impl: toInt32, Pure: true},
			{GoName: "Int64", Impl: toInt64, Pure: true},
			{GoName: "Uint8", Impl: toUint8, Pure: true},
			{GoName: "Uint16", Impl: toUint16, Pure: true},
			{GoName: "Uint32", Impl: toUint32, Pure: true},
			{GoName: "Uint64", Impl: toUint64, Pure: true},
			{GoName: "Float32", Impl: toFloat32, Pure: true},
			{GoName: "Float64", Impl: toFloat64, Pure: true},
			{GoName: "Boolean", Impl: toBoolean, Pure: true},
			{GoName: "BigInt", Impl: toBigInt, Pure: true},
			{GoName: "BigDecimal", Impl: toBigDecimal, Pure: true},
			{GoName: "JSON", Impl: toJSON, Pure: true},
		},
	}
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case *big.Int:
		return x.String()
	case *big.Float:
		return x.Text('g', -1)
	}
	if f, ok := asFloat(v); ok {
		// Prefer int formatting when value is whole & an int kind.
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return strconv.FormatInt(rv.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return strconv.FormatUint(rv.Uint(), 10)
		}
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return fmt.Sprint(v)
}

func toInt8(v any) (int8, error)   { i, err := anyToInt64(v); return int8(i), err }
func toInt16(v any) (int16, error) { i, err := anyToInt64(v); return int16(i), err }
func toInt32(v any) (int32, error) { i, err := anyToInt64(v); return int32(i), err }
func toInt64(v any) (int64, error) { return anyToInt64(v) }

func toUint8(v any) (uint8, error) {
	i, err := anyToInt64(v)
	return uint8(i), err
}
func toUint16(v any) (uint16, error) { i, err := anyToInt64(v); return uint16(i), err }
func toUint32(v any) (uint32, error) { i, err := anyToInt64(v); return uint32(i), err }
func toUint64(v any) (uint64, error) { i, err := anyToInt64(v); return uint64(i), err }

func toFloat32(v any) (float32, error) {
	f, err := anyToFloat64(v)
	return float32(f), err
}
func toFloat64(v any) (float64, error) { return anyToFloat64(v) }

func toBoolean(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		return strconv.ParseBool(x)
	}
	if f, ok := asFloat(v); ok {
		return f != 0, nil
	}
	return false, fmt.Errorf("cannot convert %T to boolean", v)
}

func toBigInt(v any) (*big.Int, error) {
	switch x := v.(type) {
	case *big.Int:
		return new(big.Int).Set(x), nil
	case string:
		z, ok := new(big.Int).SetString(x, 10)
		if !ok {
			return nil, fmt.Errorf("cannot parse %q as bigInt", x)
		}
		return z, nil
	}
	if i, err := anyToInt64(v); err == nil {
		return big.NewInt(i), nil
	}
	return nil, fmt.Errorf("cannot convert %T to bigInt", v)
}

func toBigDecimal(v any) (*big.Float, error) {
	switch x := v.(type) {
	case *big.Float:
		return new(big.Float).Set(x), nil
	case string:
		z, _, err := big.ParseFloat(x, 10, 128, big.ToNearestEven)
		if err != nil {
			return nil, fmt.Errorf("cannot parse %q as bigDecimal", x)
		}
		return z, nil
	}
	if f, err := anyToFloat64(v); err == nil {
		return new(big.Float).SetFloat64(f), nil
	}
	return nil, fmt.Errorf("cannot convert %T to bigDecimal", v)
}

func toJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func anyToInt64(v any) (int64, error) {
	switch x := v.(type) {
	case string:
		return strconv.ParseInt(x, 10, 64)
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	case *big.Int:
		return x.Int64(), nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(rv.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return int64(rv.Float()), nil
	}
	return 0, fmt.Errorf("cannot convert %T to int64", v)
}

func anyToFloat64(v any) (float64, error) {
	switch x := v.(type) {
	case string:
		return strconv.ParseFloat(x, 64)
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	case *big.Int:
		f, _ := new(big.Float).SetInt(x).Float64()
		return f, nil
	case *big.Float:
		f, _ := x.Float64()
		return f, nil
	}
	if f, ok := asFloat(v); ok {
		return f, nil
	}
	return 0, fmt.Errorf("cannot convert %T to float64", v)
}

// ---------- json ----------

func jsonPackage() registry.PackageSpec {
	return registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Parse", Impl: jsonParse, Pure: true},
			{GoName: "Stringify", Impl: jsonStringify, Pure: true},
		},
	}
}

func jsonParse(s string) (any, error) {
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func jsonStringify(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
