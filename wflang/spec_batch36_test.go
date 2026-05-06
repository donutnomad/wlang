// Batch 36 covers Tier-2D: Compile-stage Trace observability.
//
//	TC-600 编译阶段顺序可观察
package wflang_test

import (
	"testing"

	"github.com/wflang/wflang/compiler"
	"github.com/wflang/wflang/wflang"
)

// TC-600: With Trace enabled, CompileJSON must record an ordered list of
// pipeline events Decode → Normalize → Parse → Resolve → TypeCheck →
// Capability → Lower (LANGUAGE.md §7.1).
func TestTC600_CompilePhaseOrderObservable(t *testing.T) {
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"return":{"literal":{"type":"int64","value":"7"}}}
	]}`)

	eng := wflang.NewEngine(wflang.EngineOptions{Trace: true})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got := prog.CompileTrace()
	want := []compiler.Phase{
		compiler.PhaseDecode,
		compiler.PhaseNormalize,
		compiler.PhaseParse,
		compiler.PhaseResolve,
		compiler.PhaseTypeCheck,
		compiler.PhaseCapability,
		compiler.PhaseLower,
	}
	if len(got) != len(want) {
		t.Fatalf("trace has %d events, want %d (%+v)", len(got), len(want), got)
	}
	for i, p := range want {
		if got[i].Phase != string(p) {
			t.Fatalf("event %d phase = %q, want %q (full trace=%+v)",
				i, got[i].Phase, p, got)
		}
		if got[i].Order != i {
			t.Fatalf("event %d Order = %d, want %d", i, got[i].Order, i)
		}
	}
}

// Without Trace enabled the slice must be empty so callers can detect that
// trace recording is opt-in.
func TestTC600_TraceDefaultOff(t *testing.T) {
	src := []byte(`{"lang":"wflang/v1","program":[{"return":{"literal":{"type":"int64","value":"1"}}}]}`)
	eng := wflang.NewEngine(wflang.EngineOptions{})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got := prog.CompileTrace(); len(got) != 0 {
		t.Fatalf("expected empty trace by default, got %+v", got)
	}
}

// CompilePhases is the canonical phase ordering — exposed so tooling can
// document or assert the contract independently of a real compile run.
func TestTC600_CompilePhasesContract(t *testing.T) {
	got := wflang.CompilePhases()
	want := []compiler.Phase{
		compiler.PhaseDecode,
		compiler.PhaseNormalize,
		compiler.PhaseParse,
		compiler.PhaseResolve,
		compiler.PhaseTypeCheck,
		compiler.PhaseCapability,
		compiler.PhaseLower,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d phases, want %d", len(got), len(want))
	}
	for i, p := range want {
		if got[i] != p {
			t.Fatalf("phase %d = %q, want %q", i, got[i], p)
		}
	}
}
