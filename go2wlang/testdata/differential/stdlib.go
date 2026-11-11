package differential

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	str "strings"
	"time"
)

// ---- strings ----

func StringsCase() []string {
	return []string{
		strings.ToUpper("order-ä"), strings.ToLower("ÀB c"), strings.ToUpper(""),
		strings.TrimSpace("  \t x y \n"), strings.TrimSpace(""),
		strings.Trim("--x--", "-"), strings.TrimLeft("--x--", "-"), strings.TrimRight("--x--", "-"),
		strings.TrimPrefix("order-1", "order-"), strings.TrimPrefix("x", "order-"),
		strings.TrimSuffix("a.json", ".json"),
		strings.Replace("aaaa", "a", "b", 2), strings.Replace("aaaa", "a", "b", -1), strings.ReplaceAll("a-b-c", "-", ""),
		strings.Repeat("ab", 3), strings.Repeat("x", 0),
		strings.Join([]string{"a", "b", "c"}, ", "), strings.Join(nil, ","),
	}
}

func StringsPredicates() []bool {
	return []bool{
		strings.HasPrefix("order-1", "order"), strings.HasPrefix("", ""), strings.HasSuffix("a.json", ".json"),
		strings.Contains("seafood", "foo"), strings.Contains("", ""), strings.Contains("abc", "d"),
		strings.EqualFold("Go", "GO"), strings.EqualFold("ß", "SS"),
	}
}

func StringsIndexes() []int {
	return []int{
		strings.Index("chicken", "ken"), strings.Index("chicken", "dmr"), strings.Index("héllo", "l"),
		strings.LastIndex("go gopher", "go"), strings.LastIndex("x", ""),
		strings.Count("cheese", "e"), strings.Count("five", ""),
	}
}

func StringsSplitFields() [][]string {
	parts := strings.Split("a,b,,c", ",")
	empty := strings.Split("", ",")
	chars := strings.Split("abc", "")
	fields := strings.Fields("  foo bar\tbaz  ")
	none := strings.Fields("   ")
	parts[0] = "A"
	return [][]string{parts, empty, chars, fields, none, []string{strconv.Itoa(len(none))}}
}

func StringsSplitIndexAndAppend() string {
	parts := strings.Split("x=1;y=2", ";")
	parts = append(parts, "z=3")
	out := ""
	for i, part := range parts {
		kv := strings.Split(part, "=")
		out += strconv.Itoa(i) + kv[0] + kv[1]
	}
	return out
}

func StringsAliasedImport() string {
	return str.ToUpper("alias")
}

func StringsRepeatNegative() string {
	n := -1
	return strings.Repeat("x", n)
}

func StringsBuilder() string {
	var b strings.Builder
	b.WriteString("x")
	return b.String()
}

func StringsTitle() string {
	return strings.Title("hello")
}

func StringsFunctionValue() string {
	upper := strings.ToUpper
	return upper("x")
}

// ---- strconv ----

func StrconvFormat() []string {
	return []string{
		strconv.Itoa(0), strconv.Itoa(-42), strconv.Itoa(math.MaxInt64),
		strconv.FormatInt(-255, 16), strconv.FormatInt(math.MinInt64, 10), strconv.FormatInt(35, 36),
		strconv.FormatBool(true), strconv.FormatBool(false),
		strconv.FormatFloat(3.14159, 'f', 2, 64), strconv.FormatFloat(1e21, 'g', -1, 64),
		strconv.FormatFloat(0.1, 'e', -1, 64), strconv.FormatFloat(-0.5, 'f', 0, 64),
		strconv.FormatFloat(2.5, 'f', 0, 64), strconv.FormatFloat(1.0/3.0, 'g', 5, 32),
	}
}

func StrconvAtoi() []any {
	n, err := strconv.Atoi("42")
	m, err2 := strconv.Atoi("-7")
	_, bad := strconv.Atoi("4x2")
	_, empty := strconv.Atoi("")
	_, big := strconv.Atoi("99999999999999999999")
	return []any{n, err == nil, m, err2 == nil, bad.Error(), empty.Error(), big.Error()}
}

func StrconvParse() []any {
	i, err := strconv.ParseInt("-ff", 16, 64)
	small, rangeErr := strconv.ParseInt("300", 10, 8)
	f, ferr := strconv.ParseFloat("1e-3", 64)
	_, fbad := strconv.ParseFloat("1.2.3", 64)
	b, berr := strconv.ParseBool("TRUE")
	_, bbad := strconv.ParseBool("yes")
	return []any{i, err == nil, small, rangeErr.Error(), f, ferr == nil, fbad.Error(), b, berr == nil, bbad.Error()}
}

func StrconvVarTuple() []any {
	var n, err = strconv.Atoi("12")
	return []any{n, err == nil}
}

func StrconvErrorFlow() string {
	if _, err := strconv.Atoi("nope"); err != nil {
		return "failed: " + err.Error()
	}
	return "ok"
}

func StrconvQuote() string {
	return strconv.Quote("x")
}

// ---- fmt ----

type Status string

type Level int

type Named struct{ Name string }

func (n Named) String() string { return n.Name }

type Celsius float64

func (c Celsius) String() string { return "C" }

func FmtSprintfScalars() []string {
	var small int8 = -5
	var code uint16 = 255
	var ratio float32 = 0.25
	return []string{
		fmt.Sprintf("order-%d", 42), fmt.Sprintf("%05d|%-4d|%+d|% d", 42, 7, 3, 9),
		fmt.Sprintf("%v %v %v %v", 1, "s", true, 2.5), fmt.Sprintf("%+v", -3),
		fmt.Sprintf("%s|%10s|%-6s|", "go", "right", "left"),
		fmt.Sprintf("%x %X %x %#x %o %b", 255, 255, "hi", 255, 8, 5),
		fmt.Sprintf("%t %v", false, true),
		fmt.Sprintf("%05.2f|%.3e|%g|%G|%8.3f|%F", 3.14159, 123456.789, 1e21, 1e-7, -2.5, 1.5),
		fmt.Sprintf("%v %v %v", small, code, ratio),
		fmt.Sprintf("%d%%", 50),
		fmt.Sprintf("plain"),
		fmt.Sprintf("%v|%s", Status("paid"), Status("open")), fmt.Sprintf("%d", Level(3)),
		fmt.Sprintf("%v", 1.0/3.0), fmt.Sprintf("%.2f", 2.675),
		fmt.Sprintf("%s", "ünï"), fmt.Sprintf("%x", "ü"),
	}
}

func FmtSprintAndLn() []string {
	return []string{
		fmt.Sprint("a", 1, 2, "b", true, 3.5), fmt.Sprint(), fmt.Sprint(1, 2),
		fmt.Sprintln("a", 1, true), fmt.Sprintln(),
	}
}

func FmtErrorValues() []string {
	_, err := strconv.Atoi("x")
	wrapped := fmt.Errorf("parse id %s failed: %w", "x", err)
	var none error
	plain := fmt.Errorf("code %d", 7)
	return []string{
		fmt.Sprintf("%v", err), fmt.Sprintf("%s", err), fmt.Sprintf("%v", none), fmt.Sprint(none),
		wrapped.Error(), plain.Error(), fmt.Sprintf("[%v]", plain),
	}
}

func FmtErrorfQuoteVerb() string {
	return fmt.Errorf("parse %q", "x").Error()
}

func FmtErrorfWrapped() []any {
	_, err := strconv.Atoi("x")
	wrapped := fmt.Errorf("outer: %w", err)
	return []any{wrapped.Error(), wrapped != nil}
}

func FmtQuoteVerb() string {
	return fmt.Sprintf("%q", "x")
}

func FmtTypeVerb() string {
	return fmt.Sprintf("%T", 1)
}

func FmtVerbMismatch() string {
	return fmt.Sprintf("%d", "x")
}

func FmtCountMismatch() string {
	return fmt.Sprintf("%d %d", 1)
}

func FmtNonConstantFormat() string {
	format := "%d"
	return fmt.Sprintf(format, 1)
}

func FmtStructOperand() string {
	return fmt.Sprintf("%v", Point{X: 1})
}

func FmtStringerOperand() string {
	return fmt.Sprintf("%v", Named{Name: "x"})
}

func FmtStringerScalar() string {
	return fmt.Sprintf("%v", Celsius(1))
}

func FmtDurationOperand() string {
	return fmt.Sprintf("%v", 2*time.Second)
}

func FmtSliceOperand() string {
	return fmt.Sprint([]int{1})
}

func FmtAnyOperand() string {
	var v any = 1
	return fmt.Sprintf("%v", v)
}

func FmtStarWidth() string {
	return fmt.Sprintf("%*d", 3, 1)
}

func FmtSprintfWrapVerb() string {
	_, err := strconv.Atoi("x")
	return fmt.Sprintf("%w", err)
}

// ---- math ----

func MathFunctions() []float64 {
	return []float64{
		math.Abs(-2.5), math.Abs(0), math.Max(1.5, -3), math.Min(1.5, -3), math.Max(-0.0, 0),
		math.Floor(-1.5), math.Ceil(-1.5), math.Round(2.5), math.Round(-2.5), math.Round(0.49999999999999994),
		math.Trunc(-1.9), math.Sqrt(2), math.Sqrt(0), math.Mod(7, 3), math.Mod(-7, 3), math.Mod(7.5, 2),
	}
}

func MathSpecialValues() []bool {
	inf := math.Inf(1)
	nan := math.NaN()
	return []bool{
		math.IsInf(inf, 1), math.IsInf(math.Inf(-1), -1), math.IsInf(1, 0), inf > math.MaxFloat64,
		math.IsNaN(nan), nan == nan, nan != nan, math.IsNaN(math.Sqrt(-1)), math.IsNaN(math.Max(nan, 1)),
		math.IsNaN(math.Mod(1, 0)),
	}
}

func MathConstants() []any {
	var maxUnsigned uint64 = math.MaxUint64
	return []any{
		math.MaxInt64, math.MinInt64, math.MaxInt32, math.MaxInt, maxUnsigned, math.MaxUint32,
		math.Pi, math.MaxFloat64, math.SmallestNonzeroFloat64,
	}
}

func MathPow() float64 {
	return math.Pow(2, 10)
}

// ---- rejected packages ----

func SortStrings() []string {
	xs := []string{"b", "a"}
	sort.Strings(xs)
	return xs
}

func ErrorsNew() string {
	return errors.New("x").Error()
}

func DurationString() string {
	d := 2 * time.Second
	return d.String()
}

func TimeNow() int64 {
	return time.Now().Unix()
}
