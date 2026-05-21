package wflang_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/donutnomad/wlang/wflang"
)

// publisher with both successful and failing methods.
type publisher struct {
	mu    sync.Mutex
	calls int
	boom  chan struct{}
}

func (p *publisher) Echo(s string) (string, error) {
	return "echo:" + s, nil
}

func (p *publisher) EchoAfterSignal(s string) (string, error) {
	return "echo:" + s, nil
}

func (p *publisher) Publish(s string) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return 1, nil
}

func (p *publisher) Boom(s string) (int64, error) {
	if p.boom != nil {
		close(p.boom)
	}
	return 0, errors.New("routine fail")
}

func (p *publisher) Pair(s string) (string, int64, error) {
	return s, int64(len(s)), nil
}

func (p *publisher) NoValue(s string) error {
	return nil
}

func TestRoutineAwaitSingleHandle(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*publisher)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	pub := &publisher{}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[
		{"let":{"h":{"routine":{"Echo":[{"var":"p"},{"literal":{"type":"string","value":"hi"}}]}}}},
		{"return":{"await":{"var":"h"}}}
	]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"p": pub},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "tuple<string,error>" || unwrap1(t, v) != "echo:hi" || unwrapErr(t, v) != nil {
		t.Fatalf("want tuple<string,error>{echo:hi,nil}, got %s %v", v.TypeName(), v.Go())
	}
}

func TestRoutineCapturesArgumentsBeforeLaunch(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*publisher)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	pub := &publisher{}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[
		{"let":{"token":{"literal":{"type":"string","value":"before"}}}},
		{"let":{"h":{"routine":{"EchoAfterSignal":[{"var":"p"},{"var":"token"}]}}}},
		{"set":{"token":{"literal":{"type":"string","value":"after"}}}},
		{"return":{"await":{"var":"h"}}}
	]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"p": pub},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "tuple<string,error>" || unwrap1(t, v) != "echo:before" || unwrapErr(t, v) != nil {
		t.Fatalf("want tuple<string,error>{echo:before,nil}, got %s %v", v.TypeName(), v.Go())
	}
}

func TestRoutineAwaitManyHandles(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*publisher)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	pub := &publisher{}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[
		{"let":{"a":{"routine":{"Echo":[{"var":"p"},{"literal":{"type":"string","value":"a"}}]}}}},
		{"let":{"b":{"routine":{"Echo":[{"var":"p"},{"literal":{"type":"string","value":"bb"}}]}}}},
		{"return":{"await":[{"var":"a"},{"var":"b"}]}}
	]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"p": pub},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "array<any>" {
		t.Fatalf("want array<any>, got %s", v.TypeName())
	}
	got, ok := v.Go().([]any)
	if !ok || len(got) != 2 {
		t.Fatalf("unexpected await results: %T %#v", v.Go(), v.Go())
	}
	first, ok := got[0].([]any)
	if !ok || len(first) != 2 || first[0] != "echo:a" || first[1] != nil {
		t.Fatalf("first await result: %T %#v", got[0], got[0])
	}
	second, ok := got[1].([]any)
	if !ok || len(second) != 2 || second[0] != "echo:bb" || second[1] != nil {
		t.Fatalf("second await result: %T %#v", got[1], got[1])
	}
}

func TestRoutineAwaitReturnsErrorValue(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*publisher)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	pub := &publisher{}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[
		{"let":{"h":{"routine":{"Boom":[{"var":"p"},{"literal":{"type":"string","value":"x"}}]}}}},
		{"return":{"await":{"var":"h"}}}
	]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"p": pub},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "tuple<int64,error>" {
		t.Fatalf("want tuple<int64,error>, got %s %v", v.TypeName(), v.Go())
	}
	if got := unwrapErr(t, v); got == nil || got.(error).Error() != "routine fail" {
		t.Fatalf("want routine fail error value, got %v", got)
	}
}

func TestRoutineAwaitCanRepeat(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*publisher)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	pub := &publisher{}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(`[
		{"let":{"h":{"routine":{"Pair":[{"var":"p"},{"literal":{"type":"string","value":"abc"}}]}}}},
		{"let":{"first":{"await":{"var":"h"}}}},
		{"let":{"second":{"await":{"var":"h"}}}},
		{"return":{"==":[{"var":"first"},{"var":"second"}]}}
	]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"p": pub},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "boolean" || v.Go() != true {
		t.Fatalf("want boolean true, got %s %v", v.TypeName(), v.Go())
	}
}

func TestAwaitRejectsNonHandle(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON([]byte(`[
		{"return":{"await":{"literal":{"type":"int64","value":"1"}}}}
	]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{})
	if err == nil {
		t.Fatalf("want non-handle await error, got nil")
	}
}

// TC-208 routine 单调用立即返回 handle and continues
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

// TC-209 host error is a routine result value.
func TestTC209_RoutineHostErrorIsResultValue(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*publisher)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	pub := &publisher{boom: make(chan struct{})}
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
	case <-pub.boom:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("routine did not run")
	}
	select {
	case e := <-got:
		t.Fatalf("handler got ordinary host error result: %v", e)
	default:
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
