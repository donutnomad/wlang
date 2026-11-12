package registry

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/donutnomad/wlang/types"
)

// node is type Node struct{ Name string; Kids []Node; Parent *Node }.
func nodeDescriptors() map[string]types.TypeDescriptor {
	return map[string]types.TypeDescriptor{
		"x.Node": {ID: "x.Node", LanguageName: "x.Node", Kind: types.TypeKindRecord, Fields: []types.FieldDescriptor{
			{GoName: "Name", JSONName: "name", TypeID: types.TString},
			{GoName: "Kids", JSONName: "kids", TypeID: "[]x.Node"},
			{GoName: "Parent", JSONName: "parent", TypeID: "*x.Node"},
		}},
		"[]x.Node": {ID: "[]x.Node", LanguageName: "array<x.Node>", Kind: types.TypeKindSlice, Elem: "x.Node"},
		"*x.Node":  {ID: "*x.Node", LanguageName: "*x.Node", Kind: types.TypeKindPointer, Elem: "x.Node"},
	}
}

func TestRecursiveDescriptorsBindAndRoundTrip(t *testing.T) {
	r := New()
	if err := r.BindDescriptors(nodeDescriptors()); err != nil {
		t.Fatalf("BindDescriptors: %v", err)
	}
	zero, err := r.ZeroValue("x.Node", "/")
	if err != nil {
		t.Fatalf("ZeroValue: %v", err)
	}
	if raw, err := r.EncodeJSONValue(zero); err != nil || string(raw) != `{"kids":null,"name":"","parent":null}` {
		t.Fatalf("zero encodes as %s, %v", raw, err)
	}
	payload := `{"kids":[{"kids":[{"kids":null,"name":"c","parent":null}],"name":"b","parent":null}],"name":"a","parent":{"kids":null,"name":"p","parent":null}}`
	value, err := r.DecodeJSONValue("x.Node", json.RawMessage(payload))
	if err != nil {
		t.Fatalf("DecodeJSONValue: %v", err)
	}
	raw, err := r.EncodeJSONValue(value)
	if err != nil || string(raw) != payload {
		t.Fatalf("round trip = %s, %v\nwant %s", raw, err, payload)
	}
}

func TestDirectSelfContainmentIsRejected(t *testing.T) {
	r := New()
	err := r.BindDescriptors(map[string]types.TypeDescriptor{
		"x.Loop": {ID: "x.Loop", LanguageName: "x.Loop", Kind: types.TypeKindRecord, Fields: []types.FieldDescriptor{
			{GoName: "Self", JSONName: "self", TypeID: "[1]x.Loop"},
		}},
		"[1]x.Loop": {ID: "[1]x.Loop", LanguageName: "array<x.Loop>", Kind: types.TypeKindArray, Elem: "x.Loop", Length: 1},
	})
	if err == nil || !strings.Contains(err.Error(), "contains itself without a pointer, slice or map") {
		t.Fatalf("BindDescriptors error = %v", err)
	}
}

// A slice and a fixed-length array of one element type share the WLang name
// array<T>; the name resolves to the slice whatever the map order.
func TestSliceAndArrayShareLanguageName(t *testing.T) {
	for i := 0; i < 20; i++ {
		r := New()
		if err := r.BindDescriptors(map[string]types.TypeDescriptor{
			"[]string":  {ID: "[]string", LanguageName: "array<string>", Kind: types.TypeKindSlice, Elem: types.TString},
			"[2]string": {ID: "[2]string", LanguageName: "array<string>", Kind: types.TypeKindArray, Elem: types.TString, Length: 2},
			"[3]string": {ID: "[3]string", LanguageName: "array<string>", Kind: types.TypeKindArray, Elem: types.TString, Length: 3},
		}); err != nil {
			t.Fatalf("BindDescriptors: %v", err)
		}
		if id, ok := r.DescriptorID("array<string>"); !ok || id != "[]string" {
			t.Fatalf("array<string> resolves to %q", id)
		}
	}
	err := New().BindDescriptors(map[string]types.TypeDescriptor{
		"[]string": {ID: "[]string", LanguageName: "array<string>", Kind: types.TypeKindSlice, Elem: types.TString},
		"[]int64":  {ID: "[]int64", LanguageName: "array<string>", Kind: types.TypeKindSlice, Elem: types.TInt64},
	})
	if err == nil || !strings.Contains(err.Error(), "maps to both") {
		t.Fatalf("distinct element types sharing a name: %v", err)
	}
}

// any holds a value of every type: a record field of type any accepts it, and
// a record inside it encodes through its descriptor.
func TestAnyHoldsEveryValue(t *testing.T) {
	r := New()
	descriptors := nodeDescriptors()
	descriptors["x.Box"] = types.TypeDescriptor{ID: "x.Box", LanguageName: "x.Box", Kind: types.TypeKindRecord, Fields: []types.FieldDescriptor{
		{GoName: "Value", JSONName: "value", TypeID: types.TAny},
	}}
	if err := r.BindDescriptors(descriptors); err != nil {
		t.Fatal(err)
	}
	if err := r.ValidateValue(types.TAny, types.NewValue(types.TBoolean, true), "/"); err != nil {
		t.Fatalf("any rejects boolean: %v", err)
	}
	zero, err := r.ZeroValue("x.Box", "/")
	if err != nil {
		t.Fatalf("ZeroValue: %v", err)
	}
	if raw, err := r.EncodeJSONValue(zero); err != nil || string(raw) != `{"value":null}` {
		t.Fatalf("zero box encodes as %s, %v", raw, err)
	}
	node, err := r.DecodeJSONValue("x.Node", json.RawMessage(`{"name":"n","kids":null,"parent":null}`))
	if err != nil {
		t.Fatal(err)
	}
	record, _ := types.AsRecord(node)
	box := types.NewRecord("x.Box", map[string]types.Value{"Value": types.NewValue(types.TAny, []any{record, "s"})})
	raw, err := r.EncodeJSONValue(box)
	if err != nil || string(raw) != `{"value":[{"kids":null,"name":"n","parent":null},"s"]}` {
		t.Fatalf("encode = %s, %v", raw, err)
	}
}

func TestRecordPassesToAnyHostParameter(t *testing.T) {
	record := types.NewRecord("x.Box", map[string]types.Value{})
	rv, err := coerce(record, reflect.TypeFor[any]())
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if _, ok := rv.Interface().(*types.Record); !ok {
		t.Fatalf("host received %T, want *types.Record", rv.Interface())
	}
}
