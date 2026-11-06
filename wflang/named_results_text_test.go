package wflang_test

import (
	"strings"
	"testing"

	"github.com/donutnomad/wlang/wflang"
)

func TestNamedResultsTextBareAndAssigned(t *testing.T) {
	bare := []byte(strings.Join([]string{
		`let out = "body"`,
		`let err = "e"`,
		`return named (out, err)`,
	}, "\n"))
	jsonOut, err := wflang.CompileText(bare)
	if err != nil {
		t.Fatalf("CompileText bare: %v", err)
	}
	got := string(jsonOut)
	for _, needle := range []string{`"named": [`, `"out"`, `"err"`} {
		if !strings.Contains(got, needle) {
			t.Fatalf("bare return missing %q:\n%s", needle, got)
		}
	}

	assigned := []byte(strings.Join([]string{
		`let out = "body"`,
		`let err = "e"`,
		`return named (out, err) = out`,
	}, "\n"))
	jsonOut, err = wflang.CompileText(assigned)
	if err != nil {
		t.Fatalf("CompileText assigned: %v", err)
	}
	got = string(jsonOut)
	for _, needle := range []string{`"named": [`, `"expr"`} {
		if !strings.Contains(got, needle) {
			t.Fatalf("assigned return missing %q:\n%s", needle, got)
		}
	}
}

func TestNR_Schema_ExplicitExpr(t *testing.T) {
	src := []byte(`[{"return":{"named":["out","err"],"expr":{"tuple":[
	  {"var":"out"},{"var":"err"}]}}}]`)
	if issues := wflang.ValidateSchema(src); len(issues) != 0 {
		t.Fatalf("ValidateSchema rejected explicit named return: %v", issues)
	}
}

func TestNR_Lint_ExplicitExpr(t *testing.T) {
	src := []byte(`[{"return":{"named":["out","err"],"expr":{"tuple":[
	  {"var":"out"},{"var":"err"}]}}}]`)
	for _, issue := range wflang.Lint(src, wflang.DefaultRegistry(), wflang.LintOptions{}) {
		if issue.Code == "L_MULTI_KEY" {
			t.Fatalf("Lint flagged the named-return wrapper as multi-key: %v", issue)
		}
	}
}

func TestNamedResultsPseudoRoundTrip(t *testing.T) {
	src := []byte(`[
	  {"return":{"named":["out","err"]}},
	  {"return":{"named":["out","err"],"expr":{"tuple":[
	    {"var":"out"},{"var":"err"}]}}}
	]`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("CompileJSON: %v", err)
	}
	dumped, err := wflang.DumpAST(prog)
	if err != nil {
		t.Fatalf("DumpAST: %v", err)
	}
	again, err := eng.CompileJSON(dumped)
	if err != nil {
		t.Fatalf("recompile dumped: %v\n%s", err, dumped)
	}
	dumpedAgain, err := wflang.DumpAST(again)
	if err != nil {
		t.Fatalf("DumpAST again: %v", err)
	}
	if string(dumped) != string(dumpedAgain) {
		t.Fatalf("named-result dump unstable:\n%s\n---\n%s", dumped, dumpedAgain)
	}
	pseudo, err := wflang.FormatPseudoCode(dumped)
	if err != nil {
		t.Fatalf("FormatPseudoCode: %v", err)
	}
	if !strings.Contains(string(pseudo), "return named (out, err)") {
		t.Fatalf("pseudo missing named result list:\n%s", pseudo)
	}
	if !strings.Contains(string(pseudo), "return named (out, err) =") {
		t.Fatalf("pseudo missing assigned named return:\n%s", pseudo)
	}
}
