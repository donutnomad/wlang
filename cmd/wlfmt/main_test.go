package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWLFmtCLIFormatsPseudoToJSON(t *testing.T) {
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "rule.wl")
	src := strings.Join([]string{
		"import demo",
		"",
		"let title = \"demo\"",
		"return demo.Report(title)",
	}, "\n")
	if err := os.WriteFile(srcPath, []byte(src), 0o600); err != nil {
		t.Fatalf("write pseudo: %v", err)
	}

	cmd := exec.Command("go", "run", ".", "-text", "-json", srcPath)
	cmd.Env = append(os.Environ(), "GOROOT=", "GOTOOLCHAIN=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wlfmt: %v\n%s", err, out)
	}
	got := string(out)
	for _, needle := range []string{
		`"lang": "wflang/v1"`,
		`"imports": [`,
		`"pkg": "demo"`,
		`"Report": [`,
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestWLFmtCLIFormatsPseudoToPseudo(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "-text")
	cmd.Env = append(os.Environ(), "GOROOT=", "GOTOOLCHAIN=")
	cmd.Stdin = strings.NewReader("let x = 1\nreturn x\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wlfmt: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "let x = 1\nreturn x" {
		t.Fatalf("unexpected pseudo:\n%s", out)
	}
}

func TestWLFmtCLIReportsMalformedPseudo(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "-text", "-json")
	cmd.Env = append(os.Environ(), "GOROOT=", "GOTOOLCHAIN=")
	cmd.Stdin = strings.NewReader("if x {\n  return 1\n")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure, got:\n%s", out)
	}
	got := string(out)
	if !strings.Contains(got, "line") || !strings.Contains(got, "expected }") {
		t.Fatalf("unexpected diagnostic:\n%s", got)
	}
}
