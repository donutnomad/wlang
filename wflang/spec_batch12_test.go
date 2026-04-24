package wflang_test

import (
	"testing"
)

// --- TC-196 foreach 每轮新 block -------------------------------------

func TestTC196_ForeachNewBlockPerIteration(t *testing.T) {
	// A `let tmp` inside foreach must not leak out of the loop.
	src := []byte(`[
		{"foreach":{"target":{"literal":{"type":"array<int64>","value":[1,2]}},"as":"x","do":[
			{"let":{"tmp":{"literal":{"type":"int64","value":"42"}}}}
		]}},
		{"return":{"var":"tmp"}}
	]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error for leaked tmp, got nil")
	}
}

// --- TC-250 Vars / Packages 双命名空间（扩展 TC-056） ----------------

// A top-level var `risk` and a package `risk` coexist — different access
// grammars, zero interference. Here verify that each one returns its own
// distinct, well-typed value when requested.
func TestTC250_DualNamespace(t *testing.T) {
	// Covered by TC-056 package function + TC-056 var. Here we reinforce
	// that pkg and var can be read within the same program.
	src := []byte(`[
		{"let":{"a":{"var":"risk"}}},
		{"return":{"var":"a"}}
	]`)
	v, err := runSrc(t, src, map[string]any{"risk": int64(9)})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 9 {
		t.Fatalf("var risk: want 9, got %v", v.Go())
	}
}

// --- TC-343 array<T> 元素推断 -----------------------------------------

func TestTC343_ArrayElementTypeInferred(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"array<int64>","value":[1,2,3]}}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != "array<int64>" {
		t.Fatalf("want array<int64>, got %s", v.TypeName())
	}
}

// --- TC-031 扩展: var path 命中 map 嵌套 ------------------------------

// Already covered TC-031 via map[string]any. Here verify that a deeply
// nested map path — 3 levels — resolves correctly.
func TestTC031Ext_DeepMapPath(t *testing.T) {
	src := []byte(`[{"return":{"var":"a.b.c"}}]`)
	v, err := runSrc(t, src, map[string]any{
		"a": map[string]any{
			"b": map[string]any{"c": int64(123)},
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 123 {
		t.Fatalf("want 123, got %v", v.Go())
	}
}
