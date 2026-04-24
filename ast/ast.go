// Package ast defines wflang's AST (LANGUAGE.md §3, §7.3).
// Every node carries a JSON Pointer so diagnostics can locate the source.
package ast

import "github.com/wflang/wflang/types"

// Node is the AST interface. All nodes expose their JSON Pointer path.
type Node interface {
	Path() string
}

// Base embeds into concrete node types and exposes JSON Pointer.
type Base struct{ P string }

// Path returns this node's JSON Pointer.
func (b Base) Path() string { return b.P }

// ---------- Expressions ----------

// Literal is a typed literal (§2.5.1).
type Literal struct {
	Base
	Value types.Value
}

// Var reads a path from the current scope (§2.2, §3.6).
// Name holds the dot-separated path (first segment is the root variable name).
type Var struct {
	Base
	Name    string
	Default Node // optional default node when path missing (may be nil)
}

// Pkg represents a package receiver `{"pkg":"name"}`.
type Pkg struct {
	Base
	Name string
}

// Call is a JSONLogic operator invocation (§3.2).
// Key holds the operator name (e.g. "+", "Len", "Run").
// Args is the argument list. For method/package calls the first arg is the receiver.
type Call struct {
	Base
	Op   string
	Args []Node
}

// IfExpr is the expression form of if (§16.4).
type IfExpr struct {
	Base
	Cond Node
	Then []Node // statements
	Else []Node
}

// Array is an array<T> literal.
type Array struct {
	Base
	Elem  string // element type name
	Items []Node
}

// ---------- Statements ----------

// Let defines a local variable.
type Let struct {
	Base
	Name string
	Type string // optional declared type
	Expr Node
}

// Set assigns to an existing variable (inner-most search outwards).
type Set struct {
	Base
	Name string
	Expr Node
}

// Return ends the program with a value.
type Return struct {
	Base
	Expr Node
}

// IfStmt is the block form of if (also used by if expression statements).
type IfStmt struct {
	Base
	Cond Node
	Then []Node
	Else []Node
}

// Foreach iterates an array.
type Foreach struct {
	Base
	Target Node
	As     string
	Index  string // optional
	Do     []Node
}

// Fori integer range loop.
type Fori struct {
	Base
	Var  string
	From Node
	To   Node
	Step Node // optional
	Do   []Node
}

// Break and Continue are control flow statements.
type Break struct{ Base }
type Continue struct{ Base }

// Panic raises a runtime panic.
type Panic struct {
	Base
	Expr Node
}

// ExprStmt discards a value (§3.3 expr).
type ExprStmt struct {
	Base
	Expr Node
}

// Routine is a single host-call launched in a goroutine (§3.3 routine).
type Routine struct {
	Base
	Call *Call
}

// Try captures errors raised inside Do and exposes them as a `error` typed
// value bound to Bind in the Catch block (LANGUAGE.md §9.1 / §9.1.1).
type Try struct {
	Base
	Do    []Node
	Bind  string
	Catch []Node
}

// Program is a sequence of statements.
type Program struct {
	Lang    string
	Imports []string
	Body    []Node
}

// helper for constructing path strings
func NewPath(parent, key string) string {
	if parent == "" {
		return "/" + key
	}
	return parent + "/" + key
}
