// Package errors defines wflang's diagnostic error model (LANGUAGE.md §9).
package errors

import (
	"errors"
	"fmt"
)

// Error code constants (LANGUAGE.md §9.3 + §14).
const (
	CodeJSONDecode         = "E_JSON_DECODE"
	CodeASTShape           = "E_AST_SHAPE"
	CodeSymbol             = "E_SYMBOL"
	CodeType               = "E_TYPE"
	CodeCapability         = "E_CAPABILITY"
	CodeRuntime            = "E_RUNTIME"
	CodeBudget             = "E_BUDGET"
	CodeHost               = "E_HOST"
	CodeNilReceiver        = "E_NIL_RECEIVER"
	CodePanic              = "E_PANIC"
	CodeRoutine            = "E_ROUTINE"
	CodeReadonlyVar        = "E_READONLY_VAR"
	CodeLangVersionConflct = "E_LANG_VERSION_CONFLICT"
	CodeYieldTokenMismatch = "E_YIELD_TOKEN_MISMATCH"
	CodeAmbiguousOverload  = "E_AMBIGUOUS_OVERLOAD"
	CodeOperatorNotFound   = "E_OPERATOR_NOT_FOUND"
	CodeInvalidControlFlow = "E_INVALID_CONTROL_FLOW"
)

// Numeric codes from §2.6. Mapped onto the semantic codes above when emitted.
const (
	E1001 = CodeSymbol   // 函数缺失
	E1002 = CodeType     // 类型匹配失败
	E1003 = CodeSymbol   // 变量缺失
	E1004 = CodeType     // 参数数量错误
	E1005 = CodeASTShape // AST 结构异常
	E1007 = CodeASTShape // typed literal 不支持的类型
)

// LangError is the structured diagnostic (§9.2).
type LangError struct {
	Code     string
	Message  string
	Path     string
	Function string
	Expected string
	Actual   string
	Hint     string
	Cause    error
}

func (e *LangError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("[%s] %s (at %s)", e.Code, e.Message, e.Path)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *LangError) Unwrap() error { return e.Cause }

// Is supports errors.Is comparing LangError by code.
func (e *LangError) Is(target error) bool {
	var t *LangError
	if errors.As(target, &t) {
		return t.Code == e.Code
	}
	return false
}

// New creates a LangError with code and message.
func New(code, message string) *LangError {
	return &LangError{Code: code, Message: message}
}

// Newf formats a LangError message.
func Newf(code, format string, args ...any) *LangError {
	return &LangError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithPath returns a copy with Path set.
func (e *LangError) WithPath(path string) *LangError {
	cp := *e
	cp.Path = path
	return &cp
}

// WithHint returns a copy with Hint set.
func (e *LangError) WithHint(hint string) *LangError {
	cp := *e
	cp.Hint = hint
	return &cp
}

// WithExpectedActual fills the Expected/Actual type names.
func (e *LangError) WithExpectedActual(expected, actual string) *LangError {
	cp := *e
	cp.Expected = expected
	cp.Actual = actual
	return &cp
}

// List aggregates multiple diagnostics (§9.4 多错误聚合).
type List struct {
	Errors []*LangError
}

func (l *List) Error() string {
	if len(l.Errors) == 0 {
		return "no errors"
	}
	if len(l.Errors) == 1 {
		return l.Errors[0].Error()
	}
	return fmt.Sprintf("%d diagnostics: %s ...", len(l.Errors), l.Errors[0].Error())
}

// Add appends a LangError to the list.
func (l *List) Add(e *LangError) { l.Errors = append(l.Errors, e) }

// Err returns the list as error or nil when empty.
func (l *List) Err() error {
	if len(l.Errors) == 0 {
		return nil
	}
	return l
}

// As allows errors.As(err, *LangError) on the first element.
func (l *List) As(target any) bool {
	if len(l.Errors) == 0 {
		return false
	}
	if t, ok := target.(**LangError); ok {
		*t = l.Errors[0]
		return true
	}
	return false
}
