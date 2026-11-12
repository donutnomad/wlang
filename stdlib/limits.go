package stdlib

import (
	"encoding"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/donutnomad/wlang/types"
)

// Guards for the bound functions whose result can be much larger than their
// arguments (registry.FuncSpec.Guard). Each computes the result size from the
// arguments, exactly or as an upper bound, and refuses a result over the
// execution's limits before the function allocates it. The bounds depend only
// on the argument values, so the same call is refused on every host.
//
// Functions whose result is at most a small constant factor of their
// arguments (ToUpper and ToLower: 3x for invalid UTF-8; Trim*, Sort, Distinct)
// have no guard: the registry checks every host result after the call.

func guardRepeat(l types.Limits, args []any) error {
	s, count := args[0].(string), args[1].(int)
	if count <= 0 || len(s) == 0 {
		return nil
	}
	if l.MaxStringBytes > 0 && len(s) > l.MaxStringBytes/count {
		return l.CheckString(int64(l.MaxStringBytes) + 1)
	}
	return l.CheckString(int64(len(s)) * int64(count))
}

// replacedLen is the length of strings.Replace(s, old, new, n).
func replacedLen(s, old, new string, n int) int64 {
	if len(new) <= len(old) {
		return int64(len(s))
	}
	k := strings.Count(s, old)
	if n >= 0 && n < k {
		k = n
	}
	return int64(len(s)) + int64(k)*int64(len(new)-len(old))
}

func guardReplace(l types.Limits, args []any) error {
	return l.CheckString(replacedLen(args[0].(string), args[1].(string), args[2].(string), args[3].(int)))
}

func guardReplaceAll(l types.Limits, args []any) error {
	return l.CheckString(replacedLen(args[0].(string), args[1].(string), args[2].(string), -1))
}

func guardJoin(l types.Limits, args []any) error {
	elems, sep := args[0].([]string), args[1].(string)
	if len(elems) == 0 {
		return nil
	}
	n := int64(len(sep)) * int64(len(elems)-1)
	for _, elem := range elems {
		n += int64(len(elem))
	}
	return l.CheckString(n)
}

func guardSplit(l types.Limits, args []any) error {
	s, sep := args[0].(string), args[1].(string)
	if sep == "" {
		return l.CheckArray(int64(utf8.RuneCountInString(s)))
	}
	return l.CheckArray(int64(strings.Count(s, sep)) + 1)
}

func guardFields(l types.Limits, args []any) error {
	var n int64
	for range strings.FieldsSeq(args[0].(string)) {
		n++
	}
	return l.CheckArray(n)
}

// guardFormatFloat bounds strconv.FormatFloat: at most 310 digits before the
// point (float64 'f'), an exponent, and prec digits after it.
func guardFormatFloat(l types.Limits, args []any) error {
	return l.CheckString(int64(max(args[2].(int), 0)) + 330)
}

func guardSprintf(l types.Limits, args []any) error {
	return l.CheckString(sprintfBound(l, args[0].(string), args[1:]))
}

func guardSprint(l types.Limits, args []any) error {
	return l.CheckString(sprintBound(l, args, false))
}

func guardSprintln(l types.Limits, args []any) error {
	return l.CheckString(sprintBound(l, args, true))
}

// guardToString bounds to.String, which formats anything other than a
// string, bool or number with fmt.Sprint.
func guardToString(l types.Limits, args []any) error {
	return l.CheckString(sprintBound(l, args, false))
}

func guardJSON(l types.Limits, args []any) error {
	s := newSizer(l)
	s.json(reflect.ValueOf(args[0]), 0)
	return l.CheckString(s.n)
}

func guardFlatten(l types.Limits, args []any) error {
	rv := reflect.ValueOf(args[0])
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil
	}
	var n int64
	for i := 0; i < rv.Len(); i++ {
		el := rv.Index(i)
		if el.Kind() == reflect.Interface {
			el = el.Elem()
		}
		if el.IsValid() && (el.Kind() == reflect.Slice || el.Kind() == reflect.Array) {
			n += int64(el.Len())
		} else {
			n++
		}
	}
	return l.CheckArray(n)
}

// fmtMaxNum is the largest width or precision fmt parses from a format
// (larger numbers are rejected as bad widths): fmt stops accumulating digits
// once the value passes 1e6.
const fmtMaxNum = 1e7 + 9

// sizerMaxDepth caps how deep the size walk follows nested values. A deeper
// value, including a map or array that contains itself, counts as over the
// limit, which also keeps fmt and json from recursing into a cycle.
const sizerMaxDepth = 10_000

// sizer adds up an upper bound of an output size and stops counting once the
// bound passes the limit, so walking a large or self-referencing value costs
// at most about limit steps.
type sizer struct {
	limit int64
	n     int64
}

func newSizer(l types.Limits) *sizer {
	limit := int64(l.MaxStringBytes)
	if limit <= 0 {
		limit = 1 << 62
	}
	return &sizer{limit: limit}
}

func (s *sizer) over() bool { return s.n > s.limit }

func (s *sizer) add(n int64) {
	if !s.over() {
		s.n += n
	}
}

// fmtVerb is one formatting directive.
type fmtVerb struct {
	verb  rune
	sharp bool
	width int64
	prec  int64
}

// sprintfBound bounds the length of fmt.Sprintf(format, args...). Every
// directive takes one argument (explicit [n] indexes may take one again);
// arguments left over are printed as %!(EXTRA ...).
func sprintfBound(l types.Limits, format string, args []any) int64 {
	s := newSizer(l)
	s.add(int64(len(format)))
	argNum := 0
	for i := 0; i < len(format) && !s.over(); {
		if format[i] != '%' {
			i++
			continue
		}
		i++
		v := fmtVerb{}
		for i < len(format) && strings.IndexByte("+-# 0", format[i]) >= 0 {
			v.sharp = v.sharp || format[i] == '#'
			i++
		}
		i, argNum = fmtArgIndex(format, i, argNum, len(args))
		if i < len(format) && format[i] == '*' {
			v.width, argNum = fmtStarArg(args, argNum)
			i++
		} else {
			v.width, i = fmtNum(format, i)
		}
		if i < len(format) && format[i] == '.' {
			i++
			i, argNum = fmtArgIndex(format, i, argNum, len(args))
			if i < len(format) && format[i] == '*' {
				v.prec, argNum = fmtStarArg(args, argNum)
				i++
			} else {
				v.prec, i = fmtNum(format, i)
			}
		}
		i, argNum = fmtArgIndex(format, i, argNum, len(args))
		if i >= len(format) {
			s.add(16) // %!(NOVERB)
			break
		}
		r, size := utf8.DecodeRuneInString(format[i:])
		i += size
		v.verb = r
		if r == '%' {
			continue
		}
		// %!verb(MISSING), %!verb(BADWIDTH) and %!verb(type=value) wrappers.
		s.add(48)
		if argNum >= len(args) {
			continue
		}
		s.fmtArg(reflect.ValueOf(args[argNum]), v)
		argNum++
	}
	for _, arg := range args[min(argNum, len(args)):] {
		s.add(48)
		s.fmtArg(reflect.ValueOf(arg), fmtVerb{verb: 'v'})
	}
	return s.n
}

// sprintBound bounds fmt.Sprint / Sprintln of args: %v of each argument, a
// space between arguments and the newline.
func sprintBound(l types.Limits, args []any, newline bool) int64 {
	s := newSizer(l)
	s.add(int64(max(len(args)-1, 0)))
	if newline {
		s.add(1)
	}
	for _, arg := range args {
		s.fmtArg(reflect.ValueOf(arg), fmtVerb{verb: 'v'})
	}
	return s.n
}

// fmtNum parses a width or precision the way fmt does, capped at fmtMaxNum.
func fmtNum(format string, i int) (int64, int) {
	var n int64
	for i < len(format) && format[i] >= '0' && format[i] <= '9' {
		if n <= fmtMaxNum {
			n = n*10 + int64(format[i]-'0')
		}
		i++
	}
	return min(n, fmtMaxNum), i
}

// fmtArgIndex skips an explicit [n] argument index and moves argNum to it.
func fmtArgIndex(format string, i, argNum, numArgs int) (int, int) {
	if i >= len(format) || format[i] != '[' {
		return i, argNum
	}
	end := strings.IndexByte(format[i:], ']')
	if end < 0 {
		return len(format), argNum
	}
	n, next := fmtNum(format, i+1)
	if next == i+end && n >= 1 && n <= int64(numArgs) {
		argNum = int(n - 1)
	}
	return i + end + 1, argNum
}

// fmtStarArg reads a * width or precision from the argument list.
func fmtStarArg(args []any, argNum int) (int64, int) {
	if argNum >= len(args) {
		return 0, argNum
	}
	rv := reflect.ValueOf(args[argNum])
	var n int64
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n = rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n = int64(min(rv.Uint(), fmtMaxNum))
	}
	if n < 0 {
		n = -max(n, -fmtMaxNum) // a negative width means the - flag
	}
	return min(n, fmtMaxNum), argNum + 1
}

// fmtArg adds a bound of one argument formatted with v.
func (s *sizer) fmtArg(rv reflect.Value, v fmtVerb) {
	s.fmtValue(rv, v, 0)
}

// leaf adds one scalar of at most n bytes: precision adds digits (or zero
// padding) to numbers, width pads every scalar of a composite value, and a
// verb that does not fit the scalar wraps it as %!verb(type=value) (wrap).
func (s *sizer) leaf(n int64, v fmtVerb, wrap int64) {
	if v.verb == 'v' {
		wrap = 0
	}
	s.add(max(n+v.prec, v.width) + wrap)
}

// text adds a string-like scalar. %x, %X and %q (and %#v, which quotes)
// expand each byte to at most 5 bytes ("\xff", "% #x" gives "0xff ").
func (s *sizer) text(n int64, v fmtVerb, wrap int64) {
	switch {
	case v.verb == 'x' || v.verb == 'X' || v.verb == 'q' || (v.verb == 'v' && v.sharp):
		n = 5*n + 2
	}
	if strings.ContainsRune("vsqxX", v.verb) {
		wrap = 0
	}
	s.add(max(n, v.width) + wrap)
}

func (s *sizer) fmtValue(rv reflect.Value, v fmtVerb, depth int) {
	if s.over() {
		return
	}
	if depth > sizerMaxDepth {
		s.n = s.limit + 1
		return
	}
	if !rv.IsValid() {
		s.leaf(8, v, 16) // <nil>, %!d(<nil>)
		return
	}
	if rv.Kind() == reflect.Interface {
		s.fmtValue(rv.Elem(), v, depth+1)
		return
	}
	wrap := int64(len(rv.Type().String())) + 6
	if rv.CanInterface() && (rv.Kind() != reflect.Pointer || !rv.IsNil()) {
		// The methods fmt calls instead of walking the value: Format
		// (big.Int, big.Float), GoString for %#v, Error and String for the
		// string verbs.
		sharpV := v.sharp && v.verb == 'v'
		switch x := rv.Interface().(type) {
		case *big.Int:
			s.leaf(int64(x.BitLen())+8, v, wrap)
			return
		case *big.Float:
			exp := int64(x.MantExp(nil))
			s.leaf(max(exp, -exp)+int64(x.Prec())+32, v, wrap)
			return
		case fmt.GoStringer:
			if sharpV {
				s.text(int64(len(x.GoString())), v, wrap)
				return
			}
		}
		if !sharpV && strings.ContainsRune("vsqxX", v.verb) {
			switch x := rv.Interface().(type) {
			case error:
				s.text(int64(len(x.Error())), v, wrap)
				return
			case fmt.Stringer:
				s.text(int64(len(x.String())), v, wrap)
				return
			}
		}
	}
	if v.sharp && v.verb == 'v' {
		s.add(int64(len(rv.Type().String())) + 4)
	}
	switch rv.Kind() {
	case reflect.Bool:
		s.leaf(8, v, wrap)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		s.leaf(72, v, wrap) // %b of a 64-bit value, sign and prefix
	case reflect.Uint8:
		s.leaf(16, v, wrap)
	case reflect.Float32, reflect.Float64:
		s.leaf(340, v, wrap) // %f of 1e308
	case reflect.Complex64, reflect.Complex128:
		s.leaf(2*340+8, v, wrap)
	case reflect.String:
		s.text(int64(rv.Len()), v, wrap)
	case reflect.Slice, reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Uint8 && strings.ContainsRune("sqxX", v.verb) {
			s.text(int64(rv.Len()), v, wrap)
			return
		}
		s.add(2)
		for i := 0; i < rv.Len() && !s.over(); i++ {
			s.add(2)
			s.fmtValue(rv.Index(i), v, depth+1)
		}
	case reflect.Map:
		s.add(6)
		iter := rv.MapRange()
		for iter.Next() && !s.over() {
			s.add(4)
			s.fmtValue(iter.Key(), v, depth+1)
			s.fmtValue(iter.Value(), v, depth+1)
		}
	case reflect.Struct:
		s.add(2)
		for i := 0; i < rv.NumField() && !s.over(); i++ {
			s.add(int64(len(rv.Type().Field(i).Name)) + 4)
			s.fmtValue(rv.Field(i), v, depth+1)
		}
	case reflect.Pointer:
		// fmt follows a pointer only at the top level (&{...}); deeper
		// pointers print as an address.
		if depth == 0 && !rv.IsNil() {
			s.add(1)
			s.fmtValue(rv.Elem(), v, depth+1)
			return
		}
		s.leaf(24, v, wrap)
	default:
		s.leaf(24, v, wrap) // chan, func, unsafe pointer: an address
	}
}

// jsonString bounds the length of str encoded as a JSON string by
// encoding/json: <, >, &, U+2028, U+2029, control characters and invalid
// bytes take six bytes; quote, backslash, \n, \r and \t two. Go releases
// differ in a few of these (\b and \f in two bytes, invalid bytes as a raw
// U+FFFD), always in fewer bytes than counted here.
func jsonString(str string) int64 {
	n := int64(2)
	for i := 0; i < len(str); {
		b := str[i]
		if b < utf8.RuneSelf {
			switch {
			case b == '"' || b == '\\' || b == '\n' || b == '\r' || b == '\t':
				n += 2
			case b < 0x20 || b == '<' || b == '>' || b == '&':
				n += 6
			default:
				n++
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(str[i:])
		if (r == utf8.RuneError && size == 1) || r == ' ' || r == ' ' {
			n += 6
		} else {
			n += int64(size)
		}
		i += size
	}
	return n
}

var (
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

// json adds a bound of rv encoded by encoding/json.
func (s *sizer) json(rv reflect.Value, depth int) {
	if s.over() {
		return
	}
	if depth > sizerMaxDepth {
		s.n = s.limit + 1
		return
	}
	if !rv.IsValid() {
		s.add(4)
		return
	}
	if rv.CanInterface() {
		switch x := rv.Interface().(type) {
		case *big.Int:
			if x != nil {
				s.add(int64(x.BitLen()) + 4)
				return
			}
		case *big.Float:
			if x != nil {
				s.add(int64(x.Prec()) + 40)
				return
			}
		case *big.Rat:
			if x != nil {
				s.add(int64(x.Num().BitLen()) + int64(x.Denom().BitLen()) + 8)
				return
			}
		}
		if rv.Kind() != reflect.Pointer || !rv.IsNil() {
			if rv.Type().Implements(jsonMarshalerType) {
				if out, err := rv.Interface().(json.Marshaler).MarshalJSON(); err == nil {
					s.add(int64(len(out)))
				}
				return
			}
			if rv.Type().Implements(textMarshalerType) {
				if out, err := rv.Interface().(encoding.TextMarshaler).MarshalText(); err == nil {
					s.add(jsonString(string(out)))
				}
				return
			}
		}
	}
	switch rv.Kind() {
	case reflect.Bool:
		s.add(5)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		s.add(20)
	case reflect.Float32, reflect.Float64:
		s.add(32)
	case reflect.String:
		s.add(jsonString(rv.String()))
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			s.add(int64(rv.Len()+2)/3*4 + 2) // base64
			return
		}
		s.add(2)
		for i := 0; i < rv.Len() && !s.over(); i++ {
			s.add(1)
			s.json(rv.Index(i), depth+1)
		}
	case reflect.Map:
		s.add(2)
		iter := rv.MapRange()
		for iter.Next() && !s.over() {
			key := iter.Key()
			if key.Kind() == reflect.String {
				s.add(jsonString(key.String()) + 2)
			} else {
				s.add(24)
			}
			s.json(iter.Value(), depth+1)
		}
	case reflect.Struct:
		s.add(2)
		for i := 0; i < rv.NumField() && !s.over(); i++ {
			field := rv.Type().Field(i)
			if !field.IsExported() && !field.Anonymous {
				continue
			}
			s.add(jsonString(field.Name) + int64(len(field.Tag.Get("json"))) + 2)
			s.json(rv.Field(i), depth+1)
		}
	case reflect.Interface, reflect.Pointer:
		if rv.IsNil() {
			s.add(4)
			return
		}
		s.json(rv.Elem(), depth+1)
	}
}
