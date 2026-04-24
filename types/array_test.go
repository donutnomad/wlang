package types

import (
	"reflect"
	"testing"
)

// TC-014 array<T> 元素类型保持
func TestArrayType(t *testing.T) {
	v, err := NewArray(TInt64, []Value{
		mustLit(t, TInt64, "1"),
		mustLit(t, TInt64, "2"),
		mustLit(t, TInt64, "3"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.TypeName() != "array<int64>" {
		t.Fatalf("type: %s", v.TypeName())
	}
	arr, ok := v.Go().([]int64)
	if !ok {
		t.Fatalf("Go(): %T", v.Go())
	}
	if len(arr) != 3 || arr[0] != 1 || arr[2] != 3 {
		t.Fatalf("contents: %v", arr)
	}
}

// TC-904 array<int64> 含 string 元素 -> E_TYPE
func TestArrayElementCheck(t *testing.T) {
	_, err := NewArray(TInt64, []Value{
		mustLit(t, TString, "abc"),
	})
	if err == nil {
		t.Fatalf("expected E_TYPE when element type mismatches")
	}
}

// TC-016 自动宿主类型名
type book struct{}

func TestAutoHostTypeName(t *testing.T) {
	got := AutoHostTypeName(reflect.TypeOf(&book{}))
	want := "*github.com/wflang/wflang/types.book"
	if got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
	got2 := AutoHostTypeName(reflect.TypeOf(book{}))
	want2 := "github.com/wflang/wflang/types.book"
	if got2 != want2 {
		t.Fatalf("non-pointer: got=%q want=%q", got2, want2)
	}
}

// TC-321 tuple 类型名
func TestTupleType(t *testing.T) {
	got := TupleType([]string{"*p.Book", "int64", "boolean", "string"})
	want := "tuple<*p.Book,int64,boolean,string>"
	if got != want {
		t.Fatalf("got=%q", got)
	}
}

func mustLit(t *testing.T, typ, raw string) Value {
	t.Helper()
	v, err := NewLiteral(typ, raw)
	if err != nil {
		t.Fatalf("mustLit(%s,%s): %v", typ, raw, err)
	}
	return v
}
