package wflang_test

import (
	"context"
	"strings"
	"testing"

	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/wflang"
)

// runSrc is a tiny helper that compiles & runs src on a registry with the
// standard library preloaded.
func runSrc(t *testing.T, src []byte, vars map[string]any) (wflang.Value, error) {
	t.Helper()
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		return wflang.Value{}, err
	}
	return prog.Run(context.Background(), wflang.RunOptions{Vars: vars})
}

func expectCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %s, got nil", wantCode)
	}
	if le, ok := err.(*werr.LangError); ok {
		if le.Code == wantCode {
			return
		}
		t.Fatalf("want %s, got %s (%v)", wantCode, le.Code, le)
	}
	if !strings.Contains(err.Error(), wantCode) {
		t.Fatalf("want %s in %q", wantCode, err.Error())
	}
}

// --- TC-001 / TC-002 -----------------------------------------------------

func TestTC001_NoUserDefinedFunctions(t *testing.T) {
	for _, kw := range []string{"function", "def", "lambda"} {
		src := []byte(`[{"` + kw + `":{}}]`)
		_, err := runSrc(t, src, nil)
		if err == nil {
			t.Fatalf("keyword %s: want error, got nil", kw)
		}
	}
}

func TestTC002_EmptyRegistryUnresolvedPkg(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.NewRegistry()})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Foo":[{"pkg":"bar"}]}}]`))
	if err != nil {
		return // compile-time rejection is fine.
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{})
	if err == nil {
		t.Fatalf("want E_SYMBOL, got nil")
	}
}

// --- TC-010 / TC-011 / TC-012 / TC-015 -----------------------------------

func TestTC010_IntegerLiteralTypes(t *testing.T) {
	for _, typ := range []string{"int8", "int16", "int32", "int64", "uint8", "uint16", "uint32", "uint64"} {
		src := []byte(`[{"return":{"literal":{"type":"` + typ + `","value":"1"}}}]`)
		v, err := runSrc(t, src, nil)
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		if v.TypeName() != typ {
			t.Fatalf("want %s, got %s", typ, v.TypeName())
		}
	}
}

func TestTC012_ScalarLiterals(t *testing.T) {
	cases := []struct {
		typ     string
		val     string
		rawNull bool
	}{
		{"boolean", "true", false},
		{"string", "hi", false},
		{"null", "", true},
	}
	for _, c := range cases {
		var src []byte
		if c.rawNull {
			src = []byte(`[{"return":{"literal":{"type":"null","value":null}}}]`)
		} else {
			src = []byte(`[{"return":{"literal":{"type":"` + c.typ + `","value":` + quoteForJSON(c.typ, c.val) + `}}}]`)
		}
		v, err := runSrc(t, src, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.typ, err)
		}
		if v.TypeName() != c.typ {
			t.Fatalf("want %s, got %s", c.typ, v.TypeName())
		}
	}
}

func quoteForJSON(typ, v string) string {
	if typ == "boolean" {
		return v
	}
	return `"` + v + `"`
}

func TestTC015_PlatformIntRejected(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"int","value":"1"}}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatalf("want error, got nil")
	}
}

// --- TC-014 array<T> typed literal ---------------------------------------

func TestTC014_ArrayTypedLiteral(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"array<int64>","value":[1,2,3]}}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "array<int64>" {
		t.Fatalf("want array<int64>, got %s", v.TypeName())
	}
	arr, ok := v.Go().([]int64)
	if !ok {
		t.Fatalf("want []int64, got %T", v.Go())
	}
	if len(arr) != 3 || arr[0] != 1 || arr[1] != 2 || arr[2] != 3 {
		t.Fatalf("want [1,2,3], got %v", arr)
	}
}

// --- TC-018 / TC-019 operator coverage -----------------------------------

func TestTC018_IntegerOperators(t *testing.T) {
	cases := map[string]int64{
		`{"+":[{"literal":{"type":"int64","value":"2"}},{"literal":{"type":"int64","value":"3"}}]}`:  5,
		`{"-":[{"literal":{"type":"int64","value":"5"}},{"literal":{"type":"int64","value":"2"}}]}`:  3,
		`{"*":[{"literal":{"type":"int64","value":"4"}},{"literal":{"type":"int64","value":"3"}}]}`:  12,
		`{"/":[{"literal":{"type":"int64","value":"10"}},{"literal":{"type":"int64","value":"2"}}]}`: 5,
	}
	for body, want := range cases {
		src := []byte(`[{"return":` + body + `}]`)
		v, err := runSrc(t, src, nil)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if v.Go().(int64) != want {
			t.Fatalf("%s: want %d, got %v", body, want, v.Go())
		}
	}
}

func TestTC019_StringOperators(t *testing.T) {
	cases := map[string]bool{
		`{"contains":[{"literal":{"type":"string","value":"hello"}},{"literal":{"type":"string","value":"ell"}}]}`:  true,
		`{"startsWith":[{"literal":{"type":"string","value":"hello"}},{"literal":{"type":"string","value":"he"}}]}`: true,
		`{"endsWith":[{"literal":{"type":"string","value":"hello"}},{"literal":{"type":"string","value":"lo"}}]}`:   true,
		`{"endsWith":[{"literal":{"type":"string","value":"hello"}},{"literal":{"type":"string","value":"zz"}}]}`:   false,
	}
	for body, want := range cases {
		src := []byte(`[{"return":` + body + `}]`)
		v, err := runSrc(t, src, nil)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if v.Go().(bool) != want {
			t.Fatalf("%s: want %v, got %v", body, want, v.Go())
		}
	}
}

// --- TC-120 .. TC-123 error codes ----------------------------------------

func TestTC120_UnknownOperator(t *testing.T) {
	src := []byte(`[{"return":{"DoesNotExist":[{"literal":{"type":"int64","value":"1"}}]}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if le, ok := err.(*werr.LangError); ok {
		// Spec allows either E_SYMBOL or the more specific E_OPERATOR_NOT_FOUND.
		if le.Code != werr.CodeSymbol && le.Code != werr.CodeOperatorNotFound {
			t.Fatalf("want E_SYMBOL/E_OPERATOR_NOT_FOUND, got %s", le.Code)
		}
	}
}

func TestTC121_TypeMismatchOnAddition(t *testing.T) {
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"string","value":"x"}}
	]}}]`)
	_, err := runSrc(t, src, nil)
	expectCode(t, err, "E_TYPE")
}

func TestTC122_UnknownVar(t *testing.T) {
	src := []byte(`[{"return":{"var":"nope"}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

// --- TC-190 / TC-191 / TC-192 / TC-193 / TC-197 / TC-198 loops -----------

func TestTC190_ReturnShortCircuits(t *testing.T) {
	src := []byte(`[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"return":{"var":"x"}},
		{"set":{"x":{"literal":{"type":"int64","value":"99"}}}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 1 {
		t.Fatalf("want 1, got %v", v.Go())
	}
}

func TestTC192_ForeachSumsElements(t *testing.T) {
	src := []byte(`[
		{"let":{"sum":{"literal":{"type":"int64","value":"0"}}}},
		{"foreach":{"target":{"array":{"elem":"int64","items":[
			{"literal":{"type":"int64","value":"10"}},
			{"literal":{"type":"int64","value":"20"}},
			{"literal":{"type":"int64","value":"30"}}
		]}},"as":"item","do":[
			{"set":{"sum":{"+":[{"var":"sum"},{"var":"item"}]}}}
		]}},
		{"return":{"var":"sum"}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 60 {
		t.Fatalf("want 60, got %v", v.Go())
	}
}

func TestTC198_ForiBasicRange(t *testing.T) {
	src := []byte(`[
		{"let":{"s":{"literal":{"type":"int64","value":"0"}}}},
		{"fori":{"var":"i","from":{"literal":{"type":"int64","value":"0"}},"to":{"literal":{"type":"int64","value":"5"}},"step":{"literal":{"type":"int64","value":"1"}},"do":[
			{"set":{"s":{"+":[{"var":"s"},{"var":"i"}]}}}
		]}},
		{"return":{"var":"s"}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 10 {
		t.Fatalf("want 10, got %v", v.Go())
	}
}

func TestTC199_ForiStepZeroRejected(t *testing.T) {
	src := []byte(`[
		{"fori":{"var":"i","from":{"literal":{"type":"int64","value":"0"}},"to":{"literal":{"type":"int64","value":"5"}},"step":{"literal":{"type":"int64","value":"0"}},"do":[]}}
	]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}
