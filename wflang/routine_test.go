package wflang_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wflang/wflang/wflang"
)

// publisher with both successful and failing methods.
type publisher struct {
	mu    sync.Mutex
	calls int
}

func (p *publisher) Publish(s string) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return 1, nil
}

func (p *publisher) Boom(s string) (int64, error) {
	return 0, errors.New("routine fail")
}

func (p *publisher) Yieldy(s string) (int64, error) {
	return 0, wflang.NewYield("tok-1", map[string]any{"kind": "async"})
}

// TC-208 routine 单调用立即返回 null
func TestTC208_RoutineFireAndForget(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*publisher)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	pub := &publisher{}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	sess, err := eng.NewSession(wflang.SessionOptions{
		Vars: map[string]any{"p": pub},
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	src := []byte(`[
		{"routine":{"Publish":[{"var":"p"},{"literal":{"type":"string","value":"hi"}}]}},
		{"return":{"literal":{"type":"int64","value":"1"}}}
	]`)
	v, err := sess.AppendRun(context.Background(), src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 1 {
		t.Fatalf("want 1, got %v", v.Go())
	}
	// Give the background goroutine a moment to fire.
	waitUntil(t, 200*time.Millisecond, func() bool {
		pub.mu.Lock()
		defer pub.mu.Unlock()
		return pub.calls == 1
	})
}

// TC-209 routine error 进入 RoutineErrorHandler
func TestTC209_RoutineErrorHandler(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*publisher)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	pub := &publisher{}
	got := make(chan error, 1)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	sess, err := eng.NewSession(wflang.SessionOptions{
		Vars: map[string]any{"p": pub},
		RoutineErrorHandler: func(ctx context.Context, e error) {
			got <- e
		},
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	src := []byte(`[
		{"routine":{"Boom":[{"var":"p"},{"literal":{"type":"string","value":"x"}}]}},
		{"return":{"literal":{"type":"int64","value":"1"}}}
	]`)
	if _, err := sess.AppendRun(context.Background(), src); err != nil {
		t.Fatalf("run: %v", err)
	}
	select {
	case e := <-got:
		if e == nil || e.Error() == "" {
			t.Fatalf("want non-nil error, got %v", e)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("RoutineErrorHandler was not called")
	}
}

// TC-210 routine yield 进入 RoutineYieldHandler
func TestTC210_RoutineYieldHandler(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*publisher)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	pub := &publisher{}
	got := make(chan wflang.YieldState, 1)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	sess, err := eng.NewSession(wflang.SessionOptions{
		Vars: map[string]any{"p": pub},
		RoutineYieldHandler: func(ctx context.Context, y wflang.YieldState) {
			got <- y
		},
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	src := []byte(`[
		{"routine":{"Yieldy":[{"var":"p"},{"literal":{"type":"string","value":"x"}}]}},
		{"return":{"literal":{"type":"int64","value":"1"}}}
	]`)
	if _, err := sess.AppendRun(context.Background(), src); err != nil {
		t.Fatalf("run: %v", err)
	}
	select {
	case y := <-got:
		if y.Token != "tok-1" {
			t.Fatalf("want token tok-1, got %q", y.Token)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("RoutineYieldHandler was not called")
	}
}

// TC-093 前台 yield error 按普通 error 处理（不进 yield handler）
func TestTC093_ForegroundYieldIsOrdinaryError(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*publisher)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	pub := &publisher{}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"Yieldy":[{"var":"p"},{"literal":{"type":"string","value":"x"}}]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"p": pub},
	})
	if err == nil {
		t.Fatalf("want ordinary error bubble, got nil")
	}
}

func waitUntil(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", d)
}
