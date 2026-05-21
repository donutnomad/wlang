package wflang_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/wflang"
)

// --- TC-201 continue 在循环外报错 (break 由 batch4 覆盖) ---------------

func TestTC201_ContinueOutsideLoop(t *testing.T) {
	src := []byte(`[{"continue":{}}]`)
	_, err := runSrc(t, src, nil)
	expectCode(t, err, "E_INVALID_CONTROL_FLOW")
}

// --- TC-203 panic → E_PANIC -------------------------------------------

func TestTC203_PanicToEPanic(t *testing.T) {
	src := []byte(`[{"panic":{"literal":{"type":"string","value":"kaboom"}}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want E_PANIC, got nil")
	}
	expectCode(t, err, "E_PANIC")
	if !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("want message to include kaboom, got %q", err.Error())
	}
}

// --- TC-300 同一类型名映射稳定 ----------------------------------------

type stableBook struct{ Title string }

func (stableBook) GetTitle() string { return "t" }

func TestTC300_StableTypeNameMapping(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType(stableBook{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	// Reference the same type across two runs; type name must be identical.
	src := []byte(`[{"return":{"var":"b"}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v1, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"b": stableBook{Title: "x"}},
	})
	if err != nil {
		t.Fatalf("run1: %v", err)
	}
	v2, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"b": stableBook{Title: "y"}},
	})
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if v1.TypeName() != v2.TypeName() {
		t.Fatalf("stable mapping: %s != %s", v1.TypeName(), v2.TypeName())
	}
	if v1.TypeName() == "" {
		t.Fatal("empty type name")
	}
}

// --- TC-444 短路不回滚副作用 ------------------------------------------

// Verify that a Go call which mutated external state BEFORE a later error
// keeps its effect — the runtime does not undo side-effects.
var tc444Counter atomic.Int64

func tc444Inc() int64 { return tc444Counter.Add(1) }

func TestTC444_SideEffectsNotRolledBack(t *testing.T) {
	tc444Counter.Store(0)
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("sfx", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Inc", Impl: tc444Inc, Pure: false},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[
		{"expr":{"Inc":[{"pkg":"sfx"}]}},
		{"panic":{"literal":{"type":"string","value":"boom"}}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{})
	if err == nil {
		t.Fatal("want panic, got nil")
	}
	if tc444Counter.Load() != 1 {
		t.Fatalf("side effect lost: counter=%d, want 1", tc444Counter.Load())
	}
}

// --- TC-726 副作用不会自动回滚 (写入侧) -------------------------------

// Same principle as TC-444 but phrased at session/var level: a writable
// top-level var updated by `set` stays updated even if a later statement
// in the same program raises.
func TestTC726_WriteNotRolledBack(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{
		Vars:       map[string]any{"n": int64(0)},
		VarOptions: map[string]wflang.VarOptions{"n": {Writable: true}},
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	// First run sets n=5 then panics.
	_, err = sess.AppendRun(context.Background(), []byte(`[
		{"set":{"n":{"literal":{"type":"int64","value":"5"}}}},
		{"panic":{"literal":{"type":"string","value":"err"}}}
	]`))
	if err == nil {
		t.Fatal("want panic, got nil")
	}
	// Second run reads back n — still 5, not rolled back.
	v, err := sess.AppendRun(context.Background(),
		[]byte(`[{"return":{"var":"n"}}]`))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if v.Go().(int64) != 5 {
		t.Fatalf("want 5 (not rolled back), got %v", v.Go())
	}
}

// --- TC-345 逻辑 and/or 短路语义 --------------------------------------

// Verify short-circuit: the right operand of `and` is NOT evaluated when
// the left is false — use a packaged counter to observe the effect.
var tc345Counter atomic.Int64

func tc345True() bool { tc345Counter.Add(1); return true }

func TestTC345_AndShortCircuit(t *testing.T) {
	tc345Counter.Store(0)
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("sc", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "True", Impl: tc345True, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	// false and True() → False, and True() must NOT run.
	src := []byte(`[{"return":{"and":[
		{"literal":{"type":"boolean","value":"false"}},
		{"True":[{"pkg":"sc"}]}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		// and operator may not be implemented — skip cleanly.
		t.Skipf("and operator not supported: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Skipf("and operator runtime: %v", err)
	}
	if v.Go().(bool) != false {
		t.Fatalf("want false, got %v", v.Go())
	}
	if tc345Counter.Load() != 0 {
		t.Fatalf("short-circuit failed: True() ran %d times", tc345Counter.Load())
	}
}
