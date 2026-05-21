package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/donutnomad/wlang/types"
)

func TestInvokeCoercesNilErrorValueToNilInterface(t *testing.T) {
	r := New()
	if err := r.BindGoPackage("nilcase", PackageSpec{
		Functions: []FuncSpec{
			{
				GoName: "UseError",
				Impl: func(err error) string {
					if err == nil {
						return "nil"
					}
					return err.Error()
				},
			},
		},
	}); err != nil {
		t.Fatalf("BindGoPackage: %v", err)
	}

	v, err := r.Invoke(context.Background(), "UseError",
		types.NewValue("__pkg__", struct{ Name string }{Name: "nilcase"}),
		[]types.Value{types.NewValue(types.TError, nil)},
		"/test")
	if err != nil {
		t.Fatalf("Invoke nil error: %v", err)
	}
	if v.TypeName() != types.TString || v.Go().(string) != "nil" {
		t.Fatalf("want string nil, got %s=%v", v.TypeName(), v.Go())
	}
}

func TestInvokeCoercesNonNilErrorValueToErrorInterface(t *testing.T) {
	r := New()
	if err := r.BindGoPackage("nilcase", PackageSpec{
		Functions: []FuncSpec{
			{
				GoName: "UseError",
				Impl: func(err error) string {
					if err == nil {
						return "nil"
					}
					return err.Error()
				},
			},
		},
	}); err != nil {
		t.Fatalf("BindGoPackage: %v", err)
	}

	v, err := r.Invoke(context.Background(), "UseError",
		types.NewValue("__pkg__", struct{ Name string }{Name: "nilcase"}),
		[]types.Value{types.NewValue(types.TError, errors.New("boom"))},
		"/test")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if v.TypeName() != types.TString || v.Go().(string) != "boom" {
		t.Fatalf("want string boom, got %s=%v", v.TypeName(), v.Go())
	}
}
