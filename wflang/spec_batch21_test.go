package wflang_test

import (
	"testing"

	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/wflang"
)

// --- TC-901 参数数量严格校验 ---------------------------------------
// A fixed-arity Go function receives the wrong number of args → E_TYPE.
// The implementation reports the arity mismatch under E_TYPE (there is
// no dedicated E_ARITY code).
func tc901Add(a, b int64) int64 { return a + b }

func TestTC901_ArityStrict(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("m1", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Add", Impl: tc901Add, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	src := []byte(`[{"return":{"Add":[
		{"pkg":"m1"},
		{"literal":{"type":"int64","value":"1"}}
	]}}]`)
	_, err := runProgramWithRegistry(t, reg, src, nil)
	expectCode(t, err, "E_TYPE")
}

// --- TC-902 反射调用安全检查 ---------------------------------------
// Passing a string where the Go signature wants int64 must fail with
// E_TYPE before the Go function is invoked.
var tc902Called bool

func tc902Expect(i int64) int64 {
	tc902Called = true
	return i
}

func TestTC902_ReflectTypeMismatch(t *testing.T) {
	tc902Called = false
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("m2", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Expect", Impl: tc902Expect, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	src := []byte(`[{"return":{"Expect":[
		{"pkg":"m2"},
		{"literal":{"type":"string","value":"not a number"}}
	]}}]`)
	_, err := runProgramWithRegistry(t, reg, src, nil)
	expectCode(t, err, "E_TYPE")
	if tc902Called {
		t.Fatal("Go function should not have been invoked on type mismatch")
	}
}

// --- TC-903 typed literal 构造错误直返 -----------------------------
// A malformed typed literal (bigInt "abc") must fail immediately.
func TestTC903_MalformedBigIntLiteral(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"bigInt","value":"abc"}}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want parse error for 'abc', got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok {
		t.Fatalf("want LangError, got %T", err)
	}
	// Accept either CodeType or CodeASTShape — both are valid early-reject
	// signals per §14.1.
	if le.Code != werr.CodeType && le.Code != werr.CodeASTShape {
		t.Fatalf("want E_TYPE or E_AST_SHAPE, got %s: %v", le.Code, err)
	}
}

// --- TC-904 array<T> 元素类型校验 ----------------------------------
// array<int64> containing a string element must be rejected.
func TestTC904_ArrayElementTypeCheck(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"array<int64>","value":[1,"s",3]}}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error for mixed-type array, got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok {
		t.Fatalf("want LangError, got %T", err)
	}
	if le.Code != werr.CodeType && le.Code != werr.CodeASTShape {
		t.Fatalf("want E_TYPE or E_AST_SHAPE, got %s", le.Code)
	}
}

// --- TC-913 if.cond 非布尔 → E_TYPE --------------------------------
func TestTC913_IfCondMustBeBoolean(t *testing.T) {
	src := []byte(`[{"if":{
		"cond":{"literal":{"type":"int64","value":"1"}},
		"then":[{"return":{"literal":{"type":"string","value":"y"}}}]
	}}]`)
	_, err := runSrc(t, src, nil)
	expectCode(t, err, "E_TYPE")
}

// --- TC-915 JSON Pointer 错误路径 ----------------------------------
// A runtime error carries LangError.Path in JSON-Pointer form starting
// with "/".
func TestTC915_ErrorPathIsJSONPointer(t *testing.T) {
	src := []byte(`[{"return":{"var":"missing"}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok {
		t.Fatalf("want LangError, got %T", err)
	}
	if le.Path == "" || le.Path[0] != '/' {
		t.Fatalf("Path must be JSON Pointer starting with '/', got %q", le.Path)
	}
}

// --- helpers --------------------------------------------------------

// runProgramWithRegistry compiles and runs against a custom Registry.
func runProgramWithRegistry(t *testing.T, reg *wflang.Registry, src []byte, vars map[string]any) (wflang.Value, error) {
	t.Helper()
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		return wflang.Value{}, err
	}
	return prog.Run(t.Context(), wflang.RunOptions{Vars: vars})
}
