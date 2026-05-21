package wflang_test

import (
	"context"
	"testing"

	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/wflang"
)

// --- TC-017 自动宿主类型可继续传递 ------------------------------------

type passBook struct{ Title string }

func TestTC017_AutoHostTypePassAlong(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("books", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "New", Impl: func() *passBook { return &passBook{Title: "t"} }, Pure: true},
			{GoName: "TitleOf", Impl: func(b *passBook) string { return b.Title }, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	// Store the result of New in `x`, then pass x to TitleOf.
	src := []byte(`[
		{"let":{"x":{"New":[{"pkg":"books"}]}}},
		{"return":{"TitleOf":[{"pkg":"books"},{"var":"x"}]}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "t" {
		t.Fatalf("want t, got %v", v.Go())
	}
}

// --- TC-037 操作符调用必须是 JSONLogic 单键 ---------------------------

func TestTC037_OperatorMustBeSingleKey(t *testing.T) {
	src := []byte(`[{"return":{"+":[{"literal":{"type":"int64","value":"1"}},{"literal":{"type":"int64","value":"2"}}],"-":[{"literal":{"type":"int64","value":"1"}}]}}]`)
	_, err := runSrc(t, src, nil)
	expectCode(t, err, "E_AST_SHAPE")
}

// --- TC-050 PushScope/PopScope 嵌套：内层 let 不外泄 ------------------

func TestTC050_NestedScopeIsolation(t *testing.T) {
	src := []byte(`[
		{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
			{"let":{"x":{"literal":{"type":"int64","value":"1"}}}}
		],"else":[]}},
		{"return":{"var":"x"}}
	]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error (x leaked from inner scope), got nil")
	}
}

// --- TC-051 SetVar 向外查找 -------------------------------------------

func TestTC051_SetSearchesOuter(t *testing.T) {
	src := []byte(`[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
			{"set":{"x":{"literal":{"type":"int64","value":"2"}}}}
		],"else":[]}},
		{"return":{"var":"x"}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 2 {
		t.Fatalf("want 2, got %v", v.Go())
	}
}

// --- TC-106 重载通过 typed literal 收敛 -------------------------------

// Two overloads AddInt64/AddFloat64 + int64 typed literal must hit AddInt64
// unambiguously (covered partially by TC-089; here the literal is explicitly
// typed, so no numeric widening is involved).
type addDual struct{}

func (addDual) AddInt64(v int64) int64     { return v + 1 }
func (addDual) AddFloat64(v float64) int64 { return int64(v) + 100 }

func TestTC106_TypedLiteralConverges(t *testing.T) {
	reg := wflang.NewRegistry()
	if err := reg.AutoBindType(addDual{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := reg.BindMethodOverloads("github.com/donutnomad/wlang/wflang_test.addDual",
		"Add", []wflang.GoMethodOverload{
			{GoMethod: "AddInt64"},
			{GoMethod: "AddFloat64"},
		}); err != nil {
		t.Fatalf("overloads: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"Add":[
		{"var":"d"},
		{"literal":{"type":"int64","value":"5"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"d": addDual{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 6 {
		t.Fatalf("want 6 (AddInt64), got %v", v.Go())
	}
}

// --- TC-156 片段中 set 命中只读顶级 Vars → E_READONLY_VAR -------------

func TestTC156_SetReadOnlyTopLevel(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{Vars: map[string]any{"input": int64(1)}})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	_, err = sess.AppendRun(context.Background(),
		[]byte(`[{"set":{"input":{"literal":{"type":"int64","value":"2"}}}}]`))
	expectCode(t, err, "E_READONLY_VAR")
}

// --- TC-173 包函数表达式 str.Len("hello") = 5 -------------------------

func TestTC173_StrLenViaPackage(t *testing.T) {
	src := []byte(`[{"return":{"Len":[{"pkg":"str"},{"literal":{"type":"string","value":"hello"}}]}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 5 {
		t.Fatalf("want 5, got %v", v.Go())
	}
}

// --- TC-341 var 路径类型推断 (运行时 TypeName) ------------------------

type pathUser struct {
	Name string `json:"name"`
}

func TestTC341_VarPathTypeInferred(t *testing.T) {
	src := []byte(`[{"return":{"var":"u.Name"}}]`)
	v, err := runSrc(t, src, map[string]any{"u": &pathUser{Name: "alice"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "string" {
		t.Fatalf("want string, got %s", v.TypeName())
	}
}
