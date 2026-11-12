package stdlib

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/donutnomad/wlang/types"
)

var unlimited = types.Limits{}

// The guards' bounds must never be below the real output: a bound below it
// would let a call allocate more than the limit before the result check.

func TestSprintfBoundCoversOutput(t *testing.T) {
	big1 := new(big.Int).Lsh(big.NewInt(1), 300)
	values := []any{
		0, -1, int64(math.MinInt64), uint64(math.MaxUint64), uint8(255), 3.5, math.MaxFloat64,
		-math.SmallestNonzeroFloat64, math.Inf(1), true, "", "plain", "<&>\"\\\x00\xff é漢",
		[]byte("bytes\xff"), []string{"a", "bc"}, []any{1, "x", nil, []int64{1, 2}},
		map[string]any{"k": []any{1.5, "v"}, "z": nil}, struct {
			A string
			b int
		}{"x", 2},
		errors.New("boom"), big1, new(big.Float).SetMantExp(big.NewFloat(1), 4000),
		big.NewRat(-7, 3), nil, &struct{ P *int }{},
	}
	formats := []string{
		"%v", "%+v", "%#v", "%s", "%q", "%x", "% X", "%#x", "% #x", "%d", "%b", "%o", "%O", "%c", "%U", "%#U",
		"%e", "%E", "%f", "%F", "%g", "%G", "%t", "%T", "%p", "%z", "%5v", "%-12s", "%012d", "%.3f",
		"%10.4v", "%.0s", "%*d", "%-*.*f", "%[2]v %[1]v", "%[3]v", "%!", "%", "%%", "%v %v %v",
		"%300d", "%.200f", "%[1]*d",
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for _, format := range formats {
		for range 1000 {
			args := make([]any, rng.IntN(4))
			for i := range args {
				args[i] = values[rng.IntN(len(values))]
			}
			got := sprintfBound(unlimited, format, args)
			want := int64(len(fmt.Sprintf(format, args...)))
			if got < want {
				t.Fatalf("sprintfBound(%q, %#v) = %d, output is %d bytes", format, args, got, want)
			}
		}
	}
	for _, v := range values {
		for _, newline := range []bool{false, true} {
			args := []any{v, v}
			want := int64(len(fmt.Sprint(args...)))
			if newline {
				want = int64(len(fmt.Sprintln(args...)))
			}
			if got := sprintBound(unlimited, args, newline); got < want {
				t.Fatalf("sprintBound(%#v) = %d, output is %d bytes", args, got, want)
			}
		}
	}
}

func TestJSONStringBound(t *testing.T) {
	for _, s := range []string{"", "plain", "\"\\\n\r\t\b\f", "\x00\x1f\x7f", "<>&", "  ", "\xff\xfe", "é漢😀", "a\xc3"} {
		out, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		got := jsonString(s)
		if got < int64(len(out)) || (got != int64(len(out)) && !strings.ContainsAny(s, "\b\f") && utf8.ValidString(s)) {
			t.Fatalf("jsonString(%q) = %d, json.Marshal gives %d bytes (%s)", s, got, len(out), out)
		}
	}
}

func TestJSONBoundCoversOutput(t *testing.T) {
	type inner struct {
		Name string `json:"name,omitempty"`
		N    *int
	}
	type outer struct {
		inner
		Tags []string
		Data map[string]any
		raw  string
	}
	n := 7
	values := []any{
		nil, true, int64(math.MinInt64), math.MaxFloat64, "<tag>", []byte("bytes"), []any{"a", nil, 1.5},
		map[string]any{"k": "<&>", "n": []int64{1, 2}}, map[int64]string{-5: "x"},
		outer{inner: inner{Name: "n", N: &n}, Tags: []string{"t"}, Data: map[string]any{"x": nil}, raw: "hidden"},
		new(big.Int).Lsh(big.NewInt(1), 500), big.NewFloat(1e300), big.NewRat(1, 3),
	}
	for _, v := range values {
		out, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		s := newSizer(unlimited)
		s.json(reflect.ValueOf(v), 0)
		if s.n < int64(len(out)) {
			t.Fatalf("json bound of %#v = %d, json.Marshal gives %d bytes", v, s.n, len(out))
		}
	}
}

func TestGuardsCountExactly(t *testing.T) {
	l := types.Limits{MaxStringBytes: 100, MaxArrayLength: 10}
	ok := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	over := func(err error) {
		t.Helper()
		if err == nil {
			t.Fatal("want E_BUDGET")
		}
	}
	ok(guardRepeat(l, []any{"abcd", 25}))
	over(guardRepeat(l, []any{"abcd", 26}))
	ok(guardRepeat(l, []any{"", math.MaxInt}))
	over(guardRepeat(l, []any{"ab", math.MaxInt}))
	ok(guardReplace(l, []any{strings.Repeat("a", 50), "a", "aa", -1}))
	over(guardReplace(l, []any{strings.Repeat("a", 51), "a", "aa", -1}))
	ok(guardReplaceAll(l, []any{strings.Repeat("é", 33), "", "x"})) // 66 + 34 inserts
	over(guardReplaceAll(l, []any{strings.Repeat("é", 34), "", "x"}))
	ok(guardJoin(l, []any{[]string{strings.Repeat("a", 49), strings.Repeat("a", 50)}, ","}))
	over(guardJoin(l, []any{[]string{strings.Repeat("a", 50), strings.Repeat("a", 50)}, ","}))
	ok(guardSplit(l, []any{"a,b,c,d,e,f,g,h,i,j", ","}))
	over(guardSplit(l, []any{"a,b,c,d,e,f,g,h,i,j,k", ","}))
	ok(guardFields(l, []any{strings.Repeat(" a\t", 10)}))
	over(guardFields(l, []any{strings.Repeat(" a\t", 11)}))
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{{strings.Repeat("é", 33), -1, ""}, {"aaaa", 2, "a"}} {
		if got, want := replacedLen(c.s, c.want, "xyz", c.n), len(strings.Replace(c.s, c.want, "xyz", c.n)); got != int64(want) {
			t.Fatalf("replacedLen(%q) = %d, want %d", c.s, got, want)
		}
	}
}
