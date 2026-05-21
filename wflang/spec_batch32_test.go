// Batch 32 covers Tier-2A: Session lang/imports envelope semantics
// (LANGUAGE.md §3.1):
//
//	TC-158 lang version conflict        → E_LANG_VERSION_CONFLICT
//	TC-159 imports take union           → session.Imports() is union of
//	                                      all envelope imports + session
//	                                      seed.
package wflang_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/wflang"
)

// ---------- TC-158 lang 版本冲突 ---------------------------------------------
func TestTC158_LangVersionConflict(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{}) // defaults to wflang/v1
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if got := sess.Lang(); got != "wflang/v1" {
		t.Fatalf("default lang = %q, want wflang/v1", got)
	}
	// Same-lang envelope must succeed.
	body1 := []byte(`{"lang":"wflang/v1","program":[{"let":{"x":{"literal":{"type":"int64","value":"1"}}}}]}`)
	if _, err := sess.AppendRun(context.Background(), body1); err != nil {
		t.Fatalf("v1 envelope: %v", err)
	}
	// Conflicting envelope must fail with E_LANG_VERSION_CONFLICT.
	body2 := []byte(`{"lang":"wflang/v2","program":[{"return":{"var":"x"}}]}`)
	_, err = sess.AppendRun(context.Background(), body2)
	if err == nil {
		t.Fatal("expected E_LANG_VERSION_CONFLICT error, got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok {
		t.Fatalf("err type = %T, want *werr.LangError", err)
	}
	if le.Code != werr.CodeLangVersionConflct {
		t.Fatalf("code = %s, want %s", le.Code, werr.CodeLangVersionConflct)
	}
}

// ---------- TC-159 imports 取并集 --------------------------------------------
func TestTC159_ImportsUnion(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{
		Imports: []string{"std/math"},
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	// First fragment imports [std/math, std/str]; std/math is dup → ignored.
	body1 := []byte(`{"lang":"wflang/v1","imports":["std/math","std/str"],` +
		`"program":[{"let":{"x":{"literal":{"type":"int64","value":"1"}}}}]}`)
	if _, err := sess.AppendRun(context.Background(), body1); err != nil {
		t.Fatalf("frag1: %v", err)
	}
	// Second fragment imports [std/json].
	body2 := []byte(`{"lang":"wflang/v1","imports":["std/json"],` +
		`"program":[{"let":{"y":{"literal":{"type":"int64","value":"2"}}}}]}`)
	if _, err := sess.AppendRun(context.Background(), body2); err != nil {
		t.Fatalf("frag2: %v", err)
	}
	want := []string{"std/json", "std/math", "std/str"}
	got := sess.Imports()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("imports = %v, want %v", got, want)
	}
}

// Default lang seed: SessionOptions.Lang is honored when set explicitly.
func TestTC158_LangCustomDefault(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{Lang: "wflang/v3"})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if got := sess.Lang(); got != "wflang/v3" {
		t.Fatalf("lang = %q, want wflang/v3", got)
	}
	// Plain statement (no envelope) does not trip lang validation.
	if _, err := sess.AppendRun(context.Background(),
		[]byte(`{"let":{"x":{"literal":{"type":"int64","value":"1"}}}}`)); err != nil {
		t.Fatalf("plain stmt: %v", err)
	}
	// Wrong-lang envelope fails.
	_, err = sess.AppendRun(context.Background(),
		[]byte(`{"lang":"wflang/v1","program":[{"return":{"var":"x"}}]}`))
	if err == nil {
		t.Fatal("expected lang conflict")
	}
	if !strings.Contains(err.Error(), "wflang/v3") || !strings.Contains(err.Error(), "wflang/v1") {
		t.Fatalf("err = %v; should mention both versions", err)
	}
}
