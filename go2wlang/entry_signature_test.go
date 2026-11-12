package go2wlang_test

import (
	"context"
	"testing"

	"github.com/donutnomad/wlang/go2wlang"
	"github.com/donutnomad/wlang/wflang"
)

// TestEntrySignatureExtraction covers the payload boundary cases a workflow
// generator has to get right, including the ones that must deliberately yield
// nothing so the host falls back to the untyped path.
func TestEntrySignatureExtraction(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		wantVar string
		wantIn  string
		wantOut string
	}{
		{
			name: "typed input and output",
			source: `package orders
import "context"
type In struct { Amount int64 }
type Out struct { OK bool }
func Workflow(ctx context.Context, in In) (Out, error) {
	return Out{OK: true}, nil
}`,
			wantVar: "in", wantIn: "orders.In", wantOut: "orders.Out",
		},
		{
			// error-only results leave Output empty; that is what tells a host
			// there is no business value to encode.
			name: "error only result",
			source: `package orders
import "context"
type In struct { Amount int64 }
func Workflow(ctx context.Context, in In) error { return nil }`,
			wantVar: "in", wantIn: "orders.In", wantOut: "",
		},
		{
			// A pointer parameter is keyed "*"+elem by addType, so the entry has
			// to carry that same spelling to stay a valid key of Types.
			name: "pointer input keeps the pointer id",
			source: `package orders
import "context"
type In struct { Amount int64 }
func Workflow(ctx context.Context, in *In) error { return nil }`,
			wantVar: "in", wantIn: "*orders.In", wantOut: "",
		},
		{
			name: "slice input",
			source: `package orders
import "context"
type In struct { Amount int64 }
func Workflow(ctx context.Context, items []In) error { return nil }`,
			// Collection IDs use Go spelling ("[]T"), not the language name
			// ("array<T>"); the ID is what keys Types.
			wantVar: "items", wantIn: "[]orders.In", wantOut: "",
		},
		{
			// A bare scalar never reaches Types at a non-payload boundary, so
			// naming it as Input would dangle; the body still refers to it by
			// name, so InputVar is set.
			name: "scalar input names the variable only",
			source: `package orders
import "context"
func Workflow(ctx context.Context, name string) error { return nil }`,
			wantVar: "name", wantIn: "", wantOut: "",
		},
		{
			// Untyped data has no descriptor but the body references it by the
			// parameter name, which is not necessarily "input".
			name: "map input names the variable only",
			source: `package orders
import "context"
func Workflow(ctx context.Context, req map[string]any) error { return nil }`,
			wantVar: "req", wantIn: "", wantOut: "",
		},
		{
			name: "any input names the variable only",
			source: `package orders
import "context"
func Workflow(ctx context.Context, payload any) error { return nil }`,
			wantVar: "payload", wantIn: "", wantOut: "",
		},
		{
			name: "plain slice input names the variable only",
			source: `package orders
import "context"
func Workflow(ctx context.Context, ids []string) error { return nil }`,
			wantVar: "ids", wantIn: "", wantOut: "",
		},
		{
			name: "no input at all",
			source: `package orders
import "context"
func Workflow(ctx context.Context) error { return nil }`,
			wantVar: "", wantIn: "", wantOut: "",
		},
		{
			// The translator never seeds an unnamed parameter into locals, so the
			// body cannot reference it and there is no name to bind.
			name: "unnamed parameter yields no entry input",
			source: `package orders
import "context"
type In struct { Amount int64 }
func Workflow(context.Context, In) error { return nil }`,
			wantVar: "", wantIn: "", wantOut: "",
		},
		{
			// Two parameters sharing one type: picking either would be a guess.
			name: "multiple names on one type yields no entry input",
			source: `package orders
import "context"
type In struct { Amount int64 }
func Workflow(ctx context.Context, a, b In) error { return nil }`,
			wantVar: "", wantIn: "", wantOut: "",
		},
		{
			name: "typed output without typed input",
			source: `package orders
import "context"
type Out struct { OK bool }
func Workflow(ctx context.Context) (Out, error) { return Out{}, nil }`,
			wantVar: "", wantIn: "", wantOut: "orders.Out",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := go2wlang.TranslateFileDetailed([]byte(test.source), go2wlang.Options{FuncName: "Workflow"})
			if err != nil {
				t.Fatalf("TranslateFileDetailed: %v", err)
			}
			entry := result.Entry
			if entry.InputVar != test.wantVar {
				t.Errorf("InputVar = %q, want %q", entry.InputVar, test.wantVar)
			}
			if entry.Input != test.wantIn {
				t.Errorf("Input = %q, want %q", entry.Input, test.wantIn)
			}
			if entry.Output != test.wantOut {
				t.Errorf("Output = %q, want %q", entry.Output, test.wantOut)
			}
		})
	}
}

// TestEntryIDsAreKeysOfTypes is the invariant that keeps a consumer safe: if an
// entry names an ID, that ID must be resolvable in Types. Otherwise decoding
// fails at runtime with "no JSON descriptor" on an artifact that looked valid.
func TestEntryIDsAreKeysOfTypes(t *testing.T) {
	sources := []string{
		`package orders
import "context"
type In struct { Amount int64 }
type Out struct { OK bool }
func Workflow(ctx context.Context, in In) (Out, error) { return Out{}, nil }`,
		`package orders
import "context"
type In struct { Amount int64 }
func Workflow(ctx context.Context, in *In) error { return nil }`,
		`package orders
import "context"
type In struct { Amount int64 }
func Workflow(ctx context.Context, items []In) error { return nil }`,
		`package orders
import "context"
type Nested struct { N int64 }
type In struct { Child Nested }
func Workflow(ctx context.Context, in In) error { return nil }`,
	}
	for _, source := range sources {
		result, err := go2wlang.TranslateFileDetailed([]byte(source), go2wlang.Options{FuncName: "Workflow"})
		if err != nil {
			t.Fatalf("TranslateFileDetailed: %v", err)
		}
		for _, ref := range []struct{ field, id string }{
			{"Input", result.Entry.Input},
			{"Output", result.Entry.Output},
		} {
			if ref.id == "" {
				continue
			}
			if _, ok := result.Types[ref.id]; !ok {
				t.Fatalf("Entry.%s = %q is not a key of Types (keys: %v)\nsource:\n%s",
					ref.field, ref.id, typeKeys(result.Types), source)
			}
		}
	}
}

// TestEntryInputVarMatchesBodyReference ties InputVar to what the body actually
// emits: the generated program references the parameter by its Go name, so a
// host that binds any other name leaves the body with an unresolved symbol.
func TestEntryInputVarMatchesBodyReference(t *testing.T) {
	source := `package orders
import "context"
type In struct { Amount int64 }
func Workflow(ctx context.Context, in In) error {
	total := in.Amount
	_ = total
	return nil
}`
	result, err := go2wlang.TranslateFileDetailed([]byte(source), go2wlang.Options{FuncName: "Workflow"})
	if err != nil {
		t.Fatalf("TranslateFileDetailed: %v", err)
	}
	if result.Entry.InputVar != "in" {
		t.Fatalf("InputVar = %q, want %q", result.Entry.InputVar, "in")
	}
	if !containsSubstring(string(result.JSON), `"in.Amount"`) {
		t.Fatalf("body does not reference the parameter by InputVar:\n%s", result.JSON)
	}
}

// TestUntypedEntryInputRunsUnderInputVar binds an untyped input the way a host
// does, under Entry.InputVar, and runs the program: the body must read the
// parameter by the name the author chose.
func TestUntypedEntryInputRunsUnderInputVar(t *testing.T) {
	source := `package orders
import "context"
func Workflow(ctx context.Context, req map[string]any) any {
	return req["k"]
}`
	result, err := go2wlang.TranslateFileDetailed([]byte(source), go2wlang.Options{FuncName: "Workflow"})
	if err != nil {
		t.Fatalf("TranslateFileDetailed: %v", err)
	}
	if result.Entry.InputVar != "req" || result.Entry.Input != "" {
		t.Fatalf("Entry = %+v, want InputVar req and no Input", result.Entry)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON(result.JSON)
	if err != nil {
		t.Fatalf("CompileJSON: %v", err)
	}
	value, err := prog.Run(context.Background(), wflang.RunOptions{Vars: map[string]any{
		result.Entry.InputVar: map[string]any{"k": "v"},
	}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, ok := value.Go().(string); !ok || got != "v" {
		t.Fatalf("result = %#v, want %q", value.Go(), "v")
	}
}

func typeKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func containsSubstring(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
