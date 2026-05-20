package go2wlang_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wflang/wflang/go2wlang"
	"github.com/wflang/wflang/wflang"
)

func TestTranslateFileRuleFunction(t *testing.T) {
	src := []byte(`package rules

import "example.com/demo"

func Rule(user demo.User, scores []int64) demo.Report {
	total := int64(0)
	for i, score := range scores {
		if score > 0 {
			total = total + score
		} else {
			continue
		}
		_ = i
	}
	risk, err := demo.Score(user, total)
	status := "normal"
	if risk >= 10 {
		status = "high"
	}
	return demo.Report{Risk: risk, Error: err, Status: status}
}
`)
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	if _, err := eng.CompileJSON(out); err != nil {
		t.Fatalf("generated JSON should parse and typecheck: %v\n%s", err, out)
	}
	pseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("pseudo: %v", err)
	}
	got := string(pseudo)
	for _, needle := range []string{
		"import demo",
		"let total = 0",
		"foreach score, i in scores {",
		"if score > 0 {",
		"let risk, err = demo.Score(user, total)",
		"return struct demo.Report {",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestTranslateFileConcurrencySubset(t *testing.T) {
	src := []byte(`package rules

import "example.com/demo"

func Concurrent(input string) string {
	ch := make(chan string, 1)
	defer demo.Close(input)
	go demo.Publish(input)
	go func() {
		ch <- input
	}()
	select {
	case msg, ok := <-ch:
		if ok {
			return msg
		}
	default:
		return "idle"
	}
	return "done"
}
`)
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Concurrent"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	pseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("pseudo: %v", err)
	}
	got := string(pseudo)
	for _, needle := range []string{
		"let ch = chan<string>(1)",
		"defer demo.Close(input)",
		"routine demo.Publish(input)",
		"routine {",
		"ch.send(ch, input)",
		"select {",
		"recv ch -> msg, ok {",
		"default {",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestTranslateFileUnsupportedReportsDiagnostic(t *testing.T) {
	src := []byte(`package rules

func Rule(x any) string {
	s, ok := x.(string)
	if ok {
		return s
	}
	return ""
}
`)
	_, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
	if err == nil {
		t.Fatal("expected unsupported diagnostic")
	}
	var diag *go2wlang.DiagnosticError
	if !errors.As(err, &diag) {
		t.Fatalf("err = %T %[1]v, want DiagnosticError", err)
	}
	if diag.Line == 0 || diag.Column == 0 {
		t.Fatalf("missing position in diagnostic: %+v", diag)
	}
	if !strings.Contains(diag.Node, "TypeAssertExpr") {
		t.Fatalf("node = %q, want TypeAssertExpr", diag.Node)
	}
}

func TestTranslateFileMissingFunction(t *testing.T) {
	src := []byte(`package rules

func Other() int64 { return 1 }
`)
	_, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
	if err == nil {
		t.Fatal("expected missing function error")
	}
	if !strings.Contains(err.Error(), "function Rule not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestExampleApprovalRuleTranslates(t *testing.T) {
	path := filepath.Join("examples", "approval_rule.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "ApprovalRule"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	pseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("pseudo: %v", err)
	}
	got := string(pseudo)
	for _, needle := range []string{
		"import audit",
		"import notify",
		"import policy",
		"defer audit.Close(\"approval-rule\")",
		"let normalized = policy.Normalize(user)",
		"let risk, riskErr = scorer.Score(normalized, total)",
		"let saveErr = store.Save(decision)",
		"routine notify.Publish(status)",
		"let events = chan<string>(1)",
		"select {",
		"recv events -> msg, ok {",
		"return decision",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}
