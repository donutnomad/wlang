// Batch 27 covers capability enforcement (LANGUAGE.md §5.5 / §10.2):
// TC-440 (capability missing → E_CAPABILITY) and TC-441 (capability granted
// → call succeeds).
package wflang_test

import (
	"errors"
	"testing"

	"github.com/wflang/wflang/registry"
	"github.com/wflang/wflang/wflang"
)

func tc440Fetch(url string) string { return "fetched:" + url }

func TestTC440_CapabilityMissingRejectsCall(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("net", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Fetch", Impl: tc440Fetch, Pure: false,
				Capabilities: []string{"net:http"}},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry:     reg,
		Capabilities: nil, // empty: capability not granted
	})
	src := []byte(`[{"return":{"Fetch":[{"pkg":"net"},
		{"literal":{"type":"string","value":"http://x"}}]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(t.Context(), wflang.RunOptions{})
	if err == nil {
		t.Fatalf("want E_CAPABILITY, got nil")
	}
	var le *wflang.LangError
	if !errors.As(err, &le) || le.Code != "E_CAPABILITY" {
		t.Fatalf("want E_CAPABILITY, got %v (%T)", err, err)
	}
}

// --- TC-441 capability 授予后通过 ----------------------------------------
func TestTC441_CapabilityGrantedAllowsCall(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("net", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Fetch", Impl: tc440Fetch, Pure: false,
				Capabilities: []string{"net:http"}},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry:     reg,
		Capabilities: wflang.CapabilitySet{"net:http": true},
	})
	src := []byte(`[{"return":{"Fetch":[{"pkg":"net"},
		{"literal":{"type":"string","value":"http://x"}}]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(t.Context(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "fetched:http://x" {
		t.Fatalf("want fetched:http://x, got %v", v.Go())
	}
}

// --- TC-955 阶段三：缺 capability 在执行前报告 ------------------------
// The current implementation reports E_CAPABILITY at invocation time; the
// "before execution" report (Explain/Compile) is part of the toolchain.
// This test covers the runtime guarantee.
func TestTC955_CapabilityMissingReportedAtInvocation(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("net", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Fetch", Impl: tc440Fetch, Pure: false,
				Capabilities: []string{"net:http"}},
		},
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"Fetch":[{"pkg":"net"},
		{"literal":{"type":"string","value":"x"}}]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(t.Context(), wflang.RunOptions{})
	var le *wflang.LangError
	if !errors.As(err, &le) || le.Code != "E_CAPABILITY" {
		t.Fatalf("want E_CAPABILITY, got %v (%T)", err, err)
	}
	if le.Path == "" {
		t.Fatalf("E_CAPABILITY must include path")
	}
}
