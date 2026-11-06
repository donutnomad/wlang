package runtime

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/donutnomad/wlang/compiler"
	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/types"
)

// deferFunctionRecorder observes host calls in registration/execution order.
type deferFunctionRecorder struct {
	events []string
}

func (r *deferFunctionRecorder) Record(s string) { r.events = append(r.events, s) }
func (r *deferFunctionRecorder) RecordInt(n int64) {
	r.events = append(r.events, "n="+strconv.FormatInt(n, 10))
}
func (r *deferFunctionRecorder) Fail() error { return errors.New("boom") }

func (r *deferFunctionRecorder) eventsString() string { return strings.Join(r.events, ",") }

func runDeferProgram(t *testing.T, src string) (*deferFunctionRecorder, types.Value, error) {
	t.Helper()
	rec := &deferFunctionRecorder{}
	reg := registry.New()
	if err := reg.BindGoPackage("probe", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Record", Impl: rec.Record},
			{GoName: "RecordInt", Impl: rec.RecordInt},
			{GoName: "Fail", Impl: rec.Fail},
		},
	}); err != nil {
		t.Fatalf("BindGoPackage: %v", err)
	}
	prog, err := compiler.ParseProgram([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	exec := NewExecutor(context.Background(), NewScope(), reg, reg.PackageNames(), Budget{})
	v, runErr := exec.RunProgram(prog)
	return rec, v, runErr
}

func assertDeferEvents(t *testing.T, rec *deferFunctionRecorder, want ...string) {
	t.Helper()
	if got := rec.eventsString(); got != strings.Join(want, ",") {
		t.Fatalf("defer events = %q, want %q", got, strings.Join(want, ","))
	}
}

// Defer registered inside an if block belongs to the enclosing function and
// runs when the function returns, not when the if block exits.
func TestDeferInIfRunsAtFunctionExit(t *testing.T) {
	src := `[
		{"let":{"out":{"literal":{"type":"string","value":"body|"}}}},
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
				{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"if-defer"}}]}},
				{"set":{"out":{"+":[{"var":"out"},{"literal":{"type":"string","value":"in-if|"}}]}}}
			]}},
			{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"fn-defer"}}]}},
			{"set":{"out":{"+":[{"var":"out"},{"literal":{"type":"string","value":"after-if|"}}]}}},
			{"return":{"named":"out"}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, v, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertDeferEvents(t, rec, "fn-defer", "if-defer")
	if v.TypeName() != types.TString || v.Go().(string) != "body|in-if|after-if|" {
		t.Fatalf("want body|in-if|after-if|, got %s=%v", v.TypeName(), v.Go())
	}
}

// Nested if blocks do not drain on exit; all registrations stay on the
// function frame and run in reverse registration order at return.
func TestDeferInNestedBlocksRunsAtFunctionExit(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
				{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"nested-defer"}}]}},
				{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"inside-if"}}]}},
				{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
					{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"inner-if-defer"}}]}},
					{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"inside-inner"}}]}}
				]}}
			]}},
			{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"after-blocks"}}]}},
			{"return":{"literal":{"type":"string","value":"done"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertDeferEvents(t, rec,
		"inside-if", "inside-inner", "after-blocks", "inner-if-defer", "nested-defer")
}

// Loop exits via break/continue do not run the function's defers; every
// iteration registration stays on the function frame until it returns.
func TestDeferInLoopSurvivesBreakAndContinue(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"fori":{"var":"i","from":{"literal":{"type":"int64","value":"0"}},
				"to":{"literal":{"type":"int64","value":"3"}},"do":[
				{"if":{"cond":{"==":[{"var":"i"},{"literal":{"type":"int64","value":"0"}}]},"then":[
					{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"iter-0"}}]}},
					{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"body-0"}}]}}
				]}},
				{"if":{"cond":{"==":[{"var":"i"},{"literal":{"type":"int64","value":"1"}}]},"then":[
					{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"iter-1"}}]}},
					{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"body-1"}}]}},
					{"continue":{}}
				]}},
				{"if":{"cond":{"==":[{"var":"i"},{"literal":{"type":"int64","value":"2"}}]},"then":[
					{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"iter-2"}}]}},
					{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"body-2"}}]}},
					{"break":{}}
				]}}
			]}},
			{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"loop-done"}}]}},
			{"return":{"literal":{"type":"string","value":"done"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertDeferEvents(t, rec,
		"body-0", "body-1", "body-2", "loop-done", "iter-2", "iter-1", "iter-0")
}

// Each function invocation owns its defer stack: an inner call drains its own
// frame and never consumes the caller's defers.
func TestNestedFunctionsHaveIndependentDeferStacks(t *testing.T) {
	src := `[
		{"let":{"inner":{"fn":{"params":[],"returns":["string"],"do":[
			{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
				{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"inner-late"}}]}}
			]}},
			{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"inner-early"}}]}},
			{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"inner-body"}}]}},
			{"return":{"literal":{"type":"string","value":"i"}}}
		]}}}},
		{"let":{"outer":{"fn":{"params":[],"returns":["string"],"do":[
			{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"outer-early"}}]}},
			{"expr":{"call":{"fn":{"var":"inner"},"args":[]}}},
			{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"outer-after"}}]}},
			{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"outer-late"}}]}},
			{"return":{"literal":{"type":"string","value":"o"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"outer"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertDeferEvents(t, rec,
		"inner-body", "inner-early", "inner-late", "outer-after", "outer-late", "outer-early")
}

func TestRecursiveCallsDrainTheirOwnDefers(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[["n","int64"]],"returns":["int64"],"do":[
			{"defer":{"RecordInt":[{"pkg":"probe"},{"var":"n"}]}},
			{"if":{"cond":{">":[{"var":"n"},{"literal":{"type":"int64","value":"0"}}]},"then":[
				{"return":{"call":{"fn":{"var":"f"},"args":[
					{"-":[{"var":"n"},{"literal":{"type":"int64","value":"1"}}]}
				]}}}
			]}},
			{"return":{"var":"n"}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[{"literal":{"type":"int64","value":"2"}}]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertDeferEvents(t, rec, "n=0", "n=1", "n=2")
}

// A deferred closure keeps a variable from an already-exited lexical block
// alive, and its write is visible to the named return after the drain.
func TestDeferredClosureCapturesExitedBlockVariable(t *testing.T) {
	src := `[
		{"let":{"out":{"literal":{"type":"string","value":"start"}}}},
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
				{"let":{"local":{"literal":{"type":"string","value":"captured"}}}},
				{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
					{"set":{"out":{"+":[{"var":"local"},{"literal":{"type":"string","value":"!"}}]}}},
					{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"deferred-capture"}}]}}
				]}},"args":[]}}},
				{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"in-block"}}]}}
			]}},
			{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"after-block"}}]}},
			{"return":{"named":"out"}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, v, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertDeferEvents(t, rec, "in-block", "after-block", "deferred-capture")
	if v.Go().(string) != "captured!" {
		t.Fatalf("named return after defer = %v, want captured!", v.Go())
	}
}

func TestDeferArgumentsAreEvaluatedAtRegistration(t *testing.T) {
	src := `[
		{"let":{"x":{"literal":{"type":"string","value":"old"}}}},
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"defer":{"Record":[{"pkg":"probe"},{"var":"x"}]}},
			{"set":{"x":{"literal":{"type":"string","value":"new"}}}},
			{"return":{"var":"x"}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertDeferEvents(t, rec, "old")
}

// A defer registered by a deferred function belongs to that invocation and
// drains before the deferred function returns.
func TestDeferredFunctionOwnsNestedDefers(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
				{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"inner-defer"}}]}},
				{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"deferred-body"}}]}}
			]}},"args":[]}}},
			{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"body"}}]}},
			{"return":{"literal":{"type":"string","value":"x"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertDeferEvents(t, rec, "body", "deferred-body", "inner-defer")
}

// A failing later defer does not stop earlier defers: the LIFO drain continues
// and the first error in drain order is reported.
func TestFailingDeferDoesNotStopRemainingDefers(t *testing.T) {
	// The later-registered defer fails with a return-type error; the earlier
	// defer must still run and the deferred error must surface.
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"registered-first"}}]}},
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":["string"],"do":[
				{"return":{"literal":{"type":"int64","value":"1"}}}
			]}},"args":[]}}},
			{"return":{"literal":{"type":"string","value":"ok"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err == nil {
		t.Fatal("want deferred failure")
	}
	if le, ok := err.(*werr.LangError); !ok || le.Code != werr.CodeType {
		t.Fatalf("want %s, got %T %v", werr.CodeType, err, err)
	}
	assertDeferEvents(t, rec, "registered-first")
}

// An existing runtime error still drains the function frame LIFO, while the
// original runtime error keeps precedence over a deferred failure.
func TestRuntimeErrorExitStillDrainsDefers(t *testing.T) {
	src := `[
		{"let":{"empty":{"array":{"elem":"int64","items":[]}}}},
		{"let":{"f":{"fn":{"params":[],"returns":["int64"],"do":[
			{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"ran-before-error"}}]}},
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":["string"],"do":[
				{"return":{"literal":{"type":"int64","value":"1"}}}
			]}},"args":[]}}},
			{"return":{"arr.get":[{"var":"empty"},{"literal":{"type":"int64","value":"0"}}]}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err == nil {
		t.Fatal("want runtime error")
	}
	if le, ok := err.(*werr.LangError); !ok || le.Code != werr.CodeRuntime {
		t.Fatalf("want %s, got %T %v", werr.CodeRuntime, err, err)
	}
	assertDeferEvents(t, rec, "ran-before-error")
}

// The routine body is its own implicit function: its defers drain when the
// body ends and never touch the caller's frame.
func TestRoutineDefersAreIsolatedFromCaller(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"let":{"h":{"routine":{"do":[
				{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
					{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"routine-defer"}}]}}
				]}},
				{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"routine-body"}}]}},
				{"return":{"literal":{"type":"string","value":"r"}}}
			]}}}},
			{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"caller-defer"}}]}},
			{"expr":{"await":{"var":"h"}}},
			{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"after-await"}}]}},
			{"return":{"literal":{"type":"string","value":"done"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertDeferEvents(t, rec, "routine-body", "routine-defer", "after-await", "caller-defer")
}

// An early return from inside a block still drains all defers registered on
// the function frame, including ones registered before the return branch.
func TestEarlyReturnDrainsFunctionDefers(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"outer"}}]}},
			{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
				{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"inner"}}]}},
				{"return":{"literal":{"type":"string","value":"early"}}}
			]}},
			{"return":{"literal":{"type":"string","value":"late"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertDeferEvents(t, rec, "inner", "outer")
}

// A routine body is an implicit function frame: named return resolves after
// its own defers run, and the caller's await observes the final value.
func TestRoutineBodyNamedReturnResolvesAfterDefer(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"let":{"h":{"routine":{"do":[
				{"let":{"r":{"literal":{"type":"string","value":"body"}}}},
				{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
					{"set":{"r":{"+":[{"var":"r"},{"literal":{"type":"string","value":"|defer"}}]}}}
				]}},"args":[]}}},
				{"return":{"named":"r"}}
			]}}}},
			{"return":{"await":{"var":"h"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	_, v, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != types.TString || v.Go().(string) != "body|defer" {
		t.Fatalf("routine named return after defer = %s=%v, want body|defer", v.TypeName(), v.Go())
	}
}

// A closure registers defer on its invocation frame, not on the frame that
// created it: the creator's block has already exited when the closure runs.
func TestClosureDeferUsesInvocationFrame(t *testing.T) {
	src := `[
		{"let":{"c":{"fn":{"params":[],"returns":[],"do":[]}}}},
		{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
			{"set":{"c":{"fn":{"params":[],"returns":[],"do":[
				{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"closure-defer"}}]}},
				{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"closure-body"}}]}}
			]}}}}
		]}},
		{"let":{"caller":{"fn":{"params":[],"returns":["string"],"do":[
			{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"caller-defer"}}]}},
			{"expr":{"call":{"fn":{"var":"c"},"args":[]}}},
			{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"after-closure"}}]}},
			{"return":{"literal":{"type":"string","value":"done"}}}
		]}}}},
		{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"program-defer"}}]}},
		{"return":{"call":{"fn":{"var":"caller"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertDeferEvents(t, rec,
		"closure-body", "closure-defer", "after-closure", "caller-defer", "program-defer")
}

// Go discards a deferred call's return value. A host error returned as a
// value does not replace the named return; authors must capture and merge it.
func TestDeferredHostErrorValueIsDiscarded(t *testing.T) {
	src := `[
		{"let":{"out":{"literal":{"type":"string","value":"original"}}}},
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"defer":{"Fail":[{"pkg":"probe"}]}},
			{"return":{"named":"out"}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	_, v, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != types.TString || v.Go().(string) != "original" {
		t.Fatalf("deferred host error value must be discarded, got %s=%v", v.TypeName(), v.Go())
	}
}

// When several defers produce LangErrors, the first error in LIFO execution
// order is reported and the remaining defers still run.
func TestFirstDeferredLangErrorWinsInLIFO(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"first-registered"}}]}},
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":["string"],"do":[
				{"return":{"literal":{"type":"int64","value":"1"}}}
			]}},"args":[]}}},
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
				{"expr":{"var":"missing"}}
			]}},"args":[]}}},
			{"return":{"literal":{"type":"string","value":"ok"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err == nil {
		t.Fatal("want deferred LangError")
	}
	if le, ok := err.(*werr.LangError); !ok || le.Code != werr.CodeSymbol {
		t.Fatalf("want first LIFO deferred error %s, got %T %v", werr.CodeSymbol, err, err)
	}
	assertDeferEvents(t, rec, "first-registered")
}

// match and expression-if blocks use evalBlock, which has no drain of its own.
// Defers registered there must reach the enclosing function frame.
func TestDeferInMatchAndExpressionIfBlocks(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"let":{"matched":{"match":{"value":{"literal":{"type":"int64","value":"1"}},"cases":[
				{"when":{"literal":{"type":"int64","value":"1"}},"do":[
					{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"match-defer"}}]}},
					{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"in-match"}}]}},
					{"literal":{"type":"int64","value":"7"}}
				]}
			]}}}},
			{"let":{"chosen":{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
				{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"expr-if-defer"}}]}},
				{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"in-expr-if"}}]}},
				{"literal":{"type":"int64","value":"8"}}
			]}}}},
			{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"after"}}]}},
			{"return":{"literal":{"type":"string","value":"done"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertDeferEvents(t, rec, "in-match", "in-expr-if", "after", "expr-if-defer", "match-defer")
}

// A select case body is a lexical block; its defer drains at function return.
func TestDeferInSelectCaseRunsAtFunctionExit(t *testing.T) {
	src := `[
		{"let":{"ch":{"chan":["int64",{"literal":{"type":"int64","value":"1"}}]}}},
		{"expr":{"ch.send":[{"var":"ch"},{"literal":{"type":"int64","value":"7"}}]}},
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"select":[{"case":{"recv":[{"var":"ch"}],"bind":["v","_"],"do":[
				{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"select-defer"}}]}},
				{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"in-case"}}]}}
			]}}]},
			{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"after-select"}}]}},
			{"return":{"literal":{"type":"string","value":"done"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertDeferEvents(t, rec, "in-case", "after-select", "select-defer")
}

// Foreach registers one defer per iteration; all accumulate on the function
// frame and drain in reverse registration order at return.
func TestForeachDefersAccumulateUntilFunctionExit(t *testing.T) {
	src := `[
		{"let":{"items":{"array":{"elem":"int64","items":[
			{"literal":{"type":"int64","value":"1"}},
			{"literal":{"type":"int64","value":"2"}}
		]}}}},
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"foreach":{"target":{"var":"items"},"as":"item","do":[
				{"if":{"cond":{"==":[{"var":"item"},{"literal":{"type":"int64","value":"1"}}]},"then":[
					{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"item-1"}}]}},
					{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"body-1"}}]}}
				]}},
				{"if":{"cond":{"==":[{"var":"item"},{"literal":{"type":"int64","value":"2"}}]},"then":[
					{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"item-2"}}]}},
					{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"body-2"}}]}}
				]}}
			]}},
			{"expr":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"done"}}]}},
			{"return":{"literal":{"type":"string","value":"x"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertDeferEvents(t, rec, "body-1", "body-2", "done", "item-2", "item-1")
}

// A routine body that exits with a runtime error still drains its own defers;
// the original error keeps priority through await.
func TestRoutineErrorExitStillDrainsDefers(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"let":{"h":{"routine":{"do":[
				{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"routine-error-defer"}}]}},
				{"expr":{"arr.get":[{"array":{"elem":"int64","items":[]}},{"literal":{"type":"int64","value":"0"}}]}}
			]}}}},
			{"expr":{"await":{"var":"h"}}},
			{"return":{"literal":{"type":"string","value":"ok"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err == nil {
		t.Fatal("want routine runtime error")
	}
	if le, ok := err.(*werr.LangError); !ok || le.Code != werr.CodeRuntime {
		t.Fatalf("want %s, got %T %v", werr.CodeRuntime, err, err)
	}
	assertDeferEvents(t, rec, "routine-error-defer")
}

// panic exits with E_PANIC; defers still drain and do not recover the panic.
func TestPanicExitStillDrainsDefers(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"defer":{"Record":[{"pkg":"probe"},{"literal":{"type":"string","value":"before-panic"}}]}},
			{"panic":{"literal":{"type":"string","value":"boom"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	rec, _, err := runDeferProgram(t, src)
	if err == nil {
		t.Fatal("want panic error")
	}
	if le, ok := err.(*werr.LangError); !ok || le.Code != werr.CodePanic {
		t.Fatalf("want %s, got %T %v", werr.CodePanic, err, err)
	}
	assertDeferEvents(t, rec, "before-panic")
}

// RunProgram treats the program body as an implicit function frame even when
// the host supplies a non-root scope.
func TestProgramTopLevelDeferOnChildScope(t *testing.T) {
	src := `[
		{"let":{"out":{"literal":{"type":"string","value":"body"}}}},
		{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
			{"set":{"out":{"+":[{"var":"out"},{"literal":{"type":"string","value":"|defer"}}]}}}
		]}},"args":[]}}},
		{"return":{"named":"out"}}
	]`
	root := NewScope()
	root.Let("rootVar", types.NewValue(types.TString, "root"), "")
	root.PushDeferred("Unused", types.Value{}, nil, "/test")
	child := root.Push()
	v, err := runSrcWithScope(t, src, child)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != types.TString || v.Go().(string) != "body|defer" {
		t.Fatalf("top-level defer on child scope = %s=%v, want body|defer", v.TypeName(), v.Go())
	}
	if len(root.deferred) != 1 {
		t.Fatalf("program run consumed ancestor frame defers: %d pending", len(root.deferred))
	}
	root.PopDeferred()
	if _, ok := child.FrameVars()["rootVar"]; !ok {
		t.Fatal("program scope was not restored to a non-root frame")
	}
}

// A deferred LangError wins over a named-return resolution error.
func TestDeferredErrorWinsOverNamedReturnResolutionError(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":["string"],"do":[
				{"return":{"literal":{"type":"int64","value":"1"}}}
			]}},"args":[]}}},
			{"return":{"named":"missing"}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	_, _, err := runDeferProgram(t, src)
	if err == nil {
		t.Fatal("want deferred error")
	}
	if le, ok := err.(*werr.LangError); !ok || le.Code != werr.CodeType {
		t.Fatalf("want deferred %s, got %T %v", werr.CodeType, err, err)
	}
}

// A deferred LangError also wins over return-type validation.
func TestDeferredErrorWinsOverReturnTypeValidation(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
				{"expr":{"var":"missing"}}
			]}},"args":[]}}},
			{"return":{"literal":{"type":"int64","value":"1"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	_, _, err := runDeferProgram(t, src)
	if err == nil {
		t.Fatal("want deferred error")
	}
	if le, ok := err.(*werr.LangError); !ok || le.Code != werr.CodeSymbol {
		t.Fatalf("want deferred %s, got %T %v", werr.CodeSymbol, err, err)
	}
}
