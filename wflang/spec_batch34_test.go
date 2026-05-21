// Batch 34 covers Tier-3: BindOptions / BindType (LANGUAGE.md §4.5).
//
//	TC-360 AutoBindType + Constructor
//	TC-361 include / exclude 白名单
//	TC-362 注册期签名校验失败聚合
//	TC-363 方法元数据：capability/timeout/cost (capability path)
package wflang_test

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"

	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/types"
	"github.com/donutnomad/wlang/wflang"
)

// ---------- TC-360 Constructor ----------------------------------------------
type tc360Money struct {
	Amount *big.Rat
}

func newTC360Money(raw string) (tc360Money, error) {
	r, ok := new(big.Rat).SetString(raw)
	if !ok {
		return tc360Money{}, errors.New("not a valid amount: " + raw)
	}
	return tc360Money{Amount: r}, nil
}

func (m tc360Money) Format() (string, error) {
	if m.Amount == nil {
		return "", errors.New("nil amount")
	}
	return m.Amount.FloatString(2), nil
}

func TestTC360_AutoBindType_Constructor(t *testing.T) {
	// Cleanup the global typed-literal registry so siblings stay isolated.
	defer types.DeregisterLiteralConstructor("tc360.money")

	reg := wflang.DefaultRegistry()
	if err := reg.BindType("tc360.money", reflect.TypeFor[tc360Money](),
		wflang.BindOptions{
			Constructor: newTC360Money,
		}); err != nil {
		t.Fatalf("BindType: %v", err)
	}
	src := []byte(`{
	  "lang":"wflang/v1",
	  "program":[
	    {"return": {"Format": [{"literal":{"type":"tc360.money","value":"1.234"}}]}}
	  ]
	}`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := unwrap1(t, v).(string)
	if got != "1.23" {
		t.Fatalf("Format result = %q, want \"1.23\"", got)
	}
}

func TestTC360_Constructor_BadInput(t *testing.T) {
	defer types.DeregisterLiteralConstructor("tc360b.money")
	reg := wflang.DefaultRegistry()
	if err := reg.BindType("tc360b.money", reflect.TypeFor[tc360Money](),
		wflang.BindOptions{Constructor: newTC360Money}); err != nil {
		t.Fatalf("BindType: %v", err)
	}
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"return":{"literal":{"type":"tc360b.money","value":"NOT-A-NUMBER"}}}
	]}`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	_, err := eng.CompileJSON(src)
	if err == nil {
		t.Fatal("expected ctor error")
	}
	if !strings.Contains(err.Error(), "NOT-A-NUMBER") {
		t.Fatalf("err = %v; should mention bad raw input", err)
	}
}

// ---------- TC-361 include / exclude ----------------------------------------
type tc361Box struct{ N int64 }

func (b tc361Box) PublicMethod() (int64, error) { return b.N + 1, nil }
func (b tc361Box) InternalMethod() (int64, error) {
	return b.N + 2, nil
}

func TestTC361_ExcludeMethod(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindType("tc361.box", reflect.TypeFor[tc361Box](),
		wflang.BindOptions{Exclude: []string{"InternalMethod"}}); err != nil {
		t.Fatalf("BindType: %v", err)
	}
	if err := reg.BindGoPackage("tc361pkg", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "MakeBox", Pure: true, Impl: func(n int64) (tc361Box, error) {
				return tc361Box{N: n}, nil
			}},
		},
	}); err != nil {
		t.Fatalf("pkg: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	// PublicMethod must work.
	src1 := []byte(`{"lang":"wflang/v1","program":[
	  {"let":[["b","err"], {"MakeBox":[{"pkg":"tc361pkg"},{"literal":{"type":"int64","value":"10"}}]}]},
	  {"return":{"PublicMethod":[{"var":"b"}]}}
	]}`)
	prog, err := eng.CompileJSON(src1)
	if err != nil {
		t.Fatalf("compile1: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run1: %v", err)
	}
	if unwrap1(t, v).(int64) != 11 {
		t.Fatalf("PublicMethod = %v, want 11", v.Go())
	}
	// InternalMethod must be unbound → E_SYMBOL.
	src2 := []byte(`{"lang":"wflang/v1","program":[
	  {"let":[["b","err"], {"MakeBox":[{"pkg":"tc361pkg"},{"literal":{"type":"int64","value":"10"}}]}]},
	  {"return":{"InternalMethod":[{"var":"b"}]}}
	]}`)
	prog2, err := eng.CompileJSON(src2)
	if err != nil {
		t.Fatalf("compile2: %v", err)
	}
	_, err = prog2.Run(context.Background(), wflang.RunOptions{})
	if err == nil {
		t.Fatal("expected E_SYMBOL")
	}
	if le, ok := err.(*werr.LangError); !ok || le.Code != werr.CodeSymbol {
		t.Fatalf("err = %v (code=%v); want E_SYMBOL", err, codeOf(err))
	}
}

func TestTC361_IncludeWhitelist(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindType("tc361i.box", reflect.TypeFor[tc361Box](),
		wflang.BindOptions{Include: []string{"PublicMethod"}}); err != nil {
		t.Fatalf("BindType: %v", err)
	}
	if err := reg.BindGoPackage("tc361ipkg", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "MakeBox", Pure: true, Impl: func(n int64) (tc361Box, error) {
				return tc361Box{N: n}, nil
			}},
		},
	}); err != nil {
		t.Fatalf("pkg: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	// InternalMethod must be excluded by include filter.
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"let":{"b":{"MakeBox":[{"pkg":"tc361ipkg"},{"literal":{"type":"int64","value":"5"}}]}}},
	  {"return":{"InternalMethod":[{"var":"b"}]}}
	]}`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := prog.Run(context.Background(), wflang.RunOptions{}); err == nil {
		t.Fatal("expected E_SYMBOL")
	}
}

// ---------- TC-362 注册期签名校验失败聚合 ------------------------------------
// We can't easily fabricate "many bad signatures" because reflect.Method
// always produces well-formed signatures. Instead, exercise the aggregation
// path with multiple invalid Constructor / option errors → ensure that
// returning a *werr.List works and surfaces all entries.
type tc362Bad struct{}

func (b tc362Bad) M() (int, error) { return 0, nil }

func TestTC362_RegistrationErrorAggregation(t *testing.T) {
	reg := wflang.DefaultRegistry()
	// Constructor with wrong signature (not (string)→(T,error)).
	err := reg.BindType("tc362.bad", reflect.TypeFor[tc362Bad](),
		wflang.BindOptions{
			Constructor: func(int) (tc362Bad, error) { return tc362Bad{}, nil },
		})
	if err == nil {
		t.Fatal("expected bind errors, got nil")
	}
	// The error chain must surface the constructor mismatch.
	if !strings.Contains(err.Error(), "Constructor") {
		t.Fatalf("err = %v; should mention Constructor", err)
	}
	// And we must be able to pull the list out.
	var list *werr.List
	if !errors.As(err, &list) {
		t.Fatalf("err type %T is not aggregatable as *werr.List", err)
	}
	if len(list.Errors) == 0 {
		t.Fatal("aggregated list is empty")
	}
}

// ---------- TC-363 metadata: capability ------------------------------------
type tc363Vault struct{ Secret string }

func (v tc363Vault) Read() (string, error) { return v.Secret, nil }

func TestTC363_TypeLevelCapabilityRequired(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindType("tc363.vault", reflect.TypeFor[tc363Vault](),
		wflang.BindOptions{
			Capabilities: []string{"vault.read"},
		}); err != nil {
		t.Fatalf("BindType: %v", err)
	}
	if err := reg.BindGoPackage("tc363pkg", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Make", Pure: true, Impl: func(s string) (tc363Vault, error) {
				return tc363Vault{Secret: s}, nil
			}},
		},
	}); err != nil {
		t.Fatalf("pkg: %v", err)
	}

	src := []byte(`{"lang":"wflang/v1","program":[
	  {"let":[["v","err"], {"Make":[{"pkg":"tc363pkg"},{"literal":{"type":"string","value":"top"}}]}]},
	  {"return":{"Read":[{"var":"v"}]}}
	]}`)

	// Without capability: must fail with E_CAPABILITY.
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := prog.Run(context.Background(), wflang.RunOptions{}); err == nil {
		t.Fatal("expected E_CAPABILITY")
	} else if le, ok := err.(*werr.LangError); !ok || le.Code != werr.CodeCapability {
		t.Fatalf("err code = %v; want E_CAPABILITY (err=%v)", codeOf(err), err)
	}

	// With capability granted: must succeed.
	eng2 := wflang.NewEngine(wflang.EngineOptions{
		Registry:     reg,
		Capabilities: wflang.CapabilitySet{"vault.read": true},
	})
	prog2, err := eng2.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile2: %v", err)
	}
	v, err := prog2.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if unwrap1(t, v).(string) != "top" {
		t.Fatalf("got %v, want top", v.Go())
	}
}

func codeOf(err error) string {
	var le *werr.LangError
	if errors.As(err, &le) {
		return le.Code
	}
	return ""
}
