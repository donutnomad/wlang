package runtime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/donutnomad/wlang/compiler"
	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/types"
)

type symbolOutReserve struct {
	ID string
}

type symbolOutWorker struct {
	Prefix string
}

func (w symbolOutWorker) Join(s string) string {
	return w.Prefix + s
}

type symbolOutGateWorker struct{}

func (symbolOutGateWorker) Secret() string { return "secret" }

type symbolOutPointerWorker struct {
	Prefix string
}

func (w *symbolOutPointerWorker) Join(s string) string {
	return w.Prefix + s
}

func runSrcWithRegistry(t *testing.T, src string, reg *registry.Registry, scope *Scope) (types.Value, error) {
	t.Helper()
	prog, err := compiler.ParseProgram([]byte(src))
	if err != nil {
		return types.Value{}, err
	}
	if scope == nil {
		scope = NewScope()
	}
	return NewExecutor(context.Background(), scope, reg, reg.PackageNames(), Budget{}).RunProgram(prog)
}

func TestSymbolFunctionCallWritesOutToTypedZero(t *testing.T) {
	reg := registry.New()
	if err := reg.BindType("orders.ReserveResult", reflect.TypeOf(symbolOutReserve{}), registry.BindOptions{}); err != nil {
		t.Fatalf("BindType: %v", err)
	}
	if err := reg.BindSymbol("orders.FillReserve", func(id string, out *symbolOutReserve) error {
		out.ID = "reserve:" + id
		return nil
	}); err != nil {
		t.Fatalf("BindSymbol: %v", err)
	}

	src := `[
		{"let":{"reserve":{"zero":"orders.ReserveResult"}}},
		{"expr":{"call":{"fn":{"symbol":"orders.FillReserve"},"args":[
			{"literal":{"type":"string","value":"r1"}},
			{"out":"reserve"}
		]}}},
		{"return":{"var":"reserve.ID"}}
	]`
	got, err := runSrcWithRegistry(t, src, reg, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.TypeName() != types.TString || got.Go() != "reserve:r1" {
		t.Fatalf("want reserve:r1, got %s=%v", got.TypeName(), got.Go())
	}
}

func TestSymbolCanPassFunctionToHostAnyAndConcreteFunc(t *testing.T) {
	reg := registry.New()
	if err := reg.BindSymbol("demo.Upper", func(s string) string {
		return strings.ToUpper(s)
	}); err != nil {
		t.Fatalf("BindSymbol: %v", err)
	}
	if err := reg.BindGoPackage("sink", registry.PackageSpec{Functions: []registry.FuncSpec{
		{GoName: "Apply", Impl: func(fn func(string) string, s string) string { return fn(s) }},
		{GoName: "Kind", Impl: func(v any) string {
			if reflect.TypeOf(v).Kind() == reflect.Func {
				return "func"
			}
			return "other"
		}},
	}}); err != nil {
		t.Fatalf("BindGoPackage: %v", err)
	}

	src := `[
		{"let":{"kind":{"Kind":[{"pkg":"sink"},{"symbol":"demo.Upper"}]}}},
		{"let":{"value":{"Apply":[{"pkg":"sink"},{"symbol":"demo.Upper"},{"literal":{"type":"string","value":"go"}}]}}},
		{"return":{"+":[{"var":"kind"},{"var":"value"}]}}
	]`
	got, err := runSrcWithRegistry(t, src, reg, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Go() != "funcGO" {
		t.Fatalf("want funcGO, got %s=%v", got.TypeName(), got.Go())
	}
}

func TestMethodValueCanBePassedAndCalledDynamically(t *testing.T) {
	reg := registry.New()
	scope := NewScope()
	scope.Let("w", types.NewValue(types.AutoHostTypeName(reflect.TypeOf(symbolOutWorker{})), symbolOutWorker{Prefix: "hi:"}), "")

	src := `[
		{"let":{"fn":{"method":[{"var":"w"},"Join"]}}},
		{"return":{"call":{"fn":{"var":"fn"},"args":[
			{"literal":{"type":"string","value":"there"}}
		]}}}
	]`
	got, err := runSrcWithRegistry(t, src, reg, scope)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.TypeName() != types.TString || got.Go() != "hi:there" {
		t.Fatalf("want hi:there, got %s=%v", got.TypeName(), got.Go())
	}
}

func TestMethodValueHonorsRegistryBindingRules(t *testing.T) {
	t.Run("excluded method", func(t *testing.T) {
		reg := registry.New()
		if err := reg.BindType("workers.Gate", reflect.TypeOf(symbolOutGateWorker{}), registry.BindOptions{
			Exclude: []string{"Secret"},
		}); err != nil {
			t.Fatalf("BindType: %v", err)
		}
		scope := NewScope()
		scope.Let("w", types.NewValue("workers.Gate", symbolOutGateWorker{}), "")
		src := `[
			{"let":{"fn":{"method":[{"var":"w"},"Secret"]}}},
			{"return":{"call":{"fn":{"var":"fn"},"args":[]}}}
		]`
		_, err := runSrcWithRegistry(t, src, reg, scope)
		assertLangCode(t, err, werr.CodeSymbol)
	})

	t.Run("capability required", func(t *testing.T) {
		reg := registry.New()
		if err := reg.BindType("workers.Gate", reflect.TypeOf(symbolOutGateWorker{}), registry.BindOptions{
			MethodOverrides: map[string]registry.MethodOptions{
				"Secret": {Capabilities: []string{"secret:read"}},
			},
		}); err != nil {
			t.Fatalf("BindType: %v", err)
		}
		scope := NewScope()
		scope.Let("w", types.NewValue("workers.Gate", symbolOutGateWorker{}), "")
		src := `[
			{"let":{"fn":{"method":[{"var":"w"},"Secret"]}}},
			{"return":{"call":{"fn":{"var":"fn"},"args":[]}}}
		]`
		_, err := runSrcWithRegistry(t, src, reg, scope)
		assertLangCode(t, err, werr.CodeCapability)
	})
}

func TestMethodValueSupportsPointerReceiverOnAddressableVariable(t *testing.T) {
	reg := registry.New()
	if err := reg.BindType("workers.Pointer", reflect.TypeOf((*symbolOutPointerWorker)(nil)), registry.BindOptions{}); err != nil {
		t.Fatalf("BindType: %v", err)
	}
	scope := NewScope()
	scope.Let("w", types.NewValue("workers.Pointer", symbolOutPointerWorker{Prefix: "ptr:"}), "")
	src := `[
		{"let":{"fn":{"method":[{"var":"w"},"Join"]}}},
		{"return":{"call":{"fn":{"var":"fn"},"args":[
			{"literal":{"type":"string","value":"ok"}}
		]}}}
	]`
	got, err := runSrcWithRegistry(t, src, reg, scope)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.TypeName() != types.TString || got.Go() != "ptr:ok" {
		t.Fatalf("want ptr:ok, got %s=%v", got.TypeName(), got.Go())
	}
}

func TestOutArgumentErrorCases(t *testing.T) {
	reg := registry.New()
	if err := reg.BindType("orders.ReserveResult", reflect.TypeOf(symbolOutReserve{}), registry.BindOptions{}); err != nil {
		t.Fatalf("BindType: %v", err)
	}
	if err := reg.BindSymbol("orders.FillReserve", func(out *symbolOutReserve) error {
		out.ID = "written"
		return nil
	}); err != nil {
		t.Fatalf("BindSymbol FillReserve: %v", err)
	}
	if err := reg.BindSymbol("orders.FillAny", func(out any) error {
		out.(*symbolOutReserve).ID = "iface"
		return nil
	}); err != nil {
		t.Fatalf("BindSymbol FillAny: %v", err)
	}
	if err := reg.BindSymbol("orders.ReturnError", func(out *symbolOutReserve) error {
		out.ID = "before-error"
		return errors.New("boom")
	}); err != nil {
		t.Fatalf("BindSymbol ReturnError: %v", err)
	}
	calls := 0
	if err := reg.BindSymbol("orders.SideEffectThenFill", func(out *symbolOutReserve) error {
		calls++
		out.ID = "after-side-effect"
		return nil
	}); err != nil {
		t.Fatalf("BindSymbol SideEffectThenFill: %v", err)
	}
	if err := reg.BindSymbol("orders.CheckSameOut", func(a, b *symbolOutReserve) string {
		if a == b {
			a.ID = "same"
			return "same"
		}
		a.ID = "left"
		b.ID = "right"
		return "different"
	}); err != nil {
		t.Fatalf("BindSymbol CheckSameOut: %v", err)
	}

	t.Run("interface parameter receives typed pointer and writes back", func(t *testing.T) {
		src := `[
			{"let":{"reserve":{"zero":"orders.ReserveResult"}}},
			{"expr":{"call":{"fn":{"symbol":"orders.FillAny"},"args":[{"out":"reserve"}]}}},
			{"return":{"var":"reserve.ID"}}
		]`
		got, err := runSrcWithRegistry(t, src, reg, nil)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if got.Go() != "iface" {
			t.Fatalf("want iface, got %s=%v", got.TypeName(), got.Go())
		}
	})

	t.Run("return error preserves prior out write", func(t *testing.T) {
		src := `[
			{"let":{"reserve":{"zero":"orders.ReserveResult"}}},
			{"let":{"err":{"call":{"fn":{"symbol":"orders.ReturnError"},"args":[{"out":"reserve"}]}}}},
			{"return":{"var":"reserve.ID"}}
		]`
		got, err := runSrcWithRegistry(t, src, reg, nil)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if got.Go() != "before-error" {
			t.Fatalf("want before-error, got %s=%v", got.TypeName(), got.Go())
		}
	})

	t.Run("missing variable", func(t *testing.T) {
		src := `[{"return":{"call":{"fn":{"symbol":"orders.FillReserve"},"args":[{"out":"missing"}]}}}]`
		_, err := runSrcWithRegistry(t, src, reg, nil)
		assertLangCode(t, err, werr.CodeSymbol)
	})

	t.Run("read only variable", func(t *testing.T) {
		scope := NewScope()
		scope.LetReadOnly("reserve", types.NewValue("orders.ReserveResult", symbolOutReserve{}))
		src := `[{"return":{"call":{"fn":{"symbol":"orders.FillReserve"},"args":[{"out":"reserve"}]}}}]`
		_, err := runSrcWithRegistry(t, src, reg, scope)
		assertLangCode(t, err, werr.CodeReadonlyVar)
	})

	t.Run("read only variable blocks host call", func(t *testing.T) {
		calls = 0
		scope := NewScope()
		scope.LetReadOnly("reserve", types.NewValue("orders.ReserveResult", symbolOutReserve{}))
		src := `[{"return":{"call":{"fn":{"symbol":"orders.SideEffectThenFill"},"args":[{"out":"reserve"}]}}}]`
		_, err := runSrcWithRegistry(t, src, reg, scope)
		assertLangCode(t, err, werr.CodeReadonlyVar)
		if calls != 0 {
			t.Fatalf("host call count = %d, want 0", calls)
		}
	})

	t.Run("same variable keeps pointer identity", func(t *testing.T) {
		src := `[
			{"let":{"reserve":{"zero":"orders.ReserveResult"}}},
			{"let":{"same":{"call":{"fn":{"symbol":"orders.CheckSameOut"},"args":[{"out":"reserve"},{"out":"reserve"}]}}}},
			{"return":{"var":"same"}}
		]`
		got, err := runSrcWithRegistry(t, src, reg, nil)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if got.TypeName() != types.TString || got.Go() != "same" {
			t.Fatalf("want same, got %s=%v", got.TypeName(), got.Go())
		}
	})

	t.Run("type mismatch", func(t *testing.T) {
		src := `[
			{"let":{"reserve":{"literal":{"type":"string","value":"wrong"}}}},
			{"return":{"call":{"fn":{"symbol":"orders.FillReserve"},"args":[{"out":"reserve"}]}}}
		]`
		_, err := runSrcWithRegistry(t, src, reg, nil)
		assertLangCode(t, err, werr.CodeType)
	})
}
