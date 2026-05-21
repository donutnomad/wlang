package wflang_test

import (
	"testing"

	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/wflang"
)

// --- TC-647 安全数值提升：int8 实参命中 int64 形参 ------------------
// When a package function accepts int64 and the caller supplies an int8
// typed literal, overload resolution must accept the widening.
func tc647AcceptsInt64(v int64) int64 { return v + 1 }

func TestTC647_WideningInt8ToInt64(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("w", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Inc", Impl: tc647AcceptsInt64, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	src := []byte(`[{"return":{"Inc":[
		{"pkg":"w"},
		{"literal":{"type":"int8","value":"3"}}
	]}}]`)
	v, err := runProgramWithRegistry(t, reg, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 4 {
		t.Fatalf("want 4, got %v", v.Go())
	}
}

// --- TC-649 any 兜底 ------------------------------------------------
// A unique Go candidate that takes `any` must accept any typed argument
// without ambiguity.
func tc649AcceptAny(v any) string {
	if v == nil {
		return "nil"
	}
	switch v.(type) {
	case int64:
		return "int64"
	case string:
		return "string"
	default:
		return "other"
	}
}

func TestTC649_AnyFallback(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("a", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Kind", Impl: tc649AcceptAny, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	cases := []struct {
		literal string
		want    string
	}{
		{`{"literal":{"type":"int64","value":"5"}}`, "int64"},
		{`{"literal":{"type":"string","value":"hi"}}`, "string"},
	}
	for _, c := range cases {
		src := []byte(`[{"return":{"Kind":[{"pkg":"a"},` + c.literal + `]}}]`)
		v, err := runProgramWithRegistry(t, reg, src, nil)
		if err != nil {
			t.Fatalf("run %s: %v", c.literal, err)
		}
		if v.Go().(string) != c.want {
			t.Fatalf("%s: want %s, got %v", c.literal, c.want, v.Go())
		}
	}
}

// --- TC-676 纯函数常量参数预求值（行为等价验证） ---------------------
// A pure Go function called with only typed literal args must produce the
// same result regardless of how many times the compiled Program is run.
// This is the observable contract even if the implementation doesn't
// literally fold the AST.
var tc676Calls int

func tc676Add(a, b int64) int64 {
	tc676Calls++
	return a + b
}

func TestTC676_PureConstantArgsStableResult(t *testing.T) {
	tc676Calls = 0
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("p", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Add", Impl: tc676Add, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Add":[
		{"pkg":"p"},
		{"literal":{"type":"int64","value":"3"}},
		{"literal":{"type":"int64","value":"4"}}
	]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for i := range 3 {
		v, err := prog.Run(t.Context(), wflang.RunOptions{})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if v.Go().(int64) != 7 {
			t.Fatalf("run %d: want 7, got %v", i, v.Go())
		}
	}
}
