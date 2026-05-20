package wflang_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wflang/wflang/registry"
	"github.com/wflang/wflang/wflang"
)

// --- TC-400 NewEngine + CompileJSON + Run 全流程 ----------------------

func TestTC400_EndToEndPipeline(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry: wflang.DefaultRegistry(),
		Budget:   wflang.Budget{MaxLoopIterations: 1000},
	})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"2"}},
		{"literal":{"type":"int64","value":"3"}}
	]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 5 {
		t.Fatalf("want 5, got %v", v.Go())
	}
}

// --- TC-420 (a,b)(R,error) ---------------------------------------------

func tc420Add(a, b int64) (int64, error) { return a + b, nil }

func TestTC420_TwoArgsResultError(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("math2", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Add", Impl: tc420Add, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Add":[
		{"pkg":"math2"},
		{"literal":{"type":"int64","value":"4"}},
		{"literal":{"type":"int64","value":"6"}}
	]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if unwrap1(t, v).(int64) != 10 {
		t.Fatalf("want 10, got %v", v.Go())
	}
}

// --- TC-421 (ctx, a)(R,error) -----------------------------------------

// The first ctx parameter is supplied by program.Run; language args are
// just `a`.
var tc421Seen context.Context

func tc421NeedsCtx(ctx context.Context, a int64) (int64, error) {
	tc421Seen = ctx
	return a + 1, nil
}

func TestTC421_CtxFirstArg(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("ctxpkg", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Inc", Impl: tc421NeedsCtx, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	// language call passes only `a`, not ctx.
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Inc":[
		{"pkg":"ctxpkg"},
		{"literal":{"type":"int64","value":"41"}}
	]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	type tcKey struct{}
	parentCtx := context.WithValue(context.Background(), tcKey{}, "marker-421")
	v, err := prog.Run(parentCtx, wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if unwrap1(t, v).(int64) != 42 {
		t.Fatalf("want 42, got %v", v.Go())
	}
	// ctx was propagated from program.Run.
	if tc421Seen == nil {
		t.Fatal("ctx was not propagated to the Go func")
	}
	if mark, _ := tc421Seen.Value(tcKey{}).(string); mark != "marker-421" {
		t.Fatalf("ctx marker mismatch: %q", mark)
	}
}

// --- TC-445 ctx cancel 中断执行 (补充 TC-445 已有 stub) ---------------
// Already covered — re-verify here with a different shape: a long Go call
// that checks ctx.Done() itself returns before long.

func tc445Cancelable(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestTC445_CtxCancelInterrupts(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("blk", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Wait", Impl: tc445Cancelable, Pure: false},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Wait":[{"pkg":"blk"}]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	v, err := prog.Run(ctx, wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got, ok := v.Go().(error)
	if !ok || got == nil {
		t.Fatalf("want deadline error value, got %s %v", v.TypeName(), v.Go())
	}
	if !errors.Is(got, context.DeadlineExceeded) {
		if !containsString(got.Error(), "deadline") && !containsString(got.Error(), "canceled") {
			t.Fatalf("want deadline/canceled error value, got %v", got)
		}
	}
}

func containsString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- TC-460 Builder 输出严格 JSONLogic 单键 ---------------------------

func TestTC460_BuilderSingleKey(t *testing.T) {
	b := wflang.NewConfigBuilder(wflang.DefaultRegistry())
	prog := b.Program().
		Return(b.Call(b.Pkg("str"), "Len", b.Lit("string", "hi")))
	out, err := prog.JSON()
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	// Verify it compiles and runs.
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	p, err := eng.CompileJSON(out)
	if err != nil {
		t.Fatalf("compile: %v\nsrc=%s", err, string(out))
	}
	v, err := p.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 2 {
		t.Fatalf("want 2, got %v", v.Go())
	}
	// Spot-check: output must contain {"Len":[... operator single-key shape.
	s := string(out)
	if !containsString(s, `"Len":[`) || !containsString(s, `"pkg":"str"`) {
		t.Fatalf("builder output shape wrong: %s", s)
	}
}

// --- TC-461 Builder.Pkg 生成 {"pkg":...} ------------------------------

func TestTC461_BuilderPkgNode(t *testing.T) {
	b := wflang.NewConfigBuilder(wflang.DefaultRegistry())
	n := b.Pkg("risk")
	m, ok := n.(map[string]any)
	if !ok {
		t.Fatalf("Pkg returned %T, want map", n)
	}
	if m["pkg"] != "risk" || len(m) != 1 {
		t.Fatalf("Pkg shape wrong: %v", m)
	}
}

// --- TC-480 Builder → JSON → Compile → Run 闭环 -----------------------

func TestTC480_RoundTripThroughBuilder(t *testing.T) {
	b := wflang.NewConfigBuilder(wflang.DefaultRegistry())
	prog := b.Program().
		Let("x", b.Lit("int64", "7")).
		Return(b.Var("x"))
	src, err := prog.JSON()
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v\nsrc=%s", err, string(src))
	}
	if v.Go().(int64) != 7 {
		t.Fatalf("want 7, got %v", v.Go())
	}
	if v.TypeName() != "int64" {
		t.Fatalf("type name: want int64, got %s", v.TypeName())
	}
}
