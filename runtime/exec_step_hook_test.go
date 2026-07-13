package runtime

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/donutnomad/wlang/compiler"
)

func TestExecutorOnStepObservesStatementScopeAndCanPause(t *testing.T) {
	prog, err := compiler.ParseProgram([]byte(`[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"set":{"x":{"literal":{"type":"int64","value":"2"}}}},
		{"return":{"var":"x"}}
	]`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	errPause := errors.New("pause")
	exec := NewExecutor(context.Background(), NewScope(), nil, nil, Budget{})
	var paths []string
	var xAtSet any
	exec.OnStep = func(info StepInfo) error {
		paths = append(paths, info.Node.Path())
		if info.RoutineID != 0 {
			t.Errorf("main-line statement %q got routineID %d, want 0",
				info.Node.Path(), info.RoutineID)
		}
		if info.Node.Path() == "/program/1/set" {
			vars := info.Scope.AllVars()
			xAtSet = vars["x"].Go()
			return errPause
		}
		return nil
	}

	_, err = exec.RunProgram(prog)
	if !errors.Is(err, errPause) {
		t.Fatalf("want pause error, got %v", err)
	}
	wantPaths := []string{"/program/0/let", "/program/1/set"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths mismatch: want %#v, got %#v", wantPaths, paths)
	}
	if xAtSet != int64(1) {
		t.Fatalf("hook should run before set mutates x, got %v", xAtSet)
	}
	if got, ok := exec.Scope().LookupPath("x"); !ok || got.Go() != int64(1) {
		t.Fatalf("x should remain 1 after pause, got ok=%v value=%v", ok, got.Go())
	}
}
