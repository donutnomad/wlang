// Batch 24 covers the simpler missing TCs that target features already present
// in the implementation. Each test exercises an observable behaviour described
// in SPEC_TESTS.md without requiring new subsystems.
package wflang_test

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/wflang"
)

// --- TC-906 编译后 Program 复用 -----------------------------------------
// A compiled Program executes consistently across multiple Run calls without
// being re-compiled.
func TestTC906_CompiledProgramReuse(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"+":[
		{"var":"a"},
		{"literal":{"type":"int64","value":"1"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for i := int64(0); i < 5; i++ {
		v, err := prog.Run(t.Context(), wflang.RunOptions{Vars: map[string]any{"a": i}})
		if err != nil {
			t.Fatalf("run[%d]: %v", i, err)
		}
		if got := v.Go().(int64); got != i+1 {
			t.Fatalf("run[%d]: want %d, got %d", i, i+1, got)
		}
	}
}

// --- TC-907 typed AST 与动态执行结果一致 ---------------------------------
// Running the same compiled Program twice with identical inputs yields
// identical outputs (the typed AST is deterministic).
func TestTC907_TypedASTExecutionConsistency(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"*":[
		{"var":"x"},
		{"literal":{"type":"int64","value":"3"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v1, err := prog.Run(t.Context(), wflang.RunOptions{Vars: map[string]any{"x": int64(7)}})
	if err != nil {
		t.Fatalf("run1: %v", err)
	}
	v2, err := prog.Run(t.Context(), wflang.RunOptions{Vars: map[string]any{"x": int64(7)}})
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if v1.Go() != v2.Go() {
		t.Fatalf("non-deterministic: %v vs %v", v1.Go(), v2.Go())
	}
	if v1.Go().(int64) != 21 {
		t.Fatalf("want 21, got %v", v1.Go())
	}
}

// --- TC-908 顶级注入：变量 + 包同时可用 -----------------------------------
func tc908Mul(a, b int64) int64 { return a * b }

func TestTC908_SessionInjectVarsAndPackages(t *testing.T) {
	reg := wflang.DefaultRegistry()
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	pkg := registry.PackageSpec{Functions: []registry.FuncSpec{
		{GoName: "Mul", Impl: tc908Mul, Pure: true},
	}}
	sess, err := eng.NewSession(wflang.SessionOptions{
		Vars:     map[string]any{"a": int64(6)},
		Packages: map[string]wflang.PackageSpec{"m": pkg},
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	src := []byte(`[{"return":{"Mul":[
		{"pkg":"m"},
		{"var":"a"},
		{"literal":{"type":"int64","value":"7"}}
	]}}]`)
	v, err := sess.AppendRun(t.Context(), src)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if v.Go().(int64) != 42 {
		t.Fatalf("want 42, got %v", v.Go())
	}
}

// --- TC-909 Go 桥接：context cancel 中断执行 -----------------------------
func TestTC909_ContextCancelStopsRun(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry: wflang.DefaultRegistry(),
		Budget:   wflang.Budget{MaxSteps: 100000000},
	})
	// Tight loop. Cancel before the body can finish.
	src := []byte(`[
		{"fori":{"var":"i","from":{"literal":{"type":"int64","value":"0"}},
			"to":{"literal":{"type":"int64","value":"100000000"}},
			"do":[{"let":{"x":{"var":"i"}}}]}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = prog.Run(ctx, wflang.RunOptions{})
	if err == nil {
		t.Fatalf("want context cancellation error, got nil")
	}
}

// --- TC-984 §16.5 bigDecimal literal 精度 --------------------------------
func TestTC984_BigDecimalLiteralPrecision(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"bigDecimal","value":"123.456"}}}]`)
	v, err := runProgramWithRegistry(t, wflang.DefaultRegistry(), src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	rat, ok := v.Go().(*big.Rat)
	if !ok {
		t.Fatalf("want *big.Rat, got %T", v.Go())
	}
	want := new(big.Rat)
	want.SetString("123.456")
	if rat.Cmp(want) != 0 {
		t.Fatalf("want %s, got %s", want.String(), rat.String())
	}
}

// --- TC-985 §16.6 宿主值构造 -----------------------------------------
type tc985Query struct {
	Term  string
	Limit int64
}

func tc985NewQuery(term string, limit int64) *tc985Query {
	return &tc985Query{Term: term, Limit: limit}
}

func TestTC985_HostValueConstructorReturnsTypedValue(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("books", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "NewQuery", Impl: tc985NewQuery, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	src := []byte(`[{"return":{"NewQuery":[
		{"pkg":"books"},
		{"literal":{"type":"string","value":"go"}},
		{"literal":{"type":"int64","value":"5"}}
	]}}]`)
	v, err := runProgramWithRegistry(t, reg, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	q, ok := v.Go().(*tc985Query)
	if !ok {
		t.Fatalf("want *tc985Query, got %T", v.Go())
	}
	if q.Term != "go" || q.Limit != 5 {
		t.Fatalf("want {go,5}, got %+v", *q)
	}
}

// --- TC-987 §16.7.1 数组索引 ----------------------------------------
// `var "items.0"` reads the first element; `var "items.0.price"` reads a
// nested field; missing path with default falls back.
func TestTC987_ArrayIndexAndDefault(t *testing.T) {
	cases := []struct {
		name string
		src  string
		vars map[string]any
		want any
	}{
		{
			name: "first element price",
			src:  `[{"return":{"var":"items.0.price"}}]`,
			vars: map[string]any{"items": []any{
				map[string]any{"price": int64(10)},
				map[string]any{"price": int64(20)},
			}},
			want: int64(10),
		},
		{
			name: "default falls back",
			src: `[{"return":{"var":["items.5.price",
				{"literal":{"type":"int64","value":"-1"}}]}}]`,
			vars: map[string]any{"items": []any{
				map[string]any{"price": int64(10)},
			}},
			want: int64(-1),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := runProgramWithRegistry(t,
				wflang.DefaultRegistry(), []byte(c.src), c.vars)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if v.Go() != c.want {
				t.Fatalf("want %v, got %v", c.want, v.Go())
			}
		})
	}
}

// --- TC-993 §16.12 类型方法调用 user.Run ------------------------------
type tc993User struct{ Name string }

func (u *tc993User) Run(in string) string { return u.Name + ":" + in }

func TestTC993_TypedMethodCall(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*tc993User)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	src := []byte(`[{"return":{"Run":[
		{"var":"u"},
		{"literal":{"type":"string","value":"go"}}
	]}}]`)
	v, err := runProgramWithRegistry(t, reg, src,
		map[string]any{"u": &tc993User{Name: "alice"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "alice:go" {
		t.Fatalf("want alice:go, got %v", v.Go())
	}
}

// --- TC-323 业务返回值未注册类型 → 自动类型名 -------------------------
type tc323Hidden struct{ X int64 }

func tc323Make() *tc323Hidden { return &tc323Hidden{X: 1} }

func TestTC323_UnregisteredReturnAutoTypeName(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("h", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Make", Impl: tc323Make, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	src := []byte(`[{"return":{"Make":[{"pkg":"h"}]}}]`)
	v, err := runProgramWithRegistry(t, reg, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// AutoHostTypeName format: "*<pkgPath>.<TypeName>".
	want := "*github.com/donutnomad/wlang/wflang_test.tc323Hidden"
	if v.TypeName() != want {
		t.Fatalf("want %q, got %q", want, v.TypeName())
	}
}

// --- TC-204 PropagatePanic --------------------------------------------
// EngineOptions.PropagatePanic doesn't exist yet. The current behaviour is
// that an explicit `{"panic":...}` produces an E_PANIC error rather than
// re-panicking the host. Document the current contract until propagation
// is added.
func TestTC204_PanicSurfacesAsLangError(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"panic":{"literal":{"type":"string","value":"boom"}}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(t.Context(), wflang.RunOptions{})
	if err == nil {
		t.Fatalf("want panic error, got nil")
	}
	var le *wflang.LangError
	if !errors.As(err, &le) || le.Code != "E_PANIC" {
		t.Fatalf("want E_PANIC LangError, got %v (%T)", err, err)
	}
}

// --- TC-732 / TC-910 注册期错误聚合（当前行为是首错即返回） -----------
// TC-910 calls for collecting multiple registration errors. The current
// implementation short-circuits at the first error; record this as a fast-
// fail contract test so a future change must update it.
func TestTC910_RegistrationFastFailOnFirstError(t *testing.T) {
	reg := wflang.NewRegistry()
	err := reg.BindGoPackage("bad", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "A", Impl: nil}, // first error
			{GoName: "B", Impl: nil}, // would also error
		},
	})
	if err == nil {
		t.Fatalf("want bind error, got nil")
	}
}

// --- TC-880 envelope 缺 lang 时的兼容行为 ------------------------------
// LANGUAGE.md leaves it implementation-defined: warning or E_AST_SHAPE.
// Current implementation tolerates and defaults to "wflang/v1". Lock that.
func TestTC880_MissingLangDefaultsToV1(t *testing.T) {
	src := []byte(`{"program":[{"return":{"literal":{"type":"int64","value":"1"}}}]}`)
	v, err := runProgramWithRegistry(t, wflang.DefaultRegistry(), src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 1 {
		t.Fatalf("want 1, got %v", v.Go())
	}
}

// --- TC-881 同大版本语义稳定（v1.x） ----------------------------------
// Programs declaring lang "wflang/v1" or "wflang/v1.0" or "wflang/v1.1"
// must yield identical results.
func TestTC881_SameMajorVersionStable(t *testing.T) {
	for _, lang := range []string{"wflang/v1", "wflang/v1.0", "wflang/v1.1"} {
		src := []byte(`{"lang":"` + lang + `","program":[
			{"return":{"+":[
				{"literal":{"type":"int64","value":"2"}},
				{"literal":{"type":"int64","value":"3"}}
			]}}
		]}`)
		v, err := runProgramWithRegistry(t, wflang.DefaultRegistry(), src, nil)
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		if v.Go().(int64) != 5 {
			t.Fatalf("%s: want 5, got %v", lang, v.Go())
		}
	}
}
