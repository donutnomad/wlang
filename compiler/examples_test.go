package compiler_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wflang/wflang/compiler"
)

func TestExamplesFullFeatureDemoParses(t *testing.T) {
	path := filepath.Join("..", "examples", "full_feature_demo.json")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	prog, err := compiler.ParseProgram(src)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if prog.Lang != "wflang/v1" {
		t.Fatalf("lang = %q, want wflang/v1", prog.Lang)
	}
	if len(prog.Imports) == 0 {
		t.Fatalf("imports should be present")
	}
	if len(prog.Body) == 0 {
		t.Fatalf("program body should be present")
	}
}
