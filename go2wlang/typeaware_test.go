package go2wlang_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wflang/wflang/go2wlang"
	"github.com/wflang/wflang/wflang"
)

func TestTranslateFilePathResolvesProjectImportAndAlias(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.25\n")
	writeFile(t, filepath.Join(root, "policy", "policy.go"), `package policykit

type User struct{ Name string }
type Decision struct{ Name string }

func Normalize(user User) User { return user }
`)
	rulePath := filepath.Join(root, "rules", "rule.go")
	writeFile(t, rulePath, `package rules

import pol "example.com/app/policy"

func Rule(user pol.User) pol.Decision {
	normalized := pol.Normalize(user)
	return pol.Decision{Name: normalized.Name}
}
`)
	out, err := go2wlang.TranslateFilePath(rulePath, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFilePath: %v", err)
	}
	pseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("pseudo: %v", err)
	}
	got := string(pseudo)
	for _, needle := range []string{
		"import pol",
		"let normalized = pol.Normalize(user)",
		"return struct pol.Decision {",
		"Name: normalized.Name",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestTranslateFilePathDetailedReturnsImportManifest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.25\n")
	writeFile(t, filepath.Join(root, "policy", "policy.go"), `package policykit

type User struct{ Name string }

func Normalize(user User) User { return user }
`)
	rulePath := filepath.Join(root, "rules", "rule.go")
	writeFile(t, rulePath, `package rules

import pol "example.com/app/policy"

func Rule(user pol.User) pol.User {
	return pol.Normalize(user)
}
`)
	result, err := go2wlang.TranslateFilePathDetailed(rulePath, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFilePathDetailed: %v", err)
	}
	if result.FuncName != "Rule" {
		t.Fatalf("FuncName = %q, want Rule", result.FuncName)
	}
	if result.Source != rulePath {
		t.Fatalf("Source = %q, want %q", result.Source, rulePath)
	}
	if got := result.Imports["pol"]; got != "example.com/app/policy" {
		t.Fatalf("Imports[pol] = %q, want import path", got)
	}
	if strings.Contains(string(result.JSON), "importMap") {
		t.Fatalf("default JSON should not embed importMap:\n%s", result.JSON)
	}

	embedded, err := go2wlang.TranslateFilePathDetailed(rulePath, go2wlang.Options{FuncName: "Rule", EmbedImportMap: true})
	if err != nil {
		t.Fatalf("TranslateFilePathDetailed embedded: %v", err)
	}
	if !strings.Contains(string(embedded.JSON), `"importMap"`) {
		t.Fatalf("embedded JSON missing importMap:\n%s", embedded.JSON)
	}
	if !strings.Contains(string(embedded.JSON), `"pol": "example.com/app/policy"`) {
		t.Fatalf("embedded JSON missing import path:\n%s", embedded.JSON)
	}
}

func TestTranslateFilePathDistinguishesLocalVariableFromImportAlias(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.25\n")
	writeFile(t, filepath.Join(root, "svc", "svc.go"), `package svc

func Normalize(input string) string { return input }
`)
	rulePath := filepath.Join(root, "rules", "rule.go")
	writeFile(t, rulePath, `package rules

import "example.com/app/svc"

type worker struct{}

func (worker) Run(input string) string { return input }

func Rule(input string, svc worker) string {
	value := svc.Run(input)
	return value
}
`)
	out, err := go2wlang.TranslateFilePath(rulePath, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFilePath: %v", err)
	}
	pseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("pseudo: %v", err)
	}
	got := string(pseudo)
	if strings.Contains(string(out), `"pkg": "svc"`) {
		t.Fatalf("local variable svc should not be emitted as package receiver:\n%s", out)
	}
	if !strings.Contains(got, "let value = svc.Run(input)") {
		t.Fatalf("missing receiver method call in:\n%s", got)
	}
}

func TestTranslateFilePathBlockLocalDoesNotShadowLaterPackageSelector(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.25\n")
	rulePath := filepath.Join(root, "rules", "rule.go")
	writeFile(t, rulePath, `package rules

func Rule(input string, ok bool) string {
	if ok {
		svc := input
		_ = svc
	}
	return svc.Normalize(input)
}
`)
	out, err := go2wlang.TranslateFilePath(rulePath, go2wlang.Options{
		FuncName:       "Rule",
		PackageAliases: map[string]string{"svc": "example.com/app/svc"},
	})
	if err != nil {
		t.Fatalf("TranslateFilePath: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `"pkg": "svc"`) {
		t.Fatalf("later package call should use package selector:\n%s", got)
	}
	if strings.Contains(got, `"Normalize": [\n          {\n            "var": "svc"`) {
		t.Fatalf("later package call emitted as receiver call:\n%s", got)
	}
}

func TestTranslateFilePathUsesResolvedPackageNameForProjectImport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.25\n")
	writeFile(t, filepath.Join(root, "shared", "approvalkit", "kit.go"), `package approvals

type Request struct{ Amount int64 }

func Score(req Request) int64 { return req.Amount }
`)
	rulePath := filepath.Join(root, "rules", "rule.go")
	writeFile(t, rulePath, `package rules

import "example.com/app/shared/approvalkit"

func Rule(req approvals.Request) int64 {
	return approvals.Score(req)
}
`)
	out, err := go2wlang.TranslateFilePath(rulePath, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFilePath: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `"pkg": "approvals"`) {
		t.Fatalf("missing resolved package name in JSON:\n%s", got)
	}
	if strings.Contains(got, `"var": "approvals"`) {
		t.Fatalf("package selector emitted as variable:\n%s", got)
	}
}

func TestTranslateFilePathResolvesGoWorkPackage(t *testing.T) {
	workspace := t.TempDir()
	appRoot := filepath.Join(workspace, "app")
	commonRoot := filepath.Join(workspace, "common")
	writeFile(t, filepath.Join(workspace, "go.work"), "go 1.25\n\nuse (\n\t./app\n\t./common\n)\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.25\n\nrequire example.com/common v0.0.0\n")
	writeFile(t, filepath.Join(commonRoot, "go.mod"), "module example.com/common\n\ngo 1.25\n")
	writeFile(t, filepath.Join(commonRoot, "risk", "engine.go"), `package riskengine

type Order struct{ Amount int64 }

func Score(order Order) int64 { return order.Amount }
`)
	rulePath := filepath.Join(appRoot, "rules", "rule.go")
	writeFile(t, rulePath, `package rules

import "example.com/common/risk"

func Rule(order riskengine.Order) int64 {
	return riskengine.Score(order)
}
`)
	out, err := go2wlang.TranslateFilePath(rulePath, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFilePath: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `"pkg": "riskengine"`) {
		t.Fatalf("missing go.work package name in JSON:\n%s", got)
	}
}

func TestTranslateFilePathResolvesThirdPartyPackageName(t *testing.T) {
	root := t.TempDir()
	modCache := filepath.Join(t.TempDir(), "pkg", "mod")
	t.Setenv("GOMODCACHE", modCache)
	t.Setenv("GOPATH", filepath.Join(t.TempDir(), "gopath"))

	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.25\n\nrequire example.com/vendor/decimal v1.2.3\n")
	writeFile(t, filepath.Join(modCache, "example.com", "vendor", "decimal@v1.2.3", "mathx", "decimal.go"), `package decimal

type Value struct{ Raw int64 }

func Round(v Value) Value { return v }
`)
	rulePath := filepath.Join(root, "rules", "rule.go")
	writeFile(t, rulePath, `package rules

import "example.com/vendor/decimal/mathx"

func Rule(v decimal.Value) decimal.Value {
	return decimal.Round(v)
}
`)
	out, err := go2wlang.TranslateFilePath(rulePath, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFilePath: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `"pkg": "decimal"`) {
		t.Fatalf("missing third-party package name in JSON:\n%s", got)
	}
}

func TestTranslateFilePathResolvesLocalReplacePackageName(t *testing.T) {
	workspace := t.TempDir()
	appRoot := filepath.Join(workspace, "app")
	commonRoot := filepath.Join(workspace, "common")
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.25\n\nrequire example.com/common v0.0.0\n\nreplace example.com/common => ../common\n")
	writeFile(t, filepath.Join(commonRoot, "go.mod"), "module example.com/common\n\ngo 1.25\n")
	writeFile(t, filepath.Join(commonRoot, "approval", "approval.go"), `package approvalkit

type Request struct{ Name string }

func Normalize(req Request) Request { return req }
`)
	rulePath := filepath.Join(appRoot, "rules", "rule.go")
	writeFile(t, rulePath, `package rules

import "example.com/common/approval"

func Rule(req approvalkit.Request) approvalkit.Request {
	return approvalkit.Normalize(req)
}
`)
	result, err := go2wlang.TranslateFilePathDetailed(rulePath, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFilePathDetailed: %v", err)
	}
	if got := result.Imports["approvalkit"]; got != "example.com/common/approval" {
		t.Fatalf("Imports[approvalkit] = %q, want replace import path", got)
	}
	if !strings.Contains(string(result.JSON), `"pkg": "approvalkit"`) {
		t.Fatalf("missing replaced package selector:\n%s", result.JSON)
	}
}

func TestTranslateFilePathResolvesVendorPackageName(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.25\n\nrequire example.com/vendor/audit v1.0.0\n")
	writeFile(t, filepath.Join(root, "vendor", "example.com", "vendor", "audit", "audit.go"), `package auditlog

type Entry struct{ Message string }

func Save(entry Entry) Entry { return entry }
`)
	rulePath := filepath.Join(root, "rules", "rule.go")
	writeFile(t, rulePath, `package rules

import "example.com/vendor/audit"

func Rule(entry auditlog.Entry) auditlog.Entry {
	return auditlog.Save(entry)
}
`)
	result, err := go2wlang.TranslateFilePathDetailed(rulePath, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFilePathDetailed: %v", err)
	}
	if got := result.Imports["auditlog"]; got != "example.com/vendor/audit" {
		t.Fatalf("Imports[auditlog] = %q, want vendor import path", got)
	}
}

func TestTranslateFilePathSkipsBuildTaggedPackageFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.25\n")
	writeFile(t, filepath.Join(root, "client", "aaa_ignore.go"), `//go:build ignore_go2wlang

package wrongclient
`)
	writeFile(t, filepath.Join(root, "client", "zzz_client.go"), `package clientapi

type Request struct{ Name string }

func Send(req Request) Request { return req }
`)
	rulePath := filepath.Join(root, "rules", "rule.go")
	writeFile(t, rulePath, `package rules

import "example.com/app/client"

func Rule(req clientapi.Request) clientapi.Request {
	return clientapi.Send(req)
}
`)
	result, err := go2wlang.TranslateFilePathDetailed(rulePath, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFilePathDetailed: %v", err)
	}
	if got := result.Imports["clientapi"]; got != "example.com/app/client" {
		t.Fatalf("Imports[clientapi] = %q, want build-matched import path; imports=%v", got, result.Imports)
	}
	if _, ok := result.Imports["wrongclient"]; ok {
		t.Fatalf("build-tagged package should be skipped: %v", result.Imports)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
