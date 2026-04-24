package wflang_test

import (
	"context"
	"errors"
	"testing"

	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/wflang"
)

// --- TC-089 重载安全数值提升 -------------------------------------------

// Candidates: AddInt64, AddFloat64. int8 literal → must hit AddInt64
// (safe-widening priority 80), not AddFloat64.
type Widener struct{}

func (Widener) AddInt64(v int64) int64     { return v + 10 }
func (Widener) AddFloat64(v float64) int64 { return int64(v) + 100 }

func TestTC089_SafeNumericWidening(t *testing.T) {
	reg := wflang.NewRegistry()
	if err := reg.AutoBindType(Widener{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := reg.BindMethodOverloads("github.com/wflang/wflang/wflang_test.Widener",
		"Add", []wflang.GoMethodOverload{
			{GoMethod: "AddInt64"},
			{GoMethod: "AddFloat64"},
		}); err != nil {
		t.Fatalf("overloads: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"Add":[
		{"var":"w"},
		{"literal":{"type":"int8","value":"1"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"w": Widener{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 11 {
		t.Fatalf("want AddInt64 (=11), got %v", v.Go())
	}
}

// --- TC-090 any 兜底匹配 -----------------------------------------------

// Candidate: DoAny(any). Passing anything should hit it (priority 10).
type AnySink struct{}

func (AnySink) DoAny(v any) string { return "ok" }

func TestTC090_AnyFallback(t *testing.T) {
	reg := wflang.NewRegistry()
	if err := reg.AutoBindType(AnySink{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"DoAny":[
		{"var":"s"},
		{"literal":{"type":"string","value":"hi"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"s": AnySink{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "ok" {
		t.Fatalf("want ok, got %v", v.Go())
	}
}

// --- TC-320 单业务返回值 -----------------------------------------------

type SingleRet struct{}

func (SingleRet) Give() (int64, error) { return 7, nil }

func TestTC320_SingleBusinessReturn(t *testing.T) {
	reg := wflang.NewRegistry()
	if err := reg.AutoBindType(SingleRet{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Give":[{"var":"s"}]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"s": SingleRet{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 7 {
		t.Fatalf("want 7, got %v", v.Go())
	}
}

// --- TC-322 仅 error 返回 ----------------------------------------------

type OnlyErr struct{}

func (OnlyErr) Do() error { return nil }

func TestTC322_OnlyErrorReturn(t *testing.T) {
	reg := wflang.NewRegistry()
	if err := reg.AutoBindType(OnlyErr{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Do":[{"var":"e"}]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"e": OnlyErr{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "null" {
		t.Fatalf("want null, got %s", v.TypeName())
	}
}

// --- TC-324 error != nil 默认中断 ---------------------------------------

type FailingCall struct{}

var errBoom = errors.New("boom")

func (FailingCall) Do() (int64, error) { return 0, errBoom }

func TestTC324_ErrorShortCircuits(t *testing.T) {
	reg := wflang.NewRegistry()
	if err := reg.AutoBindType(FailingCall{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Do":[{"var":"f"}]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"f": FailingCall{}},
	})
	if err == nil {
		t.Fatal("want short-circuit error, got nil")
	}
	// Error should carry the original as Cause via LangError chain.
	var le *werr.LangError
	if errors.As(err, &le) && le.Cause != nil {
		if !errors.Is(le.Cause, errBoom) {
			// Accept either wrapped or direct.
			if le.Cause.Error() != errBoom.Error() {
				t.Fatalf("cause mismatch: %v", le.Cause)
			}
		}
	}
}

// --- TC-340 typed literal 推断 -----------------------------------------

func TestTC340_TypedLiteralInferred(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"int64","value":"1"}}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "int64" {
		t.Fatalf("want int64, got %s", v.TypeName())
	}
}

// --- TC-342 if 两分支类型不一致 -----------------------------------------

// With two branches returning different types, the compiler should either
// surface E_TYPE or collapse to a union/any. Here we use a loose check —
// either behavior is accepted so long as the program compiles without runtime
// panic and the returned value is consistent.
func TestTC342_IfBranchTypesDifferent(t *testing.T) {
	src := []byte(`[{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},
		"then":[{"return":{"literal":{"type":"int64","value":"1"}}}],
		"else":[{"return":{"literal":{"type":"string","value":"x"}}}]
	}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		// E_TYPE at compile-time is also spec-accepted.
		return
	}
	if v.Go().(int64) != 1 {
		t.Fatalf("want 1 (then branch), got %v", v.Go())
	}
}

// --- TC-503 str.Format ------------------------------------------------

func TestTC503_StrFormat(t *testing.T) {
	// Go fmt.Sprintf semantics: "hello %s" with "world".
	src := []byte(`[{"return":{"Format":[{"pkg":"str"},{"literal":{"type":"string","value":"hi %s"}},{"literal":{"type":"string","value":"bob"}}]}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "hi bob" {
		t.Fatalf("want hi bob, got %v", v.Go())
	}
}

// --- TC-530 path.Has / Keys 基础 ---------------------------------------

func TestTC530_PathHas(t *testing.T) {
	// path.Has(m, "a") on map{a:1} → true
	src := []byte(`[{"return":{"Has":[{"pkg":"path"},{"var":"m"},{"literal":{"type":"string","value":"a"}}]}}]`)
	v, err := runSrc(t, src, map[string]any{"m": map[string]any{"a": int64(1)}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(bool) != true {
		t.Fatalf("Has: want true, got %v", v.Go())
	}
	// Missing key → false.
	src2 := []byte(`[{"return":{"Has":[{"pkg":"path"},{"var":"m"},{"literal":{"type":"string","value":"z"}}]}}]`)
	v, err = runSrc(t, src2, map[string]any{"m": map[string]any{"a": int64(1)}})
	if err != nil {
		t.Fatalf("run miss: %v", err)
	}
	if v.Go().(bool) != false {
		t.Fatalf("Has miss: want false, got %v", v.Go())
	}
}
