// Batch 26 covers Budget enforcement (LANGUAGE.md §10.1):
// MaxCallDepth (TC-801), MaxRoutines (TC-803), MaxArrayLength (TC-804),
// and the routine-spawn budget tie-in (TC-211).
package wflang_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/wflang"
)

// --- TC-801 MaxCallDepth -----------------------------------------------
// A host function that recursively calls back into wflang would normally
// be required to demonstrate true recursion; in this implementation host
// functions are leaves, so we validate MaxCallDepth via a deeply nested
// call chain. The single host call increments callDepth once; for a real
// depth test we chain N host calls within a single expression.
func tc801Echo(v int64) int64 { return v }

func TestTC801_MaxCallDepthExceeded(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("e", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Echo", Impl: tc801Echo, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	// Build deeply nested host calls: Echo(Echo(Echo(...Lit...))).
	const depth = 16
	var src strings.Builder
	src.WriteString(`[{"return":`)
	for i := 0; i < depth; i++ {
		src.WriteString(`{"Echo":[{"pkg":"e"},`)
	}
	src.WriteString(`{"literal":{"type":"int64","value":"1"}}`)
	for i := 0; i < depth; i++ {
		src.WriteString(`]}`)
	}
	src.WriteString(`}]`)
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry: reg,
		Budget:   wflang.Budget{MaxCallDepth: 4},
	})
	prog, err := eng.CompileJSON([]byte(src.String()))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(t.Context(), wflang.RunOptions{})
	if err == nil {
		t.Fatalf("want E_BUDGET, got nil")
	}
	var le *wflang.LangError
	if !errors.As(err, &le) || le.Code != "E_BUDGET" {
		t.Fatalf("want E_BUDGET, got %v (%T)", err, err)
	}
}

// --- TC-803 MaxRoutines ------------------------------------------------
type tc803Sleeper struct {
	mu    sync.Mutex
	hold  chan struct{}
	calls int
}

func (s *tc803Sleeper) Wait() (int64, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	<-s.hold
	return 0, nil
}

func TestTC803_MaxRoutinesExceeded(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*tc803Sleeper)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	sleeper := &tc803Sleeper{hold: make(chan struct{})}
	defer close(sleeper.hold)

	gotErr := make(chan error, 4)
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry: reg,
		Budget:   wflang.Budget{MaxRoutines: 1},
	})
	sess, err := eng.NewSession(wflang.SessionOptions{
		Vars: map[string]any{"s": sleeper},
		RoutineErrorHandler: func(ctx context.Context, e error) {
			gotErr <- e
		},
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	// First two routines: first holds the slot, second should be rejected.
	src := []byte(`[
		{"routine":{"Wait":[{"var":"s"}]}},
		{"routine":{"Wait":[{"var":"s"}]}},
		{"return":{"literal":{"type":"int64","value":"1"}}}
	]`)
	_, err = sess.AppendRun(t.Context(), src)
	if err == nil {
		t.Fatalf("want E_BUDGET, got nil")
	}
	var le *wflang.LangError
	if !errors.As(err, &le) || le.Code != "E_BUDGET" {
		t.Fatalf("want E_BUDGET, got %v (%T)", err, err)
	}
}

// --- TC-211 routine 受 capability 与 MaxRoutines 限制 -----------------
// The MaxRoutines half is exercised here. The capability half is covered in
// Batch 27 (TC-440/441).
func TestTC211_RoutineSpawnBoundedByMaxRoutines(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*tc803Sleeper)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	sleeper := &tc803Sleeper{hold: make(chan struct{})}
	defer close(sleeper.hold)

	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry: reg,
		Budget:   wflang.Budget{MaxRoutines: 0}, // 0 means unlimited
	})
	sess, err := eng.NewSession(wflang.SessionOptions{
		Vars: map[string]any{"s": sleeper},
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	src := []byte(`[
		{"routine":{"Wait":[{"var":"s"}]}},
		{"routine":{"Wait":[{"var":"s"}]}},
		{"return":{"literal":{"type":"int64","value":"1"}}}
	]`)
	if _, err := sess.AppendRun(t.Context(), src); err != nil {
		t.Fatalf("unlimited budget rejected routines: %v", err)
	}
	// Settle: the goroutines are blocked, but registration succeeded.
	time.Sleep(20 * time.Millisecond)
}

// --- TC-804 MaxArrayLength ---------------------------------------------
func TestTC804_MaxArrayLengthExceeded(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry: wflang.DefaultRegistry(),
		Budget:   wflang.Budget{MaxArrayLength: 2},
	})
	src := []byte(`[{"return":{"array":{"elem":"int64","items":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"2"}},
		{"literal":{"type":"int64","value":"3"}}
	]}}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(t.Context(), wflang.RunOptions{})
	if err == nil {
		t.Fatalf("want E_BUDGET, got nil")
	}
	var le *wflang.LangError
	if !errors.As(err, &le) || le.Code != "E_BUDGET" {
		t.Fatalf("want E_BUDGET, got %v (%T)", err, err)
	}
}

// --- TC-805 MaxAllocBytes (current behaviour: not enforced) -----------
// Document the current contract: MaxAllocBytes is reserved for a future
// implementation. Until enforcement lands, the field is accepted but does
// not stop oversized allocations. A test asserting non-enforcement keeps
// the gap visible without skipping.
func TestTC805_MaxAllocBytesNotEnforcedYet(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry: wflang.DefaultRegistry(),
		Budget:   wflang.Budget{MaxAllocBytes: 1},
	})
	// A small array allocation under any reasonable limit; should still run.
	src := []byte(`[{"return":{"array":{"elem":"int64","items":[
		{"literal":{"type":"int64","value":"1"}}
	]}}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := prog.Run(t.Context(), wflang.RunOptions{}); err != nil {
		t.Fatalf("MaxAllocBytes incorrectly enforced: %v", err)
	}
}
