package compiler_test

import (
	"strings"
	"testing"

	"github.com/wflang/wflang/ast"
	"github.com/wflang/wflang/compiler"
	werr "github.com/wflang/wflang/errors"
)

func TestParseFunctionLiteralAndDynamicCall(t *testing.T) {
	src := []byte(`[
		{"let":{"f":{"fn":{
			"params":[["s","string"],["n","int64"]],
			"returns":["string"],
			"do":[{"return":{"var":"s"}}]
		}}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[
			{"literal":{"type":"string","value":"ok"}},
			{"literal":{"type":"int64","value":"1"}}
		]}}}
	]`)
	prog, err := compiler.ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	if len(prog.Body) != 2 {
		t.Fatalf("want 2 statements, got %d", len(prog.Body))
	}
	let, ok := prog.Body[0].(*ast.Let)
	if !ok || len(let.Bindings) != 1 {
		t.Fatalf("want single let, got %T", prog.Body[0])
	}
	fn, ok := let.Bindings[0].Expr.(*ast.FuncLit)
	if !ok {
		t.Fatalf("want FuncLit, got %T", let.Bindings[0].Expr)
	}
	if len(fn.Params) != 2 || fn.Params[0].Name != "s" || fn.Params[1].Type != "int64" {
		t.Fatalf("bad params: %#v", fn.Params)
	}
	if len(fn.Returns) != 1 || fn.Returns[0] != "string" {
		t.Fatalf("bad returns: %#v", fn.Returns)
	}
	ret, ok := prog.Body[1].(*ast.Return)
	if !ok {
		t.Fatalf("want return, got %T", prog.Body[1])
	}
	call, ok := ret.Expr.(*ast.FuncCall)
	if !ok {
		t.Fatalf("want FuncCall, got %T", ret.Expr)
	}
	if len(call.Args) != 2 {
		t.Fatalf("want 2 args, got %d", len(call.Args))
	}
}

func TestParseFunctionLiteralAndCallRejectInvalidShapes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		msg  string
	}{
		{
			name: "fn body must be object",
			src:  `[{"let":{"f":{"fn":[]}}}]`,
			msg:  "fn body must be object",
		},
		{
			name: "params must be array",
			src:  `[{"let":{"f":{"fn":{"params":{},"do":[]}}}}]`,
			msg:  "fn.params must be an array",
		},
		{
			name: "param pair required",
			src:  `[{"let":{"f":{"fn":{"params":[["x"]],"do":[]}}}}]`,
			msg:  "fn.params[0] must be [name,type]",
		},
		{
			name: "param name type required",
			src:  `[{"let":{"f":{"fn":{"params":[["","string"]],"do":[]}}}}]`,
			msg:  "requires non-empty name and type",
		},
		{
			name: "returns must be array",
			src:  `[{"let":{"f":{"fn":{"returns":{},"do":[]}}}}]`,
			msg:  "fn.returns must be an array",
		},
		{
			name: "return type required",
			src:  `[{"let":{"f":{"fn":{"returns":[""],"do":[]}}}}]`,
			msg:  "fn.returns[0] must be a non-empty string",
		},
		{
			name: "call body must be object",
			src:  `[{"return":{"call":[]}}]`,
			msg:  "call body must be object",
		},
		{
			name: "call fn required",
			src:  `[{"return":{"call":{"args":[]}}}]`,
			msg:  "call.fn is required",
		},
		{
			name: "call args must be array",
			src:  `[{"return":{"call":{"fn":{"var":"f"},"args":{}}}}]`,
			msg:  "call.args must be an array",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compiler.ParseProgram([]byte(tc.src))
			assertParseShapeError(t, err, tc.msg)
		})
	}
}

func assertParseShapeError(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want parse error containing %q", msg)
	}
	le, ok := err.(*werr.LangError)
	if !ok {
		t.Fatalf("want LangError, got %T: %v", err, err)
	}
	if le.Code != werr.CodeASTShape {
		t.Fatalf("want %s, got %s", werr.CodeASTShape, le.Code)
	}
	if !strings.Contains(le.Message, msg) {
		t.Fatalf("want message containing %q, got %q", msg, le.Message)
	}
}
