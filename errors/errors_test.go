package errors

import (
	stderrors "errors"
	"testing"
)

// TC-126 / TC-730 LangError carries code/message/path/hint/expected/actual.
func TestLangErrorFields(t *testing.T) {
	e := New(CodeType, "type mismatch").
		WithPath("/program/0").
		WithExpectedActual("int64", "string").
		WithHint("use Len with string")
	if e.Code != CodeType || e.Message != "type mismatch" {
		t.Fatalf("code/message wrong: %+v", e)
	}
	if e.Path != "/program/0" {
		t.Fatalf("path not set: %q", e.Path)
	}
	if e.Expected != "int64" || e.Actual != "string" {
		t.Fatalf("expected/actual not set: %+v", e)
	}
	if e.Hint == "" {
		t.Fatalf("hint not set")
	}
	// errors.Is by code
	other := New(CodeType, "x")
	if !stderrors.Is(e, other) {
		t.Fatalf("errors.Is should match by code")
	}
	if stderrors.Is(e, New(CodeSymbol, "x")) {
		t.Fatalf("errors.Is should not match different codes")
	}
}

// TC-732 多错误聚合
func TestListAggregation(t *testing.T) {
	l := &List{}
	if l.Err() != nil {
		t.Fatalf("empty list should return nil")
	}
	l.Add(New(CodeSymbol, "missing"))
	l.Add(New(CodeType, "bad type"))
	if l.Err() == nil {
		t.Fatalf("non-empty list should return error")
	}
	// As → first LangError
	var first *LangError
	if !stderrors.As(l.Err(), &first) {
		t.Fatalf("errors.As should extract first LangError")
	}
	if first.Code != CodeSymbol {
		t.Fatalf("As returned wrong one: %+v", first)
	}
}

// TC-731 各错误码存在
func TestAllCodesDefined(t *testing.T) {
	codes := []string{
		CodeJSONDecode, CodeASTShape, CodeSymbol, CodeType, CodeCapability,
		CodeRuntime, CodeBudget, CodeHost, CodeNilReceiver, CodePanic, CodeRoutine,
		CodeReadonlyVar, CodeLangVersionConflct, CodeYieldTokenMismatch,
		CodeAmbiguousOverload, CodeOperatorNotFound, CodeInvalidControlFlow,
	}
	for _, c := range codes {
		if c == "" {
			t.Fatalf("empty code in table")
		}
	}
}
