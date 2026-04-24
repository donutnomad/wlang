package wflang_test

import (
	"context"
	"math/big"
	"testing"

	"github.com/wflang/wflang/wflang"
)

// --- TC-013 bigInt / bigDecimal 映射 ------------------------------------

func TestTC013_BigIntBigDecimal(t *testing.T) {
	bi := []byte(`[{"return":{"literal":{"type":"bigInt","value":"100000000000000000000"}}}]`)
	v, err := runSrc(t, bi, nil)
	if err != nil {
		t.Fatalf("bigInt: %v", err)
	}
	if v.TypeName() != "bigInt" {
		t.Fatalf("want bigInt, got %s", v.TypeName())
	}
	bv, ok := v.Go().(*big.Int)
	if !ok {
		t.Fatalf("want *big.Int, got %T", v.Go())
	}
	want, _ := new(big.Int).SetString("100000000000000000000", 10)
	if bv.Cmp(want) != 0 {
		t.Fatalf("want %s, got %s", want, bv)
	}

	bd := []byte(`[{"return":{"literal":{"type":"bigDecimal","value":"1.000"}}}]`)
	v, err = runSrc(t, bd, nil)
	if err != nil {
		t.Fatalf("bigDecimal: %v", err)
	}
	if v.TypeName() != "bigDecimal" {
		t.Fatalf("want bigDecimal, got %s", v.TypeName())
	}
}

// --- TC-033 int64 literal 求值 -----------------------------------------

func TestTC033_Int64Literal(t *testing.T) {
	v, err := runSrc(t, []byte(`[{"return":{"literal":{"type":"int64","value":"42"}}}]`), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 42 {
		t.Fatalf("want 42, got %v", v.Go())
	}
}

// --- TC-102 boolean literal --------------------------------------------

func TestTC102_BooleanLiteral(t *testing.T) {
	v, err := runSrc(t, []byte(`[{"return":{"literal":{"type":"boolean","value":"true"}}}]`), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(bool) != true {
		t.Fatalf("want true, got %v", v.Go())
	}
}

// --- TC-103 float64 literal --------------------------------------------

func TestTC103_Float64Literal(t *testing.T) {
	v, err := runSrc(t, []byte(`[{"return":{"literal":{"type":"float64","value":"1.5"}}}]`), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(float64) != 1.5 {
		t.Fatalf("want 1.5, got %v", v.Go())
	}
}

// --- TC-170 单键对象合法 ------------------------------------------------

func TestTC170_SingleKeyObject(t *testing.T) {
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"2"}}
	]}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 3 {
		t.Fatalf("want 3, got %v", v.Go())
	}
}

// --- TC-172 array<int64> 裸数字元素需为 typed -----------------------------

func TestTC172_ArrayElemMustBeTyped(t *testing.T) {
	// Using the `{"array":{"elem":"int64","items":[1,2,3]}}` form with raw
	// ints is invalid — items must be Node expressions (typed literals).
	src := []byte(`[{"return":{"array":{"elem":"int64","items":[1,2,3]}}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatalf("want error, got nil")
	}
}

// --- TC-200 fori 类型不一致 ---------------------------------------------

func TestTC200_ForiTypeMismatch(t *testing.T) {
	src := []byte(`[
		{"fori":{"var":"i","from":{"literal":{"type":"int64","value":"0"}},"to":{"literal":{"type":"float64","value":"5.0"}},"do":[]}}
	]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

// --- TC-201 break/continue 在循环外 ------------------------------------

func TestTC201_BreakOutsideLoop(t *testing.T) {
	src := []byte(`[{"break":{}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

// --- TC-202 return 优先 break --------------------------------------------

func TestTC202_ReturnBeforeBreak(t *testing.T) {
	src := []byte(`[
		{"fori":{"var":"i","from":{"literal":{"type":"int64","value":"0"}},"to":{"literal":{"type":"int64","value":"10"}},"do":[
			{"return":{"literal":{"type":"int64","value":"7"}}},
			{"break":{}}
		]}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 7 {
		t.Fatalf("want 7, got %v", v.Go())
	}
}

// --- TC-205 expr 丢弃返回值 ---------------------------------------------

func TestTC205_ExprDiscardsValue(t *testing.T) {
	src := []byte(`[
		{"expr":{"+":[{"literal":{"type":"int64","value":"1"}},{"literal":{"type":"int64","value":"2"}}]}},
		{"return":{"literal":{"type":"int64","value":"9"}}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 9 {
		t.Fatalf("want 9, got %v", v.Go())
	}
}

// --- TC-207 routine 必须是单个宿主调用 ----------------------------------

func TestTC207_RoutineMustBeSingleCall(t *testing.T) {
	// `{"routine":{"do":[...]}}` is the invalid form (do + list).
	src := []byte(`[{"routine":{"do":[{"literal":{"type":"int64","value":"1"}}]}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

// --- TC-230 词法作用域 shadow -------------------------------------------

func TestTC230_ShadowDoesNotLeak(t *testing.T) {
	src := []byte(`[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
			{"let":{"x":{"literal":{"type":"int64","value":"2"}}}}
		],"else":[]}},
		{"return":{"var":"x"}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 1 {
		t.Fatalf("outer x should stay 1, got %v", v.Go())
	}
}

// --- TC-270 var 路径数组下标 --------------------------------------------

func TestTC270_VarPathIntoArrayIndex(t *testing.T) {
	items := []any{map[string]any{"price": int64(10)}}
	src := []byte(`[{"return":{"var":"items.0.price"}}]`)
	v, err := runSrc(t, src, map[string]any{"items": items})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 10 {
		t.Fatalf("want 10, got %v", v.Go())
	}
}

// --- TC-271 越界诊断 / 带默认值 ------------------------------------------

func TestTC271_OutOfRangeWithDefault(t *testing.T) {
	// Without default — error.
	items := []any{map[string]any{"price": int64(10)}}
	_, err := runSrc(t, []byte(`[{"return":{"var":"items.5"}}]`), map[string]any{"items": items})
	if err == nil {
		t.Fatal("expected error for out-of-range without default")
	}
	// With default — value returned.
	src := []byte(`[{"return":{"var":["items.5",{"literal":{"type":"int64","value":"-1"}}]}}]`)
	v, err := runSrc(t, src, map[string]any{"items": items})
	if err != nil {
		t.Fatalf("with default: %v", err)
	}
	if v.Go().(int64) != -1 {
		t.Fatalf("want -1, got %v", v.Go())
	}
}

// --- TC-154 Session Vars ------------------------------------------------

func TestTC154_SessionVars(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{
		Vars: map[string]any{"x": int64(42)},
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	v, err := sess.AppendRun(context.Background(), []byte(`[{"return":{"var":"x"}}]`))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 42 {
		t.Fatalf("want 42, got %v", v.Go())
	}
}

// --- TC-155 片段间共享根作用域 ------------------------------------------

func TestTC155_AppendRunScopePersists(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := sess.AppendRun(context.Background(),
		[]byte(`[{"let":{"y":{"literal":{"type":"int64","value":"2"}}}}]`)); err != nil {
		t.Fatalf("frag1: %v", err)
	}
	v, err := sess.AppendRun(context.Background(),
		[]byte(`[{"return":{"var":"y"}}]`))
	if err != nil {
		t.Fatalf("frag2: %v", err)
	}
	if v.Go().(int64) != 2 {
		t.Fatalf("want 2, got %v", v.Go())
	}
}
