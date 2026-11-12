// Size budgets (LANGUAGE.md §10.1, TC-804): MaxStringBytes, MaxArrayLength
// and MaxObjectKeys on every operation that builds a value. Each guarded
// operation is run just under its limit (the result equals Go's) and just
// over it (E_BUDGET); amplifying calls with huge arguments fail before
// allocating.
package wflang_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/types"
	"github.com/donutnomad/wlang/wflang"
)

const (
	sizeTestStringBytes = 1024
	sizeTestArrayLength = 100
	sizeTestObjectKeys  = 100
)

func sizeBudget() wflang.Budget {
	return wflang.Budget{
		MaxSteps:       100_000,
		MaxStringBytes: sizeTestStringBytes,
		MaxArrayLength: sizeTestArrayLength,
		MaxObjectKeys:  sizeTestObjectKeys,
	}
}

func runSized(t *testing.T, reg *registry.Registry, src string, vars map[string]any) (wflang.Value, error) {
	t.Helper()
	if reg == nil {
		reg = wflang.DefaultRegistry()
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg, Budget: sizeBudget()})
	prog, err := eng.CompileJSON([]byte(src))
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, src)
	}
	return prog.Run(t.Context(), wflang.RunOptions{Vars: vars})
}

func wantBudgetError(t *testing.T, err error, mention string) {
	t.Helper()
	var le *wflang.LangError
	if !errors.As(err, &le) || le.Code != "E_BUDGET" {
		t.Fatalf("want E_BUDGET, got %v (%T)", err, err)
	}
	if !strings.Contains(le.Message, mention) {
		t.Fatalf("error %q does not mention %q", le.Message, mention)
	}
}

func lit(typ, value string) string {
	return fmt.Sprintf(`{"literal":{"type":%q,"value":%q}}`, typ, value)
}

func sizeCall(pkg, name string, args ...string) string {
	return fmt.Sprintf(`{%q:[{"pkg":%q}%s]}`, name, pkg, prefixComma(args))
}

func prefixComma(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return "," + strings.Join(args, ",")
}

func str(s string) string { return lit("string", s) }

func i64(n int64) string { return lit("int64", strconv.FormatInt(n, 10)) }

func stringArray(items []string) types.Value {
	return types.NewValue(types.ArrayType(types.TString), items)
}

// sizeCase runs expr twice: with the under-limit vars it must return want,
// with the over-limit vars it must fail with E_BUDGET naming limit.
type sizeCase struct {
	name  string
	expr  string
	under map[string]any
	want  any
	over  map[string]any
	limit string
}

func TestSizeBudgetGuardsEachOperation(t *testing.T) {
	as := func(n int) string { return strings.Repeat("a", n) }
	commas := func(n int) string { return strings.Repeat(",", n) }
	quarter := func(n int) []string { return []string{as(n), as(n), as(n), as(n)} }
	cases := []sizeCase{
		{
			name:  "concat",
			expr:  `{"+":[{"var":"a"},{"var":"b"}]}`,
			under: map[string]any{"a": as(512), "b": as(512)},
			want:  as(1024),
			over:  map[string]any{"a": as(512), "b": as(513)},
			limit: "MaxStringBytes",
		},
		{
			name:  "strings.Repeat",
			expr:  sizeCall("strings", "Repeat", `{"var":"s"}`, i64(512)),
			under: map[string]any{"s": "ab"},
			want:  strings.Repeat("ab", 512),
			over:  map[string]any{"s": "abc"},
			limit: "MaxStringBytes",
		},
		{
			name:  "strings.ReplaceAll",
			expr:  sizeCall("strings", "ReplaceAll", `{"var":"s"}`, str("a"), str("aa")),
			under: map[string]any{"s": as(512)},
			want:  as(1024),
			over:  map[string]any{"s": as(513)},
			limit: "MaxStringBytes",
		},
		{
			name:  "strings.Replace",
			expr:  sizeCall("strings", "Replace", `{"var":"s"}`, str("a"), str("aaaa"), `{"var":"n"}`),
			under: map[string]any{"s": as(1000), "n": int64(8)},
			want:  strings.Replace(as(1000), "a", "aaaa", 8),
			over:  map[string]any{"s": as(1000), "n": int64(9)},
			limit: "MaxStringBytes",
		},
		{
			name:  "str.Replace",
			expr:  sizeCall("str", "Replace", `{"var":"s"}`, str("a"), str("aa")),
			under: map[string]any{"s": as(512)},
			want:  as(1024),
			over:  map[string]any{"s": as(513)},
			limit: "MaxStringBytes",
		},
		{
			name:  "strings.Join",
			expr:  sizeCall("strings", "Join", `{"var":"xs"}`, str(",")),
			under: map[string]any{"xs": stringArray(quarter(255))},
			want:  strings.Join(quarter(255), ","),
			over:  map[string]any{"xs": stringArray(quarter(256))},
			limit: "MaxStringBytes",
		},
		{
			name:  "str.Join",
			expr:  sizeCall("str", "Join", `{"var":"xs"}`, str("")),
			under: map[string]any{"xs": stringArray(quarter(256))},
			want:  as(1024),
			over:  map[string]any{"xs": stringArray(append(quarter(256), "a"))},
			limit: "MaxStringBytes",
		},
		{
			name:  "strings.Split",
			expr:  sizeCall("strings", "Split", `{"var":"s"}`, str(",")),
			under: map[string]any{"s": commas(99)},
			want:  strings.Split(commas(99), ","),
			over:  map[string]any{"s": commas(100)},
			limit: "MaxArrayLength",
		},
		{
			name:  "strings.Split runes",
			expr:  sizeCall("strings", "Split", `{"var":"s"}`, str("")),
			under: map[string]any{"s": strings.Repeat("é", 100)},
			want:  strings.Split(strings.Repeat("é", 100), ""),
			over:  map[string]any{"s": strings.Repeat("é", 101)},
			limit: "MaxArrayLength",
		},
		{
			name:  "str.Split",
			expr:  sizeCall("str", "Split", `{"var":"s"}`, str(",")),
			under: map[string]any{"s": commas(99)},
			want:  strings.Split(commas(99), ","),
			over:  map[string]any{"s": commas(100)},
			limit: "MaxArrayLength",
		},
		{
			name:  "strings.Fields",
			expr:  sizeCall("strings", "Fields", `{"var":"s"}`),
			under: map[string]any{"s": strings.Repeat("a ", 100)},
			want:  strings.Fields(strings.Repeat("a ", 100)),
			over:  map[string]any{"s": strings.Repeat("a ", 101)},
			limit: "MaxArrayLength",
		},
		{
			name:  "fmt.Sprintf width",
			expr:  sizeCall("fmt", "Sprintf", `{"var":"f"}`, i64(7)),
			under: map[string]any{"f": "%900d"},
			want:  fmt.Sprintf("%900d", 7),
			over:  map[string]any{"f": "%1100d"},
			limit: "MaxStringBytes",
		},
		{
			name:  "fmt.Sprintf precision",
			expr:  sizeCall("fmt", "Sprintf", `{"var":"f"}`, lit("float64", "1.5")),
			under: map[string]any{"f": "%.500f"},
			want:  fmt.Sprintf("%.500f", 1.5),
			over:  map[string]any{"f": "%.1100f"},
			limit: "MaxStringBytes",
		},
		{
			name:  "fmt.Sprintf strings",
			expr:  sizeCall("fmt", "Sprintf", str("%s|%s"), `{"var":"a"}`, `{"var":"b"}`),
			under: map[string]any{"a": as(400), "b": as(400)},
			want:  as(400) + "|" + as(400),
			over:  map[string]any{"a": as(600), "b": as(600)},
			limit: "MaxStringBytes",
		},
		{
			name:  "str.Format",
			expr:  sizeCall("str", "Format", `{"var":"f"}`, i64(7)),
			under: map[string]any{"f": "%900d"},
			want:  fmt.Sprintf("%900d", 7),
			over:  map[string]any{"f": "%1100d"},
			limit: "MaxStringBytes",
		},
		{
			name:  "fmt.Sprint",
			expr:  sizeCall("fmt", "Sprint", `{"var":"a"}`, `{"var":"b"}`),
			under: map[string]any{"a": as(512), "b": as(511)},
			want:  as(1023),
			over:  map[string]any{"a": as(512), "b": as(513)},
			limit: "MaxStringBytes",
		},
		{
			name:  "fmt.Sprintln",
			expr:  sizeCall("fmt", "Sprintln", `{"var":"a"}`, `{"var":"b"}`),
			under: map[string]any{"a": as(511), "b": as(511)},
			want:  fmt.Sprintln(as(511), as(511)),
			over:  map[string]any{"a": as(511), "b": as(512)},
			limit: "MaxStringBytes",
		},
		{
			name:  "strconv.FormatFloat",
			expr:  sizeCall("strconv", "FormatFloat", lit("float64", "1.5"), lit("uint8", "102"), `{"var":"p"}`, i64(64)),
			under: map[string]any{"p": int64(600)},
			want:  strconv.FormatFloat(1.5, 'f', 600, 64),
			over:  map[string]any{"p": int64(1100)},
			limit: "MaxStringBytes",
		},
		{
			name:  "json.Stringify aliased strings",
			expr:  sizeCall("json", "Stringify", `{"var":"xs"}`),
			under: map[string]any{"xs": stringArray([]string{as(300), as(300), as(300)})},
			want:  `["` + as(300) + `","` + as(300) + `","` + as(300) + `"]`,
			over:  map[string]any{"xs": stringArray([]string{as(300), as(300), as(300), as(300)})},
			limit: "MaxStringBytes",
		},
		{
			name:  "json.Stringify escapes",
			expr:  sizeCall("json", "Stringify", `{"var":"s"}`),
			under: map[string]any{"s": strings.Repeat("<", 170)},
			want:  mustJSON(strings.Repeat("<", 170)),
			over:  map[string]any{"s": strings.Repeat("<", 171)},
			limit: "MaxStringBytes",
		},
		{
			name:  "to.JSON",
			expr:  sizeCall("to", "JSON", `{"var":"s"}`),
			under: map[string]any{"s": as(1022)},
			want:  `"` + as(1022) + `"`,
			over:  map[string]any{"s": as(1023)},
			limit: "MaxStringBytes",
		},
		{
			name:  "to.String array",
			expr:  sizeCall("to", "String", `{"var":"xs"}`),
			under: map[string]any{"xs": stringArray([]string{as(300), as(300)})},
			want:  fmt.Sprint([]string{as(300), as(300)}),
			over:  map[string]any{"xs": stringArray([]string{as(300), as(300), as(300), as(300)})},
			limit: "MaxStringBytes",
		},
		{
			name:  "arr.Flatten",
			expr:  sizeCall("arr", "Flatten", `{"var":"xs"}`),
			under: map[string]any{"xs": types.NewValue("array<any>", []any{make([]any, 50), make([]any, 50)})},
			want:  make([]any, 100),
			over:  map[string]any{"xs": types.NewValue("array<any>", []any{make([]any, 50), make([]any, 51)})},
			limit: "MaxArrayLength",
		},
		{
			// No guard: at most 3x growth (invalid UTF-8 becomes U+FFFD), so
			// the registry's result check is enough.
			name:  "strings.ToUpper result check",
			expr:  sizeCall("strings", "ToUpper", `{"var":"s"}`),
			under: map[string]any{"s": strings.Repeat("\xff", 341)},
			want:  strings.ToUpper(strings.Repeat("\xff", 341)),
			over:  map[string]any{"s": strings.Repeat("\xff", 342)},
			limit: "MaxStringBytes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `[{"return":` + tc.expr + `}]`
			got, err := runSized(t, nil, src, tc.under)
			if err != nil {
				t.Fatalf("under the limit: %v", err)
			}
			if vals, ok := types.UnpackTuple(got); ok { // (string, error) results
				got = vals[0]
			}
			if fmt.Sprint(got.Go()) != fmt.Sprint(tc.want) {
				t.Fatalf("under the limit: result differs from Go\n got %.80q\nwant %.80q", fmt.Sprint(got.Go()), fmt.Sprint(tc.want))
			}
			_, err = runSized(t, nil, src, tc.over)
			wantBudgetError(t, err, tc.limit)
		})
	}
}

// TestSizeBudgetRefusesHugeResultsBeforeAllocating calls amplifying functions
// with arguments whose results would need gigabytes to terabytes: each must
// fail quickly without allocating the result.
func TestSizeBudgetRefusesHugeResultsBeforeAllocating(t *testing.T) {
	big := strings.Repeat("x", sizeTestStringBytes)
	cases := []struct {
		name string
		expr string
		vars map[string]any
	}{
		{"strings.Repeat 1<<34", sizeCall("strings", "Repeat", str("x"), i64(1<<34)), nil},
		{"strings.Repeat overflow", sizeCall("strings", "Repeat", `{"var":"s"}`, i64(1<<62)), map[string]any{"s": big}},
		{"strings.ReplaceAll", sizeCall("strings", "ReplaceAll", `{"var":"s"}`, str(""), `{"var":"s"}`), map[string]any{"s": big}},
		{"strings.Join aliased", sizeCall("strings", "Join", `{"var":"xs"}`, `{"var":"s"}`),
			map[string]any{"xs": stringArray(make([]string, 100)), "s": big}},
		{"fmt.Sprintf widths", sizeCall("fmt", "Sprintf", str(strings.Repeat("%9999999d", 50)), i64(1)), nil},
		{"fmt.Sprintf star width", sizeCall("fmt", "Sprintf", str("%*d"), i64(1<<40), i64(1)), nil},
		{"fmt.Sprintf reused argument", sizeCall("fmt", "Sprintf", str(strings.Repeat("%[1]s", 100)), `{"var":"s"}`), map[string]any{"s": big}},
		{"strconv.FormatFloat", sizeCall("strconv", "FormatFloat", lit("float64", "1.5"), lit("uint8", "102"), i64(1<<40), i64(64)), nil},
		{"json.Stringify aliased", sizeCall("json", "Stringify", `{"var":"xs"}`), map[string]any{"xs": types.NewValue("array<any>", aliasedNest(big, 40))}},
		{"fmt.Sprint aliased", sizeCall("fmt", "Sprint", `{"var":"xs"}`), map[string]any{"xs": types.NewValue("array<any>", aliasedNest(big, 40))}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			_, err := runSized(t, nil, `[{"return":`+tc.expr+`}]`, tc.vars)
			elapsed := time.Since(start)
			runtime.ReadMemStats(&after)
			wantBudgetError(t, err, "Max")
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16<<20 {
				t.Fatalf("allocated %d bytes before refusing", allocated)
			}
			if elapsed > 2*time.Second {
				t.Fatalf("took %v to refuse", elapsed)
			}
		})
	}
}

func mustJSON(v any) string {
	out, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(out)
}

// aliasedNest builds [x, x] nested depth times: tiny in memory, 2^depth
// copies of s when encoded.
func aliasedNest(s string, depth int) []any {
	level := []any{s, s}
	for range depth {
		level = []any{level, level}
	}
	return level
}

func TestSizeBudgetStringDoublingFailsFast(t *testing.T) {
	src := `[
		{"let":{"s":` + str("x") + `}},
		{"fori":{"var":"i","from":` + i64(0) + `,"to":` + i64(40) + `,"do":[
			{"set":{"s":{"+":[{"var":"s"},{"var":"s"}]}}}
		]}},
		{"return":{"var":"s"}}
	]`
	_, err := runSized(t, nil, src, nil)
	wantBudgetError(t, err, "MaxStringBytes")
}

func TestSizeBudgetBigIntSquaringFailsFast(t *testing.T) {
	src := `[
		{"let":{"x":` + lit("bigInt", "3") + `}},
		{"fori":{"var":"i","from":` + i64(0) + `,"to":` + i64(40) + `,"do":[
			{"set":{"x":{"*":[{"var":"x"},{"var":"x"}]}}}
		]}},
		{"return":{"var":"x"}}
	]`
	_, err := runSized(t, nil, src, nil)
	wantBudgetError(t, err, "MaxStringBytes")
}

func TestSizeBudgetMapAssignmentBoundedByObjectKeys(t *testing.T) {
	program := func(n int64) string {
		return `[
			{"let":{"m":{"map":{"type":["int64","int64"],"entries":[]}}}},
			{"fori":{"var":"i","from":` + i64(0) + `,"to":` + i64(n) + `,"do":[
				{"expr":{"map.set":[{"var":"m"},{"var":"i"},{"var":"i"}]}}
			]}},
			{"expr":{"map.set":[{"var":"m"},` + i64(0) + `,` + i64(1) + `]}},
			{"return":{"map.len":[{"var":"m"}]}}
		]`
	}
	got, err := runSized(t, nil, program(sizeTestObjectKeys), nil)
	if err != nil {
		t.Fatalf("at the limit (overwriting an existing key): %v", err)
	}
	if got.Go() != int64(sizeTestObjectKeys) {
		t.Fatalf("map.len = %v, want %d", got.Go(), sizeTestObjectKeys)
	}
	_, err = runSized(t, nil, program(sizeTestObjectKeys+1), nil)
	wantBudgetError(t, err, "MaxObjectKeys")
}

func TestSizeBudgetChanBufferBoundedByArrayLength(t *testing.T) {
	program := func(n int64) string {
		return `[{"let":{"ch":{"chan":["int64",` + i64(n) + `]}}},{"return":{"ch.cap":[{"var":"ch"}]}}]`
	}
	if _, err := runSized(t, nil, program(sizeTestArrayLength), nil); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	_, err := runSized(t, nil, program(1<<40), nil)
	wantBudgetError(t, err, "MaxArrayLength")
}

// A map that contains itself must not send fmt or encoding/json into an
// endless recursion (a Go stack overflow is fatal to the process).
func TestSizeBudgetSelfReferencingValueIsRefused(t *testing.T) {
	for _, fn := range []string{sizeCall("fmt", "Sprint", `{"var":"m"}`), sizeCall("json", "Stringify", `{"var":"m"}`)} {
		src := `[
			{"let":{"m":{"map":{"type":["string","any"],"value":{}}}}},
			{"expr":{"map.set":[{"var":"m"},` + str("self") + `,{"var":"m"}]}},
			{"return":` + fn + `}
		]`
		_, err := runSized(t, nil, src, nil)
		wantBudgetError(t, err, "MaxStringBytes")
	}
}

type sizeHost struct{}

func (sizeHost) Text(n int64) string { return strings.Repeat("h", int(n)) }

func (sizeHost) Items(n int64) []int64 { return make([]int64, n) }

func (sizeHost) Pair(n int64) (string, error) { return strings.Repeat("h", int(n)), nil }

// Every host result is checked, including functions bound by the embedding
// application.
func TestSizeBudgetChecksHostResults(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackageAuto("host", sizeHost{}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		fn    string
		limit int64
		name  string
	}{
		{"Text", sizeTestStringBytes, "MaxStringBytes"},
		{"Items", sizeTestArrayLength, "MaxArrayLength"},
		{"Pair", sizeTestStringBytes, "MaxStringBytes"},
	} {
		if _, err := runSized(t, reg, `[{"return":`+sizeCall("host", tc.fn, i64(tc.limit))+`}]`, nil); err != nil {
			t.Fatalf("%s at the limit: %v", tc.fn, err)
		}
		_, err := runSized(t, reg, `[{"return":`+sizeCall("host", tc.fn, i64(tc.limit+1))+`}]`, nil)
		wantBudgetError(t, err, tc.name)
	}
}

// Zero limits keep the previous behaviour: nothing is checked.
func TestSizeBudgetZeroMeansUnlimited(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON([]byte(`[{"return":` + sizeCall("strings", "Repeat", str("ab"), i64(4096)) + `}]`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := prog.Run(t.Context(), wflang.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Go().(string)) != 8192 {
		t.Fatalf("len = %d", len(got.Go().(string)))
	}
}
