package wflang_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wflang/wflang/registry"
	"github.com/wflang/wflang/wflang"
)

// §16.13: Go Builder 生成配置并执行。
func TestBuilder_GenerateAndRun(t *testing.T) {
	reg := wflang.DefaultRegistry()
	risk := registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{
				GoName: "Score",
				Params: []registry.ParamSpec{
					{Name: "amount", Type: "float64"},
					{Name: "country", Type: "string"},
				},
				ReturnTypes: []string{"float64"},
				Impl: func(amount float64, country string) (float64, error) {
					_ = country
					return amount * 2, nil
				},
			},
		},
	}
	if err := reg.BindGoPackage("risk", risk); err != nil {
		t.Fatalf("bind: %v", err)
	}

	b := wflang.NewConfigBuilder(reg)
	data, err := b.Program().
		Return(b.Call(
			b.Pkg("risk"),
			"Score",
			b.Lit("float64", "10"),
			b.Lit("string", "US"),
		)).
		JSON()
	if err != nil {
		t.Fatalf("builder.JSON: %v", err)
	}
	// Sanity check the shape.
	var pretty any
	if err := json.Unmarshal(data, &pretty); err != nil {
		t.Fatalf("json: %v", err)
	}

	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON(data)
	if err != nil {
		t.Fatalf("compile: %v (data=%s)", err, string(data))
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Score returns (float64, error) so host call yields tuple<float64,error>.
	parts, ok := v.Go().([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("want tuple<float64,error>, got %T: %v", v.Go(), v.Go())
	}
	if parts[0].(float64) != 20 {
		t.Fatalf("want 20, got %v", parts[0])
	}
	if parts[1] != nil {
		t.Fatalf("want nil err, got %v", parts[1])
	}
}

// Var/Let 变体。
func TestBuilder_LetAndVar(t *testing.T) {
	reg := wflang.DefaultRegistry()
	b := wflang.NewConfigBuilder(reg)
	data, err := b.Program().
		Let("n", b.Lit("int64", "7")).
		Return(b.Var("n")).
		JSON()
	if err != nil {
		t.Fatalf("builder: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON(data)
	if err != nil {
		t.Fatalf("compile: %v (data=%s)", err, string(data))
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 7 {
		t.Fatalf("want 7, got %v", v.Go())
	}
}
