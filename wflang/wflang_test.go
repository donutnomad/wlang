package wflang_test

import (
	"context"
	"testing"

	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/wflang"
)

// Book is a sample domain type.
type Book struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// booksPkg implements FindByID.
func lenImpl(s string) (int64, error) {
	return int64(len(s)), nil
}

func newEngineWithStr(t *testing.T) *wflang.Engine {
	t.Helper()
	reg := wflang.NewRegistry()
	err := reg.BindGoPackage("str", wflang.PackageSpec{
		Functions: []wflang.FuncSpec{
			{GoName: "Len", Impl: lenImpl},
		},
	})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	return wflang.NewEngine(wflang.EngineOptions{Registry: reg})
}

// TC-038 包函数调用：Len(str,"hello") => int64=5
func TestTC038_PkgLen(t *testing.T) {
	eng := newEngineWithStr(t)
	src := []byte(`[{"return":{"Len":[
		{"pkg":"str"},
		{"literal":{"type":"string","value":"hello"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if unwrap1(t, v).(int64) != 5 {
		t.Fatalf("want 5, got %v", v.Go())
	}
}

// TC-081 包名重复注册报错
func TestTC081_DuplicatePackage(t *testing.T) {
	reg := wflang.NewRegistry()
	if err := reg.BindGoPackage("str", wflang.PackageSpec{}); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	if err := reg.BindGoPackage("str", wflang.PackageSpec{}); err == nil {
		t.Fatalf("want duplicate error, got nil")
	}
}

// TC-082 函数名严格大小写
func TestTC082_FuncNameStrictCase(t *testing.T) {
	eng := newEngineWithStr(t)
	src := []byte(`[{"return":{"len":[
		{"pkg":"str"},
		{"literal":{"type":"string","value":"hi"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{})
	if err == nil {
		t.Fatalf("want E_SYMBOL, got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok {
		t.Fatalf("want LangError, got %T", err)
	}
	// Either E_SYMBOL ("function not found in package") or E_OPERATOR_NOT_FOUND.
	if le.Code != werr.CodeSymbol && le.Code != werr.CodeOperatorNotFound {
		t.Fatalf("want E_SYMBOL/E_OPERATOR_NOT_FOUND, got %s", le.Code)
	}
}

// User is a sample domain type with exported and unexported methods.
type User struct {
	Name string `json:"name"`
	Age  int64  `json:"age"`
}

// Run is exported and should be discoverable via AutoBindType.
func (u *User) Run(speed float64) (string, error) {
	return u.Name + " runs", nil
}

// greet is unexported; must not be exposed.
func (u *User) greet() string { return "hi" }

// TC-086 AutoBindType 反射方法集
func TestTC086_AutoBindType(t *testing.T) {
	reg := wflang.NewRegistry()
	if err := reg.AutoBindType((*User)(nil)); err != nil {
		t.Fatalf("auto bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"Run":[
		{"var":"user"},
		{"literal":{"type":"float64","value":"10"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"user": &User{Name: "Alice"}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if unwrap1(t, v).(string) != "Alice runs" {
		t.Fatalf("want %q, got %v", "Alice runs", v.Go())
	}
}

// TC-085 第一个参数为 null 时的方法调用：E_NIL_RECEIVER
func TestTC085_NilReceiver(t *testing.T) {
	reg := wflang.NewRegistry()
	if err := reg.AutoBindType((*User)(nil)); err != nil {
		t.Fatalf("auto bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"Run":[
		{"var":"user"},
		{"literal":{"type":"float64","value":"10"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"user": nil},
	})
	if err == nil {
		t.Fatalf("want E_NIL_RECEIVER, got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok || le.Code != werr.CodeNilReceiver {
		t.Fatalf("want E_NIL_RECEIVER, got %v", err)
	}
}

// TC-083 私有 Go 函数不可调用
func TestTC083_UnexportedMethodHidden(t *testing.T) {
	reg := wflang.NewRegistry()
	if err := reg.AutoBindType((*User)(nil)); err != nil {
		t.Fatalf("auto bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"greet":[{"var":"user"}]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"user": &User{}},
	})
	if err == nil {
		t.Fatalf("want symbol error, got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok {
		t.Fatalf("want LangError, got %T", err)
	}
	if le.Code != werr.CodeSymbol && le.Code != werr.CodeOperatorNotFound {
		t.Fatalf("want symbol error, got %s", le.Code)
	}
}

// TC-152 渐进式执行三段：let -> set -> return
func TestTC152_AppendRun(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{})
	sess, err := eng.NewSession(wflang.SessionOptions{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := sess.AppendRun(context.Background(),
		[]byte(`{"let":{"x":{"literal":{"type":"int64","value":"1"}}}}`)); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if _, err := sess.AppendRun(context.Background(),
		[]byte(`{"set":{"x":{"+":[
			{"var":"x"},{"literal":{"type":"int64","value":"2"}}
		]}}}`)); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	v, err := sess.AppendRun(context.Background(),
		[]byte(`{"return":{"var":"x"}}`))
	if err != nil {
		t.Fatalf("append 3: %v", err)
	}
	if v.Go().(int64) != 3 {
		t.Fatalf("want 3, got %v", v.Go())
	}
}

// TC-153 渐进式：return 之后追加报错
func TestTC153_AppendAfterReturn(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{})
	sess, err := eng.NewSession(wflang.SessionOptions{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := sess.AppendRun(context.Background(),
		[]byte(`{"return":{"literal":{"type":"int64","value":"1"}}}`)); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := sess.AppendRun(context.Background(),
		[]byte(`{"let":{"x":{"literal":{"type":"int64","value":"1"}}}}`)); err == nil {
		t.Fatalf("want error after return, got nil")
	}
}

// TC-080 BindGoPackage 注册并调用 FindByID => *Book
func TestTC080_FindByID(t *testing.T) {
	findByID := func(id int64) (*Book, error) {
		return &Book{ID: id, Title: "War and Peace"}, nil
	}
	reg := wflang.NewRegistry()
	err := reg.BindGoPackage("books", wflang.PackageSpec{
		Functions: []wflang.FuncSpec{
			{GoName: "FindByID", Impl: findByID},
		},
	})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`[{"return":{"FindByID":[
		{"pkg":"books"},
		{"literal":{"type":"int64","value":"1001"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	bk, ok := unwrap1(t, v).(*Book)
	if !ok {
		t.Fatalf("want *Book, got %T", v.Go())
	}
	if bk.ID != 1001 || bk.Title == "" {
		t.Fatalf("unexpected book: %+v", bk)
	}
}

// TC-150 envelope：顶级 API 验证
func TestEnvelopeThroughEngine(t *testing.T) {
	reg := wflang.NewRegistry()
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	src := []byte(`{"lang":"wflang/v1","imports":[],"program":[
		{"return":{"literal":{"type":"int64","value":"42"}}}
	]}`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 42 {
		t.Fatalf("want 42, got %v", v.Go())
	}
}
