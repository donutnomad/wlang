package wflang_test

import (
	"context"
	"testing"

	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/wflang"
)

// Counter exposes AddInt8/AddInt64/AddFloat64 for overload tests.
type Counter struct{ val int64 }

func (c *Counter) AddInt8(v int8) (int64, error)         { return int64(v) + 1, nil }
func (c *Counter) AddInt64(v int64) (int64, error)       { return v + 1, nil }
func (c *Counter) AddFloat64(v float64) (float64, error) { return v + 1, nil }

func buildCounterEngine(t *testing.T) *wflang.Engine {
	t.Helper()
	reg := wflang.NewRegistry()
	if err := reg.AutoBindType((*Counter)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := reg.BindMethodOverloads("*github.com/wflang/wflang/wflang_test.Counter",
		"Add", []wflang.GoMethodOverload{
			{GoMethod: "AddInt8"},
			{GoMethod: "AddInt64"},
			{GoMethod: "AddFloat64"},
		}); err != nil {
		t.Fatalf("bindOverloads: %v", err)
	}
	return wflang.NewEngine(wflang.EngineOptions{Registry: reg})
}

// TC-087 BindMethodOverloads 多 Go 名称合并：int64 literal 命中 AddInt64
func TestTC087_OverloadInt64(t *testing.T) {
	eng := buildCounterEngine(t)
	src := []byte(`[{"return":{"Add":[
		{"var":"counter"},
		{"literal":{"type":"int64","value":"1"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"counter": &Counter{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Should match AddInt64 (return int64=2)
	if v.Go().(int64) != 2 {
		t.Fatalf("want int64=2, got %s=%v", v.TypeName(), v.Go())
	}
}

// TC-088 重载分派优先级：int8 literal 命中 AddInt8 精确
func TestTC088_OverloadInt8Exact(t *testing.T) {
	eng := buildCounterEngine(t)
	src := []byte(`[{"return":{"Add":[
		{"var":"counter"},
		{"literal":{"type":"int8","value":"1"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"counter": &Counter{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// AddInt8 returns int64=2
	if v.Go().(int64) != 2 {
		t.Fatalf("want 2, got %v", v.Go())
	}
}

// TC-091 重载歧义：两个同分候选触发 E_AMBIGUOUS_OVERLOAD
type Ambi struct{}

func (Ambi) AddA(v int64) (int64, error) { return v, nil }
func (Ambi) AddB(v int64) (int64, error) { return v, nil }

func TestTC091_AmbiguousOverload(t *testing.T) {
	reg := wflang.NewRegistry()
	if err := reg.AutoBindType(Ambi{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := reg.BindMethodOverloads("github.com/wflang/wflang/wflang_test.Ambi",
		"Add", []wflang.GoMethodOverload{
			{GoMethod: "AddA"},
			{GoMethod: "AddB"},
		}); err != nil {
		t.Fatalf("bindOverloads: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"Add":[
		{"var":"a"},
		{"literal":{"type":"int64","value":"1"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"a": Ambi{}},
	})
	if err == nil {
		t.Fatalf("want E_AMBIGUOUS_OVERLOAD, got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok || le.Code != werr.CodeAmbiguousOverload {
		t.Fatalf("want E_AMBIGUOUS_OVERLOAD, got %v", err)
	}
}
