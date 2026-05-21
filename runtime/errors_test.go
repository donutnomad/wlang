package runtime

import (
	"strings"
	"testing"

	"github.com/donutnomad/wlang/compiler"
	werr "github.com/donutnomad/wlang/errors"
)

// assertCode compiles src and asserts the resulting diagnostic has the given code.
func assertCompileCode(t *testing.T, src string, want string) {
	t.Helper()
	_, err := compiler.ParseProgram([]byte(src))
	if err == nil {
		t.Fatalf("want %s, got no error", want)
	}
	le, ok := err.(*werr.LangError)
	if !ok {
		t.Fatalf("want LangError, got %T: %v", err, err)
	}
	if le.Code != want {
		t.Fatalf("want %s, got %s: %s", want, le.Code, le.Message)
	}
}

// TC-100 裸 JSON 数字非法
func TestTC100_BareNumberRejected(t *testing.T) {
	src := `[{"return":{"+":[{"literal":{"type":"int64","value":"1"}}, 1]}}]`
	assertCompileCode(t, src, werr.CodeASTShape)
}

// TC-124 AST 结构异常 E_AST_SHAPE (E1005)
func TestTC124_BadASTShape(t *testing.T) {
	assertCompileCode(t, `[{"+":"not-array"}]`, werr.CodeASTShape)
}

// TC-125 typed literal 不支持的类型 E_LITERAL (E1007)
func TestTC125_UnknownLiteralType(t *testing.T) {
	src := `[{"return":{"literal":{"type":"unknown_t","value":"x"}}}]`
	assertCompileCode(t, src, werr.CodeASTShape)
}

// TC-105 typed literal value 类型/格式错误：int64 "abc" 立即报错
func TestTC105_LiteralValueInvalid(t *testing.T) {
	src := `[{"return":{"literal":{"type":"int64","value":"abc"}}}]`
	assertCompileCode(t, src, werr.CodeType)
}

// TC-037 操作符调用是 JSONLogic 单键：同对象多键 => E_AST_SHAPE
func TestTC037_MultiKeyCall(t *testing.T) {
	src := `[{"return":{
		"+":[{"literal":{"type":"int64","value":"1"}},{"literal":{"type":"int64","value":"2"}}],
		"-":[{"literal":{"type":"int64","value":"3"}},{"literal":{"type":"int64","value":"1"}}]
	}}]`
	assertCompileCode(t, src, werr.CodeASTShape)
}

// TC-126 LangError 携带 path / hint：path 应是 JSON Pointer
func TestTC126_LangErrorPath(t *testing.T) {
	src := `[{"return":{"literal":{"type":"int64","value":"abc"}}}]`
	_, err := compiler.ParseProgram([]byte(src))
	if err == nil {
		t.Fatalf("want error")
	}
	le := err.(*werr.LangError)
	if !strings.HasPrefix(le.Path, "/") {
		t.Fatalf("path should be JSON Pointer, got %q", le.Path)
	}
}
