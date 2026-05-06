// Batch 35 covers Tier-2E: Normalize migration + deprecation table.
//
//	TC-604 Normalize 旧版本语法迁移
//	TC-882 deprecation 表
//	TC-883 迁移器
package wflang_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wflang/wflang/wflang"
)

// ---------- TC-604 -----------------------------------------------------------
func TestTC604_Normalize_LegacyLoopMigratesToFori(t *testing.T) {
	// Legacy form uses "loop" instead of "fori"; Normalize must rewrite it
	// AND surface a deprecation diagnostic, while the program still runs.
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"let":{"sum":{"literal":{"type":"int64","value":"0"}}}},
	  {"loop":{
	     "var":"i",
	     "from":{"literal":{"type":"int64","value":"1"}},
	     "to":{"literal":{"type":"int64","value":"4"}},
	     "do":[{"set":{"sum":{"+":[{"var":"sum"},{"var":"i"}]}}}]
	  }},
	  {"return":{"var":"sum"}}
	]}`)

	eng := wflang.NewEngine(wflang.EngineOptions{})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	diags := prog.Diagnostics()
	if len(diags) == 0 {
		t.Fatal("expected at least one deprecation diagnostic, got none")
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "loop") && d.Severity == "deprecation" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no deprecation diagnostic for legacy loop: %+v", diags)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 6 { // 1+2+3
		t.Fatalf("got %v, want 6", v.Go())
	}
}

// ---------- TC-882 -----------------------------------------------------------
func TestTC882_DeprecationTableIsAvailable(t *testing.T) {
	tbl := wflang.DeprecationTable()
	if len(tbl) == 0 {
		t.Fatal("DeprecationTable is empty")
	}
	// Must include the documented loop→fori migration.
	var loopEntry *wflang.Deprecation
	for i := range tbl {
		if tbl[i].From == "loop" && tbl[i].To == "fori" {
			loopEntry = &tbl[i]
			break
		}
	}
	if loopEntry == nil {
		t.Fatalf("expected loop→fori entry in deprecation table, got %+v", tbl)
	}
	if loopEntry.Since == "" || loopEntry.Message == "" {
		t.Fatalf("deprecation entry incomplete: %+v", loopEntry)
	}
}

// TC-882 also says "deprecated 语法 still executes" — exercise that compile
// succeeds and the program runs to completion despite the diagnostic.
func TestTC882_DeprecatedStillCompilesAndRuns(t *testing.T) {
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"return_value":{"literal":{"type":"int64","value":"42"}}}
	]}`)
	eng := wflang.NewEngine(wflang.EngineOptions{})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(prog.Diagnostics()) == 0 {
		t.Fatal("expected deprecation diagnostic for return_value")
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 42 {
		t.Fatalf("got %v, want 42", v.Go())
	}
}

// ---------- TC-883 -----------------------------------------------------------
func TestTC883_MigratorReturnsCurrentASTAndDiagnostics(t *testing.T) {
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
	  {"return_value":{"var":"x"}}
	]}`)
	out, diags, err := wflang.Migrate(src)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(diags) == 0 {
		t.Fatal("expected at least one diagnostic from Migrate")
	}
	// The rewritten output must use "return", not "return_value".
	var tree map[string]any
	if err := json.Unmarshal(out, &tree); err != nil {
		t.Fatalf("migrated output is invalid JSON: %v", err)
	}
	body, ok := tree["program"].([]any)
	if !ok || len(body) < 2 {
		t.Fatalf("migrated program is malformed: %s", out)
	}
	stmt, _ := body[1].(map[string]any)
	if _, has := stmt["return"]; !has {
		t.Fatalf("migrated stmt should contain return, got %v", stmt)
	}
	if _, still := stmt["return_value"]; still {
		t.Fatalf("migrated stmt still contains return_value: %v", stmt)
	}
	// And the migrated output must compile + execute as the current form.
	eng := wflang.NewEngine(wflang.EngineOptions{})
	prog, err := eng.CompileJSON(out)
	if err != nil {
		t.Fatalf("compile migrated: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run migrated: %v", err)
	}
	if v.Go().(int64) != 1 {
		t.Fatalf("got %v, want 1", v.Go())
	}
}

// Round-trip: programs that contain no deprecated forms should pass through
// Migrate unchanged with no diagnostics.
func TestTC883_NoOpMigration(t *testing.T) {
	src := []byte(`{"lang":"wflang/v1","program":[{"return":{"literal":{"type":"int64","value":"7"}}}]}`)
	out, diags, err := wflang.Migrate(src)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("expected zero diagnostics on already-current input, got %+v", diags)
	}
	if string(out) != string(src) {
		t.Fatalf("expected byte-identical passthrough\n got %s\nwant %s", out, src)
	}
}
