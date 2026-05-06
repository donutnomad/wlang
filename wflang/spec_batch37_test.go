// Batch 37 covers Tier-2F: 编译期类型检查 (LANGUAGE.md §7.4 / §7.5).
//
//	TC-644 receiver = null literal → E_NIL_RECEIVER (compile-time)
//	TC-648 string → bigDecimal via registered constructor (overload coercion)
//	TC-652 explicit `Add@int64` overload selection is not accepted by the language
//	TC-653 any-typed var argument under overloaded operator → E_AMBIGUOUS_OVERLOAD
package wflang_test

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"testing"

	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/registry"
	"github.com/wflang/wflang/types"
	"github.com/wflang/wflang/wflang"
)

// ---------- TC-644 -----------------------------------------------------------
type tc644Box struct{ N int64 }

func (b tc644Box) Bump() (int64, error) { return b.N + 1, nil }

func TestTC644_StaticNullReceiverIsRejectedAtCompileTime(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindType("tc644.box", reflect.TypeFor[tc644Box](),
		wflang.BindOptions{}); err != nil {
		t.Fatalf("BindType: %v", err)
	}
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"return":{"Bump":[{"literal":{"type":"null","value":null}}]}}
	]}`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	_, err := eng.CompileJSON(src)
	if err == nil {
		t.Fatal("expected E_NIL_RECEIVER from compile-time check")
	}
	var le *werr.LangError
	if !errors.As(err, &le) || le.Code != werr.CodeNilReceiver {
		t.Fatalf("err = %v (want E_NIL_RECEIVER)", err)
	}
}

// ---------- TC-648 -----------------------------------------------------------
// Two overload candidates; the bigDecimal one accepts a string via registered
// constructor (TC-648 / TC-360). The picker should hit it.
type tc648Calc struct{}

func (c tc648Calc) Add(a, b *big.Rat) (*big.Rat, error) {
	return new(big.Rat).Add(a, b), nil
}

func newTC648Money(raw string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(raw)
	if !ok {
		return nil, errors.New("bad: " + raw)
	}
	return r, nil
}

func TestTC648_ConstructorCoercionInOverload(t *testing.T) {
	defer types.DeregisterLiteralConstructor("tc648.dec")
	reg := wflang.DefaultRegistry()
	if err := reg.BindType("tc648.dec", reflect.TypeFor[*big.Rat](),
		wflang.BindOptions{Constructor: newTC648Money}); err != nil {
		t.Fatalf("BindType: %v", err)
	}
	if err := reg.BindGoPackage("tc648pkg", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Add", Pure: true, Impl: func(a, b *big.Rat) (*big.Rat, error) {
				return new(big.Rat).Add(a, b), nil
			}},
		},
	}); err != nil {
		t.Fatalf("pkg: %v", err)
	}
	// Pass two *string* literals; the picker must construct *big.Rat for both.
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"return":{"Add":[
	     {"pkg":"tc648pkg"},
	     {"literal":{"type":"string","value":"1.5"}},
	     {"literal":{"type":"string","value":"2.25"}}
	  ]}}
	]}`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got, ok := v.Go().(*big.Rat)
	if !ok || got == nil {
		t.Fatalf("got %T (%v), want *big.Rat", v.Go(), v.Go())
	}
	if got.FloatString(2) != "3.75" {
		t.Fatalf("got %s, want 3.75", got.FloatString(2))
	}
}

// ---------- TC-652 -----------------------------------------------------------
// `Add@int64` is *not* legal language syntax — the operator name must come
// from the registry, never with an inline overload selector. The compiler
// must therefore reject the call as an unknown symbol.
func TestTC652_ExplicitOverloadSelectorIsNotAccepted(t *testing.T) {
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"return":{"Add@int64":[
	     {"literal":{"type":"int64","value":"1"}},
	     {"literal":{"type":"int64","value":"2"}}
	  ]}}
	]}`)
	eng := wflang.NewEngine(wflang.EngineOptions{})
	prog, err := eng.CompileJSON(src)
	if err != nil {
		// Compile-time rejection is acceptable — the language declined the form.
		return
	}
	if _, err := prog.Run(context.Background(), wflang.RunOptions{}); err == nil {
		t.Fatal("expected runtime rejection of Add@int64 form")
	}
}

// ---------- TC-653 -----------------------------------------------------------
type tc653Box struct{ N int64 }

func (b tc653Box) Mul(x int64) (int64, error)      { return b.N * x, nil }
func (b tc653Box) Mul2(x float64) (float64, error) { return float64(b.N) * x, nil }

func TestTC653_AnyTypedVarTriggersAmbiguousOverload(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindType("tc653.box", reflect.TypeFor[tc653Box](),
		wflang.BindOptions{}); err != nil {
		t.Fatalf("BindType: %v", err)
	}
	if err := reg.BindMethodOverloads(
		"tc653.box", "Scale",
		[]wflang.GoMethodOverload{{GoMethod: "Mul"}, {GoMethod: "Mul2"}},
	); err != nil {
		t.Fatalf("BindMethodOverloads: %v", err)
	}
	// `let x: any = 1`; calling Scale(box, x) — x is statically any, multiple
	// candidates exist for Scale → compiler must reject as ambiguous.
	src := []byte(`{"lang":"wflang/v1","program":[
	  {"let":{"x":{"literal":{"type":"int64","value":"3"}},"@type":"any"}},
	  {"return":{"Scale":[
	     {"var":"box"},
	     {"var":"x"}
	  ]}}
	]}`)
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	_, err := eng.CompileJSON(src)
	if err == nil {
		t.Fatal("expected E_AMBIGUOUS_OVERLOAD at compile time")
	}
	var le *werr.LangError
	if !errors.As(err, &le) || le.Code != werr.CodeAmbiguousOverload {
		t.Fatalf("err = %v (want E_AMBIGUOUS_OVERLOAD)", err)
	}
}
