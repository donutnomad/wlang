package wflang_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/wflang"
)

// TC-203 panic 转 E_PANIC
func TestTC203_PanicToLangError(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"panic":{"literal":{"type":"string","value":"boom"}}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{})
	if err == nil {
		t.Fatalf("want E_PANIC, got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok || le.Code != werr.CodePanic {
		t.Fatalf("want E_PANIC, got %v", err)
	}
	if !strings.Contains(le.Message, "boom") {
		t.Fatalf("expected message to contain \"boom\", got %q", le.Message)
	}
}

// Failing host for bubbling tests: adds Failer type with Boom() returning error.
type Failer struct{}

func (Failer) Boom() (int64, error) { return 0, errors.New("boom") }

// TC-206 expr 内部错误按短路冒泡
func TestTC206_ExprBubble(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType(Failer{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[
		{"expr":{"Boom":[{"var":"f"}]}},
		{"return":{"literal":{"type":"int64","value":"1"}}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"f": Failer{}},
	})
	if err == nil {
		t.Fatalf("want error bubbling, got nil (return 1)")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want bubble from Boom, got %v", err)
	}
}

// try 捕获错误并返回 catch 分支的值
func TestTry_CatchReturnsFallback(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType(Failer{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[
		{"let":{"x":{"try":{
			"do":[{"return":{"Boom":[{"var":"f"}]}}],
			"bind":"err",
			"catch":[{"return":{"literal":{"type":"int64","value":"42"}}}]
		}}}},
		{"return":{"var":"x"}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"f": Failer{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 42 {
		t.Fatalf("want 42, got %v", v.Go())
	}
}

// TC-092 Go error 自动暴露 Error 方法：catch 里访问 err.Error()
func TestTC092_ErrorMethod(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType(Failer{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[
		{"let":{"msg":{"try":{
			"do":[{"return":{"Boom":[{"var":"f"}]}}],
			"bind":"err",
			"catch":[{"return":{"Error":[{"var":"err"}]}}]
		}}}},
		{"return":{"var":"msg"}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"f": Failer{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "boom" {
		t.Fatalf("want \"boom\", got %v", v.Go())
	}
}
