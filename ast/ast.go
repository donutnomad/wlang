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
// LetBinding is a single (name, type, expr) entry inside a let. When more
// than one binding appears in the same let object the form is "destructuring"
// (LANGUAGE.md §3.4 / TC-231); the bindings are evaluated left-to-right and
// each name becomes a fresh variable in the current scope.
type LetBinding struct {
	Name string
	Type string // optional declared type (TC-232)
	Expr Node
}

type Let struct {
	Base
	// Name/Type/Expr describe a single binding for backward compatibility
	// with code paths that pre-date destructuring let. They mirror Bindings[0]
	// when len(Bindings)==1.
	Name string
	Type string // optional declared type
	Expr Node
	// Bindings is the canonical multi-binding list. parse.go always
	// populates it; consumers should iterate Bindings instead of using
	// Name/Type/Expr directly.
	Bindings []LetBinding
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

// MatchCase is a single arm of a match expression: `when` is the value to
// compare to the scrutinee; `do` is the statement block to evaluate on hit.
type MatchCase struct {
	When Node
	Do   []Node
}

// Match is the multi-way value-equality dispatch expression (§14.2).
//
// Semantics: evaluate Value, then evaluate each Cases[i].When in order
// comparing for runtime equality. The first matching case's Do block is
// executed and its last expression value is returned. If no case matches,
// the Default block (if present) executes; otherwise the result is null.
type Match struct {
	Base
	Value   Node
	Cases   []MatchCase
	Default []Node
}

// Program is a sequence of statements.
type Program struct {
	Lang    string
	Imports []string
	Body    []Node
	// Diagnostics carries non-fatal compile-time messages such as deprecation
	// notices emitted by Normalize (LANGUAGE.md §13.2 / TC-604, TC-882).
	Diagnostics []Diagnostic
	// CompileTrace records the pipeline phase ordering when compilation is
	// run with Trace enabled (LANGUAGE.md §7.1 / TC-600).
	CompileTrace []TraceEvent
}

// TraceEvent records that one compilation phase ran (TC-600).
type TraceEvent struct {
	Phase      string
	Order      int
	DurationUs int64
}

// Severity classifies a Diagnostic.
type Severity string

const (
	// SeverityDeprecation marks legacy syntax that was migrated.
	SeverityDeprecation Severity = "deprecation"
	// SeverityWarning is a non-fatal compiler warning.
	SeverityWarning Severity = "warning"
	// SeverityInfo is a purely informational diagnostic.
	SeverityInfo Severity = "info"
)

// Diagnostic carries a non-fatal compile-time message (LANGUAGE.md §13.2).
type Diagnostic struct {
	Severity Severity
	Code     string
	Path     string
	Message  string
}

// helper for constructing path strings
func NewPath(parent, key string) string {
	if parent == "" {
		return "/" + key
	}
	return parent + "/" + key
}
