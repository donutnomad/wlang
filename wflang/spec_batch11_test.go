package wflang_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wflang/wflang/registry"
	"github.com/wflang/wflang/wflang"
)

// --- TC-016 自动宿主类型名格式 ----------------------------------------

type autoBook struct{ Title string }

// A package function returns *autoBook. Without AutoBindType alias, the
// result type name follows §4.2.2: "*<pkg>.<Type>".
func TestTC016_AutoHostTypeName(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("books", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "New", Impl: func() *autoBook { return &autoBook{Title: "t"} }, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"New":[{"pkg":"books"}]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	name := v.TypeName()
	// Should contain the auto host type marker (has "*" and package segment).
	if !strings.Contains(name, "autoBook") {
		t.Fatalf("want auto host type name, got %s", name)
	}
}

// --- TC-092 error.Error() 方法暴露 -----------------------------------

type errProducer struct{}

var errExposed = errors.New("boom-1")

func (errProducer) Fail() error { return errExposed }

// A host function that returns only error yields an error typed value. The
// Error method on that value returns the original message.
func TestTC092_ErrorMethodExposed(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType(errProducer{}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[
		{"let":{"err":{"Fail":[{"var":"p"}]}}},
		{"return":{"Error":[{"var":"err"}]}}
	]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"p": errProducer{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "boom-1" {
		t.Fatalf("want boom-1, got %v", v.Go())
	}
}

// --- TC-251 同命名空间重复注入报错 -------------------------------------

func TestTC251_DuplicatePackageRegistration(t *testing.T) {
	reg := wflang.NewRegistry()
	if err := reg.BindGoPackage("foo", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "A", Impl: func() int64 { return 1 }, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind1: %v", err)
	}
	// Second registration of same package name must fail.
	err := reg.BindGoPackage("foo", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "A", Impl: func() int64 { return 2 }, Pure: true},
		},
	})
	if err == nil {
		t.Fatal("want error on duplicate package, got nil")
	}
}

// --- TC-275 struct 字段大小写敏感 --------------------------------------

type CaseUser struct {
	Name string `json:"name"`
}

func TestTC275_StructCaseSensitive(t *testing.T) {
	u := &CaseUser{Name: "alice"}
	// Go field name: works.
	v, err := runSrc(t, []byte(`[{"return":{"var":"u.Name"}}]`), map[string]any{"u": u})
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if v.Go().(string) != "alice" {
		t.Fatalf("Name: want alice, got %v", v.Go())
	}
	// Via json tag: also works (json tag is "name").
	v, err = runSrc(t, []byte(`[{"return":{"var":"u.name"}}]`), map[string]any{"u": u})
	if err != nil {
		t.Fatalf("name: %v", err)
	}
	if v.Go().(string) != "alice" {
		t.Fatalf("name: want alice, got %v", v.Go())
	}
	// Random case "NAME" must fail.
	_, err = runSrc(t, []byte(`[{"return":{"var":"u.NAME"}}]`), map[string]any{"u": u})
	if err == nil {
		t.Fatal("NAME: want error, got nil")
	}
}

// --- TC-084 Receiver pkg vs var 分派不冲突 ----------------------------

// A type method with the same operator name as a package function should
// dispatch to the correct table based on the receiver kind.
type dualRcvThing struct{}

func (dualRcvThing) Act() string { return "type-method" }

func TestTC084_ReceiverDispatch(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType(dualRcvThing{}); err != nil {
		t.Fatalf("bind type: %v", err)
	}
	if err := reg.BindGoPackage("dual", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Act", Impl: func() string { return "pkg-func" }, Pure: true},
		},
	}); err != nil {
		t.Fatalf("bind pkg: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})

	// Call via pkg receiver.
	prog, err := eng.CompileJSON([]byte(`[{"return":{"Act":[{"pkg":"dual"}]}}]`))
	if err != nil {
		t.Fatalf("compile pkg: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run pkg: %v", err)
	}
	if v.Go().(string) != "pkg-func" {
		t.Fatalf("pkg: want pkg-func, got %v", v.Go())
	}

	// Call via typed var receiver.
	prog2, err := eng.CompileJSON([]byte(`[{"return":{"Act":[{"var":"t"}]}}]`))
	if err != nil {
		t.Fatalf("compile var: %v", err)
	}
	v, err = prog2.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"t": dualRcvThing{}},
	})
	if err != nil {
		t.Fatalf("run var: %v", err)
	}
	if v.Go().(string) != "type-method" {
		t.Fatalf("var: want type-method, got %v", v.Go())
	}
}
