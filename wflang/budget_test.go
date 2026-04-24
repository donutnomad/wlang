package wflang_test

import (
	"context"
	"testing"

	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/wflang"
)

// TC-800 MaxSteps 触发 E_BUDGET
func TestTC800_MaxSteps(t *testing.T) {
	// Simple `return 1` program needs a handful of steps; setting MaxSteps=1
	// guarantees budget exhaustion.
	eng := wflang.NewEngine(wflang.EngineOptions{
		Budget: wflang.Budget{MaxSteps: 1},
	})
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"2"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{})
	if err == nil {
		t.Fatalf("want E_BUDGET, got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok || le.Code != werr.CodeBudget {
		t.Fatalf("want E_BUDGET, got %v", err)
	}
}

// TC-802 MaxLoopIterations 触发 E_BUDGET
func TestTC802_MaxLoopIter(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{
		Budget: wflang.Budget{MaxLoopIterations: 3, MaxSteps: 1_000_000},
	})
	// fori i := 0; i < 1000; i++ { }
	src := []byte(`[
		{"fori":{
			"var":"i",
			"from":{"literal":{"type":"int64","value":"0"}},
			"to":{"literal":{"type":"int64","value":"1000"}},
			"do":[]
		}},
		{"return":{"literal":{"type":"int64","value":"0"}}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{})
	if err == nil {
		t.Fatalf("want E_BUDGET, got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok || le.Code != werr.CodeBudget {
		t.Fatalf("want E_BUDGET, got %v", err)
	}
}

// TC-445 context cancel 中断执行
func TestTC445_CtxCancel(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{
		Budget: wflang.Budget{MaxSteps: 1_000_000, MaxLoopIterations: 1_000_000},
	})
	src := []byte(`[
		{"fori":{
			"var":"i",
			"from":{"literal":{"type":"int64","value":"0"}},
			"to":{"literal":{"type":"int64","value":"1000000"}},
			"do":[]
		}},
		{"return":{"literal":{"type":"int64","value":"0"}}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err = prog.Run(ctx, wflang.RunOptions{})
	if err == nil {
		t.Fatalf("want cancel error, got nil")
	}
}
