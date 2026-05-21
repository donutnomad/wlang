// Batch 33 covers Tier-2G: match expression (LANGUAGE.md §14.2).
//
//	TC-905 match expression with hit / default / type-merge.
package wflang_test

import (
	"context"
	"strings"
	"testing"

	"github.com/donutnomad/wlang/wflang"
)

// ---------- TC-905 match 表达式 ----------------------------------------------
func TestTC905_Match_HitBranch(t *testing.T) {
	src := []byte(`{
	  "lang":"wflang/v1",
	  "program":[
	    {"return": {"match": {
	       "value": {"literal":{"type":"int64","value":"2"}},
	       "cases": [
	         {"when": {"literal":{"type":"int64","value":"1"}},
	          "do":   [{"return": {"literal":{"type":"string","value":"one"}}}]},
	         {"when": {"literal":{"type":"int64","value":"2"}},
	          "do":   [{"return": {"literal":{"type":"string","value":"two"}}}]},
	         {"when": {"literal":{"type":"int64","value":"3"}},
	          "do":   [{"return": {"literal":{"type":"string","value":"three"}}}]}
	       ],
	       "default": [{"return": {"literal":{"type":"string","value":"other"}}}]
	    }}}
	  ]
	}`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := v.Go().(string); got != "two" {
		t.Fatalf("match result = %q, want \"two\"", got)
	}
	if v.TypeName() != "string" {
		t.Fatalf("type = %s, want string", v.TypeName())
	}
}

func TestTC905_Match_DefaultBranch(t *testing.T) {
	src := []byte(`{
	  "lang":"wflang/v1",
	  "program":[
	    {"return": {"match": {
	       "value": {"literal":{"type":"int64","value":"99"}},
	       "cases": [
	         {"when": {"literal":{"type":"int64","value":"1"}},
	          "do":   [{"return": {"literal":{"type":"string","value":"one"}}}]}
	       ],
	       "default": [{"return": {"literal":{"type":"string","value":"fallback"}}}]
	    }}}
	  ]
	}`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := v.Go().(string); got != "fallback" {
		t.Fatalf("match default = %q, want \"fallback\"", got)
	}
}

func TestTC905_Match_NoCaseNoDefault_Null(t *testing.T) {
	// No case matches, no default → null.
	src := []byte(`{
	  "lang":"wflang/v1",
	  "program":[
	    {"return": {"match": {
	       "value": {"literal":{"type":"int64","value":"9"}},
	       "cases": [
	         {"when": {"literal":{"type":"int64","value":"1"}},
	          "do":   [{"return": {"literal":{"type":"string","value":"one"}}}]}
	       ]
	    }}}
	  ]
	}`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !v.IsNull() {
		t.Fatalf("expected null, got %s = %v", v.TypeName(), v.Go())
	}
}

func TestTC905_Match_StringScrutinee(t *testing.T) {
	src := []byte(`{
	  "lang":"wflang/v1",
	  "program":[
	    {"return": {"match": {
	       "value": {"literal":{"type":"string","value":"green"}},
	       "cases": [
	         {"when": {"literal":{"type":"string","value":"red"}},
	          "do":   [{"return": {"literal":{"type":"int64","value":"1"}}}]},
	         {"when": {"literal":{"type":"string","value":"green"}},
	          "do":   [{"return": {"literal":{"type":"int64","value":"2"}}}]}
	       ],
	       "default": [{"return": {"literal":{"type":"int64","value":"0"}}}]
	    }}}
	  ]
	}`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 2 {
		t.Fatalf("got %v, want 2", v.Go())
	}
}

func TestTC905_Match_RequiresValue(t *testing.T) {
	// Missing `value` field rejected at parse time.
	src := []byte(`{"lang":"wflang/v1","program":[{"return": {"match": {"cases":[]}}}]}`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	_, err := eng.CompileJSON(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "value") {
		t.Fatalf("err = %v; should mention `value`", err)
	}
}

func TestTC905_Match_RequiresCases(t *testing.T) {
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"return": {"match": {
	    "value": {"literal":{"type":"int64","value":"1"}},
	    "cases": []
	  }}}]}`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	_, err := eng.CompileJSON(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "cases") {
		t.Fatalf("err = %v; should mention `cases`", err)
	}
}

// Match in let-position (statement form) returns null and binds the value.
func TestTC905_Match_AsStatementValue(t *testing.T) {
	src := []byte(`{
	  "lang":"wflang/v1",
	  "program":[
	    {"let": {"k": {"literal":{"type":"int64","value":"3"}}}},
	    {"let": {"label": {"match": {
	       "value": {"var":"k"},
	       "cases": [
	         {"when": {"literal":{"type":"int64","value":"1"}},
	          "do":   [{"return": {"literal":{"type":"string","value":"a"}}}]},
	         {"when": {"literal":{"type":"int64","value":"3"}},
	          "do":   [{"return": {"literal":{"type":"string","value":"c"}}}]}
	       ]
	    }}}},
	    {"return": {"var":"label"}}
	  ]
	}`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "c" {
		t.Fatalf("got %v, want c", v.Go())
	}
	_ = context.Background()
}
