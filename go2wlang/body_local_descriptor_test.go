package go2wlang_test

import (
	"testing"

	"github.com/donutnomad/wlang/go2wlang"
	"github.com/donutnomad/wlang/types"
)

// body-local DTO：局部变量声明的结构体类型必须拿到 descriptor，
// 否则运行期 zero<T> 会报 E_SYMBOL: type not registered。
func TestBodyLocalStructDescriptor(t *testing.T) {
	src := []byte(`package flows
type ChargeFailure struct {
	Reason string ` + "`json:\"reason\"`" + `
}
func Workflow() error {
	var detail ChargeFailure
	other := ChargeFailure{Reason: "insufficient_funds"}
	_ = detail
	_ = other
	return nil
}`)
	result, err := go2wlang.TranslateFileDetailed(src, go2wlang.Options{FuncName: "Workflow"})
	if err != nil {
		t.Fatalf("TranslateFileDetailed: %v", err)
	}
	descriptor, ok := result.Types["flows.ChargeFailure"]
	if !ok {
		t.Fatalf("missing body-local descriptor: %#v", result.Types)
	}
	if descriptor.Kind != types.TypeKindRecord || descriptor.LanguageName != "flows.ChargeFailure" {
		t.Fatalf("unexpected descriptor: %#v", descriptor)
	}
	if len(descriptor.Fields) != 1 || descriptor.Fields[0].JSONName != "reason" {
		t.Fatalf("unexpected fields: %#v", descriptor.Fields)
	}
}

// 标量局部变量不产生 descriptor：只有含程序具名类型的数据形状才登记。
func TestBodyLocalScalarHasNoDescriptor(t *testing.T) {
	src := []byte(`package flows
func Workflow() error {
	var count int64
	name := "x"
	_ = count
	_ = name
	return nil
}`)
	result, err := go2wlang.TranslateFileDetailed(src, go2wlang.Options{FuncName: "Workflow"})
	if err != nil {
		t.Fatalf("TranslateFileDetailed: %v", err)
	}
	for id := range result.Types {
		t.Fatalf("标量不应产生 descriptor: %s", id)
	}
}
