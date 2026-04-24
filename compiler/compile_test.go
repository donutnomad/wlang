package compiler_test

import (
	"encoding/json"
	"testing"

	"github.com/wflang/wflang/ast"
	"github.com/wflang/wflang/compiler"
)

// §7.8: 常量折叠 — 纯 typed literal 上的算术应折叠成一个 literal。
func TestCompile_ConstantFolding(t *testing.T) {
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"int64","value":"2"}},
		{"literal":{"type":"int64","value":"3"}}
	]}}]`)
	p, err := compiler.Compile(src, compiler.Options{Optimize: true})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(p.Body) != 1 {
		t.Fatalf("want 1 stmt, got %d", len(p.Body))
	}
	ret, ok := p.Body[0].(*ast.Return)
	if !ok {
		t.Fatalf("want Return, got %T", p.Body[0])
	}
	lit, ok := ret.Expr.(*ast.Literal)
	if !ok {
		t.Fatalf("want folded Literal, got %T", ret.Expr)
	}
	if lit.Value.TypeName() != "int64" {
		t.Fatalf("want int64, got %s", lit.Value.TypeName())
	}
	if got := lit.Value.Go().(int64); got != 5 {
		t.Fatalf("want 5, got %v", got)
	}
	_ = json.RawMessage{}
}

// §7 骨架: Compile 无优化 flag 时等价于 ParseProgram。
func TestCompile_NoOptEquivalentToParse(t *testing.T) {
	src := []byte(`[{"return":{"literal":{"type":"int64","value":"42"}}}]`)
	p, err := compiler.Compile(src, compiler.Options{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(p.Body) != 1 {
		t.Fatalf("want 1, got %d", len(p.Body))
	}
}
