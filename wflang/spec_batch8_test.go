package wflang_test

import (
	"context"
	"errors"
	"testing"

	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/wflang"
)

// --- TC-054 顶级 Vars 默认只读 ------------------------------------------

func TestTC054_TopLevelVarsReadOnly(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{Vars: map[string]any{"x": int64(1)}})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	_, err = sess.AppendRun(context.Background(),
		[]byte(`[{"set":{"x":{"literal":{"type":"int64","value":"2"}}}}]`))
	if err == nil {
		t.Fatal("want error for set on read-only var, got nil")
	}
}

// --- TC-347 未导出字段不可见 ------------------------------------------

type UnexportedBook struct {
	name string
	ID   int64
}

func TestTC347_UnexportedFieldHidden(t *testing.T) {
	b := UnexportedBook{name: "x", ID: 1}
	// Unexported `name` is invisible.
	_, err := runSrc(t, []byte(`[{"return":{"var":"b.name"}}]`), map[string]any{"b": b})
	if err == nil {
		t.Fatal("want error on unexported field, got nil")
	}
	// Exported `ID` works.
	v, err := runSrc(t, []byte(`[{"return":{"var":"b.ID"}}]`), map[string]any{"b": b})
	if err != nil {
		t.Fatalf("exported: %v", err)
	}
	if v.Go().(int64) != 1 {
		t.Fatalf("want 1, got %v", v.Go())
	}
}

// --- TC-348 json:"-" 字段屏蔽 ------------------------------------------

type HiddenUser struct {
	Name  string `json:"name"`
	Token string `json:"-"`
}

func TestTC348_JSONDashFieldHidden(t *testing.T) {
	u := HiddenUser{Name: "alice", Token: "secret"}
	_, err := runSrc(t, []byte(`[{"return":{"var":"u.Token"}}]`), map[string]any{"u": u})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	// json tag `name` works.
	v, err := runSrc(t, []byte(`[{"return":{"var":"u.name"}}]`), map[string]any{"u": u})
	if err != nil {
		t.Fatalf("json tag: %v", err)
	}
	if v.Go().(string) != "alice" {
		t.Fatalf("want alice, got %v", v.Go())
	}
}

// --- TC-729 E_NIL_RECEIVER 可被 try 捕获 ------------------------------

func TestTC729_NilReceiverCatchable(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType(&nullRcvBook{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	// Wrap the null-receiver call in try; return a constant afterward.
	src := []byte(`[
		{"try":{"do":[
			{"expr":{"TitleOf":[{"var":"b"}]}}
		],"bind":"err","catch":[]}},
		{"return":{"literal":{"type":"int64","value":"42"}}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		return // compile-time rejection acceptable.
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"b": nil},
	})
	if err != nil {
		t.Fatalf("want try to catch, got err: %v", err)
	}
	if v.Go().(int64) != 42 {
		t.Fatalf("want 42, got %v", v.Go())
	}
}

// --- TC-730 LangError 字段完备 ----------------------------------------

func TestTC730_LangErrorFieldsComplete(t *testing.T) {
	// A type-mismatch raises LangError with Code + Message + Path set.
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"string","value":"x"}}
	]}}]`)
	_, err := runSrc(t, src, nil)
	var le *werr.LangError
	if !errors.As(err, &le) {
		t.Fatalf("want *LangError, got %T", err)
	}
	if le.Code == "" {
		t.Fatal("Code empty")
	}
	if le.Message == "" {
		t.Fatal("Message empty")
	}
	if le.Path == "" {
		t.Fatal("Path empty")
	}
}

// --- TC-807 默认 Registry 仅含纯函数标准库 ----------------------------

func TestTC807_DefaultRegistryPureOnly(t *testing.T) {
	reg := wflang.DefaultRegistry()
	names := reg.PackageNames()
	// Must include the stdlib.
	for _, want := range []string{"str", "num", "arr", "val", "to", "json", "path"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("stdlib missing: %s", want)
		}
	}
	// Must NOT include side-effectful packages.
	for _, bad := range []string{"net", "http", "file", "os", "db", "sql"} {
		if _, ok := names[bad]; ok {
			t.Fatalf("default registry must not include side-effect pkg %q", bad)
		}
	}
}

// --- TC-809 参数数量不足在反射前报错 -----------------------------------

type TwoArg struct{}

func (TwoArg) Pair(a, b int64) int64 { return a + b }

func TestTC809_ArgCountCheckedBeforeReflect(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType(TwoArg{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	// Pass only one arg.
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Pair":[{"var":"c"},{"literal":{"type":"int64","value":"1"}}]}}]`))
	if err != nil {
		return // compile-time rejection is fine.
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"c": TwoArg{}},
	})
	if err == nil {
		t.Fatal("want error")
	}
}
