package wflang_test

import (
	"context"
	"errors"
	"testing"

	"github.com/donutnomad/wlang/runtime"
	"github.com/donutnomad/wlang/wflang"
)

func TestRunOptionsOnStepObservesPathAndScope(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{})
	prog, err := eng.CompileJSON([]byte(`[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"return":{"var":"x"}}
	]`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	errPause := errors.New("pause")
	var sawPath string
	var sawX any
	_, err = prog.Run(context.Background(), wflang.RunOptions{
		OnStep: func(info runtime.StepInfo) error {
			if info.Node.Path() == "/program/1/return" {
				sawPath = info.Node.Path()
				sawX = info.Scope.AllVars()["x"].Go()
				return errPause
			}
			return nil
		},
	})
	if !errors.Is(err, errPause) {
		t.Fatalf("want pause error, got %v", err)
	}
	if sawPath != "/program/1/return" {
		t.Fatalf("path mismatch: %q", sawPath)
	}
	if sawX != int64(1) {
		t.Fatalf("scope x mismatch: %v", sawX)
	}
}

func TestSessionOptionsOnStepObservesAppendRun(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{})

	errPause := errors.New("pause")
	var sawPath string
	var sawX any
	sess, err := eng.NewSession(wflang.SessionOptions{
		OnStep: func(info runtime.StepInfo) error {
			if info.Node.Path() == "/program/1/return" {
				sawPath = info.Node.Path()
				sawX = info.Scope.AllVars()["x"].Go()
				return errPause
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	_, err = sess.AppendRun(context.Background(), []byte(`[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"return":{"var":"x"}}
	]`))
	if !errors.Is(err, errPause) {
		t.Fatalf("want pause error, got %v", err)
	}
	if sawPath != "/program/1/return" {
		t.Fatalf("path mismatch: %q", sawPath)
	}
	if sawX != int64(1) {
		t.Fatalf("scope x mismatch: %v", sawX)
	}
}

func TestTraceProgramRunOptionsOnStepObservesPathAndScope(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{})

	errPause := errors.New("pause")
	var sawPath string
	var sawX any
	_, _, err := eng.TraceProgram(context.Background(), []byte(`[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"return":{"var":"x"}}
	]`), wflang.RunOptions{
		OnStep: func(info runtime.StepInfo) error {
			if info.Node.Path() == "/program/1/return" {
				sawPath = info.Node.Path()
				sawX = info.Scope.AllVars()["x"].Go()
				return errPause
			}
			return nil
		},
	})
	if !errors.Is(err, errPause) {
		t.Fatalf("want pause error, got %v", err)
	}
	if sawPath != "/program/1/return" {
		t.Fatalf("path mismatch: %q", sawPath)
	}
	if sawX != int64(1) {
		t.Fatalf("scope x mismatch: %v", sawX)
	}
}
