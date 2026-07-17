package wflang_test

import (
	"context"
	"testing"

	"github.com/donutnomad/wlang/types"
	"github.com/donutnomad/wlang/wflang"
)

func TestTupleExpressionProducesFirstClassTuple(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON([]byte(`[
		{"return":{"tuple":[
			{"literal":{"type":"string","value":"created"}},
			{"literal":{"type":"int64","value":"7"}}
		]}}
	]`))
	if err != nil {
		t.Fatalf("CompileJSON: %v", err)
	}
	got, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	values, ok := types.UnpackTuple(got)
	if !ok {
		t.Fatalf("expected tuple, got %s (%#v)", got.TypeName(), got.Go())
	}
	if len(values) != 2 || values[0].Go() != "created" || values[1].Go() != int64(7) {
		t.Fatalf("unexpected tuple values: %#v", values)
	}
	if got.TypeName() != "tuple<string,int64>" {
		t.Fatalf("unexpected tuple type: %s", got.TypeName())
	}
}

func TestFunctionReturnContextTypesNilErrorTupleSlot(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON([]byte(`[
		{"let":{"handler":{"fn":{
			"params":[],
			"returns":["string","error"],
			"do":[{"return":{"tuple":[
				{"literal":{"type":"string","value":"created"}},
				{"literal":{"type":"null","value":null}}
			]}}]
		}}}},
		{"return":{"call":{"fn":{"var":"handler"},"args":[]}}}
	]`))
	if err != nil {
		t.Fatalf("CompileJSON: %v", err)
	}
	got, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	values, ok := types.UnpackTuple(got)
	if !ok || len(values) != 2 {
		t.Fatalf("expected pair, got %s (%#v)", got.TypeName(), got.Go())
	}
	if got.TypeName() != "tuple<string,error>" {
		t.Fatalf("return tuple must use declared types, got %s", got.TypeName())
	}
	if !values[1].IsError() || values[1].Go() != nil {
		t.Fatalf("expected typed nil error, got %s (%#v)", values[1].TypeName(), values[1].Go())
	}
}

func TestFunctionReturnContextTypesNullablePointerTupleSlot(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON([]byte(`{
		"types":{
			"dto":{"id":"dto","language_name":"flows.DTO","kind":"record","fields":[],"fingerprint":""},
			"dto_ptr":{"id":"dto_ptr","language_name":"*flows.DTO","kind":"pointer","elem":"dto","fingerprint":""}
		},
		"program":[
			{"let":{"handler":{"fn":{
				"params":[],
				"returns":["*flows.DTO","error"],
				"do":[{"return":{"tuple":[
					{"literal":{"type":"null","value":null}},
					{"literal":{"type":"null","value":null}}
				]}}]
			}}}},
			{"return":{"call":{"fn":{"var":"handler"},"args":[]}}}
		]
	}`))
	if err != nil {
		t.Fatalf("CompileJSON: %v", err)
	}
	got, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	values, ok := types.UnpackTuple(got)
	if !ok || len(values) != 2 {
		t.Fatalf("expected pair, got %s (%#v)", got.TypeName(), got.Go())
	}
	if got.TypeName() != "tuple<*flows.DTO,error>" {
		t.Fatalf("return tuple must use declared types, got %s", got.TypeName())
	}
	if values[0].TypeName() != "*flows.DTO" || values[0].Go() != nil {
		t.Fatalf("expected typed nil pointer, got %s (%#v)", values[0].TypeName(), values[0].Go())
	}
}
