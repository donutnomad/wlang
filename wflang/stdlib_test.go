package wflang_test

import (
	"context"
	"testing"

	"github.com/donutnomad/wlang/wflang"
)

// TC-807-style: DefaultRegistry exposes pure stdlib (str.Len here).
func TestStdlib_StrLen(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"Len":[
		{"pkg":"str"},
		{"literal":{"type":"string","value":"hello"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 5 {
		t.Fatalf("want 5, got %v", v.Go())
	}
}

func TestStdlib_StrContains(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"Contains":[
		{"pkg":"str"},
		{"literal":{"type":"string","value":"hello world"}},
		{"literal":{"type":"string","value":"world"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !v.Go().(bool) {
		t.Fatalf("want true, got %v", v.Go())
	}
}

func TestStdlib_NumMin(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"Min":[
		{"pkg":"num"},
		{"literal":{"type":"float64","value":"3.5"}},
		{"literal":{"type":"float64","value":"1.25"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(float64) != 1.25 {
		t.Fatalf("want 1.25, got %v", v.Go())
	}
}
