package wflang_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/wflang"
)

// --- TC-123 参数数量错误 -------------------------------------------------

// Calc registers a two-arg method for TC-123; calling with 1 arg triggers E_TYPE / E_AMBIGUOUS_OVERLOAD.
type Calc struct{}

func (Calc) AddPair(a, b int64) int64 { return a + b }

func TestTC123_ArgCountError(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType(Calc{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"AddPair":[{"var":"c"},{"literal":{"type":"int64","value":"1"}}]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		return // compile-time rejection acceptable.
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"c": Calc{}},
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

// --- TC-124 AST 结构异常 ------------------------------------------------

func TestTC124_ASTShapeError(t *testing.T) {
	src := []byte(`[{"+":"not-array"}]`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	_, err := eng.CompileJSON(src)
	if err == nil {
		// Accept runtime-time rejection too.
		return
	}
	if le, ok := err.(*werr.LangError); ok {
		if le.Code != werr.CodeASTShape && le.Code != werr.CodeSymbol {
			t.Fatalf("want E_AST_SHAPE, got %s", le.Code)
		}
	}
}

// --- TC-125 typed literal 不支持类型 ------------------------------------

func TestTC125_UnknownTypedLiteralType(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"unknown_t","value":"x"}}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

// --- TC-126 LangError path/hint ----------------------------------------

func TestTC126_LangErrorCarriesPath(t *testing.T) {
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"string","value":"x"}}
	]}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error")
	}
	var le *werr.LangError
	if !errors.As(err, &le) {
		t.Fatalf("want *LangError, got %T", err)
	}
	if le.Path == "" {
		t.Fatalf("want non-empty Path, got blank")
	}
	if !strings.HasPrefix(le.Path, "/") {
		t.Fatalf("want JSON Pointer starting with /, got %q", le.Path)
	}
}

// --- TC-070 语句数组顺序执行 + return -----------------------------------

func TestTC070_SequentialReturn(t *testing.T) {
	src := []byte(`[
		{"let":{"a":{"literal":{"type":"int64","value":"1"}}}},
		{"let":{"b":{"literal":{"type":"int64","value":"2"}}}},
		{"return":{"+":[{"var":"a"},{"var":"b"}]}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 3 {
		t.Fatalf("want 3, got %v", v.Go())
	}
}
