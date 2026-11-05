package registry

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/donutnomad/wlang/types"
)

// out 形参声明为 any（宿主方法用 `out any`）而模板是 record 时，必须走
// map[string]any surrogate：宿主按普通 Go map 解码，commit 再还原成 record。
// 标量目标仍走 materialize 路径（见 TestPrepareOutArgInterfaceScalar）。
func TestPrepareOutArgInterfaceRecordUsesMapSurrogate(t *testing.T) {
	r := New()
	descriptor := types.TypeDescriptor{
		ID:           "flows.ChargeFailure",
		LanguageName: "flows.ChargeFailure",
		Kind:         types.TypeKindRecord,
		Fields: []types.FieldDescriptor{
			{GoName: "Reason", JSONName: "reason", TypeID: types.TString},
		},
	}
	if err := r.BindDescriptors(map[string]types.TypeDescriptor{descriptor.ID: descriptor}); err != nil {
		t.Fatalf("BindDescriptors: %v", err)
	}
	template, err := r.ZeroValue("flows.ChargeFailure", "/test")
	if err != nil {
		t.Fatalf("ZeroValue: %v", err)
	}

	var committed types.Value
	out := &types.OutArg{
		Name: "detail", TypeName: "flows.ChargeFailure", Current: template,
		Commit: func(v types.Value) error { committed = v; return nil },
	}
	rv, prepared, err := prepareOutArg(out, reflect.TypeOf((*any)(nil)).Elem(), nil, r)
	if err != nil {
		t.Fatalf("prepareOutArg: %v", err)
	}
	target, ok := rv.Interface().(*map[string]any)
	if !ok || target == nil {
		t.Fatalf("宿主应收到 *map[string]any, got %T", rv.Interface())
	}
	if err := json.Unmarshal([]byte(`{"reason":"insufficient_funds"}`), target); err != nil {
		t.Fatalf("宿主解码: %v", err)
	}
	if err := prepared.commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if committed.TypeName() != "flows.ChargeFailure" {
		t.Fatalf("commit 应还原 record, got %s", committed.TypeName())
	}
	record, ok := types.AsRecord(committed)
	if !ok {
		t.Fatalf("commit 结果不是 record: %s", committed.TypeName())
	}
	field, ok := record.Field("Reason")
	if !ok || field.Go() != "insufficient_funds" {
		t.Fatalf("record 字段未写回: %#v", record)
	}
}

// 标量目标的既有语义不变：interface target + 标量模板直接 materialize。
func TestPrepareOutArgInterfaceScalar(t *testing.T) {
	r := New()
	template := types.NewValue(types.TString, "")
	var committed types.Value
	out := &types.OutArg{
		Name: "reason", TypeName: types.TString, Current: template,
		Commit: func(v types.Value) error { committed = v; return nil },
	}
	rv, prepared, err := prepareOutArg(out, reflect.TypeOf((*any)(nil)).Elem(), nil, r)
	if err != nil {
		t.Fatalf("prepareOutArg: %v", err)
	}
	target, ok := rv.Interface().(*string)
	if !ok || target == nil {
		t.Fatalf("宿主应收到 *string, got %T", rv.Interface())
	}
	*target = "insufficient_funds"
	if err := prepared.commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if committed.TypeName() != types.TString || committed.Go() != "insufficient_funds" {
		t.Fatalf("标量未写回: %s=%v", committed.TypeName(), committed.Go())
	}
}
