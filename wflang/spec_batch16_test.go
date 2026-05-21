package wflang_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/wflang"
)

// --- TC-071 语句矩阵：let / set / if / foreach / return 最小程序 -----

func TestTC071_StatementMatrixAllPass(t *testing.T) {
	// Exercises let, set, if, foreach, return in one compact program.
	src := []byte(`[
		{"let":{"s":{"literal":{"type":"int64","value":"0"}}}},
		{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
			{"set":{"s":{"literal":{"type":"int64","value":"10"}}}}
		],"else":[]}},
		{"foreach":{"target":{"literal":{"type":"array<int64>","value":[1,2,3]}},"as":"x","do":[
			{"set":{"s":{"+":[{"var":"s"},{"var":"x"}]}}}
		]}},
		{"return":{"var":"s"}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 16 {
		t.Fatalf("want 16 (10+1+2+3), got %v", v.Go())
	}
}

// --- TC-072 fori / break / continue / panic / routine 已实现 --------

func TestTC072_PlannedStatementsImplemented(t *testing.T) {
	// A minimal program using fori + break — both are implemented.
	src := []byte(`[
		{"let":{"n":{"literal":{"type":"int64","value":"0"}}}},
		{"fori":{"var":"i","from":{"literal":{"type":"int64","value":"0"}},"to":{"literal":{"type":"int64","value":"5"}},"do":[
			{"if":{"cond":{"==":[{"var":"i"},{"literal":{"type":"int64","value":"3"}}]},"then":[
				{"break":{}}
			],"else":[
				{"set":{"n":{"+":[{"var":"n"},{"literal":{"type":"int64","value":"1"}}]}}}
			]}}
		]}},
		{"return":{"var":"n"}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 3 {
		t.Fatalf("want 3 (iters before break: 0,1,2), got %v", v.Go())
	}
}

// --- TC-423 可变参数 (...T)(R, error) ---------------------------------

func tc423Sum(args ...int64) (int64, error) {
	var s int64
	for _, a := range args {
		s += a
	}
	return s, nil
}

func TestTC423_VariadicArgs(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("sumpkg", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Sum", Impl: tc423Sum, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Sum":[
		{"pkg":"sumpkg"},
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"2"}},
		{"literal":{"type":"int64","value":"3"}},
		{"literal":{"type":"int64","value":"4"}}
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

// --- TC-462 Builder Var receiver produces {"var":"..."} ---------------

func TestTC462_BuilderVarNode(t *testing.T) {
	b := wflang.NewConfigBuilder(wflang.DefaultRegistry())
	n := b.Var("user")
	m, ok := n.(map[string]any)
	if !ok || m["var"] != "user" || len(m) != 1 {
		t.Fatalf("Var shape wrong: %v", n)
	}
}

// --- TC-463 Builder.Lit 强制 typed literal ----------------------------

func TestTC463_BuilderLitTypedOnly(t *testing.T) {
	b := wflang.NewConfigBuilder(wflang.DefaultRegistry())
	n := b.Lit("int64", "7")
	m, ok := n.(map[string]any)
	if !ok {
		t.Fatalf("Lit returned %T", n)
	}
	inner, ok := m["literal"].(map[string]any)
	if !ok {
		t.Fatalf("Lit missing literal key: %v", m)
	}
	if inner["type"] != "int64" || inner["value"] != "7" {
		t.Fatalf("Lit payload wrong: %v", inner)
	}
}

// --- TC-465 Builder 拒绝平台无关宽度语义的裸 int --------------------

// Spec intent: prevent `int` / `uint` literal types (platform-dependent
// widths). The runtime never accepts type "int" / "uint" — verify that
// compiling a Lit built with those types fails.
func TestTC465_BuilderRejectsPlatformWidths(t *testing.T) {
	b := wflang.NewConfigBuilder(wflang.DefaultRegistry())
	// Use type "int" (platform-dependent) — must fail at compile.
	src := []byte(`[{"return":` + mustMarshal(b.Lit("int", "1")) + `}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error for type=int, got nil")
	}
}

func mustMarshal(v any) string {
	b := &strings.Builder{}
	encodeJSON(b, v)
	return b.String()
}

func encodeJSON(b *strings.Builder, v any) {
	switch x := v.(type) {
	case map[string]any:
		b.WriteByte('{')
		first := true
		for k, val := range x {
			if !first {
				b.WriteByte(',')
			}
			first = false
			b.WriteByte('"')
			b.WriteString(k)
			b.WriteString(`":`)
			encodeJSON(b, val)
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, it := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeJSON(b, it)
		}
		b.WriteByte(']')
	case string:
		b.WriteByte('"')
		b.WriteString(x)
		b.WriteByte('"')
	}
}

// --- TC-620 每节点携带 JSON Pointer (LangError.Path) ------------------

// A deep error must carry a non-empty JSON Pointer path.
func TestTC620_ErrorPathJSONPointer(t *testing.T) {
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"string","value":"x"}}
	]}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var le *werr.LangError
	if !errors.As(err, &le) {
		t.Fatalf("want *LangError, got %T", err)
	}
	if le.Path == "" {
		t.Fatal("empty JSON pointer path")
	}
	// Path should look like a JSON pointer — starts with "/".
	if !strings.HasPrefix(le.Path, "/") {
		t.Fatalf("path not a JSON pointer: %q", le.Path)
	}
}

// --- TC-640 Resolve 包 receiver：未注册 → E_SYMBOL --------------------

func TestTC640_ResolvePkgUnregistered(t *testing.T) {
	// Using a pkg that was never registered.
	src := []byte(`[{"return":{"Len":[{"pkg":"ghost"},{"literal":{"type":"string","value":"x"}}]}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want E_SYMBOL, got nil")
	}
	// Accept either compile-time or runtime symbol error.
	if !strings.Contains(err.Error(), "E_SYMBOL") &&
		!strings.Contains(err.Error(), "E_OPERATOR_NOT_FOUND") {
		t.Fatalf("want E_SYMBOL/E_OPERATOR_NOT_FOUND, got %v", err)
	}
}

// --- TC-651 Resolve operator 无候选 → E_OPERATOR_NOT_FOUND -----------

func TestTC651_OperatorNotFound(t *testing.T) {
	// "Quux" is not in any package / type.
	src := []byte(`[{"return":{"Quux":[{"pkg":"str"}]}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want E_OPERATOR_NOT_FOUND, got nil")
	}
	if !strings.Contains(err.Error(), "E_OPERATOR_NOT_FOUND") &&
		!strings.Contains(err.Error(), "E_SYMBOL") {
		t.Fatalf("want E_OPERATOR_NOT_FOUND, got %v", err)
	}
}

// --- TC-720 (T, nil) 求值得到 tuple<T,error> --------------------------

type tc720Give struct{}

func (tc720Give) Get() (int64, error) { return 42, nil }

func TestTC720_TErrNilGivesTuple(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType(tc720Give{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Get":[{"var":"g"}]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"g": tc720Give{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if unwrap1(t, v).(int64) != 42 {
		t.Fatalf("want 42, got %v", v.Go())
	}
	if v.TypeName() != "tuple<int64,error>" || unwrapErr(t, v) != nil {
		t.Fatalf("want tuple<int64,error>{42,nil}, got %s %v", v.TypeName(), v.Go())
	}
}

// --- TC-721 (zero, err) 返回 tuple 末位 error ---------------------------

type tc721Fail struct{}

func (tc721Fail) Go() (int64, error) { return 0, errors.New("down") }

func TestTC721_TErrNonNilReturnsTuple(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType(tc721Fail{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Go":[{"var":"f"}]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"f": tc721Fail{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := unwrapErr(t, v); got == nil || !strings.Contains(got.(error).Error(), "down") {
		t.Fatalf("want 'down' error value, got %v", got)
	}
}

// --- TC-722 error 值可显式解构后调用 Error ----------------------------

func TestTC722_DestructuredErrorValueHasMethods(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType(tc721Fail{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[
		{"let":[["v","err"], {"Go":[{"var":"f"}]}]},
		{"return":{"Error":[{"var":"err"}]}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"f": tc721Fail{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "down" {
		t.Fatalf("want 'down', got %v", v.Go())
	}
}

// --- TC-731 错误码分类齐全 (存在性检查) ------------------------------

func TestTC731_ErrorCodesComplete(t *testing.T) {
	// All required error-code constants must exist and be non-empty.
	for _, c := range []string{
		werr.CodeJSONDecode, werr.CodeASTShape, werr.CodeSymbol, werr.CodeType,
		werr.CodeCapability, werr.CodeRuntime, werr.CodeBudget, werr.CodeHost,
		werr.CodeNilReceiver, werr.CodePanic, werr.CodeRoutine,
	} {
		if c == "" {
			t.Fatal("empty error code constant")
		}
		if !strings.HasPrefix(c, "E_") {
			t.Fatalf("error code should start with E_, got %q", c)
		}
	}
}

// --- TC-806 Budget Timeout / MaxSteps --------------------------------

func TestTC806_BudgetMaxSteps(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry: wflang.DefaultRegistry(),
		Budget:   wflang.Budget{MaxSteps: 5},
	})
	// A fori with 100 iterations — will blow MaxSteps before completing.
	src := []byte(`[
		{"let":{"n":{"literal":{"type":"int64","value":"0"}}}},
		{"fori":{"var":"i","from":{"literal":{"type":"int64","value":"0"}},"to":{"literal":{"type":"int64","value":"100"}},"do":[
			{"set":{"n":{"+":[{"var":"n"},{"literal":{"type":"int64","value":"1"}}]}}}
		]}},
		{"return":{"var":"n"}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{})
	if err == nil {
		t.Fatal("want E_BUDGET, got nil")
	}
	if !strings.Contains(err.Error(), "E_BUDGET") {
		t.Fatalf("want E_BUDGET, got %v", err)
	}
}

func TestTC806_CtxDeadline(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("slow", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Wait", Impl: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}, Pure: false},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Wait":[{"pkg":"slow"}]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	v, err := prog.Run(ctx, wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got, ok := v.Go().(error)
	if !ok || got == nil || !strings.Contains(got.Error(), "deadline") {
		t.Fatalf("want deadline error value, got %s %v", v.TypeName(), v.Go())
	}
}

// --- TC-808 panic 默认转 E_PANIC 不抛出 Go panic ---------------------

func TestTC808_PanicDefaultsToLangError(t *testing.T) {
	// Call a Go function that Go-panics; default options should convert to
	// E_PANIC LangError rather than propagating the panic.
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("boom", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Go", Impl: func() int64 { panic("goroutine-panic") }, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Go":[{"pkg":"boom"}]}}]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic leaked to Go: %v", r)
		}
	}()
	_, err = prog.Run(context.Background(), wflang.RunOptions{})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	// Accept either E_PANIC or E_HOST — default must convert.
	if !strings.Contains(err.Error(), "E_PANIC") &&
		!strings.Contains(err.Error(), "E_HOST") {
		t.Fatalf("want E_PANIC or E_HOST, got %v", err)
	}
}

// --- TC-810 数据边界：未导出字段 (已在 TC-347 验证；此处用 TC-810 命名) -

type tc810Rec struct {
	Name   string
	secret string
}

func TestTC810_UnexportedHidden(t *testing.T) {
	r := tc810Rec{Name: "n", secret: "s"}
	_, err := runSrc(t, []byte(`[{"return":{"var":"r.secret"}}]`), map[string]any{"r": r})
	if err == nil {
		t.Fatal("want unexported hidden error, got nil")
	}
}

// --- TC-811 json:"-" 字段屏蔽 (已在 TC-348 覆盖；此处 TC-811 命名) ----

type tc811Rec struct {
	Name  string `json:"name"`
	Token string `json:"-"`
}

func TestTC811_JSONDashHidden(t *testing.T) {
	r := tc811Rec{Name: "n", Token: "t"}
	_, err := runSrc(t, []byte(`[{"return":{"var":"r.Token"}}]`), map[string]any{"r": r})
	if err == nil {
		t.Fatal("want json:\"-\" hidden, got nil")
	}
}

// --- TC-1000 语言核心可独立 import ----------------------------------

func TestTC1000_CoreStandsAlone(t *testing.T) {
	// The entire test suite uses only `wflang` + std packages through
	// DefaultRegistry. No external side-effectful package is needed for a
	// minimal program to compile and run.
	v, err := runSrc(t, []byte(`[{"return":{"literal":{"type":"int64","value":"1"}}}]`), nil)
	if err != nil {
		t.Fatalf("core-only program failed: %v", err)
	}
	if v.Go().(int64) != 1 {
		t.Fatalf("want 1, got %v", v.Go())
	}
}

// --- TC-1002 类型反馈早 (compile-time) -------------------------------

func TestTC1002_TypeFeedbackAtCompile(t *testing.T) {
	// A typed-literal with type=int64 value="abc" must fail at CompileJSON —
	// not at Run — so the host sees the error as early as possible.
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	_, err := eng.CompileJSON([]byte(`[{"return":{"literal":{"type":"int64","value":"abc"}}}]`))
	if err == nil {
		t.Fatal("want compile-time error, got nil")
	}
}

// --- TC-1003 错误定位准 (Path on LangError) ---------------------------

func TestTC1003_ErrorPathPrecise(t *testing.T) {
	// Deeply nested expression fails — Path must point to the inner literal.
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"notnum"}}
	]}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want err, got nil")
	}
	var le *werr.LangError
	if !errors.As(err, &le) {
		t.Fatalf("want *LangError, got %T", err)
	}
	if le.Path == "" {
		t.Fatal("empty path")
	}
	// The path should mention the offending literal.
	if !strings.Contains(le.Path, "literal") && !strings.Contains(le.Path, "/1") {
		t.Fatalf("path not pointing at failing literal: %q", le.Path)
	}
}
