package go2wlang

import (
	"fmt"
	"go/ast"
	"go/token"
)

// DiagnosticError reports Go syntax that cannot be translated to wlang.
type DiagnosticError struct {
	Filename string
	Line     int
	Column   int
	Node     string
	Reason   string
	Hint     string
}

func (e *DiagnosticError) Error() string {
	loc := e.Filename
	if e.Line > 0 {
		loc = fmt.Sprintf("%s:%d:%d", loc, e.Line, e.Column)
	}
	msg := fmt.Sprintf("%s: unsupported %s: %s", loc, e.Node, e.Reason)
	if e.Hint != "" {
		msg += "; " + e.Hint
	}
	return msg
}

func diagnostic(fset *token.FileSet, n ast.Node, reason, hint string) *DiagnosticError {
	pos := token.Position{}
	if n != nil {
		pos = fset.Position(n.Pos())
	}
	return &DiagnosticError{
		Filename: pos.Filename,
		Line:     pos.Line,
		Column:   pos.Column,
		Node:     fmt.Sprintf("%T", n),
		Reason:   reason,
		Hint:     hint,
	}
}
