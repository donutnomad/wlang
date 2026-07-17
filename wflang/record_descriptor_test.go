package wflang_test

import (
	"context"
	"testing"

	"github.com/donutnomad/wlang/types"
	"github.com/donutnomad/wlang/wflang"
)

type recordFillResult struct {
	Value string
}

type recordNestedChild struct {
	Name string
}

type recordNestedResult struct {
	Children []recordNestedChild
	ByName   map[string]recordNestedChild
}

type recordFillPackage struct{}

func (recordFillPackage) Fill(out *recordFillResult) error {
	out.Value = "filled"
	return nil
}

func (recordFillPackage) FillMap(out *map[string]any) error {
	*out = map[string]any{"value": "from-map"}
	return nil
}

func (recordFillPackage) FillNested(out *recordNestedResult) error {
	out.Children = []recordNestedChild{{Name: "slice-child"}}
	out.ByName = map[string]recordNestedChild{"main": {Name: "map-child"}}
	return nil
}

func TestProgramEnvelopeCarriesExecutableRecordDescriptors(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON([]byte(`{
		"lang":"wflang/v1",
		"types":{
			"example.com/flows.CreateResult":{
				"id":"example.com/flows.CreateResult",
				"language_name":"flows.CreateResult",
				"kind":"record",
				"fields":[
					{"go_name":"RecordID","json_name":"record_id","type_id":"string"},
					{"go_name":"Status","json_name":"status","type_id":"string"}
				],
				"fingerprint":""
			}
		},
		"program":[
			{"return":{"struct":["flows.CreateResult",{
				"RecordID":{"literal":{"type":"string","value":"rec-7"}},
				"Status":{"literal":{"type":"string","value":"created"}}
			}]}}
		]
	}`))
	if err != nil {
		t.Fatalf("CompileJSON: %v", err)
	}
	got, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	record, ok := types.AsRecord(got)
	if !ok {
		t.Fatalf("expected record, got %s (%T)", got.TypeName(), got.Go())
	}
	if value, ok := record.Field("RecordID"); !ok || value.Go() != "rec-7" {
		t.Fatalf("RecordID mismatch: %v, %v", value.Go(), ok)
	}
}

func TestRecordFieldPathAcceptsGoAndJSONNames(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON([]byte(`{
		"types":{"result":{
			"id":"result","language_name":"flows.Result","kind":"record",
			"fields":[{"go_name":"RecordID","json_name":"record_id","type_id":"string"}],
			"fingerprint":""
		}},
		"program":[
			{"let":{"result":{"struct":["flows.Result",{"RecordID":{"literal":{"type":"string","value":"rec-9"}}}]}}},
			{"return":{"var":"result.record_id"}}
		]
	}`))
	if err != nil {
		t.Fatalf("CompileJSON: %v", err)
	}
	got, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Go() != "rec-9" {
		t.Fatalf("unexpected field value: %#v", got.Go())
	}
}

func TestRecordFieldsSupportNestedRecordSliceAndMap(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON([]byte(`{
		"types":{
			"child":{"id":"child","language_name":"flows.Child","kind":"record","fields":[{"go_name":"Name","json_name":"name","type_id":"string"}],"fingerprint":""},
			"children":{"id":"children","language_name":"array<flows.Child>","kind":"slice","elem":"child","fingerprint":""},
			"children_by_name":{"id":"children_by_name","language_name":"map<string,flows.Child>","kind":"map","key":"string","elem":"child","fingerprint":""},
			"parent":{"id":"parent","language_name":"flows.Parent","kind":"record","fields":[
				{"go_name":"Children","json_name":"children","type_id":"children"},
				{"go_name":"ByName","json_name":"by_name","type_id":"children_by_name"}
			],"fingerprint":""}
		},
		"program":[
			{"let":{"parent":{"struct":["flows.Parent",{
				"Children":{"array":{"elem":"flows.Child","items":[{"struct":["flows.Child",{"Name":{"literal":{"type":"string","value":"slice-child"}}}]}]}},
				"ByName":{"map":{"type":["string","flows.Child"],"entries":[[
					{"literal":{"type":"string","value":"main"}},
					{"struct":["flows.Child",{"Name":{"literal":{"type":"string","value":"map-child"}}}]}
				]]}}
			}]}}},
			{"return":{"tuple":[{"var":"parent.Children.0.Name"},{"var":"parent.ByName.main.Name"}]}}
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
	if !ok || len(values) != 2 || values[0].Go() != "slice-child" || values[1].Go() != "map-child" {
		t.Fatalf("unexpected nested values: %s %#v", got.TypeName(), got.Go())
	}
}

func TestRecordOutArgumentMaterializesGoStructAndCommitsResult(t *testing.T) {
	registry := wflang.DefaultRegistry()
	if err := registry.BindGoPackageAuto("host", recordFillPackage{}); err != nil {
		t.Fatalf("BindGoPackageAuto: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: registry})
	prog, err := eng.CompileJSON([]byte(`{
		"types":{"result":{
			"id":"result","language_name":"flows.Result","kind":"record",
			"fields":[{"go_name":"Value","json_name":"value","type_id":"string"}],
			"fingerprint":""
		}},
		"program":[
			{"let":{"result":{"zero":"flows.Result"}}},
			{"expr":{"Fill":[{"pkg":"host"},{"out":"result"}]}},
			{"return":{"var":"result.Value"}}
		]
	}`))
	if err != nil {
		t.Fatalf("CompileJSON: %v", err)
	}
	got, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Go() != "filled" {
		t.Fatalf("record out result mismatch: %#v", got.Go())
	}
}

func TestRecordOutArgumentUsesMapSurrogateForGenericPayloadHost(t *testing.T) {
	registry := wflang.DefaultRegistry()
	if err := registry.BindGoPackageAuto("host", recordFillPackage{}); err != nil {
		t.Fatalf("BindGoPackageAuto: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: registry})
	prog, err := eng.CompileJSON([]byte(`{
		"types":{"result":{
			"id":"result","language_name":"flows.Result","kind":"record",
			"fields":[{"go_name":"Value","json_name":"value","type_id":"string"}],
			"fingerprint":""
		}},
		"program":[
			{"let":{"result":{"zero":"flows.Result"}}},
			{"expr":{"FillMap":[{"pkg":"host"},{"out":"result"}]}},
			{"return":{"var":"result.Value"}}
		]
	}`))
	if err != nil {
		t.Fatalf("CompileJSON: %v", err)
	}
	got, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Go() != "from-map" {
		t.Fatalf("record map-surrogate result mismatch: %#v", got.Go())
	}
}

func TestRecordOutArgumentPreservesNestedCollectionDescriptors(t *testing.T) {
	registry := wflang.DefaultRegistry()
	if err := registry.BindGoPackageAuto("host", recordFillPackage{}); err != nil {
		t.Fatalf("BindGoPackageAuto: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: registry})
	prog, err := eng.CompileJSON([]byte(`{
		"types":{
			"child":{"id":"child","language_name":"flows.Child","kind":"record","fields":[{"go_name":"Name","json_name":"name","type_id":"string"}],"fingerprint":""},
			"children":{"id":"children","language_name":"array<flows.Child>","kind":"slice","elem":"child","fingerprint":""},
			"by_name":{"id":"by_name","language_name":"map<string,flows.Child>","kind":"map","key":"string","elem":"child","fingerprint":""},
			"result":{"id":"result","language_name":"flows.Result","kind":"record","fields":[
				{"go_name":"Children","json_name":"children","type_id":"children"},
				{"go_name":"ByName","json_name":"by_name","type_id":"by_name"}
			],"fingerprint":""}
		},
		"program":[
			{"let":{"result":{"zero":"flows.Result"}}},
			{"expr":{"FillNested":[{"pkg":"host"},{"out":"result"}]}},
			{"return":{"tuple":[{"var":"result.Children.0.Name"},{"var":"result.ByName.main.Name"}]}}
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
	if !ok || len(values) != 2 || values[0].Go() != "slice-child" || values[1].Go() != "map-child" {
		t.Fatalf("nested record out result mismatch: %s %#v", got.TypeName(), got.Go())
	}
}

func TestRecordFieldAssignmentRejectsWrongDescriptorType(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	prog, err := eng.CompileJSON([]byte(`{
		"types":{"result":{
			"id":"result","language_name":"flows.Result","kind":"record",
			"fields":[{"go_name":"Value","json_name":"value","type_id":"string"}],
			"fingerprint":""
		}},
		"program":[
			{"let":{"result":{"zero":"flows.Result"}}},
			{"set":{"result.Value":{"literal":{"type":"int64","value":"7"}}}},
			{"return":{"var":"result"}}
		]
	}`))
	if err != nil {
		t.Fatalf("CompileJSON: %v", err)
	}
	if _, err := prog.Run(context.Background(), wflang.RunOptions{}); err == nil {
		t.Fatal("expected descriptor type error")
	}
}

func TestDescriptorMapLiteralValidatesValuesAndNamedStringKeys(t *testing.T) {
	descriptors := `
		"key":{"id":"key","language_name":"flows.Key","kind":"alias","underlying":"string","fingerprint":""},
		"child":{"id":"child","language_name":"flows.Child","kind":"record","fields":[{"go_name":"Name","json_name":"name","type_id":"string"}],"fingerprint":""},
		"children":{"id":"children","language_name":"map<flows.Key,flows.Child>","kind":"map","key":"key","elem":"child","fingerprint":""}`

	t.Run("named string key", func(t *testing.T) {
		eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
		prog, err := eng.CompileJSON([]byte(`{"types":{` + descriptors + `},"program":[
			{"let":{"key":{"zero":"flows.Key"}}},
			{"return":{"map":{"type":["flows.Key","flows.Child"],"entries":[[
				{"var":"key"},{"struct":["flows.Child",{"Name":{"literal":{"type":"string","value":"ok"}}}]}
			]]}}}
		]}`))
		if err != nil {
			t.Fatalf("CompileJSON: %v", err)
		}
		got, err := prog.Run(context.Background(), wflang.RunOptions{})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got.TypeName() != "map<flows.Key,flows.Child>" {
			t.Fatalf("map type = %s", got.TypeName())
		}
	})

	t.Run("wrong value type", func(t *testing.T) {
		eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
		prog, err := eng.CompileJSON([]byte(`{"types":{` + descriptors + `},"program":[
			{"let":{"key":{"zero":"flows.Key"}}},
			{"return":{"map":{"type":["flows.Key","flows.Child"],"entries":[[
				{"var":"key"},{"literal":{"type":"int64","value":"7"}}
			]]}}}
		]}`))
		if err != nil {
			t.Fatalf("CompileJSON: %v", err)
		}
		if _, err := prog.Run(context.Background(), wflang.RunOptions{}); err == nil {
			t.Fatal("expected descriptor map value type error")
		}
	})
}

func TestDescriptorRecordLiteralHonorsObjectBudget(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{
		Registry: wflang.DefaultRegistry(),
		Budget:   wflang.Budget{MaxObjectKeys: 1},
	})
	prog, err := eng.CompileJSON([]byte(`{
		"types":{"pair":{"id":"pair","language_name":"flows.Pair","kind":"record","fields":[
			{"go_name":"A","json_name":"a","type_id":"string"},
			{"go_name":"B","json_name":"b","type_id":"string"}
		],"fingerprint":""}},
		"program":[{"return":{"struct":["flows.Pair",{
			"A":{"literal":{"type":"string","value":"a"}},
			"B":{"literal":{"type":"string","value":"b"}}
		}]}}]
	}`))
	if err != nil {
		t.Fatalf("CompileJSON: %v", err)
	}
	if _, err := prog.Run(context.Background(), wflang.RunOptions{}); err == nil {
		t.Fatal("expected MaxObjectKeys error")
	}
}
