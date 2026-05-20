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

func TestGo2WLCLIUsesFilePathPackageResolution(t *testing.T) {
	tmp := t.TempDir()
	writeCLIFile(t, filepath.Join(tmp, "go.mod"), "module example.com/app\n\ngo 1.25\n")
	writeCLIFile(t, filepath.Join(tmp, "internal", "approvalkit", "kit.go"), `package approvals

type Request struct{ Amount int64 }

func Score(req Request) int64 { return req.Amount }
`)
	srcPath := filepath.Join(tmp, "rules", "rule.go")
	writeCLIFile(t, srcPath, `package rules

import "example.com/app/internal/approvalkit"

func Rule(req approvals.Request) int64 {
	return approvals.Score(req)
}
`)
	cmd := exec.Command("go", "run", ".", "-func", "Rule", srcPath)
	cmd.Env = append(os.Environ(), "GOROOT=", "GOTOOLCHAIN=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go2wl: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, `"pkg": "approvals"`) {
		t.Fatalf("unexpected output:\n%s", got)
	}
}

func TestGo2WLCLIWritesManifest(t *testing.T) {
	tmp := t.TempDir()
	writeCLIFile(t, filepath.Join(tmp, "go.mod"), "module example.com/app\n\ngo 1.25\n")
	writeCLIFile(t, filepath.Join(tmp, "internal", "approvalkit", "kit.go"), `package approvals

type Request struct{ Amount int64 }

func Score(req Request) int64 { return req.Amount }
`)
	srcPath := filepath.Join(tmp, "rules", "rule.go")
	writeCLIFile(t, srcPath, `package rules

import "example.com/app/internal/approvalkit"

func Rule(req approvals.Request) int64 {
	return approvals.Score(req)
}
`)
	manifestPath := filepath.Join(tmp, "rule.imports.json")
	cmd := exec.Command("go", "run", ".", "-func", "Rule", "-manifest", manifestPath, srcPath)
	cmd.Env = append(os.Environ(), "GOROOT=", "GOTOOLCHAIN=", "GOCACHE=/private/tmp/wlang-gocache")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go2wl: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(raw), `"approvals": "example.com/app/internal/approvalkit"`) {
		t.Fatalf("unexpected manifest:\n%s", raw)
	}
	if strings.Contains(string(out), `"importMap"`) {
		t.Fatalf("manifest flag should not embed importMap in main output:\n%s", out)
	}
}

func TestGo2WLCLIEmbedsImportMap(t *testing.T) {
	tmp := t.TempDir()
	writeCLIFile(t, filepath.Join(tmp, "go.mod"), "module example.com/app\n\ngo 1.25\n")
	writeCLIFile(t, filepath.Join(tmp, "internal", "approvalkit", "kit.go"), `package approvals

type Request struct{ Amount int64 }

func Score(req Request) int64 { return req.Amount }
`)
	srcPath := filepath.Join(tmp, "rules", "rule.go")
	writeCLIFile(t, srcPath, `package rules

import "example.com/app/internal/approvalkit"

func Rule(req approvals.Request) int64 {
	return approvals.Score(req)
}
`)
	cmd := exec.Command("go", "run", ".", "-func", "Rule", "-embed-import-map", srcPath)
	cmd.Env = append(os.Environ(), "GOROOT=", "GOTOOLCHAIN=", "GOCACHE=/private/tmp/wlang-gocache")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go2wl: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, `"importMap"`) {
		t.Fatalf("missing embedded importMap:\n%s", got)
	}
	if !strings.Contains(got, `"approvals": "example.com/app/internal/approvalkit"`) {
		t.Fatalf("missing embedded import path:\n%s", got)
	}
}

func TestGo2WLCLIReportsManifestWriteFailure(t *testing.T) {
	tmp := t.TempDir()
	writeCLIFile(t, filepath.Join(tmp, "go.mod"), "module example.com/app\n\ngo 1.25\n")
	writeCLIFile(t, filepath.Join(tmp, "internal", "approvalkit", "kit.go"), `package approvals

type Request struct{ Amount int64 }

func Score(req Request) int64 { return req.Amount }
`)
	srcPath := filepath.Join(tmp, "rules", "rule.go")
	writeCLIFile(t, srcPath, `package rules

import "example.com/app/internal/approvalkit"

func Rule(req approvals.Request) int64 {
	return approvals.Score(req)
}
`)
	manifestPath := filepath.Join(tmp, "missing", "rule.imports.json")
	cmd := exec.Command("go", "run", ".", "-func", "Rule", "-manifest", manifestPath, srcPath)
	cmd.Env = append(os.Environ(), "GOROOT=", "GOTOOLCHAIN=", "GOCACHE=/private/tmp/wlang-gocache")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected manifest write failure, got output:\n%s", out)
	}
	got := string(out)
	if !strings.Contains(got, "write manifest") || !strings.Contains(got, manifestPath) {
		t.Fatalf("unexpected error output:\n%s", got)
	}
}

func writeCLIFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
