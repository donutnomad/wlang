package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGo2WLCLIProducesPseudoCode(t *testing.T) {
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "rule.go")
	src := []byte(`package rules

import "example.com/demo"

func Rule(input string) string {
	value, _ := demo.Echo(input)
	return value
}
`)
	if err := os.WriteFile(srcPath, src, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	cmd := exec.Command("go", "run", ".", "-func", "Rule", "-pseudo", srcPath)
	cmd.Env = append(os.Environ(), "GOROOT=", "GOTOOLCHAIN=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go2wl: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "let value, _ = demo.Echo(input)") {
		t.Fatalf("unexpected output:\n%s", got)
	}
}
