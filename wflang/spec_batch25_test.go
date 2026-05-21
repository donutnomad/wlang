// Batch 25 covers TCs targeting the Builder API (§5.6 / §12.6): TC-464
// (builder rejects unknown symbols early) and TC-859 (Builder→JSON→Compile→Run
// round-trip).
package wflang_test

import (
	"errors"
	"testing"

	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/wflang"
)

func tc464Add(a, b int64) int64 { return a + b }

// --- TC-464 Builder 阶段做符号检查 -----------------------------------
// Calling builder.CallE with an unknown operator must fail before the AST
// reaches CompileJSON.
func TestTC464_BuilderRejectsUnknownOperator(t *testing.T) {
	reg := wflang.DefaultRegistry()
	b := wflang.NewConfigBuilder(reg)
	_, err := b.CallE(b.Pkg("nope"), "GhostFn",
		b.Lit("int64", "1"))
	if err == nil {
		t.Fatalf("want error for unknown operator")
	}
	var le *wflang.LangError
	if !errors.As(err, &le) || le.Code != "E_SYMBOL" {
		t.Fatalf("want E_SYMBOL, got %v (%T)", err, err)
	}
}

func TestTC464_BuilderAcceptsRegisteredOperator(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("m", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Add", Impl: tc464Add, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	b := wflang.NewConfigBuilder(reg)
	if _, err := b.CallE(b.Pkg("m"), "Add",
		b.Lit("int64", "1"), b.Lit("int64", "2")); err != nil {
		t.Fatalf("registered op should succeed: %v", err)
	}
}

func TestTC464_BuilderAcceptsBuiltinOperator(t *testing.T) {
	b := wflang.NewConfigBuilder(wflang.DefaultRegistry())
	// "+" is a builtin — must be accepted regardless of registry state.
	if _, err := b.CallE(b.Lit("int64", "1"), "+", b.Lit("int64", "2")); err != nil {
		t.Fatalf("builtin op should succeed: %v", err)
	}
}

// --- TC-859 Config Builder round-trip -------------------------------------
// Builder → JSON → Compile → Run produces the expected typed result.
func TestTC859_BuilderRoundTrip(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("m", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Add", Impl: tc464Add, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	b := wflang.NewConfigBuilder(reg)
	addCall, err := b.CallE(b.Pkg("m"), "Add", b.Lit("int64", "10"), b.Lit("int64", "32"))
	if err != nil {
		t.Fatalf("build call: %v", err)
	}
	src, err := b.Program().Return(addCall).JSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	v, err := runProgramWithRegistry(t, reg, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 42 {
		t.Fatalf("want 42, got %v", v.Go())
	}
}

// --- TC-954 阶段三：Builder→Compile 闭环 -----------------------------
// Builder output is accepted by CompileJSON without modification.
func TestTC954_BuilderCompilesCleanly(t *testing.T) {
	b := wflang.NewConfigBuilder(wflang.DefaultRegistry())
	src, err := b.Program().
		Let("x", b.Lit("int64", "5")).
		Return(b.Var("x")).
		JSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(t.Context(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 5 {
		t.Fatalf("want 5, got %v", v.Go())
	}
}
