package wflang_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/wflang"
)

// --- TC-991 §16.10 routine ----------------------------------------
// A `routine` statement returns null immediately while the host call runs
// in the background. We verify both: program returns null, and the host
// function is eventually invoked.
type tc991Events struct {
	mu   sync.Mutex
	cond *sync.Cond
	seen string
}

func newTC991Events() *tc991Events {
	e := &tc991Events{}
	e.cond = sync.NewCond(&e.mu)
	return e
}

func (e *tc991Events) publish(ev string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seen = ev
	e.cond.Broadcast()
	return nil
}

func (e *tc991Events) wait(d time.Duration) string {
	deadline := time.Now().Add(d)
	e.mu.Lock()
	defer e.mu.Unlock()
	for e.seen == "" {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return ""
		}
		done := make(chan struct{})
		go func() {
			time.Sleep(remaining)
			e.mu.Lock()
			e.cond.Broadcast()
			e.mu.Unlock()
			close(done)
		}()
		e.cond.Wait()
		select {
		case <-done:
		default:
		}
	}
	return e.seen
}

func TestTC991_RoutineReturnsNullAndRunsInBackground(t *testing.T) {
	ev := newTC991Events()
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("events", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Publish", Impl: ev.publish, Pure: false},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[
		{"routine":{"Publish":[{"pkg":"events"},{"var":"e"}]}},
		{"return":{"literal":{"type":"null","value":null}}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"e": "user.created"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "null" {
		t.Fatalf("main returned %s, want null", v.TypeName())
	}
	// Wait for the background publish to land.
	got := ev.wait(2 * time.Second)
	if got != "user.created" {
		t.Fatalf("background publish not received; got %q", got)
	}
}

// --- TC-725 routine host error 不冒泡到主流程 ---------------------------
// When a routine's host call returns an error value, the main program still
// returns normally and the host call completes in the background.
type tc725Fail struct{ hits *atomic.Int32 }

func (f tc725Fail) Boom() error {
	f.hits.Add(1)
	return errors.New("boom-boom")
}

func TestTC725_RoutineErrorStaysOffMain(t *testing.T) {
	var hits atomic.Int32
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("kapow", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Boom", Impl: tc725Fail{hits: &hits}.Boom, Pure: false},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	gotErr := make(chan error, 1)
	sess, err := eng.NewSession(wflang.SessionOptions{
		RoutineErrorHandler: func(_ context.Context, err error) { gotErr <- err },
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	v, err := sess.AppendRun(context.Background(), []byte(`[
		{"routine":{"Boom":[{"pkg":"kapow"}]}},
		{"return":{"literal":{"type":"int64","value":"7"}}}
	]`))
	if err != nil {
		t.Fatalf("run (should not fail): %v", err)
	}
	if v.Go().(int64) != 7 {
		t.Fatalf("main result: want 7, got %v", v.Go())
	}
	waitUntil(t, 2*time.Second, func() bool {
		return hits.Load() == 1
	})
	select {
	case e := <-gotErr:
		t.Fatalf("handler got ordinary host error result: %v", e)
	default:
	}
}

// --- TC-443 routine 内调用共享派生 ctx ----------------------------
// The ctx the host call sees inside a routine must be derived from the
// ctx passed to Run (values propagate, cancel propagates).
func TestTC443_RoutineCtxDerived(t *testing.T) {
	type markerKey struct{}
	var mu sync.Mutex
	var gotCtx context.Context
	done := make(chan struct{}, 1)
	capture := func(ctx context.Context) error {
		mu.Lock()
		gotCtx = ctx
		mu.Unlock()
		done <- struct{}{}
		return nil
	}
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("cap", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Grab", Impl: capture, Pure: false},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[
		{"routine":{"Grab":[{"pkg":"cap"}]}},
		{"return":{"literal":{"type":"null","value":null}}}
	]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	parent := context.WithValue(context.Background(), markerKey{}, "r443")
	if _, err = prog.Run(parent, wflang.RunOptions{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("routine host never ran")
	}
	mu.Lock()
	got := gotCtx
	mu.Unlock()
	if got == nil {
		t.Fatal("routine got nil ctx")
	}
	if m, _ := got.Value(markerKey{}).(string); m != "r443" {
		t.Fatalf("ctx value not derived from Run parent: got %q", m)
	}
}
