// Batch 29 covers the toolchain surface (LANGUAGE.md §11 / §12):
// TC-830 Trace, TC-831 Explain, TC-832 DumpAST, TC-833 EvalAt,
// TC-850 schema valid, TC-851 schema rejects naked numbers,
// TC-852 Format idempotent, TC-853 Format single-arg → array,
// TC-854 Lint multi-key, TC-855 Lint capability, TC-856 Lint complexity,
// TC-857 DocGen, TC-858 TestRunner.
package wflang_test

import (
	"context"
	"strings"
	"testing"

	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/wflang"
)

// ---------- TC-830 Trace 字段完备 ---------------------------------------
func TestTC830_TraceCapturesEvents(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	// `str.Len` is a host call that flows through HostRegistry.Invoke,
	// where the tracing wrapper records events. Builtin arithmetic ops
	// are dispatched directly by the executor and don't surface here.
	src := []byte(`[{"return":{"Len":[
		{"pkg":"str"},
		{"literal":{"type":"string","value":"hello"}}]}}]`)
	v, events, err := eng.TraceProgram(context.Background(), src, wflang.RunOptions{})
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if v.Go().(int64) != 5 {
		t.Fatalf("want 5, got %v", v.Go())
	}
	if len(events) == 0 {
		t.Fatalf("trace produced no events")
	}
	saw := false
	for _, ev := range events {
		if ev.Op == "Len" {
			saw = true
			if ev.Path == "" {
				t.Fatalf("Len event missing path")
			}
			if ev.Type != "int64" {
				t.Fatalf("Len event type: %s", ev.Type)
			}
		}
	}
	if !saw {
		t.Fatalf("no Len event captured: %+v", events)
	}
}

// ---------- TC-831 Explain 报告完整 -------------------------------------
func TestTC831_ExplainReportsSurface(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("svc", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Echo", Impl: func(s string) (string, error) { return s, nil }, Pure: false},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[
		{"let":{"x":{"var":"input"}}},
		{"routine":{"Echo":[
			{"pkg":"svc"},
			{"literal":{"type":"string","value":"tok"}}]}},
		{"return":{"var":"x"}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	r := prog.Explain()
	hasInput := false
	for _, v := range r.Vars {
		if v == "input" {
			hasInput = true
		}
	}
	if !hasInput {
		t.Fatalf("Explain missed `input` var: %v", r.Vars)
	}
	if !r.HasRoutines {
		t.Fatalf("Explain missed routine flag")
	}
	hasEcho := false
	for _, op := range r.Operators {
		if op == "Echo" {
			hasEcho = true
		}
	}
	if !hasEcho {
		t.Fatalf("Explain missed `Echo` operator: %v", r.Operators)
	}
}

// ---------- TC-832 DumpAST round-trip -----------------------------------
func TestTC832_DumpASTRoundTrip(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"4"}},
		{"literal":{"type":"int64","value":"5"}}]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	dumped, err := wflang.DumpAST(prog)
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	prog2, err := eng.CompileJSON(dumped)
	if err != nil {
		t.Fatalf("recompile dumped: %v\nDUMP=\n%s", err, dumped)
	}
	dumped2, err := wflang.DumpAST(prog2)
	if err != nil {
		t.Fatalf("redump: %v", err)
	}
	if string(dumped) != string(dumped2) {
		t.Fatalf("dump not stable:\n--- first ---\n%s\n--- second ---\n%s",
			dumped, dumped2)
	}
}

func TestDumpAST_AwaitRoundTrip(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("svc", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Echo", Impl: func(s string) (string, error) { return s, nil }},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[
		{"let":{"h":{"routine":{"Echo":[{"pkg":"svc"},{"literal":{"type":"string","value":"ok"}}]}}}},
		{"return":{"await":{"var":"h"}}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	dumped, err := wflang.DumpAST(prog)
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	prog2, err := eng.CompileJSON(dumped)
	if err != nil {
		t.Fatalf("compile dumped: %v\n%s", err, dumped)
	}
	v, err := prog2.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run dumped: %v", err)
	}
	// Routine wraps host call; await yields tuple<string,error>.
	if v.TypeName() != "tuple<string,error>" {
		t.Fatalf("got %s %v", v.TypeName(), v.Go())
	}
	parts := v.Go().([]any)
	if parts[0] != "ok" || parts[1] != nil {
		t.Fatalf("got %v", parts)
	}
}

func TestTC832_DumpTypedAST(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"literal":{"type":"int64","value":"7"}}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := wflang.DumpTypedAST(prog)
	if err != nil {
		t.Fatalf("typed dump: %v", err)
	}
	if !strings.Contains(string(out), `"int64"`) {
		t.Fatalf("typed dump missing int64: %s", out)
	}
}

// ---------- TC-833 EvalAt 单点求值 --------------------------------------
func TestTC833_EvalAtSubexpr(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"40"}},
		{"literal":{"type":"int64","value":"2"}}]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// First arg's literal node sits at /program/0/return/+/0/literal.
	v, err := prog.EvalAt(context.Background(),
		"/program/0/return/+/0/literal", wflang.RunOptions{})
	if err != nil {
		t.Fatalf("EvalAt: %v", err)
	}
	if v.Go().(int64) != 40 {
		t.Fatalf("want 40, got %v", v.Go())
	}
}

func TestTC833_EvalAtUnknownPathFails(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"literal":{"type":"int64","value":"1"}}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := prog.EvalAt(context.Background(), "/no/such/path", wflang.RunOptions{}); err == nil {
		t.Fatalf("want error for unknown path, got nil")
	}
}

// ---------- TC-850 schema 校验通过 --------------------------------------
func TestTC850_SchemaValidatesGoodProgram(t *testing.T) {
	src := []byte(`[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"return":{"var":"x"}}
	]`)
	issues := wflang.ValidateSchema(src)
	if len(issues) != 0 {
		t.Fatalf("want clean, got %v", issues)
	}
}

// ---------- TC-851 schema 拒绝裸数字 ------------------------------------
func TestTC851_SchemaRejectsNakedNumeric(t *testing.T) {
	src := []byte(`[{"return":{"+":[1,2]}}]`)
	issues := wflang.ValidateSchema(src)
	if len(issues) == 0 {
		t.Fatalf("want schema issue for naked numbers")
	}
	saw := false
	for _, i := range issues {
		if i.Code == "E_AST_SHAPE" && strings.Contains(i.Message, "bare numeric") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("want bare-numeric issue, got %v", issues)
	}
}

// ---------- TC-852 Formatter 稳定输出 ----------------------------------
func TestTC852_FormatterIdempotent(t *testing.T) {
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"2"}}]}}]`)
	first, err := wflang.FormatProgram(src)
	if err != nil {
		t.Fatalf("first format: %v", err)
	}
	second, err := wflang.FormatProgram(first)
	if err != nil {
		t.Fatalf("second format: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("not idempotent:\n--- first ---\n%s\n--- second ---\n%s",
			first, second)
	}
}

// ---------- TC-853 Formatter 单参函数标准化 -----------------------------
func TestTC853_FormatterNormalizesSingleArg(t *testing.T) {
	src := []byte(`{"Len":{"literal":{"type":"string","value":"hi"}}}`)
	formatted, err := wflang.FormatProgram(src)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	// Result should contain "Len": [ ... ] (an array of one element).
	if !strings.Contains(string(formatted), `"Len": [`) {
		t.Fatalf("expected single-arg → array form, got:\n%s", formatted)
	}
}

// ---------- TC-854 Linter 多键 operator 对象 ----------------------------
func TestTC854_LintFlagsMultiKeyObject(t *testing.T) {
	reg := wflang.DefaultRegistry()
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}}],
		"-":[]}}]`)
	issues := wflang.Lint(src, reg, wflang.LintOptions{})
	saw := false
	for _, i := range issues {
		if i.Code == "L_MULTI_KEY" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("want L_MULTI_KEY, got %v", issues)
	}
}

// ---------- TC-855 Linter capability 检查 -------------------------------
func tc855Fetch(url string) string { return url }

func TestTC855_LintFlagsMissingCapability(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("net", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Fetch", Impl: tc855Fetch, Pure: false,
				Capabilities: []string{"net:http"}},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	src := []byte(`[{"return":{"Fetch":[
		{"pkg":"net"},
		{"literal":{"type":"string","value":"http://x"}}]}}]`)
	issues := wflang.Lint(src, reg, wflang.LintOptions{
		Deployment: wflang.CapabilitySet{}, // no caps granted
	})
	saw := false
	for _, i := range issues {
		if i.Code == "L_CAPABILITY" && strings.Contains(i.Message, "net:http") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("want L_CAPABILITY, got %v", issues)
	}
}

func TestTC855_LintAllowsGrantedCapability(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("net", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Fetch", Impl: tc855Fetch, Pure: false,
				Capabilities: []string{"net:http"}},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	src := []byte(`[{"return":{"Fetch":[
		{"pkg":"net"},
		{"literal":{"type":"string","value":"http://x"}}]}}]`)
	issues := wflang.Lint(src, reg, wflang.LintOptions{
		Deployment: wflang.CapabilitySet{"net:http": true},
	})
	for _, i := range issues {
		if i.Code == "L_CAPABILITY" {
			t.Fatalf("granted but still flagged: %v", issues)
		}
	}
}

// ---------- TC-856 Linter 复杂度 / 循环预算 -----------------------------
func TestTC856_LintFlagsHighComplexity(t *testing.T) {
	src := []byte(`[
		{"foreach":{"target":{"var":"xs"},"as":"x","do":[
			{"foreach":{"target":{"var":"ys"},"as":"y","do":[
				{"expr":{"+":[
					{"literal":{"type":"int64","value":"1"}},
					{"literal":{"type":"int64","value":"2"}}]}}
			]}}
		]}}
	]`)
	issues := wflang.Lint(src, wflang.DefaultRegistry(), wflang.LintOptions{
		MaxComplexity: 5,
	})
	saw := false
	for _, i := range issues {
		if i.Code == "L_COMPLEXITY" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("want L_COMPLEXITY, got %v", issues)
	}
}

// ---------- TC-857 DocGen ----------------------------------------------
func tc857Foo(x int64) int64 { return x }

func TestTC857_DocGenLists(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("toolkit", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Foo", Impl: tc857Foo, Pure: true,
				Capabilities: []string{"toolkit:basic"}},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	doc := wflang.DocGen(reg)
	if !strings.Contains(doc, "toolkit") {
		t.Fatalf("doc missing package: %s", doc)
	}
	if !strings.Contains(doc, "Foo") {
		t.Fatalf("doc missing function: %s", doc)
	}
	if !strings.Contains(doc, "toolkit:basic") {
		t.Fatalf("doc missing capability: %s", doc)
	}
}

// ---------- TC-858 Test runner JSON 用例 -------------------------------
func TestTC858_RunTestsHappy(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	cases := []wflang.TestCase{
		{
			Name: "add",
			Program: []byte(`[{"return":{"+":[
				{"literal":{"type":"int64","value":"1"}},
				{"literal":{"type":"int64","value":"2"}}]}}]`),
			Want: int64(3),
		},
		{
			Name: "fail",
			Program: []byte(`[{"return":{"+":[
				{"literal":{"type":"int64","value":"1"}},
				{"literal":{"type":"int64","value":"2"}}]}}]`),
			Want: int64(99),
		},
	}
	results := wflang.RunTests(eng, cases)
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if !results[0].Pass {
		t.Fatalf("case 0 should pass: %+v", results[0])
	}
	if results[1].Pass {
		t.Fatalf("case 1 should fail")
	}
	if results[1].Diff == "" {
		t.Fatalf("case 1 should produce diff")
	}
}

func TestTC858_RunTestsExpectError(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	cases := []wflang.TestCase{
		{
			Name: "div0",
			Program: []byte(`[{"return":{"/":[
				{"literal":{"type":"int64","value":"1"}},
				{"literal":{"type":"int64","value":"0"}}]}}]`),
			WantErr: "division by zero",
		},
	}
	results := wflang.RunTests(eng, cases)
	if len(results) != 1 || !results[0].Pass {
		t.Fatalf("want pass, got %+v", results)
	}
}
