package wflang_test

import (
	"context"
	"sync/atomic"
	"testing"

	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/registry"
	"github.com/wflang/wflang/wflang"
)

// --- TC-121 类型匹配失败：int64 + string → E_TYPE -------------------
// `+` with mismatched operand types must produce an E_TYPE error.
func TestTC121_PlusTypeMismatch(t *testing.T) {
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"string","value":"hi"}}
	]}}]`)
	_, err := runSrc(t, src, nil)
	expectCode(t, err, "E_TYPE")
}

// --- TC-122 变量缺失 → E_SYMBOL -------------------------------------
// Reading an undefined variable must fail with E_SYMBOL.
func TestTC122_UnknownVariable(t *testing.T) {
	src := []byte(`[{"return":{"var":"nosuch"}}]`)
	_, err := runSrc(t, src, nil)
	expectCode(t, err, "E_SYMBOL")
}

// --- TC-199 fori step=0 报错 ----------------------------------------
// `fori` with step=0 must produce a runtime diagnostic.
func TestTC199_ForiStepZero(t *testing.T) {
	src := []byte(`[{"fori":{
		"var":"i",
		"from":{"literal":{"type":"int64","value":"0"}},
		"to":{"literal":{"type":"int64","value":"10"}},
		"step":{"literal":{"type":"int64","value":"0"}},
		"do":[]
	}}, {"return":{"literal":{"type":"int64","value":"0"}}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want diagnostic for step=0, got nil")
	}
	// Implementation reports E_RUNTIME with "step cannot be 0".
	le, ok := err.(*werr.LangError)
	if !ok {
		t.Fatalf("want LangError, got %T: %v", err, err)
	}
	if !containsString(le.Message, "step") {
		t.Fatalf("want 'step' in message, got %q", le.Message)
	}
}

// --- TC-344 set 类型检查：fori 循环变量被错误赋值 --------------------
// A fori-introduced index variable is declared int64; assigning a string
// to it via `set` must produce E_TYPE.
func TestTC344_SetTypeMismatchInFori(t *testing.T) {
	src := []byte(`[{"fori":{
		"var":"i",
		"from":{"literal":{"type":"int64","value":"0"}},
		"to":{"literal":{"type":"int64","value":"3"}},
		"do":[
			{"set":{"i":{"literal":{"type":"string","value":"s"}}}}
		]
	}}, {"return":{"literal":{"type":"int64","value":"0"}}}]`)
	_, err := runSrc(t, src, nil)
	expectCode(t, err, "E_TYPE")
}

// --- TC-424 (ctx, ...T)(R,error) ------------------------------------
// First ctx parameter is injected; remaining variadic args come from the
// language call.
var tc424Seen context.Context

func tc424SumCtx(ctx context.Context, xs ...int64) (int64, error) {
	tc424Seen = ctx
	var s int64
	for _, x := range xs {
		s += x
	}
	return s, nil
}

func TestTC424_CtxPlusVariadic(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("cv", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Sum", Impl: tc424SumCtx, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Sum":[
		{"pkg":"cv"},
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"2"}},
		{"literal":{"type":"int64","value":"3"}},
		{"literal":{"type":"int64","value":"4"}}
	]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	type tcKey struct{}
	parent := context.WithValue(context.Background(), tcKey{}, "m-424")
	v, err := prog.Run(parent, wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 10 {
		t.Fatalf("want 10, got %v", v.Go())
	}
	if tc424Seen == nil {
		t.Fatal("ctx not propagated")
	}
	if m, _ := tc424Seen.Value(tcKey{}).(string); m != "m-424" {
		t.Fatalf("ctx marker mismatch: %q", m)
	}
}

// --- TC-442 ctx 跨 Run 的所有前台调用共享 ---------------------------
// When a single program calls multiple Go functions in sequence, they must
// all observe the same ctx that was passed to Run.
var (
	tc442Calls atomic.Int32
	tc442Last  context.Context
	tc442All   [2]context.Context
)

func tc442A(ctx context.Context) (int64, error) {
	i := tc442Calls.Add(1) - 1
	tc442All[i] = ctx
	tc442Last = ctx
	return 1, nil
}
func tc442B(ctx context.Context) (int64, error) {
	i := tc442Calls.Add(1) - 1
	if i >= int32(len(tc442All)) {
		return 0, nil
	}
	tc442All[i] = ctx
	tc442Last = ctx
	return 2, nil
}

func TestTC442_CtxSharedAcrossCalls(t *testing.T) {
	tc442Calls.Store(0)
	tc442All = [2]context.Context{}
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("share", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "A", Impl: tc442A, Pure: false},
			{GoName: "B", Impl: tc442B, Pure: false},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	// Call A and B in sequence; the result is A + B = 3 (sanity only).
	prog, err := eng.CompileJSON([]byte(`[
		{"let":{"a":{"A":[{"pkg":"share"}]}}},
		{"let":{"b":{"B":[{"pkg":"share"}]}}},
		{"return":{"+":[{"var":"a"},{"var":"b"}]}}
	]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	type tcKey struct{}
	parent := context.WithValue(context.Background(), tcKey{}, "shared")
	v, err := prog.Run(parent, wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 3 {
		t.Fatalf("want 3, got %v", v.Go())
	}
	if tc442Calls.Load() != 2 {
		t.Fatalf("want 2 calls, got %d", tc442Calls.Load())
	}
	for i, c := range tc442All {
		if c == nil {
			t.Fatalf("call %d got nil ctx", i)
		}
		if m, _ := c.Value(tcKey{}).(string); m != "shared" {
			t.Fatalf("call %d: ctx marker mismatch: %q", i, m)
		}
	}
}

// --- TC-621 source path 用于错误定位 --------------------------------
// A runtime error raised deep inside a program must carry a non-empty
// LangError.Path pointing to the offending JSON node.
func TestTC621_LangErrorPathLocation(t *testing.T) {
	src := []byte(`[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"return":{"+":[{"var":"x"},{"literal":{"type":"string","value":"no"}}]}}
	]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want type error, got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok {
		t.Fatalf("want LangError, got %T: %v", err, err)
	}
	if le.Path == "" {
		t.Fatal("LangError.Path is empty")
	}
	// Path should reference the return statement at index 1.
	if !containsString(le.Path, "/1") {
		t.Fatalf("Path should point to stmt index 1, got %q", le.Path)
	}
}
