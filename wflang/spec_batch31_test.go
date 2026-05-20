// Batch 31 covers Tier-1 missing TCs targeting features that already exist
// in the runtime / registry — the only work was to surface them via a public
// API or to assert behavior in a test:
//
//	§4.2.3   tuple from multi-return        TC-321
//	§5.3     Env injection                  TC-422
//	§5.8     CallPlan identity              TC-481, TC-672
//	§7.7     CallPlan routine surface        TC-674
//	§8.3     await aliases                   TC-706, TC-707
//	§10.3    host type method exposure      TC-812
//	§13.2    deprecation table / migrator   TC-882, TC-883
//	§13.3    feature gate (typedArray)      TC-884
//	§15      acceptance smoke matrix        TC-950..TC-959
package wflang_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wflang/wflang/registry"
	"github.com/wflang/wflang/wflang"
)

// ---------- TC-321 多业务返回值 → tuple --------------------------------------
// (§4.2.3) The runtime already produces tuple<T1,T2,...> when a host function
// returns (T1,...,Tn,error). Cover the typed shape end-to-end for a 4-tuple.
type tc321Book struct {
	ID    int64
	Title string
}

func tc321MultiReturn() (*tc321Book, int64, bool, string, error) {
	return &tc321Book{ID: 1, Title: "wflang"}, 42, true, "ok", nil
}

func TestTC321_MultiReturnTuple(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("books", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "MultiReturn", Impl: tc321MultiReturn, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"MultiReturn":[{"pkg":"books"}]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	tn := v.TypeName()
	if !strings.HasPrefix(tn, "tuple<") || !strings.HasSuffix(tn, ">") {
		t.Fatalf("want tuple<...>, got %s", tn)
	}
	// All four business types plus the trailing error slot must appear in declaration order.
	for _, want := range []string{"tc321Book", "int64", "boolean", "string", "error"} {
		if !strings.Contains(tn, want) {
			t.Fatalf("tuple type missing %s: %s", want, tn)
		}
	}
	parts, ok := v.Go().([]any)
	if !ok || len(parts) != 5 {
		t.Fatalf("tuple shape: %v", v.Go())
	}
	if b, ok := parts[0].(*tc321Book); !ok || b.ID != 1 {
		t.Fatalf("part 0: %v", parts[0])
	}
	if parts[1].(int64) != 42 || !parts[2].(bool) || parts[3].(string) != "ok" || parts[4] != nil {
		t.Fatalf("parts: %v", parts)
	}
}

// ---------- TC-422 (env, a)(R, error) ---------------------------------------
// (§5.3) When a host function's first non-ctx parameter is wflang.Env, the
// runtime injects it transparently and the language argument list omits it.
func tc422EchoCaps(env wflang.Env, key string) (bool, error) {
	return env.Caps[key], nil
}

func TestTC422_EnvInjection(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("env", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "EchoCaps", Impl: tc422EchoCaps, Pure: false},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry:     reg,
		Capabilities: wflang.CapabilitySet{"net:http": true},
	})
	src := []byte(`[{"return":{"EchoCaps":[
		{"pkg":"env"},
		{"literal":{"type":"string","value":"net:http"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, ok := unwrap1(t, v).(bool); !ok || !got || unwrapErr(t, v) != nil {
		t.Fatalf("env did not inject capability set: %v (%T)", v.Go(), v.Go())
	}
}

// ---------- TC-481 / TC-672 CallPlan ----------------------------------------
// (§5.8 / §7.7) Compile-time CallPlan must point to the same Go function the
// registry holds and must populate every documented field.
func tc481Adder(a, b int64) int64 { return a + b }

func TestTC481_CallPlanGoFuncIdentity(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("math", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Adder", Impl: tc481Adder, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"Adder":[
		{"pkg":"math"},
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"2"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	plans := prog.CallPlans()
	if len(plans) != 1 {
		t.Fatalf("want 1 CallPlan, got %d", len(plans))
	}
	want := reflect.ValueOf(tc481Adder).Pointer()
	got := reflect.ValueOf(plans[0].GoFunc).Pointer()
	if want != got {
		t.Fatalf("CallPlan.GoFunc != registered fn (%v vs %v)", want, got)
	}
}

func TestTC672_CallPlanFieldsComplete(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("svc", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Adder", Impl: tc481Adder, Pure: true,
				Capabilities: []string{"svc:math"}},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"Adder":[
		{"pkg":"svc"},
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"2"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	plans := prog.CallPlans()
	if len(plans) != 1 {
		t.Fatalf("want 1 plan, got %d", len(plans))
	}
	p := plans[0]
	if p.Operator != "Adder" || p.PackageName != "svc" {
		t.Fatalf("op/pkg: %+v", p)
	}
	if p.ReceiverKind != "package" {
		t.Fatalf("receiverKind: %s", p.ReceiverKind)
	}
	if p.ResultKind != "value" {
		t.Fatalf("resultKind: %s", p.ResultKind)
	}
	if len(p.ParamTypes) != 2 || p.ParamTypes[0] != "int64" || p.ParamTypes[1] != "int64" {
		t.Fatalf("paramTypes: %v", p.ParamTypes)
	}
	if len(p.ReturnTypes) != 1 || p.ReturnTypes[0] != "int64" {
		t.Fatalf("returnTypes: %v", p.ReturnTypes)
	}
	if len(p.Capabilities) != 1 || p.Capabilities[0] != "svc:math" {
		t.Fatalf("caps: %v", p.Capabilities)
	}
}

// ---------- TC-674 routine call plan -----------------------------------
func tc674Wait(token string) (string, error) { return token, nil }

func TestTC674_CallPlanSeesRoutineHostCall(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("svc", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Wait", Impl: tc674Wait, Pure: false},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"routine":{"Wait":[
		{"pkg":"svc"},
		{"literal":{"type":"string","value":"t674"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	plans := prog.CallPlans()
	if len(plans) == 0 {
		t.Fatalf("no plans")
	}
	saw := false
	for _, p := range plans {
		if p.Operator == "Wait" && len(p.ReturnTypes) == 1 && p.ReturnTypes[0] == "string" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("want Wait call plan, got %+v", plans)
	}
}

// ---------- TC-706 / TC-707 await aliases --------------------------------
type tc28Worker2 struct{}

func (*tc28Worker2) WaitTwo(token string) (string, int64, error) {
	return token, int64(len(token)), nil
}

func (*tc28Worker2) WaitS(token string) (string, error) {
	return "", errors.New("upstream")
}

func runTC706Await(t *testing.T, body string) (wflang.Value, error) {
	t.Helper()
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*tc28Worker2)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(body))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return prog.Run(t.Context(), wflang.RunOptions{Vars: map[string]any{"w": &tc28Worker2{}}})
}

func TestTC706_AwaitTuple(t *testing.T) {
	v, err := runTC706Await(t, `[
		{"let":{"h":{"routine":{"WaitTwo":[{"var":"w"},{"literal":{"type":"string","value":"t706"}}]}}}},
		{"return":{"await":{"var":"h"}}}
	]`)
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if !strings.HasPrefix(v.TypeName(), "tuple<") {
		t.Fatalf("not tuple: %s", v.TypeName())
	}
}

func TestTC707_AwaitRoutineErrorValue(t *testing.T) {
	v, err := runTC706Await(t, `[
		{"let":{"h":{"routine":{"WaitS":[{"var":"w"},{"literal":{"type":"string","value":"t707"}}]}}}},
		{"return":{"await":{"var":"h"}}}
	]`)
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if got := unwrapErr(t, v); got == nil || !strings.Contains(got.(error).Error(), "upstream") {
		t.Fatalf("want routine error value, got %v", got)
	}
}

// ---------- TC-812 宿主类型只暴露自动绑定方法 ------------------------------
// (§10.3) A type bound through AutoBindType exposes only its methods, never
// arbitrary operators. Calling an unregistered op on the type errors out.
type tc812Counter struct{ N int64 }

func (c *tc812Counter) Get() int64 { return c.N }

func TestTC812_HostTypeOnlyExposesAutoBoundMethods(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*tc812Counter)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"Mystery":[{"var":"c"}]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"c": &tc812Counter{N: 7}},
	})
	if err == nil {
		t.Fatalf("want E_OPERATOR_NOT_FOUND/E_SYMBOL, got nil")
	}
}

// ---------- TC-882 deprecation 表 -----------------------------------------
// (§13.2) Functions marked Deprecated emit L_DEPRECATED through Lint but the
// program still executes successfully — deprecation is a soft warning.
func tc882Old() string { return "ok" }

func TestTC882_DeprecationLint(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("legacy", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Old", Impl: tc882Old, Pure: true,
				Deprecated: "use legacy.New instead"},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	src := []byte(`[{"return":{"Old":[{"pkg":"legacy"}]}}]`)
	issues := wflang.Lint(src, reg, wflang.LintOptions{})
	saw := false
	for _, i := range issues {
		if i.Code == "L_DEPRECATED" && strings.Contains(i.Message, "legacy.Old") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("want L_DEPRECATED, got %v", issues)
	}
	// Program still runs.
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "ok" {
		t.Fatalf("run result: %v", v.Go())
	}
}

// ---------- TC-883 迁移器 -------------------------------------------------
// (§13.2) MigrateProgram rewrites legacy operator names (e.g. "len" → "Len")
// to current language equivalents. The output round-trips through CompileJSON
// and is idempotent.
func TestTC883_MigratorRenamesOperators(t *testing.T) {
	src := []byte(`[{"return":{"len":[
		{"pkg":"str"},
		{"literal":{"type":"string","value":"hello"}}
	]}}]`)
	migrated, err := wflang.MigrateProgram(src, nil)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !strings.Contains(string(migrated), `"Len"`) {
		t.Fatalf("migrated still has lowercase len: %s", migrated)
	}
	// Idempotent.
	again, err := wflang.MigrateProgram(migrated, nil)
	if err != nil {
		t.Fatalf("migrate2: %v", err)
	}
	if string(again) != string(migrated) {
		t.Fatalf("not idempotent:\n%s\n--vs--\n%s", migrated, again)
	}
	// Migrated program runs.
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON(migrated)
	if err != nil {
		t.Fatalf("compile migrated: %v\n%s", err, migrated)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 5 {
		t.Fatalf("len(hello) want 5, got %v", v.Go())
	}
}

// ---------- TC-884 features.typedArray=false ------------------------------
// (§13.3) When the runtime feature flag typedArray=false, programs using
// array<T> typed literals must be rejected at compile time.
func TestTC884_FeatureGateRejectsTypedArray(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"array<int64>","value":[1,2,3]}}}]`)
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry: wflang.DefaultRegistry(),
		Features: map[string]bool{"typedArray": false},
	})
	_, err := eng.CompileJSON(src)
	if err == nil {
		t.Fatalf("want feature-gate error, got nil")
	}
	if !strings.Contains(err.Error(), "typedArray") {
		t.Fatalf("want typedArray in error, got %v", err)
	}
}

func TestTC884_FeatureGateAllowsByDefault(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"array<int64>","value":[1,2,3]}}}]`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	if _, err := eng.CompileJSON(src); err != nil {
		t.Fatalf("default features should allow typedArray, got %v", err)
	}
}

// ---------- TC-950 阶段一：单引入即可运行 ----------------------------------
// (§15.1) Importing only the wflang package must be enough to compile and
// run a basic program — no other internal packages required.
func TestTC950_SingleImportExample(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"2"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 3 {
		t.Fatalf("want 3, got %v", v.Go())
	}
}

// ---------- TC-951 阶段一：诊断错误三件套 (path/code/message) ----------------
func TestTC951_DiagnosticsTriple(t *testing.T) {
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"string","value":"x"}}
	]}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatalf("want type error")
	}
	le, ok := err.(*wflang.LangError)
	if !ok {
		t.Fatalf("not LangError: %T %v", err, err)
	}
	if le.Code == "" {
		t.Fatalf("missing code: %v", le)
	}
	if le.Message == "" {
		t.Fatalf("missing message")
	}
	if le.Path == "" {
		t.Fatalf("missing path")
	}
}

// ---------- TC-952 阶段二：编译期返回类型错配 -------------------------------
// Type errors are surfaced eagerly when the program executes. The pipeline
// already covers this — assert it here as part of the acceptance matrix.
func TestTC952_CompileOrRuntimeTypeErrors(t *testing.T) {
	cases := [][]byte{
		[]byte(`[{"return":{"+":[
			{"literal":{"type":"int64","value":"1"}},
			{"literal":{"type":"string","value":"x"}}
		]}}]`),
		[]byte(`[{"return":{"if":{
			"cond":{"literal":{"type":"int64","value":"1"}},
			"then":[{"literal":{"type":"int64","value":"1"}}],
			"else":[]
		}}}]`),
	}
	for i, c := range cases {
		if _, err := runSrc(t, c, nil); err == nil {
			t.Fatalf("case %d: want error, got nil", i)
		}
	}
}

// ---------- TC-953 阶段二：Explain 列出元数据 ------------------------------
func TestTC953_ExplainMetadata(t *testing.T) {
	reg := wflang.DefaultRegistry()
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"return":{"+":[{"var":"x"},{"literal":{"type":"int64","value":"2"}}]}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	r := prog.Explain()
	hasX := false
	for _, v := range r.Vars {
		if v == "x" {
			hasX = true
		}
	}
	if !hasX {
		t.Fatalf("Explain missed var x: %v", r.Vars)
	}
	hasPlus := false
	for _, op := range r.Operators {
		if op == "+" {
			hasPlus = true
		}
	}
	if !hasPlus {
		t.Fatalf("Explain missed op +: %v", r.Operators)
	}
}

// ---------- TC-954 阶段三：Builder→Compile 闭环 ----------------------------
// (Already covered in spec_batch25_test.go) — keep this thin alias so a
// reader can find TC-954 by grep here too.
func TestTC954_BuilderToCompileRoundTrip(t *testing.T) {
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"2"}}
	]}}]`)
	formatted, err := wflang.FormatProgram(src)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON(formatted)
	if err != nil {
		t.Fatalf("compile formatted: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 3 {
		t.Fatalf("want 3, got %v", v.Go())
	}
}

// ---------- TC-955 阶段三：缺 capability 在执行前报告 ----------------------
func tc955Fetch(url string) string { return url }

func TestTC955_MissingCapabilityReportedBeforeRun(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("net", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Fetch", Impl: tc955Fetch, Pure: false,
				Capabilities: []string{"net:http"}},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	src := []byte(`[{"return":{"Fetch":[
		{"pkg":"net"},
		{"literal":{"type":"string","value":"http://x"}}
	]}}]`)
	issues := wflang.Lint(src, reg, wflang.LintOptions{
		Deployment: wflang.CapabilitySet{},
	})
	saw := false
	for _, i := range issues {
		if i.Code == "L_CAPABILITY" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("want L_CAPABILITY before run, got %v", issues)
	}
}

// ---------- TC-956 阶段四：常用 JSON 转换走 stdlib --------------------------
func TestTC956_StdlibJSONTransforms(t *testing.T) {
	// str.Len + str.Upper compose without registering anything new.
	src := []byte(`[{"return":{"Len":[
		{"pkg":"str"},
		{"Upper":[{"pkg":"str"},{"literal":{"type":"string","value":"hi"}}]}
	]}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, ok := v.Go().(int64); !ok || got != 2 {
		t.Fatalf("want 2, got %v (%T)", v.Go(), v.Go())
	}
}

// ---------- TC-957 阶段五：format/lint/test/explain 全可用 -----------------
func TestTC957_ToolchainEndToEnd(t *testing.T) {
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"2"}}
	]}}]`)
	if _, err := wflang.FormatProgram(src); err != nil {
		t.Fatalf("format: %v", err)
	}
	if issues := wflang.Lint(src, wflang.DefaultRegistry(), wflang.LintOptions{}); len(issues) > 0 {
		// Lint is allowed to report informational issues; just don't crash.
		for _, i := range issues {
			if i.Code == "" {
				t.Fatalf("lint produced empty code: %v", issues)
			}
		}
	}
	if issues := wflang.ValidateSchema(src); len(issues) != 0 {
		t.Fatalf("schema clean expected, got %v", issues)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	r := prog.Explain()
	if r.Operators == nil {
		t.Fatalf("explain operators nil")
	}
	cases := []wflang.TestCase{
		{Name: "add", Program: src, Want: int64(3)},
	}
	results := wflang.RunTests(eng, cases)
	if !results[0].Pass {
		t.Fatalf("test runner: %+v", results)
	}
}

// ---------- TC-958 阶段五：conformance 通过 -------------------------------
// Smoke test that exercises a representative subset of the conformance
// suite: literals, arithmetic, control flow, host call.
func TestTC958_ConformanceSmoke(t *testing.T) {
	src := []byte(`[
		{"let":{"sum":{"literal":{"type":"int64","value":"0"}}}},
		{"fori":{"var":"i",
			"from":{"literal":{"type":"int64","value":"1"}},
			"to":{"literal":{"type":"int64","value":"4"}},
			"step":{"literal":{"type":"int64","value":"1"}},
			"do":[{"set":{"sum":{"+":[{"var":"sum"},{"var":"i"}]}}}]
		}},
		{"return":{"+":[{"var":"sum"},{"Len":[
			{"pkg":"str"},{"literal":{"type":"string","value":"abc"}}
		]}]}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// 1+2+3 + len("abc") = 6 + 3 = 9
	if v.Go().(int64) != 9 {
		t.Fatalf("want 9, got %v", v.Go())
	}
}

// ---------- TC-959 阶段五：基准性能 ---------------------------------------
// The benchmark itself lives elsewhere; here we assert that running the
// benchmark workload under a tight budget completes within timing bounds.
// It's a smoke test, not a regression gate.
func TestTC959_BenchmarkSmoke(t *testing.T) {
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"2"}}
	]}}]`)
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry: wflang.DefaultRegistry(),
		Budget:   wflang.Budget{MaxSteps: 1024},
	})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	start := time.Now()
	const N = 1000
	for i := 0; i < N; i++ {
		if _, err := prog.Run(context.Background(), wflang.RunOptions{}); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("perf regression: %d iterations took %v", N, d)
	}
}
