package go2wlang_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/donutnomad/wlang/go2wlang"
	"github.com/donutnomad/wlang/types"
	"github.com/donutnomad/wlang/wflang"
)

func TestTranslateFileEmitsDescriptorForTypedHandlerResult(t *testing.T) {
	src := []byte(`package flows
type CreateResult struct {
	RecordID string ` + "`json:\"record_id\"`" + `
	Status string ` + "`json:\"status\"`" + `
}
func SetUpdateHandler(ctx any, name string, handler any) error { return nil }
func Workflow() error {
	return SetUpdateHandler(nil, "create", func() (CreateResult, error) {
		return CreateResult{RecordID: "rec-1", Status: "created"}, nil
	})
}`)
	result, err := go2wlang.TranslateFileDetailed(src, go2wlang.Options{FuncName: "Workflow"})
	if err != nil {
		t.Fatalf("TranslateFileDetailed: %v", err)
	}
	descriptor, ok := result.Types["flows.CreateResult"]
	if !ok {
		t.Fatalf("missing CreateResult descriptor: %#v", result.Types)
	}
	if descriptor.LanguageName != "flows.CreateResult" || descriptor.Kind != types.TypeKindRecord {
		t.Fatalf("unexpected descriptor: %#v", descriptor)
	}
	if len(descriptor.Fields) != 2 || descriptor.Fields[0].JSONName != "record_id" {
		t.Fatalf("unexpected fields: %#v", descriptor.Fields)
	}
	var envelope struct {
		Types map[string]types.TypeDescriptor `json:"types"`
	}
	if err := json.Unmarshal(result.JSON, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if _, ok := envelope.Types["flows.CreateResult"]; !ok {
		t.Fatalf("JSON envelope omitted descriptor: %s", result.JSON)
	}
}

func TestTranslateFilePathLoadsDTOFromSiblingFile(t *testing.T) {
	dir := t.TempDir()
	writeDescriptorFile(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\ngo 1.25\n")
	writeDescriptorFile(t, filepath.Join(dir, "dto.go"), `package flows
type CreateResult struct { RecordID string `+"`json:\"record_id\"`"+` }
`)
	workflowFile := filepath.Join(dir, "workflow.go")
	writeDescriptorFile(t, workflowFile, `package flows
func SetUpdateHandler(ctx any, name string, handler any) error { return nil }
func Workflow() error {
	return SetUpdateHandler(nil, "create", func() (CreateResult, error) {
		return CreateResult{RecordID: "rec-2"}, nil
	})
}`)
	result, err := go2wlang.TranslateFilePathDetailed(workflowFile, go2wlang.Options{FuncName: "Workflow"})
	if err != nil {
		t.Fatalf("TranslateFilePathDetailed: %v", err)
	}
	if _, ok := result.Types["example.com/app.CreateResult"]; !ok {
		t.Fatalf("missing sibling DTO descriptor: %#v", result.Types)
	}
}

func TestTranslateFilePathLoadsImportedDTO(t *testing.T) {
	dir := t.TempDir()
	writeDescriptorFile(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\ngo 1.25\n")
	if err := os.Mkdir(filepath.Join(dir, "dto"), 0o700); err != nil {
		t.Fatalf("mkdir dto: %v", err)
	}
	writeDescriptorFile(t, filepath.Join(dir, "dto", "dto.go"), `package dto
type CreateResult struct { RecordID string `+"`json:\"record_id\"`"+` }
`)
	workflowFile := filepath.Join(dir, "workflow.go")
	writeDescriptorFile(t, workflowFile, `package flows
import model "example.com/app/dto"
func SetUpdateHandler(ctx any, name string, handler any) error { return nil }
func Workflow() error {
	return SetUpdateHandler(nil, "create", func() (model.CreateResult, error) {
		return model.CreateResult{RecordID: "rec-3"}, nil
	})
}`)
	result, err := go2wlang.TranslateFilePathDetailed(workflowFile, go2wlang.Options{FuncName: "Workflow"})
	if err != nil {
		t.Fatalf("TranslateFilePathDetailed: %v", err)
	}
	descriptor, ok := result.Types["example.com/app/dto.CreateResult"]
	if !ok {
		t.Fatalf("missing imported DTO descriptor: %#v", result.Types)
	}
	if descriptor.LanguageName != "model.CreateResult" {
		t.Fatalf("import alias was not canonicalized consistently: %#v", descriptor)
	}
}

func TestTranslateFileEmitsDescriptorForNamedStringMapKey(t *testing.T) {
	src := []byte(`package flows
type Key string
type Result struct { Value string }
func SetUpdateHandler(ctx any, name string, handler any) error { return nil }
func Workflow() error {
	return SetUpdateHandler(nil, "lookup", func() (map[Key]Result, error) {
		var result map[Key]Result
		return result, nil
	})
}`)
	result, err := go2wlang.TranslateFileDetailed(src, go2wlang.Options{FuncName: "Workflow"})
	if err != nil {
		t.Fatalf("TranslateFileDetailed: %v", err)
	}
	key, ok := result.Types["flows.Key"]
	if !ok || key.Kind != types.TypeKindAlias || key.Underlying != types.TString {
		t.Fatalf("unexpected named key descriptor: %#v", key)
	}
	var foundMap bool
	for _, descriptor := range result.Types {
		if descriptor.Kind == types.TypeKindMap && descriptor.Key == "flows.Key" && descriptor.Elem == "flows.Result" {
			foundMap = true
		}
	}
	if !foundMap {
		t.Fatalf("missing named-key map descriptor: %#v", result.Types)
	}
}

func TestTranslateFileRunsLocalDTOCollectionLiterals(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		wantType string
	}{
		{
			name: "slice",
			source: `package flows
type Child struct { Name string }
func Workflow() []Child {
	return []Child{Child{Name: "one"}}
}`,
			wantType: "array<flows.Child>",
		},
		{
			name: "named string map key",
			source: `package flows
type Key string
type Child struct { Name string }
func Workflow() map[Key]Child {
	return map[Key]Child{}
}`,
			wantType: "map<flows.Key,flows.Child>",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := go2wlang.TranslateFileDetailed([]byte(test.source), go2wlang.Options{FuncName: "Workflow"})
			if err != nil {
				t.Fatalf("TranslateFileDetailed: %v", err)
			}
			engine := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
			program, err := engine.CompileJSON(result.JSON)
			if err != nil {
				t.Fatalf("CompileJSON: %v\n%s", err, result.JSON)
			}
			value, err := program.Run(context.Background(), wflang.RunOptions{})
			if err != nil {
				t.Fatalf("Run: %v\n%s", err, result.JSON)
			}
			if value.TypeName() != test.wantType {
				t.Fatalf("result type = %s, want %s", value.TypeName(), test.wantType)
			}
		})
	}
}

func writeDescriptorFile(t *testing.T, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
}
