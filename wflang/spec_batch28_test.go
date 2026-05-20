package wflang_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wflang/wflang/wflang"
)

type tc28Worker struct{}

func (*tc28Worker) WaitS(token string) (string, error) {
	return "done-" + token, nil
}

func (*tc28Worker) WaitTwo(token string) (string, int64, error) {
	return token, int64(len(token)), nil
}

func (*tc28Worker) WaitNoVal(token string) error {
	return nil
}

func (*tc28Worker) Fail(token string) (string, error) {
	return "", errors.New("failed-" + token)
}

type tc28Hidden struct{ N int64 }

func (*tc28Worker) WaitHidden(token string) (*tc28Hidden, error) {
	return &tc28Hidden{N: int64(len(token))}, nil
}

func runTC28(t *testing.T, body string) (wflang.Value, error) {
	t.Helper()
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*tc28Worker)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON([]byte(body))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"w": &tc28Worker{}},
	})
}

func TestTC160_RoutineHandleAwaitRecoverable(t *testing.T) {
	v, err := runTC28(t, `[
		{"let":{"h":{"routine":{"WaitS":[{"var":"w"},{"literal":{"type":"string","value":"t160"}}]}}}},
		{"return":{"await":{"var":"h"}}}
	]`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "tuple<string,error>" || unwrap1(t, v) != "done-t160" || unwrapErr(t, v) != nil {
		t.Fatalf("got %s %v", v.TypeName(), v.Go())
	}
}

func TestTC403_RoutineHandleCanBeAwaitedRepeatedly(t *testing.T) {
	v, err := runTC28(t, `[
		{"let":{"h":{"routine":{"WaitS":[{"var":"w"},{"literal":{"type":"string","value":"t403"}}]}}}},
		{"let":{"a":{"await":{"var":"h"}}}},
		{"let":{"b":{"await":{"var":"h"}}}},
		{"return":{"==":[{"var":"a"},{"var":"b"}]}}
	]`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "boolean" || v.Go() != true {
		t.Fatalf("got %s %v", v.TypeName(), v.Go())
	}
}

func TestTC404_AwaitRejectsNonHandle(t *testing.T) {
	_, err := runTC28(t, `[
		{"return":{"await":{"literal":{"type":"string","value":"x"}}}}
	]`)
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	var le *wflang.LangError
	if !errors.As(err, &le) || le.Code != "E_TYPE" {
		t.Fatalf("want E_TYPE, got %v (%T)", err, err)
	}
}

func TestTC405_AwaitTupleFromMultiReturn(t *testing.T) {
	v, err := runTC28(t, `[
		{"let":{"h":{"routine":{"WaitTwo":[{"var":"w"},{"literal":{"type":"string","value":"t405"}}]}}}},
		{"return":{"await":{"var":"h"}}}
	]`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "tuple<string,int64,error>" {
		t.Fatalf("want tuple<string,int64,error>, got %s", v.TypeName())
	}
	parts, ok := v.Go().([]any)
	if !ok || len(parts) != 3 || parts[0] != "t405" || parts[1] != int64(4) || parts[2] != nil {
		t.Fatalf("tuple: %T %#v", v.Go(), v.Go())
	}
}

func TestTC406_AwaitErrorOnlyReturnsNull(t *testing.T) {
	v, err := runTC28(t, `[
		{"let":{"h":{"routine":{"WaitNoVal":[{"var":"w"},{"literal":{"type":"string","value":"t406"}}]}}}},
		{"return":{"await":{"var":"h"}}}
	]`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "error" || v.Go() != nil {
		t.Fatalf("want nil error value, got %s %v", v.TypeName(), v.Go())
	}
}

func TestTC407_AwaitReturnsRoutineErrorValue(t *testing.T) {
	v, err := runTC28(t, `[
		{"let":{"h":{"routine":{"Fail":[{"var":"w"},{"literal":{"type":"string","value":"t407"}}]}}}},
		{"return":{"await":{"var":"h"}}}
	]`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := unwrapErr(t, v); got == nil || !strings.Contains(got.(error).Error(), "failed-t407") {
		t.Fatalf("want routine error value, got %v", got)
	}
}

func TestTC701_AwaitManyHandlesKeepsInputOrder(t *testing.T) {
	v, err := runTC28(t, `[
		{"let":{"a":{"routine":{"WaitS":[{"var":"w"},{"literal":{"type":"string","value":"a"}}]}}}},
		{"let":{"b":{"routine":{"WaitS":[{"var":"w"},{"literal":{"type":"string","value":"b"}}]}}}},
		{"return":{"await":[{"var":"b"},{"var":"a"}]}}
	]`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "array<any>" {
		t.Fatalf("want array<any>, got %s", v.TypeName())
	}
	got, ok := v.Go().([]any)
	if !ok || len(got) != 2 {
		t.Fatalf("array: %T %#v", v.Go(), v.Go())
	}
	first, ok := got[0].([]any)
	if !ok || len(first) != 2 || first[0] != "done-b" || first[1] != nil {
		t.Fatalf("first: %T %#v", got[0], got[0])
	}
	second, ok := got[1].([]any)
	if !ok || len(second) != 2 || second[0] != "done-a" || second[1] != nil {
		t.Fatalf("second: %T %#v", got[1], got[1])
	}
}

func TestTC708_AwaitAutoHostType(t *testing.T) {
	v, err := runTC28(t, `[
		{"let":{"h":{"routine":{"WaitHidden":[{"var":"w"},{"literal":{"type":"string","value":"t708"}}]}}}},
		{"return":{"await":{"var":"h"}}}
	]`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasPrefix(v.TypeName(), "tuple<*") || !strings.Contains(v.TypeName(), "tc28Hidden") {
		t.Fatalf("auto host type name: %q", v.TypeName())
	}
	if h, ok := unwrap1(t, v).(*tc28Hidden); !ok || h.N != 4 {
		t.Fatalf("unwrap: %T %#v", v.Go(), v.Go())
	}
}
