package go2wlang_test

// Regression lock for the package-wide type information the translator keeps
// when a package does not fully type-check.
//
// Before this behaviour existed, any packages.Load error made the translator
// check the entry file on its own. A single-file check cannot resolve
// package-level identifiers declared in sibling files, so they came back as
// variable references instead of function symbols. That is what turned a
// same-package/sibling-file activity marker into `{"var": ...}` and made
// generation fail far away from the real cause.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donutnomad/wlang/go2wlang"
)

// TestTypeErrorKeepsSiblingFileSymbols translates a file that has an unrelated
// type error while the function it references lives in a sibling file. The
// reference must still be translated as a symbol rather than a variable.
func TestTypeErrorKeepsSiblingFileSymbols(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "activities.go"), `package app

// Normalize lives in a sibling file and must stay resolvable.
func Normalize(in string) string { return in }
`)
	// The workflow file has a type error that is irrelevant to the translated
	// function: it makes the package fail to type-check as a whole.
	writeFile(t, filepath.Join(dir, "workflow.go"), `package app

func W(in string) string {
	var bad string = 1
	_ = bad
	picked := Normalize
	return picked(in)
}
`)
	translated, err := go2wlang.TranslateFileDetailed([]byte(mustReadFile(t, filepath.Join(dir, "workflow.go"))), go2wlang.Options{
		FuncName: "W",
		Filename: "workflow.go",
		Dir:      dir,
	})
	if err != nil {
		t.Fatalf("TranslateFileDetailed: %v", err)
	}
	program := string(translated.JSON)
	if !strings.Contains(program, `"symbol": "app.Normalize"`) {
		t.Fatalf("sibling-file function must stay a symbol\n%s", program)
	}
	if strings.Contains(program, `"var": "Normalize"`) {
		t.Fatalf("sibling-file function degraded to a variable reference\n%s", program)
	}
}

// TestParseErrorFallsBack checks the boundary of the tolerance: a parse error
// means the syntax tree is not trustworthy, so the translator keeps its old
// single-file fallback instead of trusting partially parsed package data.
func TestParseErrorFallsBack(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\ngo 1.21\n")
	source := "package app\n\nfunc W(in string) string {\n\treturn in\n"
	if _, err := go2wlang.TranslateFileDetailed([]byte(source), go2wlang.Options{
		FuncName: "W",
		Filename: "workflow.go",
		Dir:      dir,
	}); err == nil {
		t.Fatal("syntax error must fail translation")
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
