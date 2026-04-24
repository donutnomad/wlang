package wflang_test

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/wflang/wflang/wflang"
)

// --- TC-602 Normalize：大整数字面量保持精度 -------------------------
// A literal typed as `bigInt` with a value exceeding int64 must round-trip
// through Compile → Run without precision loss.
func TestTC602_BigIntLiteralPrecision(t *testing.T) {
	// 2^100 = 1267650600228229401496703205376
	huge := "1267650600228229401496703205376"
	src := []byte(`[{"return":{"literal":{"type":"bigInt","value":"` + huge + `"}}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "bigInt" {
		t.Fatalf("type: want bigInt, got %s", v.TypeName())
	}
	// Accept *big.Int or any Stringer that renders to the same number.
	got := ""
	switch gv := v.Go().(type) {
	case *big.Int:
		got = gv.String()
	case interface{ String() string }:
		got = gv.String()
	default:
		t.Fatalf("unexpected bigInt go type %T (%v)", gv, gv)
	}
	if got != huge {
		t.Fatalf("precision lost: %s", got)
	}
}

// --- TC-645 编译期重载收敛：同一程序多次运行结果稳定 ----------------
// A CompileJSON → Program run repeatedly must pick the same overload each
// time (dispatch resolved once at compile time, not re-scored per call).
type tc645Box struct{}

func (tc645Box) PickInt(v int64) string     { return "int" }
func (tc645Box) PickFloat(v float64) string { return "float" }

func TestTC645_OverloadStableAcrossRuns(t *testing.T) {
	reg := wflang.NewRegistry()
	if err := reg.AutoBindType(tc645Box{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := reg.BindMethodOverloads("github.com/wflang/wflang/wflang_test.tc645Box",
		"Pick", []wflang.GoMethodOverload{
			{GoMethod: "PickInt"},
			{GoMethod: "PickFloat"},
		}); err != nil {
		t.Fatalf("overloads: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Pick":[
		{"var":"b"},
		{"literal":{"type":"int64","value":"1"}}
	]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for i := range 5 {
		v, err := prog.Run(context.Background(), wflang.RunOptions{
			Vars: map[string]any{"b": tc645Box{}},
		})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if v.Go().(string) != "int" {
			t.Fatalf("run %d: want int, got %v", i, v.Go())
		}
	}
}

// --- TC-650 歧义重载 → E_AMBIGUOUS_OVERLOAD ------------------------
// Two overloads that both convert with equal non-exact score must produce
// an ambiguous-overload error.
type tc650Amb struct{}

// Both accept `any` — equal score of 10. Calling with any typed literal
// scores the same.
func (tc650Amb) TakeA(v any) string { return "a" }
func (tc650Amb) TakeB(v any) string { return "b" }

func TestTC650_AmbiguousOverload(t *testing.T) {
	reg := wflang.NewRegistry()
	if err := reg.AutoBindType(tc650Amb{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := reg.BindMethodOverloads("github.com/wflang/wflang/wflang_test.tc650Amb",
		"Take", []wflang.GoMethodOverload{
			{GoMethod: "TakeA"},
			{GoMethod: "TakeB"},
		}); err != nil {
		t.Fatalf("overloads: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Take":[
		{"var":"x"},
		{"literal":{"type":"string","value":"hi"}}
	]}}]`))
	if err != nil {
		// Dispatch may be done at compile time.
		expectCode(t, err, "E_AMBIGUOUS_OVERLOAD")
		return
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"x": tc650Amb{}},
	})
	expectCode(t, err, "E_AMBIGUOUS_OVERLOAD")
}

// --- TC-700 YieldError 接口契约 ------------------------------------
// The NewYield helper must return an error that also implements YieldError,
// with the token/payload accessible through the interface.
func TestTC700_YieldErrorContract(t *testing.T) {
	err := wflang.NewYield("my-token", map[string]any{"k": "v"})
	if err == nil {
		t.Fatal("NewYield returned nil")
	}
	var ye wflang.YieldError
	if !errors.As(err, &ye) {
		t.Fatalf("NewYield did not return a YieldError: %T", err)
	}
	if ye.Token() != "my-token" {
		t.Fatalf("token: want my-token, got %q", ye.Token())
	}
	m, ok := ye.Payload().(map[string]any)
	if !ok || m["k"] != "v" {
		t.Fatalf("payload: want map{k:v}, got %#v", ye.Payload())
	}
	// Error() message should include the token so logs are meaningful.
	if !containsString(err.Error(), "my-token") {
		t.Fatalf("Error() should include token: %q", err.Error())
	}
}

// --- TC-402 Session 多次 AppendRun 共享 let 变量 --------------------
// Variables defined in one AppendRun must remain visible to the next
// AppendRun call within the same Session.
func TestTC402_SessionSharesLetAcrossCalls(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	// First call defines `x`.
	_, err = sess.AppendRun(context.Background(),
		[]byte(`[{"let":{"x":{"literal":{"type":"int64","value":"10"}}}}]`))
	if err != nil {
		t.Fatalf("append1: %v", err)
	}
	// Second call reads `x`.
	v, err := sess.AppendRun(context.Background(),
		[]byte(`[{"return":{"+":[{"var":"x"},{"literal":{"type":"int64","value":"5"}}]}}]`))
	if err != nil {
		t.Fatalf("append2: %v", err)
	}
	if v.Go().(int64) != 15 {
		t.Fatalf("want 15, got %v", v.Go())
	}
}

// --- TC-151 CompileJSON 幂等：同一源码编译出的 Program 可重复使用 ----
// A compiled Program executed with different Vars must produce independent
// results without cross-run contamination.
func TestTC151_CompileResultIsReusable(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"+":[{"var":"a"},{"var":"b"}]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := []struct {
		a, b, want int64
	}{
		{1, 2, 3},
		{10, 20, 30},
		{-5, 5, 0},
	}
	for _, c := range cases {
		v, err := prog.Run(context.Background(), wflang.RunOptions{
			Vars: map[string]any{"a": c.a, "b": c.b},
		})
		if err != nil {
			t.Fatalf("run a=%d b=%d: %v", c.a, c.b, err)
		}
		if v.Go().(int64) != c.want {
			t.Fatalf("a=%d b=%d: want %d, got %v", c.a, c.b, c.want, v.Go())
		}
	}
}
