package wflang

import (
	"encoding/json"

	"github.com/donutnomad/wlang/compiler"
	werr "github.com/donutnomad/wlang/errors"
)

// ConfigBuilder builds JSONLogic AST trees programmatically (LANGUAGE.md §5.6 / §11).
// Node fluent constructors produce plain map[string]any / []any trees that
// marshal into the exact JSONLogic operator shapes expected by CompileJSON.
type ConfigBuilder struct {
	reg *Registry
}

// NewConfigBuilder creates a ConfigBuilder bound to a Registry (used for
// optional symbol/type checks in later iterations).
func NewConfigBuilder(reg *Registry) *ConfigBuilder {
	return &ConfigBuilder{reg: reg}
}

// Node is an opaque AST fragment produced by the builder.
type Node any

// Program starts a new program body.
func (b *ConfigBuilder) Program() *ProgramBuilder {
	return &ProgramBuilder{b: b, stmts: []Node{}}
}

// Pkg produces a `{"pkg":"name"}` receiver node.
func (b *ConfigBuilder) Pkg(name string) Node {
	return map[string]any{"pkg": name}
}

// Var produces a `{"var":"name"}` node.
func (b *ConfigBuilder) Var(name string) Node {
	return map[string]any{"var": name}
}

// Lit produces a `{"literal":{"type":t,"value":v}}` typed literal node.
func (b *ConfigBuilder) Lit(typ string, value any) Node {
	return map[string]any{"literal": map[string]any{"type": typ, "value": value}}
}

// Call produces a JSONLogic operator call: {op: [recv, arg1, ...]}.
func (b *ConfigBuilder) Call(recv Node, op string, args ...Node) Node {
	all := make([]any, 0, 1+len(args))
	all = append(all, recv)
	for _, a := range args {
		all = append(all, a)
	}
	return map[string]any{op: all}
}

// CallE is the validating variant of Call (LANGUAGE.md §5.6 / TC-464). When
// the builder is bound to a Registry, CallE rejects operators that are
// neither builtin nor registered, returning an E_SYMBOL error before the
// program ever reaches CompileJSON.
func (b *ConfigBuilder) CallE(recv Node, op string, args ...Node) (Node, error) {
	if !compiler.IsBuiltinOperator(op) && b.reg != nil && !b.reg.HasOperator(op) {
		return nil, werr.Newf(werr.CodeSymbol,
			"builder: unknown operator %q (not a builtin and not registered)", op)
	}
	return b.Call(recv, op, args...), nil
}

// ProgramBuilder accumulates statements for a program body.
type ProgramBuilder struct {
	b     *ConfigBuilder
	stmts []Node
}

// Let adds a `let` statement.
func (p *ProgramBuilder) Let(name string, expr Node) *ProgramBuilder {
	p.stmts = append(p.stmts, map[string]any{
		"let": map[string]any{name: expr},
	})
	return p
}

// Set adds a `set` statement.
func (p *ProgramBuilder) Set(name string, expr Node) *ProgramBuilder {
	p.stmts = append(p.stmts, map[string]any{
		"set": map[string]any{name: expr},
	})
	return p
}

// Return adds a `return` statement.
func (p *ProgramBuilder) Return(expr Node) *ProgramBuilder {
	p.stmts = append(p.stmts, map[string]any{
		"return": expr,
	})
	return p
}

// JSON marshals the program body to a JSON array of statements.
func (p *ProgramBuilder) JSON() ([]byte, error) {
	out, err := json.Marshal(p.stmts)
	if err != nil {
		return nil, werr.Newf(werr.CodeJSONDecode, "builder marshal: %v", err)
	}
	return out, nil
}

// Nodes returns the accumulated statement nodes (for direct embedding).
func (p *ProgramBuilder) Nodes() []Node { return p.stmts }
