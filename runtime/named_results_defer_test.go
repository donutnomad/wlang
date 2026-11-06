package runtime

// Go named-result + defer semantics (LANGUAGE.md §3.3).
//
// Oracle: runtime/testdata/named_results_oracle.go is the equivalent native Go
// program. Run `go run ./runtime/testdata/named_results_oracle.go`; each
// expected value below is copied from that output (/tmp/wlang-named-go-oracle.txt),
// not from an earlier WLang run.

import (
	"errors"
	"testing"

	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/types"
)

func runNamedProgram(t *testing.T, src string) (types.Value, error) {
	t.Helper()
	return runSrc(t, src)
}

func unpackPair(t *testing.T, v types.Value) (types.Value, types.Value) {
	t.Helper()
	values, ok := types.UnpackTuple(v)
	if !ok || len(values) != 2 {
		t.Fatalf("want pair tuple, got %s=%v", v.TypeName(), v.Go())
	}
	return values[0], values[1]
}

func assertString(t *testing.T, v types.Value, want string, label string) {
	t.Helper()
	if v.TypeName() != types.TString || v.Go().(string) != want {
		t.Fatalf("%s = %s=%v, want string=%q", label, v.TypeName(), v.Go(), want)
	}
}

func assertNull(t *testing.T, v types.Value, label string) {
	t.Helper()
	if v.TypeName() == types.TNull {
		return
	}
	if v.TypeName() == types.TError && v.Go() == nil {
		return
	}
	t.Fatalf("%s = %s=%v, want null", label, v.TypeName(), v.Go())
}

// Go: return "body", nil with defer out+="|D1" then out+="|D2" => body|D2|D1.
func TestNamedResultsDeferLIFOAndExplicitAssign(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string","error"],"do":[
			{"let":{"out":{"literal":{"type":"string","value":"body"}}}},
			{"let":{"err":{"literal":{"type":"null","value":null}}}},
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
				{"set":{"out":{"+":[{"var":"out"},{"literal":{"type":"string","value":"|D1"}}]}}}
			]}},"args":[]}}},
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
				{"set":{"out":{"+":[{"var":"out"},{"literal":{"type":"string","value":"|D2"}}]}}}
			]}},"args":[]}}},
			{"return":{"named":["out","err"],"expr":{"tuple":[{"var":"out"},{"var":"err"}]}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	v, err := runNamedProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out, errv := unpackPair(t, v)
	assertString(t, out, "body|D2|D1", "out")
	assertNull(t, errv, "err")
}

// Go: a deferred closure writing named err is visible at the function boundary.
func TestNamedResultsDeferRewritesError(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string","error"],"do":[
			{"let":{"out":{"literal":{"type":"string","value":"body"}}}},
			{"let":{"err":{"literal":{"type":"null","value":null}}}},
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
				{"set":{"err":{"var":"boom"}}}
			]}},"args":[]}}},
			{"return":{"named":["out","err"],"expr":{"tuple":[{"var":"out"},{"var":"err"}]}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	root := NewScope()
	root.Let("boom", types.NewValue(types.TError, errors.New("deferred-err")), "")
	v, err := runSrcWithScope(t, src, root)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out, errv := unpackPair(t, v)
	assertString(t, out, "body", "out")
	if errv.TypeName() != types.TError || errv.Go().(error).Error() != "deferred-err" {
		t.Fatalf("err = %s=%v, want error=deferred-err", errv.TypeName(), errv.Go())
	}
}

// Go evaluates the return expression before the deferred calls run:
// log=[expr defer], result=expr.
func TestNamedResultsReturnExprEvaluatedBeforeDefers(t *testing.T) {
	src := `[
		{"let":{"log":{"array":{"elem":"string","items":[]}}}},
		{"let":{"f":{"fn":{"params":[],"returns":["string","error"],"do":[
			{"let":{"out":{"literal":{"type":"string","value":""}}}},
			{"let":{"err":{"literal":{"type":"null","value":null}}}},
			{"let":{"rec":{"fn":{"params":[],"returns":["string"],"do":[
				{"expr":{"arr.push":[{"var":"log"},{"literal":{"type":"string","value":"expr"}}]}},
				{"return":{"literal":{"type":"string","value":"expr"}}}
			]}}}},
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
				{"expr":{"arr.push":[{"var":"log"},{"literal":{"type":"string","value":"defer"}}]}}
			]}},"args":[]}}},
			{"return":{"named":["out","err"],"expr":{"tuple":[
				{"call":{"fn":{"var":"rec"},"args":[]}},{"var":"err"}]}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	root := NewScope()
	root.Let("log", mustArray(t, "string"), "")
	v, err := runSrcWithScope(t, src, root)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out, _ := unpackPair(t, v)
	assertString(t, out, "expr", "out")
	logged, ok := root.Lookup("log")
	if !ok {
		t.Fatal("log not found")
	}
	items, typ, ok := extractArrayItems(logged)
	if !ok || typ != types.TString {
		t.Fatalf("log = %s=%v", logged.TypeName(), logged.Go())
	}
	if len(items) != 2 || items[0] != "expr" || items[1] != "defer" {
		t.Fatalf("evaluation order = %v, want [expr defer]", items)
	}
}

func mustArray(t *testing.T, elem string) types.Value {
	t.Helper()
	v, err := types.NewArray(elem, nil)
	if err != nil {
		t.Fatalf("NewArray: %v", err)
	}
	return v
}

// Go: an inner block may shadow the result name; `return out, err` assigns the
// shadowed value to the frame-root result, and the deferred write uses that
// same frame-root result.
func TestNamedResultsFrameRootAssignmentAcrossNestedCall(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string","error"],"do":[
			{"let":{"out":{"literal":{"type":"string","value":"root"}}}},
			{"let":{"err":{"literal":{"type":"null","value":null}}}},
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
				{"set":{"out":{"+":[{"var":"out"},{"literal":{"type":"string","value":"|D"}}]}}}
			]}},"args":[]}}},
			{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
				{"let":{"out":{"literal":{"type":"string","value":"shadow"}}}},
				{"return":{"named":["out","err"],"expr":{"tuple":[
					{"var":"out"},{"var":"err"}]}}}
			]}},
			{"return":{"named":["out","err"]}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	// The inner block shadows the result name; `return out, err` still assigns
	// the frame-root result, and the deferred write lands there too. Go oracle:
	// shadowed explicit return = "shadow|D" (return expression "shadow" plus
	// the deferred "|D").
	v, err := runNamedProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out, _ := unpackPair(t, v)
	assertString(t, out, "shadow|D", "out")
}

// Go: blank named results still return explicit values ("blank") and zero on a
// bare return (""). The translator binds a private placeholder.
func TestNamedResultsBlankSlots(t *testing.T) {
	explicit := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string","error"],"do":[
			{"let":{"__wlang_blank_result_0":{"literal":{"type":"string","value":""}}}},
			{"let":{"err":{"literal":{"type":"null","value":null}}}},
			{"return":{"named":["__wlang_blank_result_0","err"],"expr":{"tuple":[
				{"literal":{"type":"string","value":"blank"}},{"var":"err"}]}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	v, err := runNamedProgram(t, explicit)
	if err != nil {
		t.Fatalf("explicit run: %v", err)
	}
	out, _ := unpackPair(t, v)
	assertString(t, out, "blank", "explicit blank result")

	bare := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string","error"],"do":[
			{"let":{"__wlang_blank_result_0":{"literal":{"type":"string","value":""}}}},
			{"let":{"err":{"literal":{"type":"null","value":null}}}},
			{"return":{"named":["__wlang_blank_result_0","err"]}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	v, err = runNamedProgram(t, bare)
	if err != nil {
		t.Fatalf("bare run: %v", err)
	}
	out, _ = unpackPair(t, v)
	assertString(t, out, "", "bare blank result")
}

// Go: every call has its own named result frame; defers of one call never leak
// into the next.
func TestNamedResultsClosureCallsHaveIndependentFrames(t *testing.T) {
	src := `[
		{"let":{"count":{"literal":{"type":"int64","value":"0"}}}},
		{"let":{"f":{"fn":{"params":[],"returns":["int64","error"],"do":[
			{"let":{"out":{"literal":{"type":"int64","value":"0"}}}},
			{"let":{"err":{"literal":{"type":"null","value":null}}}},
			{"set":{"count":{"+":[{"var":"count"},{"literal":{"type":"int64","value":"1"}}]}}},
			{"set":{"out":{"var":"count"}}},
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
				{"set":{"out":{"+":[{"var":"out"},{"literal":{"type":"int64","value":"100"}}]}}}
			]}},"args":[]}}},
			{"return":{"named":["out","err"]}}
		]}}}},
		{"let":{"first":{"call":{"fn":{"var":"f"},"args":[]}}}},
		{"let":{"second":{"call":{"fn":{"var":"f"},"args":[]}}}},
		{"return":{"tuple":[{"var":"first"},{"var":"second"}]}}
	]`
	v, err := runNamedProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	values, ok := types.UnpackTuple(v)
	if !ok || len(values) != 2 {
		t.Fatalf("want two call results, got %s=%v", v.TypeName(), v.Go())
	}
	first, _ := unpackPair(t, values[0])
	second, _ := unpackPair(t, values[1])
	if first.Go().(int64) != 101 || second.Go().(int64) != 102 {
		t.Fatalf("closure results = %v,%v, want 101,102", first.Go(), second.Go())
	}
}

// Go oracle for recursion(4) = 4124; each recursive invocation owns its named
// result and drains its own defer.
func TestNamedResultsRecursionIsolatedFrames(t *testing.T) {
	src := `[
		{"let":{"rec":{"fn":{"params":[["n","int64"]],"returns":["int64","error"],"do":[
			{"let":{"out":{"literal":{"type":"int64","value":"0"}}}},
			{"let":{"err":{"literal":{"type":"null","value":null}}}},
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
				{"set":{"out":{"+":[{"var":"out"},{"literal":{"type":"int64","value":"100"}}]}}}
			]}},"args":[]}}},
			{"if":{"cond":{"<=":[{"var":"n"},{"literal":{"type":"int64","value":"1"}}]},"then":[
				{"return":{"named":["out","err"],"expr":{"tuple":[
					{"literal":{"type":"int64","value":"1"}},{"var":"err"}]}}}
			]}},
			{"let":[["sub","_"],{"call":{"fn":{"var":"rec"},"args":[
				{"-":[{"var":"n"},{"literal":{"type":"int64","value":"1"}}]}]}}]},
			{"return":{"named":["out","err"],"expr":{"tuple":[
				{"*":[{"var":"sub"},{"var":"n"}]},{"var":"err"}]}}}
		]}}}},
		{"let":[["r","_"],{"call":{"fn":{"var":"rec"},"args":[{"literal":{"type":"int64","value":"4"}}]}}]},
		{"return":{"var":"r"}}
	]`
	v, err := runNamedProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 4124 {
		t.Fatalf("recursion(4) = %v, want 4124", v.Go())
	}
}

// Unnamed tuple returns keep the return-site snapshot: the deferred write to b
// is not visible in the returned tuple.
func TestUnnamedTupleReturnKeepsReturnSnapshot(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string","string"],"do":[
			{"let":{"a":{"literal":{"type":"string","value":"a"}}}},
			{"let":{"b":{"literal":{"type":"string","value":"b"}}}},
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
				{"set":{"b":{"literal":{"type":"string","value":"changed"}}}}
			]}},"args":[]}}},
			{"return":{"tuple":[{"var":"a"},{"var":"b"}]}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	v, err := runNamedProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	a, b := unpackPair(t, v)
	assertString(t, a, "a", "a")
	assertString(t, b, "b", "b")
}

// Program top level is an implicit function frame: a bare named return reads
// the final program-level bindings after its own defers.
func TestNamedResultsProgramBareReturnAfterDefer(t *testing.T) {
	src := `[
		{"let":{"out":{"literal":{"type":"string","value":"body"}}}},
		{"let":{"err":{"literal":{"type":"null","value":null}}}},
		{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
			{"set":{"out":{"+":[{"var":"out"},{"literal":{"type":"string","value":"|main"}}]}}}
		]}},"args":[]}}},
		{"return":{"named":["out","err"]}}
	]`
	v, err := runNamedProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out, errv := unpackPair(t, v)
	assertString(t, out, "body|main", "program out")
	assertNull(t, errv, "program err")
}

// Routine bodies get the same treatment: their own frame, named list, defer.
func TestNamedResultsRoutineBareReturnAfterDefer(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
			{"let":{"h":{"routine":{"do":[
				{"let":{"r":{"literal":{"type":"string","value":"body"}}}},
				{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
					{"set":{"r":{"+":[{"var":"r"},{"literal":{"type":"string","value":"|defer"}}]}}}
				]}},"args":[]}}},
				{"return":{"named":["r"]}}
			]}}}},
			{"return":{"await":{"var":"h"}}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	_, v, err := runDeferProgram(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertString(t, v, "body|defer", "routine result")
}

// A deferred LangError keeps priority over named-result resolution: the frame
// still drains every defer (observed through the log side effect), but the
// panic identity is returned instead of the tuple. Go's oracle for this path is
// "panic wins" (`E_PANIC`), matching the existing single-named priority rule.
func TestNR_DeferredPanicWinsAndDrains(t *testing.T) {
	src := `[
		{"let":{"f":{"fn":{"params":[],"returns":["string","error"],"do":[
			{"let":{"out":{"literal":{"type":"string","value":"body"}}}},
			{"let":{"err":{"literal":{"type":"null","value":null}}}},
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
				{"expr":{"arr.push":[{"var":"log"},{"literal":{"type":"string","value":"first"}}]}}
			]}},"args":[]}}},
			{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
				{"expr":{"arr.push":[{"var":"log"},{"literal":{"type":"string","value":"panic"}}]}},
				{"panic":{"literal":{"type":"string","value":"defer-boom"}}}
			]}},"args":[]}}},
			{"return":{"named":["out","err"]}}
		]}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
	]`
	root := NewScope()
	root.Let("log", mustArray(t, types.TString), "")
	v, err := runSrcWithScope(t, src, root)
	if err == nil {
		t.Fatalf("deferred panic must win, got value %s=%v", v.TypeName(), v.Go())
	}
	le, ok := err.(*werr.LangError)
	if !ok || le.Code != werr.CodePanic {
		t.Fatalf("want %s, got %T %v", werr.CodePanic, err, err)
	}
	logged, ok := root.Lookup("log")
	if !ok {
		t.Fatal("log not found")
	}
	items, typ, ok := extractArrayItems(logged)
	if !ok || typ != types.TString {
		t.Fatalf("log = %s=%v", logged.TypeName(), logged.Go())
	}
	if len(items) != 2 || items[0] != "panic" || items[1] != "first" {
		t.Fatalf("deferred drain after panic = %v, want [panic first]", items)
	}
}

// A host may hand RunProgram a non-root scope; the program frame is promoted
// for the run, so named result lists still bind and resolve in that frame
// without consuming an ancestor frame's defers.
func TestNR_ProgramNonRootScope(t *testing.T) {
	src := `[
		{"let":{"out":{"literal":{"type":"string","value":"body"}}}},
		{"let":{"err":{"literal":{"type":"null","value":null}}}},
		{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
			{"set":{"out":{"+":[{"var":"out"},{"literal":{"type":"string","value":"|child"}}]}}}
		]}},"args":[]}}},
		{"return":{"named":["out","err"]}}
	]`
	root := NewScope()
	root.PushDeferred("Unused", types.Value{}, nil, "/test")
	child := root.Push()
	v, err := runSrcWithScope(t, src, child)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out, _ := unpackPair(t, v)
	assertString(t, out, "body|child", "non-root program out")
	if len(root.deferred) != 1 {
		t.Fatalf("program consumed ancestor frame defers: %d pending", len(root.deferred))
	}
	root.PopDeferred()
}
