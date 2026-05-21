// Batch 39 covers Tier-2B: 解构 let / 类型化 let JSON
// (LANGUAGE.md §3.4 — destructuring let and typed let bindings).
//
//	TC-231 destructuring let — multiple bindings in one `let` form.
//	TC-232 typed let         — declared type checked at let and persisted to set.
package wflang_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/wflang"
)

// ---------- TC-231 destructuring let ---------------------------------------
// `let { price: ..., count: ... }` creates both variables in one statement.
func TestTC231_DestructuringLetCreatesAllNames(t *testing.T) {
	src := []byte(`[
	  {"let":{
	     "price":{"literal":{"type":"int64","value":"100"}},
	     "count":{"literal":{"type":"int64","value":"3"}}
	  }},
	  {"return":{"*":[{"var":"price"},{"var":"count"}]}}
	]`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := v.Go().(int64); got != 300 {
		t.Fatalf("got %d, want 300", got)
	}
}

// Destructuring let with a default value via the `var` array form (per §3.4
// the spec example uses `{"var": ["item.count", default]}` for missing paths).
func TestTC231_DestructuringLetWithDefault(t *testing.T) {
	src := []byte(`[
	  {"let":{
	     "price":{"var":"item.price"},
	     "count":{"var":["item.count",{"literal":{"type":"int64","value":"1"}}]}
	  }},
	  {"return":{"*":[{"var":"price"},{"var":"count"}]}}
	]`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// item has only `price`; `count` falls back to default 1.
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{
			"item": map[string]any{"price": int64(50)},
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := v.Go().(int64); got != 50 {
		t.Fatalf("got %d, want 50 (50*1)", got)
	}
}

// ---------- TC-232 typed let — wrapper form ---------------------------------
// {"let":{"total":{"type":"int64","value":<expr>}}}
// The declared type sticks to the variable. A subsequent `set` with a
// different type must surface E_TYPE.
func TestTC232_TypedLetRejectsLaterSetMismatch(t *testing.T) {
	src := []byte(`[
	  {"let":{"total":{"type":"int64","value":{"literal":{"type":"int64","value":"0"}}}}},
	  {"set":{"total":{"literal":{"type":"float64","value":"1.0"}}}},
	  {"return":{"var":"total"}}
	]`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{})
	if err == nil {
		t.Fatal("expected E_TYPE on set with different type")
	}
	var le *wflang.LangError
	if !errors.As(err, &le) || le.Code != werr.CodeType {
		t.Fatalf("got %v, want E_TYPE", err)
	}
}

// Typed let with the `@type` ergonomic form continues to work for legacy programs.
func TestTC232_TypedLetLegacyAtTypeForm(t *testing.T) {
	src := []byte(`[
	  {"let":{"@type":"int64","total":{"literal":{"type":"int64","value":"5"}}}},
	  {"return":{"var":"total"}}
	]`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := v.Go().(int64); got != 5 {
		t.Fatalf("got %d, want 5", got)
	}
}

// Typed let with a type/value mismatch at let-time is rejected immediately.
func TestTC232_TypedLetRejectsInitialMismatch(t *testing.T) {
	src := []byte(`[
	  {"let":{"total":{"type":"int64","value":{"literal":{"type":"string","value":"x"}}}}},
	  {"return":{"var":"total"}}
	]`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{})
	if err == nil {
		t.Fatal("expected E_TYPE for typed let with mismatched init")
	}
	var le *wflang.LangError
	if !errors.As(err, &le) || le.Code != werr.CodeType {
		t.Fatalf("got %v, want E_TYPE", err)
	}
}

// `@type` may not co-exist with multiple bindings — the spec form for
// destructuring is the per-binding wrapper. Reject the ambiguous mix.
func TestTC232_AtTypeWithMultiBindingIsRejected(t *testing.T) {
	src := []byte(`[
	  {"let":{
	    "@type":"int64",
	    "a":{"literal":{"type":"int64","value":"1"}},
	    "b":{"literal":{"type":"int64","value":"2"}}
	  }}
	]`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	_, err := eng.CompileJSON(src)
	if err == nil {
		t.Fatal("expected E_AST_SHAPE for @type with multi-binding")
	}
	if !strings.Contains(err.Error(), "@type") {
		t.Fatalf("err should reference @type: %v", err)
	}
}
