package runtime

import (
	"context"
	"testing"

	"github.com/donutnomad/wlang/compiler"
	"github.com/donutnomad/wlang/types"
)

// runSrc parses and runs a wflang program, returning the program result.
func runSrc(t *testing.T, src string) (types.Value, error) {
	t.Helper()
	prog, err := compiler.ParseProgram([]byte(src))
	if err != nil {
		return types.Value{}, err
	}
	exec := NewExecutor(context.Background(), NewScope(), nil, nil, Budget{})
	return exec.RunProgram(prog)
}

// TC-151 最小程序形态：语句数组
func TestTC151_MinimalReturn(t *testing.T) {
	v, err := runSrc(t, `[{"return":{"literal":{"type":"int64","value":"1"}}}]`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != types.TInt64 {
		t.Fatalf("want int64, got %s", v.TypeName())
	}
	if v.Go().(int64) != 1 {
		t.Fatalf("want 1, got %v", v.Go())
	}
}

// TC-030 字面量加法
func TestTC030_LiteralAdd(t *testing.T) {
	src := `[{"return":{"+":[
		{"literal":{"type":"int64","value":"1"}},
		{"literal":{"type":"int64","value":"2"}}
	]}}]`
	v, err := runSrc(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != types.TInt64 || v.Go().(int64) != 3 {
		t.Fatalf("want int64=3, got %s=%v", v.TypeName(), v.Go())
	}
}

// TC-034 逻辑 and / or / ! 短路
func TestTC034_Logical(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"and-true", `[{"return":{"and":[
			{"literal":{"type":"boolean","value":"true"}},
			{"literal":{"type":"boolean","value":"true"}}]}}]`, true},
		{"and-false-short", `[{"return":{"and":[
			{"literal":{"type":"boolean","value":"false"}},
			{"literal":{"type":"boolean","value":"true"}}]}}]`, false},
		{"or-true-short", `[{"return":{"or":[
			{"literal":{"type":"boolean","value":"true"}},
			{"literal":{"type":"boolean","value":"false"}}]}}]`, true},
		{"or-false", `[{"return":{"or":[
			{"literal":{"type":"boolean","value":"false"}},
			{"literal":{"type":"boolean","value":"false"}}]}}]`, false},
		{"not-true", `[{"return":{"!":[{"literal":{"type":"boolean","value":"false"}}]}}]`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := runSrc(t, tc.src)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if v.TypeName() != types.TBoolean {
				t.Fatalf("want bool, got %s", v.TypeName())
			}
			if v.Go().(bool) != tc.want {
				t.Fatalf("want %v, got %v", tc.want, v.Go())
			}
		})
	}
}

// TC-035 if 表达式（对象形）
func TestTC035_IfExpr(t *testing.T) {
	src := `[{"return":{"if":{
		"cond":{"literal":{"type":"boolean","value":"true"}},
		"then":[{"return":{"literal":{"type":"int64","value":"1"}}}],
		"else":[{"return":{"literal":{"type":"int64","value":"2"}}}]
	}}}]`
	v, err := runSrc(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 1 {
		t.Fatalf("want 1, got %v", v.Go())
	}
}

// TC-036 let / set 基本语义
func TestTC036_LetSet(t *testing.T) {
	src := `[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"set":{"x":{"literal":{"type":"int64","value":"2"}}}},
		{"return":{"var":"x"}}
	]`
	v, err := runSrc(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != types.TInt64 || v.Go().(int64) != 2 {
		t.Fatalf("want int64=2, got %s=%v", v.TypeName(), v.Go())
	}
}

// TC-150 envelope 形态可执行
func TestTC150_Envelope(t *testing.T) {
	src := `{"lang":"wflang/v1","imports":[],"program":[
		{"return":{"literal":{"type":"int64","value":"42"}}}
	]}`
	v, err := runSrc(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 42 {
		t.Fatalf("want 42, got %v", v.Go())
	}
}

// TC-101 string typed literal
func TestTC101_StringLiteral(t *testing.T) {
	src := `[{"return":{"literal":{"type":"string","value":"hello"}}}]`
	v, err := runSrc(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != types.TString || v.Go().(string) != "hello" {
		t.Fatalf("want string=hello, got %s=%v", v.TypeName(), v.Go())
	}
}

// TC-104 null typed literal
func TestTC104_NullLiteral(t *testing.T) {
	src := `[{"return":{"literal":{"type":"null","value":null}}}]`
	v, err := runSrc(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != types.TNull {
		t.Fatalf("want null, got %s", v.TypeName())
	}
	if v.Go() != nil {
		t.Fatalf("want nil Go, got %v", v.Go())
	}
}
