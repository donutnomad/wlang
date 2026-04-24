package wflang_test

import (
	"context"
	"testing"

	"github.com/wflang/wflang/wflang"
)

// to.String: 数字转字符串
func TestStdlib_ToString(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"String":[
		{"pkg":"to"},
		{"literal":{"type":"int64","value":"42"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "42" {
		t.Fatalf("want \"42\", got %v", v.Go())
	}
}

// to.Int64: 字符串转 int64
func TestStdlib_ToInt64(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"Int64":[
		{"pkg":"to"},
		{"literal":{"type":"string","value":"123"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 123 {
		t.Fatalf("want int64=123, got %v", v.Go())
	}
}

// to.Float64: 字符串转 float64
func TestStdlib_ToFloat64(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"Float64":[
		{"pkg":"to"},
		{"literal":{"type":"string","value":"3.14"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(float64) != 3.14 {
		t.Fatalf("want 3.14, got %v", v.Go())
	}
}

// val.TypeOf 返回语言类型名
func TestStdlib_ValTypeOf(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"TypeOf":[
		{"pkg":"val"},
		{"literal":{"type":"int64","value":"1"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "int64" {
		t.Fatalf("want \"int64\", got %v", v.Go())
	}
}

// val.IsEmpty: 空字符串判断
func TestStdlib_ValIsEmptyString(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"IsEmpty":[
		{"pkg":"val"},
		{"literal":{"type":"string","value":""}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !v.Go().(bool) {
		t.Fatalf("want true, got %v", v.Go())
	}
}

// str.Format: fmt.Sprintf 风格模板格式化
func TestStdlib_StrFormat(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"Format":[
		{"pkg":"str"},
		{"literal":{"type":"string","value":"hello %s, %d"}},
		{"literal":{"type":"string","value":"world"}},
		{"literal":{"type":"int64","value":"7"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "hello world, 7" {
		t.Fatalf("want \"hello world, 7\", got %v", v.Go())
	}
}

// arr.Contains 包含判断
func TestStdlib_ArrContains(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"Contains":[
		{"pkg":"arr"},
		{"var":"xs"},
		{"literal":{"type":"int64","value":"2"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{"xs": []any{int64(1), int64(2), int64(3)}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !v.Go().(bool) {
		t.Fatalf("want true, got %v", v.Go())
	}
}

// path.Get: 读取 map 嵌套路径
func TestStdlib_PathGet(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"Get":[
		{"pkg":"path"},
		{"var":"data"},
		{"literal":{"type":"string","value":"user.name"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{
			"data": map[string]any{"user": map[string]any{"name": "alice"}},
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "alice" {
		t.Fatalf("want \"alice\", got %v", v.Go())
	}
}

// path.Has: 路径存在
func TestStdlib_PathHas(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"Has":[
		{"pkg":"path"},
		{"var":"data"},
		{"literal":{"type":"string","value":"user.age"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{
			"data": map[string]any{"user": map[string]any{"name": "alice"}},
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(bool) {
		t.Fatalf("want false (user.age missing), got %v", v.Go())
	}
}

// json.Stringify: 值序列化
func TestStdlib_JSONStringify(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	src := []byte(`[{"return":{"Stringify":[
		{"pkg":"json"},
		{"literal":{"type":"int64","value":"42"}}
	]}}]`)
	prog, err := eng.CompileJSON(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "42" {
		t.Fatalf("want \"42\", got %v", v.Go())
	}
}
