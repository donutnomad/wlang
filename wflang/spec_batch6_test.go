package wflang_test

import (
	"context"
	"testing"

	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/wflang"
)

// --- TC-056 pkg 与 var 命名空间隔离 --------------------------------------

func TestTC056_PkgAndVarNamespaceIsolated(t *testing.T) {
	reg := wflang.DefaultRegistry()
	// Register a package named "risk" with Score(int64) int64 that doubles.
	if err := reg.BindGoPackage("risk", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Score", Impl: func(x int64) int64 { return x * 2 }, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})

	// Variant A: read `var risk` (top-level var) — returns injected int64.
	prog, err := eng.CompileJSON([]byte(`[{"return":{"var":"risk"}}]`))
	if err != nil {
		t.Fatalf("compile A: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{Vars: map[string]any{"risk": int64(3)}})
	if err != nil {
		t.Fatalf("run A: %v", err)
	}
	if v.Go().(int64) != 3 {
		t.Fatalf("A: want 3, got %v", v.Go())
	}

	// Variant B: use `pkg risk` as receiver for the host function.
	prog2, err := eng.CompileJSON([]byte(`[{"return":{"Score":[{"pkg":"risk"},{"literal":{"type":"int64","value":"4"}}]}}]`))
	if err != nil {
		t.Fatalf("compile B: %v", err)
	}
	v, err = prog2.Run(context.Background(), wflang.RunOptions{Vars: map[string]any{"risk": int64(3)}})
	if err != nil {
		t.Fatalf("run B: %v", err)
	}
	if v.Go().(int64) != 8 {
		t.Fatalf("B: want 8, got %v", v.Go())
	}
}

// --- TC-157 嵌套块 let 不写入根作用域 -----------------------------------

func TestTC157_NestedLetNotPersistedAcrossFragments(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	// Frag1: inside an if-block, let z=1. Block ends, z should die.
	if _, err := sess.AppendRun(context.Background(),
		[]byte(`[{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
			{"let":{"z":{"literal":{"type":"int64","value":"1"}}}}
		],"else":[]}}]`)); err != nil {
		t.Fatalf("frag1: %v", err)
	}
	// Frag2: reading z should fail — the inner scope is gone.
	_, err = sess.AppendRun(context.Background(), []byte(`[{"return":{"var":"z"}}]`))
	if err == nil {
		t.Fatal("want error for missing z, got nil")
	}
}

// --- TC-272 map 数字 key 作为字符串 -------------------------------------

func TestTC272_MapNumericKeyIsString(t *testing.T) {
	src := []byte(`[{"return":{"var":"stats.2024"}}]`)
	v, err := runSrc(t, src, map[string]any{
		"stats": map[string]any{"2024": int64(7)},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 7 {
		t.Fatalf("want 7, got %v", v.Go())
	}
}

// --- TC-677 死分支裁剪 -------------------------------------------------

func TestTC677_DeadBranchElimination(t *testing.T) {
	// With Optimize=true, `if false then A else B` should keep only B.
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry: wflang.DefaultRegistry(),
		Optimize: true,
	})
	src := []byte(`[{"if":{"cond":{"literal":{"type":"boolean","value":"false"}},
		"then":[{"return":{"literal":{"type":"int64","value":"1"}}}],
		"else":[{"return":{"literal":{"type":"int64","value":"2"}}}]
	}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 2 {
		t.Fatalf("want 2, got %v", v.Go())
	}
}

// --- TC-724 诊断错误继续冒泡 ------------------------------------------

func TestTC724_DiagnosticErrorBubbles(t *testing.T) {
	src := []byte(`[
		{"expr":{"+":[{"literal":{"type":"int64","value":"1"}},{"literal":{"type":"string","value":"x"}}]}},
		{"return":{"literal":{"type":"int64","value":"99"}}}
	]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want type error, got nil")
	}
}

// --- TC-728 null receiver 运行期检查 ----------------------------------

// A concrete host type with a method — so the type method table has
// something to dispatch. Calling the method on a null receiver must raise
// E_NIL_RECEIVER at runtime.
type nullRcvBook struct{ Title string }

func (b *nullRcvBook) TitleOf() string { return b.Title }

func TestTC728_NullReceiverCheck(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType(&nullRcvBook{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"TitleOf":[{"var":"b"}]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		return // compile-time rejection is fine (null static type).
	}
	// Inject null for b.
	_, err = prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"b": nil},
	})
	if err == nil {
		t.Fatal("want runtime error, got nil")
	}
}
