package go2wlang_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/donutnomad/wlang/go2wlang"
	"github.com/donutnomad/wlang/go2wlang/testdata/differential"
	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/types"
	"github.com/donutnomad/wlang/wflang"
)

// typeClosureModule writes a module with a host package, a contract package
// and a workflow whose data types occur only as host call arguments, closure
// parameters and results, conversions and values flowing back from the host.
func typeClosureModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeDescriptorFile(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\ngo 1.25\n")
	for _, sub := range []string{"host", "contract"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeDescriptorFile(t, filepath.Join(dir, "host", "host.go"), `package host
func Send(name string, v any) string { return name }
func Handle(name string, handler any) {}
func Fetch(name string, out any) error { return nil }
`)
	writeDescriptorFile(t, filepath.Join(dir, "contract", "contract.go"), `package contract
type Order struct {
	ID    string            `+"`json:\"id\"`"+`
	Lines []Line            `+"`json:\"lines\"`"+`
	Notes map[string]*Note  `+"`json:\"notes,omitempty\"`"+`
}
type Line struct {
	SKU  string
	Qty  int
	Tags [2]string
}
type Note struct {
	Text  string
	Reply *Note
}
`)
	workflowFile := filepath.Join(dir, "workflow.go")
	writeDescriptorFile(t, workflowFile, `package flows

import (
	"example.com/app/contract"
	"example.com/app/host"
)

type ChildInput struct {
	Order contract.Order
	Batch []Batch
}

type Batch struct {
	Seq   int
	Steps map[string][]Step
}

type Step struct {
	Name string
	Next *Step
}

type QueryArg struct{ Key string }
type QueryResult struct{ Hits []Hit }
type Hit struct{ Score float64 }

type Signal struct{ Reason Reason }
type Reason string

func Workflow() string {
	host.Handle("query", func(arg QueryArg) (QueryResult, error) {
		return QueryResult{Hits: []Hit{{Score: 1.5}}}, nil
	})
	var reason Signal
	_ = host.Fetch("signal", &reason)
	return host.Send("child", ChildInput{
		Order: contract.Order{ID: "o-1", Lines: []contract.Line{{SKU: "a", Qty: 2, Tags: [2]string{"x", "y"}}}},
		Batch: []Batch{{Seq: 1, Steps: map[string][]Step{"s": []Step{{Name: "first"}}}}},
	})
}
`)
	return workflowFile
}

func TestTypesReachedOnlyThroughHostCallsAreRegistered(t *testing.T) {
	workflowFile := typeClosureModule(t)
	result, err := go2wlang.TranslateFilePathDetailed(workflowFile, go2wlang.Options{
		FuncName:     "Workflow",
		HostPackages: []string{"example.com/app/host"},
	})
	if err != nil {
		t.Fatalf("TranslateFilePathDetailed: %v", err)
	}
	for _, id := range []string{
		"example.com/app.ChildInput", "example.com/app.Batch", "example.com/app.Step", "*example.com/app.Step",
		"map[string][]example.com/app.Step",
		"example.com/app/contract.Order", "example.com/app/contract.Line", "[2]string",
		"example.com/app/contract.Note", "*example.com/app/contract.Note",
		"example.com/app.QueryArg", "example.com/app.QueryResult", "example.com/app.Hit",
		"example.com/app.Signal", "example.com/app.Reason",
	} {
		if _, ok := result.Types[id]; !ok {
			t.Errorf("missing descriptor %s", id)
		}
	}
	for id := range result.Types {
		if strings.HasPrefix(id, "example.com/app/host.") {
			t.Errorf("host type %s must stay host bound", id)
		}
	}

	// The program runs: the struct literal handed to the host resolves, and
	// the host encodes it through the descriptors.
	reg := wflang.DefaultRegistry()
	var sent json.RawMessage
	if err := reg.BindGoPackage("host", registry.PackageSpec{Functions: []registry.FuncSpec{
		{GoName: "Send", Impl: func(name string, v any) (string, error) {
			record, ok := v.(*types.Record)
			if !ok {
				t.Fatalf("Send received %T, want a record", v)
			}
			raw, err := reg.EncodeJSONValue(types.NewValue(record.TypeName(), record))
			sent = raw
			return name, err
		}},
		{GoName: "Handle", Impl: func(name string, handler any) {}},
		{GoName: "Fetch", Impl: func(name string, out any) error { return nil }},
	}}); err != nil {
		t.Fatal(err)
	}
	program, err := wflang.NewEngine(wflang.EngineOptions{Registry: reg}).CompileJSON(result.JSON)
	if err != nil {
		t.Fatalf("CompileJSON: %v", err)
	}
	if _, err := program.Run(context.Background(), wflang.RunOptions{}); err != nil {
		t.Fatalf("Run: %v\n%s", err, result.JSON)
	}
	want := `{"Batch":[{"Seq":1,"Steps":{"s":[{"Name":"first","Next":null}]}}],"Order":{"id":"o-1","lines":[{"Qty":2,"SKU":"a","Tags":["x","y"]}]}}`
	if got := normalJSON(sent); got != want {
		t.Fatalf("host received %s\nwant %s", got, want)
	}
}

// The type table is part of the artifact's content hash: translating the same
// source twice yields byte-identical JSON.
func TestTypeTableIsDeterministic(t *testing.T) {
	workflowFile := typeClosureModule(t)
	opts := go2wlang.Options{FuncName: "Workflow", HostPackages: []string{"example.com/app/host"}}
	first, err := go2wlang.TranslateFilePathDetailed(workflowFile, opts)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := go2wlang.TranslateFilePathDetailed(workflowFile, opts)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first.JSON, again.JSON) {
			t.Fatalf("translation %d differs:\n%s\n---\n%s", i, first.JSON, again.JSON)
		}
	}
}

// A JSON payload of a nested, recursive or cross-package type decodes through
// the descriptors and encodes back to the bytes Go produces.
func TestNestedTypesJSONRoundTrip(t *testing.T) {
	for _, fn := range []any{
		differential.DeepNestedLiteral, differential.DeepNestedZero, differential.NamedSliceMap,
		differential.RecursiveTree, differential.MutuallyRecursive, differential.CrossPackageTypes,
	} {
		name := funcName(fn)
		t.Run(name, func(t *testing.T) {
			filename := funcFile(fn)
			src, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			result, err := go2wlang.TranslateFileDetailed(src, differentialOptions(name, filename))
			if err != nil {
				t.Fatal(err)
			}
			native, err := json.Marshal(reflect.ValueOf(fn).Call(nil)[0].Interface())
			if err != nil {
				t.Fatal(err)
			}
			reg := wflang.DefaultRegistry()
			if err := reg.BindDescriptors(result.Types); err != nil {
				t.Fatal(err)
			}
			value, err := reg.DecodeJSONValue(result.Entry.Output, native)
			if err != nil {
				t.Fatalf("decode %s: %v", native, err)
			}
			encoded, err := reg.EncodeJSONValue(value)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if normalJSON(encoded) != normalJSON(native) {
				t.Fatalf("round trip\n got %s\nwant %s", encoded, native)
			}
		})
	}
}
