// Package types implements wflang's type system (LANGUAGE.md §2.1, §4).
package types

import (
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	werr "github.com/wflang/wflang/errors"
)

// Built-in language type names.
const (
	TUint8      = "uint8"
	TUint16     = "uint16"
	TUint32     = "uint32"
	TUint64     = "uint64"
	TInt8       = "int8"
	TInt16      = "int16"
	TInt32      = "int32"
	TInt64      = "int64"
	TFloat32    = "float32"
	TFloat64    = "float64"
	TBoolean    = "boolean"
	TString     = "string"
	TNull       = "null"
	TError      = "error"
	TBigInt     = "bigInt"
	TBigDecimal = "bigDecimal"
	TAny        = "any"
)

// Value is the runtime wrapper for every language value.
// It carries a language type name and a Go carrier value.
type Value struct {
	typ string
	val any
}

// TypeName returns the language type name.
func (v Value) TypeName() string { return v.typ }

// Go returns the carrier Go value.
func (v Value) Go() any { return v.val }

// IsNull reports whether this value is the null typed value.
func (v Value) IsNull() bool { return v.typ == TNull }

// IsError reports whether this value carries an error typed value.
func (v Value) IsError() bool { return v.typ == TError }

// NewValue constructs a Value without validation. Use NewLiteral for parsing.
func NewValue(typeName string, goValue any) Value {
	return Value{typ: typeName, val: goValue}
}

// NewNull returns the null typed value.
func NewNull() (Value, error) { return Value{typ: TNull, val: nil}, nil }

// NewLiteral parses a typed literal value (TC-100~105, §2.5.1).
// The `raw` parameter is the string form carried in the typed literal JSON.
func NewLiteral(typeName, raw string) (Value, error) {
	switch typeName {
	case "int", "uint":
		return Value{}, werr.Newf(werr.CodeType,
			"platform-dependent type %q is not allowed; use int8/16/32/64 or uint8/16/32/64", typeName)
	case TInt8:
		n, err := strconv.ParseInt(raw, 10, 8)
		if err != nil {
			return Value{}, parseErr(typeName, raw, err)
		}
		return Value{typ: typeName, val: int8(n)}, nil
	case TInt16:
		n, err := strconv.ParseInt(raw, 10, 16)
		if err != nil {
			return Value{}, parseErr(typeName, raw, err)
		}
		return Value{typ: typeName, val: int16(n)}, nil
	case TInt32:
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return Value{}, parseErr(typeName, raw, err)
		}
		return Value{typ: typeName, val: int32(n)}, nil
	case TInt64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return Value{}, parseErr(typeName, raw, err)
		}
		return Value{typ: typeName, val: n}, nil
	case TUint8:
		n, err := strconv.ParseUint(raw, 10, 8)
		if err != nil {
			return Value{}, parseErr(typeName, raw, err)
		}
		return Value{typ: typeName, val: uint8(n)}, nil
	case TUint16:
		n, err := strconv.ParseUint(raw, 10, 16)
		if err != nil {
			return Value{}, parseErr(typeName, raw, err)
		}
		return Value{typ: typeName, val: uint16(n)}, nil
	case TUint32:
		n, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			return Value{}, parseErr(typeName, raw, err)
		}
		return Value{typ: typeName, val: uint32(n)}, nil
	case TUint64:
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return Value{}, parseErr(typeName, raw, err)
		}
		return Value{typ: typeName, val: n}, nil
	case TFloat32:
		n, err := strconv.ParseFloat(raw, 32)
		if err != nil {
			return Value{}, parseErr(typeName, raw, err)
		}
		return Value{typ: typeName, val: float32(n)}, nil
	case TFloat64:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return Value{}, parseErr(typeName, raw, err)
		}
		return Value{typ: typeName, val: n}, nil
	case TBoolean:
		switch raw {
		case "true":
			return Value{typ: typeName, val: true}, nil
		case "false":
			return Value{typ: typeName, val: false}, nil
		default:
			return Value{}, parseErr(typeName, raw, fmt.Errorf("expected true|false"))
		}
	case TString:
		return Value{typ: typeName, val: raw}, nil
	case TNull:
		return Value{typ: typeName, val: nil}, nil
	case TBigInt:
		bi, ok := new(big.Int).SetString(raw, 10)
		if !ok {
			return Value{}, parseErr(typeName, raw, fmt.Errorf("not a valid bigInt"))
		}
		return Value{typ: typeName, val: bi}, nil
	case TBigDecimal:
		// big.Rat accepts "1.000" and fractional forms "1/2".
		br, ok := new(big.Rat).SetString(raw)
		if !ok {
			return Value{}, parseErr(typeName, raw, fmt.Errorf("not a valid bigDecimal"))
		}
		return Value{typ: typeName, val: br}, nil
	default:
		return Value{}, werr.Newf(werr.CodeASTShape,
			"unsupported typed literal type %q", typeName)
	}
}

func parseErr(typ, raw string, cause error) *werr.LangError {
	e := werr.Newf(werr.CodeType, "cannot parse %q as %s: %v", raw, typ, cause)
	return e
}

// ArrayType returns the language type name array<T>.
func ArrayType(elem string) string { return "array<" + elem + ">" }

// NewArray builds an array<T> typed value. Every element's TypeName must match
// the declared element type (TC-014, TC-904).
func NewArray(elem string, elems []Value) (Value, error) {
	for i, e := range elems {
		if e.TypeName() != elem {
			return Value{}, werr.Newf(werr.CodeType,
				"array<%s> element %d has type %s", elem, i, e.TypeName())
		}
	}
	// Build a typed []T slice for well-known element types so Go consumers
	// get the same carrier they would get from the underlying Go type.
	goSlice := buildTypedSlice(elem, elems)
	return Value{typ: ArrayType(elem), val: goSlice}, nil
}

func buildTypedSlice(elem string, elems []Value) any {
	switch elem {
	case TInt8:
		out := make([]int8, len(elems))
		for i, e := range elems {
			out[i] = e.val.(int8)
		}
		return out
	case TInt16:
		out := make([]int16, len(elems))
		for i, e := range elems {
			out[i] = e.val.(int16)
		}
		return out
	case TInt32:
		out := make([]int32, len(elems))
		for i, e := range elems {
			out[i] = e.val.(int32)
		}
		return out
	case TInt64:
		out := make([]int64, len(elems))
		for i, e := range elems {
			out[i] = e.val.(int64)
		}
		return out
	case TUint8:
		out := make([]uint8, len(elems))
		for i, e := range elems {
			out[i] = e.val.(uint8)
		}
		return out
	case TUint16:
		out := make([]uint16, len(elems))
		for i, e := range elems {
			out[i] = e.val.(uint16)
		}
		return out
	case TUint32:
		out := make([]uint32, len(elems))
		for i, e := range elems {
			out[i] = e.val.(uint32)
		}
		return out
	case TUint64:
		out := make([]uint64, len(elems))
		for i, e := range elems {
			out[i] = e.val.(uint64)
		}
		return out
	case TFloat32:
		out := make([]float32, len(elems))
		for i, e := range elems {
			out[i] = e.val.(float32)
		}
		return out
	case TFloat64:
		out := make([]float64, len(elems))
		for i, e := range elems {
			out[i] = e.val.(float64)
		}
		return out
	case TBoolean:
		out := make([]bool, len(elems))
		for i, e := range elems {
			out[i] = e.val.(bool)
		}
		return out
	case TString:
		out := make([]string, len(elems))
		for i, e := range elems {
			out[i] = e.val.(string)
		}
		return out
	default:
		// Fallback to []any for unknown element types.
		out := make([]any, len(elems))
		for i, e := range elems {
			out[i] = e.val
		}
		return out
	}
}

// TupleType returns tuple<...>.
func TupleType(elems []string) string {
	var sb strings.Builder
	sb.WriteString("tuple<")
	for i, e := range elems {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(e)
	}
	sb.WriteByte('>')
	return sb.String()
}

// AutoHostTypeNameOf derives a stable type name for a Go value.
func AutoHostTypeNameOf(v any) string {
	if v == nil {
		return TNull
	}
	return AutoHostTypeName(reflect.TypeOf(v))
}

// AutoHostTypeName derives a stable type name for an unregistered Go type
// (LANGUAGE.md §2.1 / §4.2.2).
func AutoHostTypeName(rt reflect.Type) string {
	if rt == nil {
		return TNull
	}
	// Pointer: prepend *
	if rt.Kind() == reflect.Pointer {
		return "*" + fullTypeName(rt.Elem())
	}
	return fullTypeName(rt)
}

func fullTypeName(rt reflect.Type) string {
	if pkg := rt.PkgPath(); pkg != "" {
		return pkg + "." + rt.Name()
	}
	// Unnamed types (slice, map, interface{}, etc.) fall back to String().
	return rt.String()
}
