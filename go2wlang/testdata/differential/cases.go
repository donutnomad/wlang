// Package differential holds the Go side of the go2wlang differential test.
// Every exported function takes no arguments; the test runs it natively and,
// translated by go2wlang, in the WLang interpreter, and compares the results.
package differential

import (
	"time"
)

type Point struct {
	X int
	Y int
}

type Box struct {
	Name  string
	Min   Point
	Tags  []string
	Count int
}

type Common struct {
	OrderID string
}

type Embedded struct {
	Common
	Amount int
}

// ---- range ----

func RangeIndexOnly() []int {
	xs := []string{"a", "b", "c"}
	out := []int{}
	for i := range xs {
		out = append(out, i)
	}
	return out
}

func RangeIndexReadsElement() []string {
	ids := []string{"x", "y", "z"}
	out := []string{}
	for i := range ids {
		out = append(out, ids[i])
	}
	return out
}

func RangeKeyValue() []int {
	xs := []int{5, 6, 7}
	out := []int{}
	for i, v := range xs {
		out = append(out, i*100+v)
	}
	return out
}

func RangeValueOnly() int {
	total := 0
	for _, v := range []int{1, 2, 3} {
		total += v
	}
	return total
}

func RangeNoVars() int {
	n := 0
	for range []string{"a", "b"} {
		n += 1
	}
	return n
}

func RangeInt() []int {
	out := []int{}
	for i := range 4 {
		out = append(out, i)
	}
	return out
}

func RangeIntNoVar() int {
	n := 0
	for range 3 {
		n += 2
	}
	return n
}

func RangeElementWriteVisible() []int {
	xs := []int{1, 2, 3}
	out := []int{}
	for i, v := range xs {
		if i+1 < len(xs) {
			xs[i+1] = xs[i+1] * 10
		}
		out = append(out, v)
	}
	return out
}

func RangeLengthFixed() int {
	xs := []int{1, 2}
	n := 0
	for range xs {
		xs = append(xs, 9)
		n += 1
	}
	return n*10 + len(xs)
}

func RangeMapSum() int {
	m := map[string]int{"a": 1, "bb": 2, "ccc": 3}
	total := 0
	for k, v := range m {
		total += v * len(k)
	}
	return total
}

func RangeMapKeys() []string {
	m := map[string]int{"b": 1, "a": 2, "c": 3}
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func RangeMapDeleteUnvisited() int {
	m := map[string]int{"a": 1, "b": 2}
	count := 0
	for k := range m {
		count += 1
		if k == "a" {
			delete(m, "b")
		} else {
			delete(m, "a")
		}
	}
	return count
}

func RangeString() []int {
	out := []int{}
	for i := range "héllo" {
		out = append(out, i)
	}
	return out
}

func RangeClosureCapture() []int {
	fns := []func() int{}
	for _, v := range []int{1, 2, 3} {
		fns = append(fns, func() int { return v })
	}
	out := []int{}
	for _, f := range fns {
		out = append(out, f())
	}
	return out
}

// ---- for ----

func ForBoundReevaluated() []int {
	queue := []int{1}
	out := []int{}
	for i := 0; i < len(queue); i++ {
		out = append(out, queue[i])
		if queue[i] < 4 {
			queue = append(queue, queue[i]+1)
		}
	}
	return out
}

func ForBodyWritesLoopVar() []int {
	out := []int{}
	for i := 0; i < 6; i++ {
		out = append(out, i)
		if i == 1 {
			i++
		}
	}
	return out
}

func ForLessEqual() int {
	total := 0
	for i := 1; i <= 3; i++ {
		total += i
	}
	return total
}

func ForDescending() []int {
	out := []int{}
	for i := 3; i >= 0; i-- {
		out = append(out, i)
	}
	return out
}

func ForStep() []int {
	out := []int{}
	for i := 0; i < 10; i += 3 {
		out = append(out, i)
	}
	return out
}

func ForMultiplyPost() []int {
	out := []int{}
	for i := 1; i < 100; i = i * 3 {
		out = append(out, i)
	}
	return out
}

func ForClosurePerIteration() []int {
	fns := []func() int{}
	for i := 0; i < 3; i++ {
		fns = append(fns, func() int { return i })
	}
	out := []int{}
	for _, f := range fns {
		out = append(out, f())
	}
	return out
}

func ForClosureSeesBodyWrite() []int {
	fns := []func() int{}
	for i := 0; i < 3; i++ {
		fns = append(fns, func() int { return i * 10 })
		i += 0
	}
	out := []int{}
	for _, f := range fns {
		out = append(out, f())
	}
	return out
}

func ForBreakContinue() []int {
	out := []int{}
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			continue
		}
		if i > 6 {
			break
		}
		out = append(out, i)
	}
	return out
}

func ForLabelledContinueOuter() []int {
	out := []int{}
outer:
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if j == 1 {
				continue outer
			}
			out = append(out, i*10+j)
		}
		out = append(out, 99)
	}
	return out
}

func ForLabelledBreakFromSwitch() []int {
	out := []int{}
loop:
	for _, v := range []int{1, 2, 3, 4} {
		switch {
		case v == 3:
			break loop
		case v == 1:
			continue
		}
		out = append(out, v)
	}
	return out
}

func ForBreakInsideSelectLikeSwitch() []string {
	out := []string{}
	for i := 0; i < 2; i++ {
		var v any = i
		switch v.(type) {
		case int:
			if i == 0 {
				break
			}
			out = append(out, "int")
		}
		out = append(out, "after")
	}
	return out
}

func ForClosureAfterBodyWrite() []int {
	fns := []func() int{}
	for i := 0; i < 3; i++ {
		fns = append(fns, func() int { return i })
		i += 1
	}
	out := []int{}
	for _, f := range fns {
		out = append(out, f())
	}
	return out
}

func ForLabelledBreak() []int {
	out := []int{}
outer:
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if j == 2 {
				continue outer
			}
			if i == 2 {
				break outer
			}
			out = append(out, i*10+j)
		}
	}
	return out
}

func ForLabelledInnermost() []int {
	out := []int{}
	for i := 0; i < 3; i++ {
	inner:
		for j := 0; j < 5; j++ {
			if j == i {
				break inner
			}
			out = append(out, i*10+j)
		}
	}
	return out
}

func Goto() int {
	n := 0
loop:
	n += 1
	if n < 3 {
		goto loop
	}
	return n
}

// ---- switch ----

func SwitchExpr() []string {
	out := []string{}
	for _, v := range []int{1, 2, 3, 4} {
		switch v {
		case 1:
			out = append(out, "one")
		case 2, 3:
			out = append(out, "two-three")
		default:
			out = append(out, "other")
		}
	}
	return out
}

func SwitchTagless() []string {
	out := []string{}
	for _, v := range []int{-1, 0, 5} {
		switch {
		case v < 0:
			out = append(out, "neg")
		case v == 0:
			out = append(out, "zero")
		default:
			out = append(out, "pos")
		}
	}
	return out
}

func SwitchDefaultFirst() []string {
	out := []string{}
	for _, v := range []string{"a", "b"} {
		switch v {
		default:
			out = append(out, "default")
		case "a":
			out = append(out, "A")
		}
	}
	return out
}

func SwitchTagEvaluatedOnce() []int {
	calls := 0
	next := func() int {
		calls += 1
		return calls
	}
	result := 0
	switch next() {
	case 5:
		result = 5
	case 6:
		result = 6
	case 1:
		result = 1
	}
	return []int{result, calls}
}

func SwitchCaseEvaluationOrder() []int {
	log := []int{}
	probe := func(v int) int {
		log = append(log, v)
		return v
	}
	switch 2 {
	case probe(1), probe(2), probe(3):
		log = append(log, 100)
	case probe(4):
		log = append(log, 200)
	}
	return log
}

func SwitchBreakInLoop() []int {
	out := []int{}
	for i := 0; i < 3; i++ {
		switch i {
		case 1:
			if i == 1 {
				break
			}
			out = append(out, 99)
		}
		out = append(out, i)
	}
	return out
}

func SwitchContinueInLoop() []int {
	out := []int{}
	for i := 0; i < 3; i++ {
		switch i {
		case 1:
			continue
		}
		out = append(out, i)
	}
	return out
}

func SwitchFallthrough() []string {
	out := []string{}
	switch 1 {
	case 1:
		out = append(out, "one")
		fallthrough
	case 2:
		out = append(out, "two")
	}
	return out
}

func SwitchInit() string {
	switch x := 3; {
	case x > 2:
		return "big"
	default:
		return "small"
	}
}

func TypeSwitch() []string {
	out := []string{}
	for _, v := range []any{1, "s", true, nil, 2.5} {
		switch x := v.(type) {
		case int:
			out = append(out, "int")
			_ = x
		case string:
			out = append(out, "string:"+x)
		case nil:
			out = append(out, "nil")
		case bool, float64:
			out = append(out, "bool-or-float")
		}
	}
	return out
}

// ---- defer ----

func DeferOrder() (out []string) {
	out = []string{}
	defer func() { out = append(out, "first") }()
	defer func() { out = append(out, "second") }()
	out = append(out, "body")
	return out
}

func DeferArgumentEvaluation() (out []int) {
	out = []int{}
	x := 1
	record := func(v int) { out = append(out, v) }
	defer record(x)
	x = 2
	defer func() { out = append(out, x) }()
	x = 3
	return out
}

func DeferNamedResult() (n int) {
	defer func() { n = n * 2 }()
	n = 3
	return n + 1
}

func Recover() (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = "recovered"
		}
	}()
	panic("boom")
}

// ---- integers ----

func IntDivisionNegative() []int {
	a, b := -7, 2
	return []int{a / b, -a / b, a / -b}
}

func IntModuloNegative() []int {
	a, b := -7, 3
	return []int{a % b, 7 % -3, 7 % 3}
}

func IntOverflowInt64() int64 {
	x := int64(9223372036854775807)
	x += 1
	return x
}

func IntOverflowInt32() int32 {
	var x int32 = 2147483647
	x = x + 1
	return x
}

func IntOverflowUint8() uint8 {
	var x uint8 = 250
	x += 10
	return x
}

func IntShift() []int {
	x := 1
	n := 3
	return []int{x << n, 256 >> n, x << 10}
}

func IntBitwise() []int {
	a, b := 12, 10
	return []int{a & b, a | b, a ^ b, a &^ b}
}

func IntToFloat() float64 {
	n := 7
	return float64(n) / 2
}

func FloatToIntTruncates() int {
	f := 7.9
	return int(f)
}

func NegativeFloat() float64 {
	f := 2.5
	return -f
}

func FloatDivision() float64 {
	a := 1.0
	return a / 4
}

func UntypedConstants() []float64 {
	const half = 1.0 / 2
	f := 3.0
	return []float64{f * half, f + 1, 10 / 4.0}
}

func IntConstantExpression() int {
	const big = 1 << 40
	return big >> 38
}

func DivideByZero() int {
	zero := 0
	return 10 / zero
}

// ---- strings ----

func StringIndexByte() int {
	s := "héllo"
	return int(s[1])
}

func StringLen() int {
	return len("héllo")
}

func StringSlice() string {
	s := "hello"
	return s[1:3]
}

func StringConcatCompare() []bool {
	a := "ab"
	b := a + "c"
	return []bool{b == "abc", a < b, "b" > b, a != b}
}

func StringBytes() []byte {
	return []byte("hi")
}

func StringRunes() int {
	return len([]rune("héllo"))
}

// ---- slices ----

func SliceNilJSON() []int {
	var xs []int
	return xs
}

func SliceEmptyJSON() []int {
	return []int{}
}

func SliceAliasWrite() []int {
	xs := []int{1, 2, 3}
	ys := xs
	ys[0] = 100
	return xs
}

func SliceAppendIntoSpareCapacity() []int {
	xs := []int{1, 2, 3, 4}
	ys := xs[:2]
	ys = append(ys, 9)
	return xs
}

func SliceAppendExpression() []int {
	xs := []int{1}
	ys := append(xs, 2)
	return ys
}

func SliceNilAppend() []int {
	var xs []int
	n := len(xs)
	xs = append(xs, n+1)
	return xs
}

func SliceSubsliceShares() []int {
	xs := []int{1, 2, 3, 4}
	ys := xs[1:3]
	ys[0] = 20
	return xs
}

func SliceCopy() []int {
	dst := []int{0, 0, 0}
	src := []int{7, 8}
	n := copy(dst, src)
	dst = append(dst, n)
	return dst
}

func SliceFullExpression() int {
	xs := []int{1, 2, 3, 4}
	ys := xs[0:2:2]
	return cap(ys)
}

func SliceAppendMany() []int {
	xs := []int{}
	xs = append(xs, 1, 2, 3)
	return xs
}

func SliceAppendSpread() []int {
	xs := []int{1}
	ys := []int{2, 3}
	xs = append(xs, ys...)
	return xs
}

func SliceOfStructValueCopy() []Point {
	ps := []Point{{X: 1, Y: 1}}
	p := ps[0]
	p.X = 9
	return ps
}

// ---- maps ----

func MapMissingKeyZero() []int {
	m := map[string]int{"a": 1}
	return []int{m["a"], m["missing"]}
}

func MapCommaOk() []bool {
	m := map[string]int{"a": 0}
	_, ok1 := m["a"]
	_, ok2 := m["b"]
	return []bool{ok1, ok2}
}

func MapCommaOkAssignOuter() int {
	m := map[string]int{"a": 5}
	v := 0
	ok := false
	if len(m) > 0 {
		v, ok = m["a"]
	}
	if !ok {
		return -1
	}
	return v
}

func MapNilRead() int {
	var m map[string]int
	return m["x"] + len(m)
}

func MapNilWrite() int {
	var m map[string]int
	m["x"] = 1
	return 0
}

func MapLen() int {
	m := map[string]bool{"a": true, "b": false}
	delete(m, "a")
	return len(m)
}

func MapAliasWrite() int {
	m := map[string]int{}
	n := m
	n["k"] = 4
	return m["k"]
}

func MapNilJSON() map[string]int {
	var m map[string]int
	return m
}

// ---- structs ----

func StructCopyOnAssign() []Point {
	a := Point{X: 1, Y: 2}
	b := a
	b.X = 10
	return []Point{a, b}
}

func StructCopyOnCall() Point {
	a := Point{X: 1, Y: 2}
	mutate := func(p Point) Point {
		p.X = 99
		return p
	}
	_ = mutate(a)
	return a
}

func StructNestedCopy() Box {
	a := Box{Name: "a", Min: Point{X: 1}}
	b := a
	b.Min.X = 5
	return a
}

func StructCompare() []bool {
	a := Point{X: 1, Y: 2}
	b := Point{X: 1, Y: 2}
	c := Point{X: 2, Y: 2}
	return []bool{a == b, a == c, a != c}
}

func StructZeroValue() Box {
	var b Box
	return b
}

func StructPointerShares() int {
	p := &Point{X: 1}
	q := p
	q.X = 7
	return p.X
}

func StructNewPointer() int {
	p := new(Point)
	p.X = 3
	return p.X
}

func EmbeddedPromotedField() string {
	e := Embedded{Common: Common{OrderID: "o-1"}, Amount: 3}
	return e.OrderID
}

func IndexedFieldRead() int {
	ps := []Point{{X: 4, Y: 5}}
	X := 100
	_ = X
	return ps[0].X
}

func IndexedFieldWrite() []Point {
	ps := []Point{{X: 4, Y: 5}}
	ps[0].X = 8
	return ps
}

func CallResultField() int {
	mk := func() Point { return Point{X: 6} }
	return mk().X
}

// ---- assignment ----

func Swap() []int {
	a, b := 1, 2
	a, b = b, a
	return []int{a, b}
}

func ParallelAssignEvaluatesFirst() []int {
	xs := []int{1, 2, 3}
	i := 0
	i, xs[i] = 1, 9
	return []int{xs[0], xs[1], i}
}

func TupleAssignOuterVars() []int {
	pair := func() (int, int) { return 3, 4 }
	a, b := 0, 0
	if true {
		a, b = pair()
	}
	return []int{a, b}
}

func TypeAssertOkAssignOuter() []any {
	var v any = "x"
	s := ""
	ok := false
	if v != nil {
		s, ok = v.(string)
	}
	return []any{s, ok}
}

func ShadowingInBlock() []int {
	x := 1
	if true {
		x := 2
		x += 1
		_ = x
	}
	return []int{x}
}

func ShadowingTupleDefine() []int {
	pair := func() (int, int) { return 7, 8 }
	a := 1
	if true {
		a, b := pair()
		_ = b
		_ = a
	}
	return []int{a}
}

func IncDecStatements() []int {
	i := 5
	i++
	j := 5
	j--
	return []int{i, j}
}

func CompoundAssignOnField() Point {
	p := Point{X: 1}
	p.X += 4
	p.Y -= 2
	return p
}

// ---- boolean logic ----

func ShortCircuitAnd() []int {
	log := []int{}
	f := func(v bool, tag int) bool {
		log = append(log, tag)
		return v
	}
	if f(false, 1) && f(true, 2) {
		log = append(log, 99)
	}
	if f(true, 3) || f(true, 4) {
		log = append(log, 100)
	}
	return log
}

// ---- functions ----

func VariadicClosure() int {
	sum := func(xs ...int) int {
		total := 0
		for _, x := range xs {
			total += x
		}
		return total
	}
	return sum(1, 2, 3)
}

func ClosureCounter() []int {
	n := 0
	inc := func() int {
		n += 1
		return n
	}
	inc()
	inc()
	return []int{inc(), n}
}

func NamedResultsBareReturn() (a int, b string) {
	a = 4
	b = "x"
	return
}

// ---- constants ----

const (
	StatusPending = iota
	StatusDone
	StatusFailed
)

const Retries = 3

func IotaConstants() []int {
	return []int{StatusPending, StatusDone, StatusFailed}
}

func PackageConstant() int {
	return Retries * 2
}

func LocalConstant() int {
	const n = 4
	return n + 1
}

// ---- time.Duration ----

func DurationArithmetic() []int64 {
	d := 2 * time.Hour
	n := 3
	e := time.Duration(n) * time.Minute
	return []int64{int64(d), int64(e), int64(d + e)}
}

func DurationConversionFromFloat() int64 {
	f := 1.5
	return int64(time.Duration(f * float64(time.Second)))
}

// ---- type assertions ----

func TypeAssertField() int {
	var v any = Point{X: 3}
	return v.(Point).X
}

func TypeAssertCommaOk() []bool {
	var v any = "s"
	_, ok1 := v.(string)
	_, ok2 := v.(int)
	return []bool{ok1, ok2}
}

func TypeAssertPanics() int {
	var v any = "s"
	return v.(int)
}

// ---- panics ----

func PanicString() int {
	panic("boom")
}

func IndexOutOfRange() int {
	xs := []int{1}
	i := 3
	return xs[i]
}

// ---- additional sweep ----

func StructRangeVarIsCopy() []Point {
	ps := []Point{{X: 1}, {X: 2}}
	for _, p := range ps {
		p.X = 9
		_ = p
	}
	return ps
}

func StructAppendThenMutate() []Point {
	a := Point{X: 1}
	xs := []Point{}
	xs = append(xs, a)
	a.X = 9
	return xs
}

func StructMapValueCopy() Point {
	p := Point{X: 1}
	m := map[string]Point{}
	m["a"] = p
	p.X = 5
	return m["a"]
}

func StructFieldStructCopy() Box {
	p := Point{X: 1}
	b := Box{Min: p}
	p.X = 7
	return b
}

func StructSliceElementCopy() []Point {
	ps := []Point{{X: 1, Y: 1}}
	p := ps[0]
	p.X = 9
	ps[0] = p
	q := ps[0]
	q.Y = 7
	return ps
}

func IfInitShadow() []int {
	m := map[string]int{"a": 2}
	v := 100
	if v, ok := m["a"]; ok {
		v += 1
		_ = v
	}
	return []int{v}
}

func TupleRedeclareReuse() []int {
	pair := func(n int) (int, int) { return n, n * 2 }
	a, b := pair(1)
	c, b := pair(5)
	return []int{a, b, c}
}

func TupleAssignToField() Point {
	pair := func() (int, int) { return 3, 4 }
	p := Point{}
	p.X, p.Y = pair()
	return p
}

func IntConstDivision() []int {
	x := 10 / 4
	y := -7 / 2
	return []int{x, y}
}

func SwitchNoMatch() string {
	out := "none"
	switch 5 {
	case 1:
		out = "one"
	}
	return out
}

func DeferInLoop() (out []int) {
	out = []int{}
	for i := 0; i < 3; i++ {
		defer func() { out = append(out, i) }()
	}
	return out
}

func NewScalarPointer() int {
	p := new(int)
	*p = 4
	return *p
}

type Pair[T any] struct {
	A T
	B T
}

func GenericStruct() Pair[int] {
	return Pair[int]{A: 1, B: 2}
}

func MapRangeValueWrite() map[string]int {
	m := map[string]int{"a": 1, "b": 2}
	for k, v := range m {
		m[k] = v * 10
	}
	return m
}

func MinBuiltin() int {
	a := 3
	return min(a, 1)
}

func MinConstantFolded() int {
	return min(3, 1, 2)
}

func FloatDivideByZero() float64 {
	zero := 0.0
	return 1 / zero
}
