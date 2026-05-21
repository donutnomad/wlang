// Batch 40 covers Tier-2C: Strict 模式 + 多错误聚合
// (LANGUAGE.md §5.2 / §9.4 / §15.2).
//
//	TC-401 Strict 模式拒绝宽松输入
//	TC-732 多错误聚合 — 一次性返回多 LangError 集合
package wflang_test

import (
	"errors"
	"reflect"
	"testing"

	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/wflang"
)

// ---------- TC-401 Strict 模式拒绝宽松输入 ----------------------------------
// 裸 JSON primitive 在任何模式下均非法 (LANGUAGE.md §3.3 / TC-100)，但 Strict
// 模式还会在 §7.5 的静态检查里启用更严格的多错误聚合 (TC-732)。本用例验证
// Strict=true 下裸 JSON 数字仍被诊断，并保留语言契约要求的 E_AST_SHAPE 码。
func TestTC401_StrictRejectsBareJSONLiteral(t *testing.T) {
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"return":{"+":[
	     {"literal":{"type":"int64","value":"1"}},
	     2
	  ]}}
	]}`)
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry: wflang.DefaultRegistry(),
		Strict:   true,
	})
	_, err := eng.CompileJSON(src)
	if err == nil {
		t.Fatal("expected E_AST_SHAPE for bare JSON literal under Strict")
	}
	var le *werr.LangError
	if !errors.As(err, &le) {
		t.Fatalf("err = %v (%T); want unwrappable to *werr.LangError", err, err)
	}
	if le.Code != werr.CodeASTShape {
		t.Fatalf("code = %s, want %s", le.Code, werr.CodeASTShape)
	}
}

// Sanity: non-strict mode also rejects bare primitives — this is a language-
// level invariant (TC-100), not a Strict-only check. Documented here so that
// future relaxations are caught.
func TestTC401_NonStrictAlsoRejectsBareJSON(t *testing.T) {
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"return":{"+":[{"literal":{"type":"int64","value":"1"}},2]}}
	]}`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	if _, err := eng.CompileJSON(src); err == nil {
		t.Fatal("non-strict must also reject bare primitives")
	}
}

// ---------- TC-732 多错误聚合 ------------------------------------------------
// 多个静态可证伪的违规 (TC-644 受 nil literal 接收者) 在 Strict=true 下应一次
// 性返回，由 *werr.List 承载，而不是早返回第一条。
type tc732Box struct{ N int64 }

func (b tc732Box) Bump() (int64, error) { return b.N + 1, nil }

func TestTC732_StrictAggregatesMultipleNullReceivers(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindType("tc732.box", reflect.TypeFor[tc732Box](),
		wflang.BindOptions{}); err != nil {
		t.Fatalf("BindType: %v", err)
	}
	// Three sibling expressions each with a static null receiver. Strict=true
	// turns on TypeCheckOpts(Aggregate=true), so all three must surface in a
	// single *werr.List instead of bailing out at the first.
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"let":{"a":{"Bump":[{"literal":{"type":"null","value":null}}]}}},
	  {"let":{"b":{"Bump":[{"literal":{"type":"null","value":null}}]}}},
	  {"return":{"Bump":[{"literal":{"type":"null","value":null}}]}}
	]}`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg, Strict: true})
	_, err := eng.CompileJSON(src)
	if err == nil {
		t.Fatal("expected aggregated diagnostics from Strict CompileJSON")
	}
	var list *werr.List
	if !errors.As(err, &list) {
		t.Fatalf("err type %T is not *werr.List; multi-error aggregation broken", err)
	}
	if got := len(list.Errors); got < 3 {
		t.Fatalf("aggregated %d errors, want ≥3 (one per static null receiver)", got)
	}
	for i, e := range list.Errors {
		if e.Code != werr.CodeNilReceiver {
			t.Fatalf("list[%d].Code = %s, want %s", i, e.Code, werr.CodeNilReceiver)
		}
	}
}

// In non-strict mode the type checker bails out early — exactly one diagnostic
// surfaces, wrapped as a plain *LangError (not a list).
func TestTC732_NonStrictReturnsFirstErrorOnly(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindType("tc732.box2", reflect.TypeFor[tc732Box](),
		wflang.BindOptions{}); err != nil {
		t.Fatalf("BindType: %v", err)
	}
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"let":{"a":{"Bump":[{"literal":{"type":"null","value":null}}]}}},
	  {"return":{"Bump":[{"literal":{"type":"null","value":null}}]}}
	]}`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	_, err := eng.CompileJSON(src)
	if err == nil {
		t.Fatal("expected single LangError")
	}
	var list *werr.List
	if errors.As(err, &list) {
		t.Fatalf("non-strict should not return *werr.List, got %d entries", len(list.Errors))
	}
	var le *werr.LangError
	if !errors.As(err, &le) || le.Code != werr.CodeNilReceiver {
		t.Fatalf("err = %v, want single E_NIL_RECEIVER", err)
	}
}
