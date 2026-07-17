package registry

import (
	"encoding/json"
	"errors"
	"testing"

	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/types"
)

func TestDescriptorCodecRoundTrip(t *testing.T) {
	r := New()
	bindCodecDescriptors(t, r)

	value, err := r.DecodeJSONValue("flows.Result", json.RawMessage(`{
		"record_id":"rec-7",
		"count":18446744073709551615,
		"child":{"name":"nested"},
		"items":[{"name":"first"}],
		"by_key":{"primary":{"name":"mapped"}},
		"ignored":"discarded",
		"future_field":true
	}`))
	if err != nil {
		t.Fatalf("DecodeJSONValue: %v", err)
	}
	record, ok := types.AsRecord(value)
	if !ok {
		t.Fatalf("expected record, got %s (%T)", value.TypeName(), value.Go())
	}
	count, ok := record.Field("Count")
	if !ok || count.Go() != uint64(18446744073709551615) {
		t.Fatalf("uint64 precision lost: %#v", count.Go())
	}
	status, ok := record.Field("Status")
	if !ok || status.Go() != "" {
		t.Fatalf("missing field must use its zero value: %#v", status.Go())
	}

	raw, err := r.EncodeJSONValue(value)
	if err != nil {
		t.Fatalf("EncodeJSONValue: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal encoded value: %v", err)
	}
	if string(got["count"]) != "18446744073709551615" {
		t.Fatalf("uint64 encoding mismatch: %s", got["count"])
	}
	if _, present := got["status"]; present {
		t.Fatalf("omitempty status was encoded: %s", raw)
	}
	if _, present := got["ignored"]; present {
		t.Fatalf("ignored field was encoded: %s", raw)
	}
}

func TestDescriptorCodecNullability(t *testing.T) {
	r := New()
	bindCodecDescriptors(t, r)

	for _, typeName := range []string{"*flows.Child", "array<flows.Child>", "map<flows.Key,flows.Child>", types.TAny} {
		value, err := r.DecodeJSONValue(typeName, json.RawMessage(`null`))
		if err != nil {
			t.Fatalf("DecodeJSONValue(%s): %v", typeName, err)
		}
		if value.Go() != nil {
			t.Fatalf("DecodeJSONValue(%s) = %#v, want nil", typeName, value.Go())
		}
	}

	value, err := r.DecodeJSONValue("flows.Result", json.RawMessage(`null`))
	if err != nil {
		t.Fatalf("DecodeJSONValue(record null): %v", err)
	}
	record, ok := types.AsRecord(value)
	if !ok {
		t.Fatalf("record null must decode as a zero record, got %T", value.Go())
	}
	field, _ := record.Field("RecordID")
	if field.Go() != "" {
		t.Fatalf("zero record field = %#v", field.Go())
	}
}

func TestDescriptorCodecRejectsOverflowAndWrongShape(t *testing.T) {
	r := New()
	bindCodecDescriptors(t, r)

	if _, err := r.DecodeJSONValue(types.TInt8, json.RawMessage(`128`)); err == nil {
		t.Fatal("expected int8 overflow error")
	}
	if _, err := r.DecodeJSONValue("flows.Result", json.RawMessage(`[]`)); err == nil {
		t.Fatal("expected record shape error")
	}
	if _, err := r.DecodeJSONValue("map<flows.Key,flows.Child>", json.RawMessage(`{"x":1}`)); err == nil {
		t.Fatal("expected nested record shape error")
	}
}

func TestRegistryOwnsDescriptorCopies(t *testing.T) {
	r := New()
	descriptor := types.TypeDescriptor{
		ID: "flows.Result", LanguageName: "flows.Result", Kind: types.TypeKindRecord,
		Fields: []types.FieldDescriptor{{GoName: "Value", JSONName: "value", TypeID: types.TString}},
	}
	descriptor.Fingerprint = types.DescriptorFingerprint(descriptor)
	descriptors := map[string]types.TypeDescriptor{"flows.Result": descriptor}
	if err := r.BindDescriptors(descriptors); err != nil {
		t.Fatalf("BindDescriptors: %v", err)
	}
	descriptors["flows.Result"].Fields[0].JSONName = "mutated_input"
	got, _ := r.Descriptor("flows.Result")
	got.Fields[0].JSONName = "mutated_output"
	again, _ := r.Descriptor("flows.Result")
	if again.Fields[0].JSONName != "value" {
		t.Fatalf("registry descriptor was mutated through an external slice: %#v", again.Fields)
	}
}

func TestBindDescriptorsRejectsUnsafeArrayLengths(t *testing.T) {
	tests := []struct {
		name   string
		length int64
		code   string
	}{
		{name: "negative", length: -1, code: werr.CodeASTShape},
		{name: "expanded zero budget", length: maxDescriptorZeroValues + 1, code: werr.CodeBudget},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := New()
			err := r.BindDescriptors(map[string]types.TypeDescriptor{
				"items": {
					ID: "items", LanguageName: "array<string>", Kind: types.TypeKindArray,
					Elem: types.TString, Length: test.length,
				},
			})
			var langErr *werr.LangError
			if !errors.As(err, &langErr) || langErr.Code != test.code {
				t.Fatalf("BindDescriptors error = %v, want %s", err, test.code)
			}
		})
	}
}

func TestBindDescriptorsBudgetsNestedArrayZeroExpansion(t *testing.T) {
	r := New()
	err := r.BindDescriptors(map[string]types.TypeDescriptor{
		"inner": {
			ID: "inner", LanguageName: "array<string>", Kind: types.TypeKindArray,
			Elem: types.TString, Length: 2048,
		},
		"outer": {
			ID: "outer", LanguageName: "array<array<string>>", Kind: types.TypeKindArray,
			Elem: "inner", Length: 2048,
		},
	})
	var langErr *werr.LangError
	if !errors.As(err, &langErr) || langErr.Code != werr.CodeBudget {
		t.Fatalf("BindDescriptors error = %v, want %s", err, werr.CodeBudget)
	}
}

func TestEncodeJSONValueRejectsWrongBuiltinCarrier(t *testing.T) {
	r := New()
	if _, err := r.EncodeJSONValue(types.NewValue(types.TInt8, int64(127))); err == nil {
		t.Fatal("expected int8 carrier type error")
	}
	if _, err := r.EncodeJSONValue(types.NewValue(types.TString, int64(7))); err == nil {
		t.Fatal("expected string carrier type error")
	}
}

func TestEncodeJSONValueValidatesNestedAliasSliceAndMap(t *testing.T) {
	r := New()
	bindCodecDescriptors(t, r)
	value, err := r.DecodeJSONValue("flows.Result", json.RawMessage(`{
		"record_id":"rec-typed",
		"count":7,
		"child":{"name":"nested"},
		"items":[{"name":"slice"}],
		"by_key":{"primary":{"name":"map"}}
	}`))
	if err != nil {
		t.Fatalf("DecodeJSONValue: %v", err)
	}
	if _, err := r.EncodeJSONValue(value); err != nil {
		t.Fatalf("EncodeJSONValue valid graph: %v", err)
	}

	record, _ := types.AsRecord(value)
	items, _ := record.Field("Items")
	carriers := items.Go().([]any)
	child := carriers[0].(*types.Record)
	child.SetField("Name", types.NewValue(types.TInt64, int64(9)))
	if _, err := r.EncodeJSONValue(value); err == nil {
		t.Fatal("expected nested slice record field type error")
	}
}

func TestGenericBuiltinCollectionCodecRoundTrip(t *testing.T) {
	r := New()

	arrayValue, err := r.DecodeJSONValue("array<uint64>", json.RawMessage(`[1,18446744073709551615]`))
	if err != nil {
		t.Fatalf("DecodeJSONValue array<uint64>: %v", err)
	}
	if got, ok := arrayValue.Go().([]uint64); !ok || len(got) != 2 || got[1] != ^uint64(0) {
		t.Fatalf("array<uint64> carrier = %#v (%T)", arrayValue.Go(), arrayValue.Go())
	}
	arrayJSON, err := r.EncodeJSONValue(arrayValue)
	if err != nil {
		t.Fatalf("EncodeJSONValue array<uint64>: %v", err)
	}
	if string(arrayJSON) != `[1,18446744073709551615]` {
		t.Fatalf("array<uint64> JSON = %s", arrayJSON)
	}

	mapValue, err := r.DecodeJSONValue("map<string,string>", json.RawMessage(`{"primary":"ready"}`))
	if err != nil {
		t.Fatalf("DecodeJSONValue map<string,string>: %v", err)
	}
	if got, ok := mapValue.Go().(map[string]string); !ok || got["primary"] != "ready" {
		t.Fatalf("map<string,string> carrier = %#v (%T)", mapValue.Go(), mapValue.Go())
	}
	mapJSON, err := r.EncodeJSONValue(mapValue)
	if err != nil {
		t.Fatalf("EncodeJSONValue map<string,string>: %v", err)
	}
	if string(mapJSON) != `{"primary":"ready"}` {
		t.Fatalf("map<string,string> JSON = %s", mapJSON)
	}
}

func TestGenericBuiltinCollectionCodecPreservesNull(t *testing.T) {
	r := New()
	for _, typeName := range []string{"array<uint64>", "map<string,string>"} {
		value, err := r.DecodeJSONValue(typeName, json.RawMessage(`null`))
		if err != nil {
			t.Fatalf("DecodeJSONValue(%s): %v", typeName, err)
		}
		if value.TypeName() != typeName || value.Go() != nil {
			t.Fatalf("DecodeJSONValue(%s) = %s %#v", typeName, value.TypeName(), value.Go())
		}
		raw, err := r.EncodeJSONValue(value)
		if err != nil {
			t.Fatalf("EncodeJSONValue(%s): %v", typeName, err)
		}
		if string(raw) != "null" {
			t.Fatalf("EncodeJSONValue(%s) = %s", typeName, raw)
		}
	}
}

func TestGenericBuiltinCollectionCodecRejectsWrongElementCarrier(t *testing.T) {
	r := New()
	value := types.NewValue("array<uint64>", []any{int64(1)})
	if _, err := r.EncodeJSONValue(value); err == nil {
		t.Fatal("expected array<uint64> element carrier error")
	}
}

func TestGenericAnyMapDecodesLegacyNumericCarriers(t *testing.T) {
	r := New()
	value, err := r.DecodeJSONValue("map<string,any>", json.RawMessage(`{
		"count":7,
		"max":18446744073709551615,
		"ratio":1.5,
		"nested":{"attempt":2},
		"items":[3]
	}`))
	if err != nil {
		t.Fatalf("DecodeJSONValue map<string,any>: %v", err)
	}
	got, ok := value.Go().(map[string]any)
	if !ok {
		t.Fatalf("map<string,any> carrier = %T", value.Go())
	}
	if got["count"] != int64(7) {
		t.Fatalf("count = %#v (%T)", got["count"], got["count"])
	}
	if got["max"] != ^uint64(0) {
		t.Fatalf("max = %#v (%T)", got["max"], got["max"])
	}
	if got["ratio"] != float64(1.5) {
		t.Fatalf("ratio = %#v (%T)", got["ratio"], got["ratio"])
	}
	if nested := got["nested"].(map[string]any); nested["attempt"] != int64(2) {
		t.Fatalf("nested = %#v", nested)
	}
	if items := got["items"].([]any); items[0] != int64(3) {
		t.Fatalf("items = %#v", items)
	}
	if _, err := r.EncodeJSONValue(value); err != nil {
		t.Fatalf("EncodeJSONValue map<string,any>: %v", err)
	}
}

func bindCodecDescriptors(t *testing.T, r *Registry) {
	t.Helper()
	descriptors := map[string]types.TypeDescriptor{
		"flows.Key": {
			ID: "flows.Key", LanguageName: "flows.Key", Kind: types.TypeKindAlias, Underlying: types.TString,
		},
		"flows.Child": {
			ID: "flows.Child", LanguageName: "flows.Child", Kind: types.TypeKindRecord,
			Fields: []types.FieldDescriptor{{GoName: "Name", JSONName: "name", TypeID: types.TString}},
		},
		"*flows.Child": {
			ID: "*flows.Child", LanguageName: "*flows.Child", Kind: types.TypeKindPointer, Elem: "flows.Child",
		},
		"[]flows.Child": {
			ID: "[]flows.Child", LanguageName: "array<flows.Child>", Kind: types.TypeKindSlice, Elem: "flows.Child",
		},
		"map[flows.Key]flows.Child": {
			ID: "map[flows.Key]flows.Child", LanguageName: "map<flows.Key,flows.Child>", Kind: types.TypeKindMap,
			Key: "flows.Key", Elem: "flows.Child",
		},
		"flows.Result": {
			ID: "flows.Result", LanguageName: "flows.Result", Kind: types.TypeKindRecord,
			Fields: []types.FieldDescriptor{
				{GoName: "RecordID", JSONName: "record_id", TypeID: types.TString},
				{GoName: "Status", JSONName: "status", TypeID: types.TString, OmitEmpty: true},
				{GoName: "Count", JSONName: "count", TypeID: types.TUint64},
				{GoName: "Child", JSONName: "child", TypeID: "*flows.Child"},
				{GoName: "Items", JSONName: "items", TypeID: "[]flows.Child"},
				{GoName: "ByKey", JSONName: "by_key", TypeID: "map[flows.Key]flows.Child"},
				{GoName: "Ignored", JSONName: "-", TypeID: types.TString},
			},
		},
	}
	for id, descriptor := range descriptors {
		descriptor.Fingerprint = types.DescriptorFingerprint(descriptor)
		descriptors[id] = descriptor
	}
	if err := r.BindDescriptors(descriptors); err != nil {
		t.Fatalf("BindDescriptors: %v", err)
	}
}
