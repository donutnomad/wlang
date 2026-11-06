package go2wlang_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donutnomad/wlang/go2wlang"
	"github.com/donutnomad/wlang/types"
	"github.com/donutnomad/wlang/wflang"
)

func translateNamed(t *testing.T, src string) string {
	t.Helper()
	out, err := go2wlang.TranslateFile([]byte(src), go2wlang.Options{FuncName: "W"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	if _, err := eng.CompileJSON(out); err != nil {
		t.Fatalf("generated JSON should compile: %v\n%s", err, out)
	}
	return string(out)
}

func TestTranslateNamedResultsExplicitAndBareReturn(t *testing.T) {
	src := `package rules

func W(flag bool) (out string, err error) {
	out = "body"
	defer func() { out += "|defer" }()
	if flag {
		return "explicit", nil
	}
	return
}`
	got := translateNamed(t, src)
	for _, needle := range []string{
		`"named": [`,
		`"out"`,
		`"err"`,
		`"expr"`,
		`"|defer"`,
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
	if strings.Contains(got, `"named": "err"`) {
		t.Fatalf("named result list must not fall back to the legacy single-name shape:\n%s", got)
	}
}

func TestTranslateNamedResultsGroupedAndNestedClosure(t *testing.T) {
	src := `package rules

func W() (left, right int64, err error) {
	f := func() (sum int64, inner error) {
		sum = 7
		defer func() { sum += 100 }()
		return
	}
	left, err = f()
	right = left + 1
	return
}`
	got := translateNamed(t, src)
	for _, needle := range []string{
		`"named": [`,
		`"left"`,
		`"right"`,
		`"sum"`,
		`"inner"`,
		`"fn"`,
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestTranslateNamedResultsBlankAndTupleProducingCall(t *testing.T) {
	src := `package rules

func pair() (string, error) {
	return "pair", nil
}

func W() (_ string, err error) {
	return pair()
}`
	got := translateNamed(t, src)
	for _, needle := range []string{
		`"__wlang_blank_result_0"`,
		`"named": [`,
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
	if !strings.Contains(got, `"rules.pair"`) {
		t.Fatalf("tuple-producing call missing:\n%s", got)
	}
}

func TestTranslateUnnamedTupleReturnStaysSnapshot(t *testing.T) {
	src := `package rules

func W() (string, string) {
	a := "a"
	b := "b"
	defer func() { b = "changed" }()
	return a, b
}`
	got := translateNamed(t, src)
	if strings.Contains(got, `"named": [`) {
		t.Fatalf("unnamed results must stay a plain return tuple:\n%s", got)
	}
	if !strings.Contains(got, `"tuple"`) {
		t.Fatalf("missing return tuple:\n%s", got)
	}
}

func TestTranslatedNamedResultsDeferWritesFinalValue(t *testing.T) {
	src := `package rules

func W() (out string, err error) {
	out = "body"
	defer func() { out += "|defer" }()
	f := func() (s string, e error) {
		s = "closure"
		defer func() { s += "!" }()
		return
	}
	out, err = f()
	return
}`
	out, err := go2wlang.TranslateFile([]byte(src), go2wlang.Options{FuncName: "W"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON(out)
	if err != nil {
		t.Fatalf("CompileJSON: %v\n%s", err, out)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	values, ok := v.Go().([]any)
	if !ok || len(values) != 2 {
		t.Fatalf("want result tuple, got %T %v\n%s", v.Go(), v.Go(), out)
	}
	if values[0] != "closure!|defer" {
		t.Fatalf("out = %v, want closure!|defer\n%s", values[0], out)
	}
}

func runTranslatedNamed(t *testing.T, src string) types.Value {
	t.Helper()
	out, err := go2wlang.TranslateFile([]byte(src), go2wlang.Options{FuncName: "W"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON(out)
	if err != nil {
		t.Fatalf("CompileJSON: %v\n%s", err, out)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	return v
}

func TestTranslatedGroupedNamedResultsUseFinalValues(t *testing.T) {
	src := `package rules

func W() (a, b int64, err error) {
	defer func() { a += 100 }()
	a = 1
	b = 2
	return a, b, nil
}`
	v := runTranslatedNamed(t, src)
	values, ok := v.Go().([]any)
	if !ok || len(values) != 3 {
		t.Fatalf("want 3-result tuple, got %T %v", v.Go(), v.Go())
	}
	if values[0] != int64(101) || values[1] != int64(2) || values[2] != nil {
		t.Fatalf("grouped named results = %#v, want [101 2 <nil>]", values)
	}
}

func TestTranslatedBlankNamedResultKeepsExplicitValue(t *testing.T) {
	src := `package rules

func W() (_ string, err error) {
	return "blank", nil
}`
	v := runTranslatedNamed(t, src)
	values, ok := v.Go().([]any)
	if !ok || len(values) != 2 {
		t.Fatalf("want 2-result tuple, got %T %v", v.Go(), v.Go())
	}
	if values[0] != "blank" || values[1] != nil {
		t.Fatalf("blank named result = %#v, want [blank <nil>]", values)
	}
}

// Mixed named/unnamed results are rejected by Go's parser before translation.
// This covers both translator entry paths: single-file (Dir empty) and
// package-loaded with a real module directory.
func TestNR_MixedResultsRejectedAtParserEntry(t *testing.T) {
	src := `package rules

func W() (a int64, error) {
	return 0, nil
}`
	if _, err := go2wlang.TranslateFile([]byte(src), go2wlang.Options{FuncName: "W"}); err == nil {
		t.Fatal("Dir=empty: mixed results must fail before translation")
	} else if !strings.Contains(err.Error(), "missing parameter type") {
		t.Fatalf("Dir=empty: want parser diagnostic, got %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module rules\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "w.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write w.go: %v", err)
	}
	_, err := go2wlang.TranslateFileDetailed([]byte(src), go2wlang.Options{
		FuncName: "W", Filename: "w.go", Dir: dir,
	})
	if err == nil {
		t.Fatal("Dir=module: mixed results must fail before translation")
	}
	if !strings.Contains(err.Error(), "missing parameter type") {
		t.Fatalf("Dir=module: want parser diagnostic, got %v", err)
	}
}
