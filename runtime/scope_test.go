package runtime

import (
	"context"
	"testing"

	"github.com/donutnomad/wlang/compiler"
	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/types"
)

// runWith injects top-level variables then parses and runs src.
func runWith(t *testing.T, src string, inject func(s *Scope)) (types.Value, error) {
	t.Helper()
	prog, err := compiler.ParseProgram([]byte(src))
	if err != nil {
		return types.Value{}, err
	}
	s := NewScope()
	if inject != nil {
		inject(s)
	}
	exec := NewExecutor(context.Background(), s, nil, nil, Budget{})
	return exec.RunProgram(prog)
}

// TC-031 var 读取嵌套路径
func TestTC031_VarPath(t *testing.T) {
	src := `[{"return":{"==":[
		{"var":"user.status"},
		{"literal":{"type":"string","value":"active"}}
	]}}]`
	v, err := runWith(t, src, func(s *Scope) {
		s.LetReadOnly("user", types.NewValue("map[string]any",
			map[string]any{"name": "alice", "status": "active"}))
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !v.Go().(bool) {
		t.Fatalf("want true, got %v", v.Go())
	}
}

// TC-032 var 缺省值（路径缺失）
func TestTC032_VarDefault(t *testing.T) {
	src := `[{"return":{"var":["user.name",
		{"literal":{"type":"string","value":"anonymous"}}
	]}}]`
	v, err := runWith(t, src, func(s *Scope) {
		s.LetReadOnly("user", types.NewValue("map[string]any", map[string]any{}))
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "anonymous" {
		t.Fatalf("want anonymous, got %v", v.Go())
	}
}

// TC-052 LookupPath 支持 struct json tag
func TestTC052_StructJSONTag(t *testing.T) {
	type User struct {
		Name string `json:"name"`
	}
	src := `[{"return":{"var":"u.name"}}]`
	v, err := runWith(t, src, func(s *Scope) {
		s.LetReadOnly("u", types.NewValue("runtime.User", User{Name: "a"}))
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "a" {
		t.Fatalf("want a, got %v", v.Go())
	}
}

// TC-053 LookupPath 支持数组下标
func TestTC053_ArrayIndex(t *testing.T) {
	src := `[{"return":{"var":"items.1"}}]`
	v, err := runWith(t, src, func(s *Scope) {
		s.LetReadOnly("items", types.NewValue("[]int64", []int64{10, 20, 30}))
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 20 {
		t.Fatalf("want 20, got %v", v.Go())
	}
}

// TC-054 顶级变量默认只读
func TestTC054_TopLevelReadOnly(t *testing.T) {
	src := `[{"set":{"input":{"literal":{"type":"int64","value":"2"}}}}]`
	_, err := runWith(t, src, func(s *Scope) {
		s.LetReadOnly("input", types.NewValue(types.TInt64, int64(1)))
	})
	if err == nil {
		t.Fatalf("want E_READONLY_VAR, got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok {
		t.Fatalf("want LangError, got %T: %v", err, err)
	}
	if le.Code != werr.CodeReadonlyVar {
		t.Fatalf("want E_READONLY_VAR, got %s", le.Code)
	}
}

// TC-055 顶级变量可显式声明可写
func TestTC055_TopLevelWritable(t *testing.T) {
	src := `[
		{"set":{"counter":{"literal":{"type":"int64","value":"1"}}}},
		{"return":{"var":"counter"}}
	]`
	v, err := runWith(t, src, func(s *Scope) {
		s.LetWritable("counter", types.NewValue(types.TInt64, int64(0)))
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 1 {
		t.Fatalf("want 1, got %v", v.Go())
	}
}

// TC-050 PushScope / PopScope 嵌套：内层 let 不影响外层
func TestTC050_ScopePopDiscardsInner(t *testing.T) {
	// Outer block declares x, inner if.then also declares x locally (shadow)
	// After exiting inner, outer x is unchanged.
	src := `[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"if":{
			"cond":{"literal":{"type":"boolean","value":"true"}},
			"then":[{"let":{"x":{"literal":{"type":"int64","value":"99"}}}}],
			"else":[]
		}},
		{"return":{"var":"x"}}
	]`
	v, err := runWith(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 1 {
		t.Fatalf("want 1 (outer x untouched), got %v", v.Go())
	}
}

// TC-051 SetVar 向外查找
func TestTC051_SetOuter(t *testing.T) {
	src := `[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"if":{
			"cond":{"literal":{"type":"boolean","value":"true"}},
			"then":[{"set":{"x":{"literal":{"type":"int64","value":"2"}}}}],
			"else":[]
		}},
		{"return":{"var":"x"}}
	]`
	v, err := runWith(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 2 {
		t.Fatalf("want 2, got %v", v.Go())
	}
}
