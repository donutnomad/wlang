package registry_test

import (
	"testing"

	"github.com/wflang/wflang/registry"
)

// §11: Registry.ExportLanguageSpec 至少暴露包/函数/类型方法元数据。
func TestExportLanguageSpec(t *testing.T) {
	r := registry.New()
	pkg := registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{
				GoName: "Add",
				Params: []registry.ParamSpec{
					{Name: "a", Type: "int64"},
					{Name: "b", Type: "int64"},
				},
				ReturnTypes: []string{"int64"},
				Impl:        func(a, b int64) int64 { return a + b },
			},
		},
	}
	if err := r.BindGoPackage("math", pkg); err != nil {
		t.Fatalf("bind: %v", err)
	}
	spec, err := r.ExportLanguageSpec()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	p, ok := spec.Packages["math"]
	if !ok {
		t.Fatalf("want package math in spec: %#v", spec)
	}
	if len(p.Functions) != 1 || p.Functions[0].GoName != "Add" {
		t.Fatalf("want Add, got %#v", p.Functions)
	}
}
