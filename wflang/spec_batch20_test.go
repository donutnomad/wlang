package wflang_test

import (
	"context"
	"math/big"
	"testing"

	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/wflang"
)

// --- TC-980 §16.1 多项加法 -----------------------------------------
func TestTC980_SumThreeInts(t *testing.T) {
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"2"}},
		{"literal":{"type":"int64","value":"3"}}
	]}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 6 {
		t.Fatalf("want 6, got %v", v.Go())
	}
}

// --- TC-981 §16.2 嵌套 var -----------------------------------------
type tc981User struct {
	Age int64 `json:"age"`
}

func TestTC981_NestedVar(t *testing.T) {
	src := []byte(`[{"return":{"var":"input.user.age"}}]`)
	v, err := runSrc(t, src, map[string]any{
		"input": map[string]any{"user": &tc981User{Age: 42}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 42 {
		t.Fatalf("want 42, got %v", v.Go())
	}
}

// --- TC-982 §16.3 缺省值 -------------------------------------------
func TestTC982_VarDefault(t *testing.T) {
	src := []byte(`[{"return":{"var":["input.user.name",
		{"literal":{"type":"string","value":"guest"}}]}}]`)
	v, err := runSrc(t, src, map[string]any{
		"input": map[string]any{"user": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "guest" {
		t.Fatalf("want guest, got %v", v.Go())
	}
}

// --- TC-983 §16.4 if 对象形 -----------------------------------------
func TestTC983_IfBranches(t *testing.T) {
	src := []byte(`[{"if":{
		"cond":{">=":[{"var":"input.age"},{"literal":{"type":"int64","value":"18"}}]},
		"then":[{"return":{"literal":{"type":"string","value":"adult"}}}],
		"else":[{"return":{"literal":{"type":"string","value":"minor"}}}]
	}}]`)
	v, err := runSrc(t, src, map[string]any{
		"input": map[string]any{"age": int64(20)},
	})
	if err != nil {
		t.Fatalf("run(20): %v", err)
	}
	if v.Go().(string) != "adult" {
		t.Fatalf("age=20: want adult, got %v", v.Go())
	}
	v, err = runSrc(t, src, map[string]any{
		"input": map[string]any{"age": int64(10)},
	})
	if err != nil {
		t.Fatalf("run(10): %v", err)
	}
	if v.Go().(string) != "minor" {
		t.Fatalf("age=10: want minor, got %v", v.Go())
	}
}

// --- TC-986/987 §16.7 数组构造 + 索引 -------------------------------
// The implementation's array<T> literal accepts scalar values; we verify
// construction and index-based access (§16.7 + §16.7.1) via a scalar array.
func TestTC986_ArrayLiteralAndIndex(t *testing.T) {
	src := []byte(`[
		{"let":{"xs":{"literal":{"type":"array<int64>","value":[7,9,11]}}}},
		{"return":{"var":"xs.0"}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 7 {
		t.Fatalf("want 7, got %v", v.Go())
	}
}

// --- TC-988 §16.8 sum prices 程序 ----------------------------------
// Uses an array<bigDecimal> literal as the foreach target so the runtime
// can iterate. Each `item` is itself the price value (scalar).
func TestTC988_SumPricesProgram(t *testing.T) {
	src := []byte(`[
		{"let":{"total":{"literal":{"type":"bigDecimal","value":"0"}}}},
		{"foreach":{"target":{"literal":{"type":"array<bigDecimal>","value":[
			"1.5","2.25","0.75"
		]}},"as":"item","do":[
			{"set":{"total":{"+":[{"var":"total"},{"var":"item"}]}}}
		]}},
		{"return":{"var":"total"}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "bigDecimal" {
		t.Fatalf("type: want bigDecimal, got %s", v.TypeName())
	}
	br, ok := v.Go().(*big.Rat)
	if !ok {
		t.Fatalf("want *big.Rat, got %T", v.Go())
	}
	want, _ := new(big.Rat).SetString("4.5")
	if br.Cmp(want) != 0 {
		t.Fatalf("want 4.5, got %s", br.String())
	}
}

// --- TC-989 §16.9 fori + break + continue --------------------------
func TestTC989_ForiBreakContinue(t *testing.T) {
	src := []byte(`[
		{"let":{"sum":{"literal":{"type":"int64","value":"0"}}}},
		{"fori":{
			"var":"i",
			"from":{"literal":{"type":"int64","value":"0"}},
			"to":{"literal":{"type":"int64","value":"10"}},
			"step":{"literal":{"type":"int64","value":"1"}},
			"do":[
				{"if":{"cond":{"==":[{"var":"i"},{"literal":{"type":"int64","value":"5"}}]},
					"then":[{"continue":{}}]}},
				{"if":{"cond":{">=":[{"var":"i"},{"literal":{"type":"int64","value":"8"}}]},
					"then":[{"break":{}}]}},
				{"set":{"sum":{"+":[{"var":"sum"},{"var":"i"}]}}}
			]
		}},
		{"return":{"var":"sum"}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// 0+1+2+3+4+6+7 = 23
	if v.Go().(int64) != 23 {
		t.Fatalf("want 23, got %v", v.Go())
	}
}

// --- TC-990 §16.10 panic 默认 → E_PANIC ----------------------------
func TestTC990_PanicDefault(t *testing.T) {
	src := []byte(`[{"panic":{"literal":{"type":"string","value":"invalid state"}}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want panic error, got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok {
		t.Fatalf("want LangError, got %T", err)
	}
	if le.Code != werr.CodePanic {
		t.Fatalf("code: want E_PANIC, got %s", le.Code)
	}
	if !containsString(le.Message, "invalid state") {
		t.Fatalf("message should contain 'invalid state': %q", le.Message)
	}
}

// --- TC-992 §16.11 包函数 risk.Score -------------------------------
func tc992Score(amount float64, country string) (float64, error) {
	// Dummy: amount factor + country factor.
	cf := 0.0
	if country == "US" {
		cf = 1.0
	}
	return amount*0.01 + cf, nil
}

func TestTC992_RiskScorePackage(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("risk", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Score", Impl: tc992Score, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Score":[
		{"pkg":"risk"},
		{"var":"input.amount"},
		{"var":"input.country"}
	]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{
			"input": map[string]any{"amount": 100.0, "country": "US"},
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	f, ok := unwrap1(t, v).(float64)
	if !ok {
		t.Fatalf("want float64, got %T", unwrap1(t, v))
	}
	if f != 2.0 {
		t.Fatalf("want 2.0, got %v", f)
	}
}

// --- TC-994 §16.13 Builder→Compile→Run 闭环 -----------------------
func TestTC994_BuilderRoundTripRiskScore(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("risk", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Score", Impl: tc992Score, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	b := wflang.NewConfigBuilder(reg)
	prog := b.Program().Return(b.Call(b.Pkg("risk"), "Score",
		b.Lit("float64", 50.0),
		b.Lit("string", "US"),
	))
	src, err := prog.JSON()
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	p, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v\nsrc=%s", err, src)
	}
	v, err := p.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	f, ok := unwrap1(t, v).(float64)
	if !ok {
		t.Fatalf("want float64, got %T", unwrap1(t, v))
	}
	if f != 1.5 {
		t.Fatalf("want 1.5, got %v", f)
	}
}

// --- TC-900 null 独立类型 -------------------------------------------
// A `null` typed literal must yield TypeName="null", distinct from any or
// error.
func TestTC900_NullIsOwnType(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"null","value":null}}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "null" {
		t.Fatalf("want null, got %s", v.TypeName())
	}
	if v.TypeName() == "any" || v.TypeName() == "error" {
		t.Fatalf("null must differ from any/error")
	}
}

// --- TC-723 host error 显式解构后可作为普通 error 值处理 -------------
func tc723Fail(i int64) (int64, error) {
	if i == 2 {
		return 0, werr.Newf(werr.CodeHost, "simulated at i=%d", i)
	}
	return i, nil
}

func TestTC723_ForeachHostErrorIsExplicitValue(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("bail", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Fail", Impl: tc723Fail, Pure: false},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[
		{"let":{"result":{"literal":{"type":"null","value":null}}}},
		{"foreach":{"target":{"literal":{"type":"array<int64>","value":[2]}},"as":"x","do":[
			{"set":{"result":{"Fail":[{"pkg":"bail"},{"var":"x"}]}}}
		]}},
		{"return":{"var":"result"}}
	]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "tuple<int64,error>" {
		t.Fatalf("want tuple<int64,error>, got %s %v", v.TypeName(), v.Go())
	}
	if got := unwrapErr(t, v); got == nil || !containsString(got.(error).Error(), "simulated at i=2") {
		t.Fatalf("want simulated at i=2 error value, got %v", got)
	}
}
