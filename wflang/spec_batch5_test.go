package wflang_test

import (
	"context"
	"testing"

	"github.com/donutnomad/wlang/wflang"
)

// --- TC-011 float 映射 --------------------------------------------------

func TestTC011_FloatLiteralTypes(t *testing.T) {
	for _, typ := range []string{"float32", "float64"} {
		src := []byte(`[{"return":{"literal":{"type":"` + typ + `","value":"1.5"}}}]`)
		v, err := runSrc(t, src, nil)
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		if v.TypeName() != typ {
			t.Fatalf("want %s, got %s", typ, v.TypeName())
		}
	}
}

// --- TC-030 字面量加法 ---------------------------------------------------

func TestTC030_LiteralAdd(t *testing.T) {
	src := []byte(`[{"return":{"+":[{"literal":{"type":"int64","value":"1"}},{"literal":{"type":"int64","value":"2"}}]}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 3 {
		t.Fatalf("want 3, got %v", v.Go())
	}
}

// --- TC-031 var 嵌套路径 -------------------------------------------------

func TestTC031_VarNestedPath(t *testing.T) {
	src := []byte(`[{"return":{"==":[
		{"var":"user.status"},
		{"literal":{"type":"string","value":"active"}}
	]}}]`)
	vars := map[string]any{
		"user": map[string]any{"name": "alice", "status": "active"},
	}
	v, err := runSrc(t, src, vars)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(bool) != true {
		t.Fatalf("want true, got %v", v.Go())
	}
}

// --- TC-032 var 缺省值 --------------------------------------------------

func TestTC032_VarDefault(t *testing.T) {
	src := []byte(`[{"return":{"var":["user.name",{"literal":{"type":"string","value":"anonymous"}}]}}]`)
	v, err := runSrc(t, src, map[string]any{"user": map[string]any{}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "anonymous" {
		t.Fatalf("want anonymous, got %v", v.Go())
	}
}

// --- TC-034 逻辑 and/or/! ----------------------------------------------

func TestTC034_Logical(t *testing.T) {
	cases := map[string]bool{
		`{"and":[{"literal":{"type":"boolean","value":"true"}},{"literal":{"type":"boolean","value":"false"}}]}`: false,
		`{"and":[{"literal":{"type":"boolean","value":"true"}},{"literal":{"type":"boolean","value":"true"}}]}`:  true,
		`{"or":[{"literal":{"type":"boolean","value":"false"}},{"literal":{"type":"boolean","value":"true"}}]}`:  true,
		`{"!":[{"literal":{"type":"boolean","value":"false"}}]}`:                                                 true,
	}
	for body, want := range cases {
		src := []byte(`[{"return":` + body + `}]`)
		v, err := runSrc(t, src, nil)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if v.Go().(bool) != want {
			t.Fatalf("%s: want %v, got %v", body, want, v.Go())
		}
	}
}

// --- TC-035 if 表达式 ----------------------------------------------------

func TestTC035_IfTakesMatchingBranch(t *testing.T) {
	srcT := []byte(`[{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
		{"return":{"literal":{"type":"int64","value":"10"}}}
	],"else":[
		{"return":{"literal":{"type":"int64","value":"20"}}}
	]}}]`)
	v, err := runSrc(t, srcT, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 10 {
		t.Fatalf("then: want 10, got %v", v.Go())
	}
	srcF := []byte(`[{"if":{"cond":{"literal":{"type":"boolean","value":"false"}},"then":[
		{"return":{"literal":{"type":"int64","value":"10"}}}
	],"else":[
		{"return":{"literal":{"type":"int64","value":"20"}}}
	]}}]`)
	v, err = runSrc(t, srcF, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 20 {
		t.Fatalf("else: want 20, got %v", v.Go())
	}
}

// --- TC-036 let / set 基本语义 -----------------------------------------

func TestTC036_LetSet(t *testing.T) {
	src := []byte(`[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"set":{"x":{"literal":{"type":"int64","value":"2"}}}},
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

// --- TC-101 string typed literal ---------------------------------------

func TestTC101_StringLiteral(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"string","value":"hello"}}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "hello" {
		t.Fatalf("want hello, got %v", v.Go())
	}
}

// --- TC-104 null typed literal -----------------------------------------

func TestTC104_NullLiteral(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"null","value":null}}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "null" {
		t.Fatalf("want null type, got %s", v.TypeName())
	}
	if v.Go() != nil {
		t.Fatalf("want nil go value, got %v", v.Go())
	}
}

// --- TC-105 typed literal 格式错误 --------------------------------------

func TestTC105_InvalidTypedLiteralValue(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"int64","value":"abc"}}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

// --- TC-151 最小程序形态：语句数组 -------------------------------------

func TestTC151_BareProgram(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"int64","value":"1"}}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 1 {
		t.Fatalf("want 1, got %v", v.Go())
	}
}

// --- TC-171 多键对象非法 -----------------------------------------------

func TestTC171_MultiKeyExpressionInvalid(t *testing.T) {
	src := []byte(`[{"return":{"+":[{"literal":{"type":"int64","value":"1"}},{"literal":{"type":"int64","value":"2"}}],"-":[{"literal":{"type":"int64","value":"1"}},{"literal":{"type":"int64","value":"2"}}]}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

// --- TC-252 局部 let 遮蔽顶级 var --------------------------------------

func TestTC252_LocalLetShadowsRoot(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{Vars: map[string]any{"x": int64(1)}})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	// inside an if-block, let x=2 shadows; return inside shadow block
	src := []byte(`[{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
		{"let":{"x":{"literal":{"type":"int64","value":"2"}}}},
		{"return":{"var":"x"}}
	],"else":[]}}]`)
	v, err := sess.AppendRun(context.Background(), src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 2 {
		t.Fatalf("want 2, got %v", v.Go())
	}
}

// --- TC-253 后续片段继承顶级上下文 -------------------------------------

func TestTC253_TopLevelInheritedAcrossFragments(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{Vars: map[string]any{"input": int64(7)}})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	// frag1: noop let
	if _, err := sess.AppendRun(context.Background(),
		[]byte(`[{"let":{"a":{"literal":{"type":"int64","value":"1"}}}}]`)); err != nil {
		t.Fatalf("frag1: %v", err)
	}
	// frag2: read top-level input
	v, err := sess.AppendRun(context.Background(),
		[]byte(`[{"return":{"var":"input"}}]`))
	if err != nil {
		t.Fatalf("frag2: %v", err)
	}
	if v.Go().(int64) != 7 {
		t.Fatalf("want 7, got %v", v.Go())
	}
}

// --- TC-273 slice 下标 --------------------------------------------------

func TestTC273_SliceIndex(t *testing.T) {
	src := []byte(`[{"return":{"var":"arr.1"}}]`)
	v, err := runSrc(t, src, map[string]any{"arr": []any{int64(10), int64(20)}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 20 {
		t.Fatalf("want 20, got %v", v.Go())
	}
}

// --- TC-675 常量折叠 ----------------------------------------------------

func TestTC675_ConstantFolding(t *testing.T) {
	// With Optimize=true a pure int64 + is folded to a single Literal.
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry: wflang.DefaultRegistry(),
		Optimize: true,
	})
	src := []byte(`[{"return":{"+":[{"literal":{"type":"int64","value":"2"}},{"literal":{"type":"int64","value":"3"}}]}}]`)
	prog, err := eng.CompileJSON(src)
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

// --- TC-727 error 与 null 严格区分 -------------------------------------

// A host function returning (T, nil) with T being null-typed value:
// `val.Coalesce(null, null)` returns null typed value (not error short-circuit).
func TestTC727_ErrorVsNullStrict(t *testing.T) {
	src := []byte(`[{"return":{"Coalesce":[{"pkg":"val"},{"literal":{"type":"null","value":null}},{"literal":{"type":"null","value":null}}]}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "null" {
		t.Fatalf("want null, got %s", v.TypeName())
	}
}
