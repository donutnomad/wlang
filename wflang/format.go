package wflang

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/wflang/wflang/ast"
	"github.com/wflang/wflang/compiler"
	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/types"
)

// FormatJSON pretty-prints a wflang JSON document with 2-space indent and
// stable key ordering (LANGUAGE.md §12.2). Objects are recursively sorted by
// key; arrays keep their original order. The output is always valid JSON.
func FormatJSON(src []byte) ([]byte, error) {
	var tree any
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return nil, werr.Newf(werr.CodeJSONDecode, "format: %v", err)
	}
	var buf bytes.Buffer
	if err := writeSorted(&buf, tree, 0); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeSorted(buf *bytes.Buffer, v any, indent int) error {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			buf.WriteString("{}")
			return nil
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
			writeIndent(buf, indent+1)
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteString(": ")
			if err := writeSorted(buf, x[k], indent+1); err != nil {
				return err
			}
		}
		buf.WriteByte('\n')
		writeIndent(buf, indent)
		buf.WriteByte('}')
	case []any:
		if len(x) == 0 {
			buf.WriteString("[]")
			return nil
		}
		buf.WriteByte('[')
		for i, el := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
			writeIndent(buf, indent+1)
			if err := writeSorted(buf, el, indent+1); err != nil {
				return err
			}
		}
		buf.WriteByte('\n')
		writeIndent(buf, indent)
		buf.WriteByte(']')
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return werr.Newf(werr.CodeJSONDecode, "format: %v", err)
		}
		buf.Write(b)
	}
	return nil
}

func writeIndent(buf *bytes.Buffer, n int) {
	for range n {
		buf.WriteString("  ")
	}
}

// FormatPseudoCode parses a wflang JSON document and renders it as compact
// developer-facing pseudocode. It is intended for review/debug tooling; the
// JSON AST remains the executable source of truth.
func FormatPseudoCode(src []byte) ([]byte, error) {
	prog, err := compiler.ParseProgram(src)
	if err != nil {
		return nil, err
	}
	f := pseudoFormatter{}
	f.program(prog)
	return []byte(strings.TrimRight(f.buf.String(), "\n")), nil
}

type pseudoFormatter struct {
	buf bytes.Buffer
}

func (f *pseudoFormatter) program(p *ast.Program) {
	for _, im := range p.Imports {
		f.line(0, "import "+im)
	}
	if len(p.Imports) > 0 && len(p.Body) > 0 {
		f.buf.WriteByte('\n')
	}
	f.blockLines(p.Body, 0)
}

func (f *pseudoFormatter) blockLines(nodes []ast.Node, indent int) {
	for _, n := range nodes {
		f.stmt(n, indent)
	}
}

func (f *pseudoFormatter) stmt(n ast.Node, indent int) {
	switch x := n.(type) {
	case *ast.Let:
		f.letStmt(x, indent)
	case *ast.Set:
		f.line(indent, fmt.Sprintf("%s = %s", x.Name, f.expr(x.Expr)))
	case *ast.Return:
		f.line(indent, "return "+f.expr(x.Expr))
	case *ast.IfStmt:
		f.ifStmt(x, indent)
	case *ast.Foreach:
		head := "foreach " + x.As
		if x.Index != "" {
			head += ", " + x.Index
		}
		f.line(indent, head+" in "+f.expr(x.Target)+" {")
		f.blockLines(x.Do, indent+1)
		f.line(indent, "}")
	case *ast.Fori:
		step := ""
		if x.Step != nil {
			step = "; step " + f.expr(x.Step)
		}
		f.line(indent, fmt.Sprintf("for %s := %s; %s < %s%s {",
			x.Var, f.expr(x.From), x.Var, f.expr(x.To), step))
		f.blockLines(x.Do, indent+1)
		f.line(indent, "}")
	case *ast.Break:
		f.line(indent, "break")
	case *ast.Continue:
		f.line(indent, "continue")
	case *ast.Panic:
		f.line(indent, "panic("+f.expr(x.Expr)+")")
	case *ast.ExprStmt:
		f.line(indent, f.expr(x.Expr))
	case *ast.Routine:
		f.line(indent, f.expr(x))
	case *ast.Defer:
		f.line(indent, "defer "+f.callExpr(x.Call))
	case *ast.SelectStmt:
		f.selectStmt(x, indent)
	default:
		f.line(indent, f.expr(n))
	}
}

func (f *pseudoFormatter) letStmt(x *ast.Let, indent int) {
	if x.Destructure != nil {
		names := make([]string, len(x.Destructure.Names))
		for i, name := range x.Destructure.Names {
			names[i] = name
			if i < len(x.Destructure.Types) && x.Destructure.Types[i] != "" {
				names[i] += ": " + x.Destructure.Types[i]
			}
		}
		f.line(indent, "let "+strings.Join(names, ", ")+" = "+f.expr(x.Expr))
		return
	}
	bindings := x.Bindings
	if len(bindings) == 0 && x.Name != "" {
		bindings = []ast.LetBinding{{Name: x.Name, Type: x.Type, Expr: x.Expr}}
	}
	for _, b := range bindings {
		name := b.Name
		if b.Type != "" {
			name += ": " + b.Type
		}
		f.line(indent, "let "+name+" = "+f.expr(b.Expr))
	}
}

func (f *pseudoFormatter) ifStmt(x *ast.IfStmt, indent int) {
	f.line(indent, "if "+f.expr(x.Cond)+" {")
	f.blockLines(x.Then, indent+1)
	if len(x.Else) > 0 {
		f.line(indent, "} else {")
		f.blockLines(x.Else, indent+1)
	}
	f.line(indent, "}")
}

func (f *pseudoFormatter) selectStmt(x *ast.SelectStmt, indent int) {
	f.line(indent, "select {")
	for _, c := range x.Cases {
		switch c.Kind {
		case ast.SelectCaseRecv:
			binds := selectBindList(c.BindVal, c.BindOK)
			suffix := ""
			if binds != "" {
				suffix = " -> " + binds
			}
			f.line(indent+1, "recv "+f.expr(c.Chan)+suffix+" {")
		case ast.SelectCaseSend:
			f.line(indent+1, "send "+f.expr(c.Chan)+" <- "+f.expr(c.SendExpr)+" {")
		}
		f.blockLines(c.Do, indent+2)
		f.line(indent+1, "}")
	}
	if x.Default != nil {
		f.line(indent+1, "default {")
		f.blockLines(x.Default, indent+2)
		f.line(indent+1, "}")
	}
	f.line(indent, "}")
}

func selectBindList(names ...string) string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if name != "" {
			out = append(out, name)
		}
	}
	return strings.Join(out, ", ")
}

func (f *pseudoFormatter) expr(n ast.Node) string {
	switch x := n.(type) {
	case nil:
		return "<nil>"
	case *ast.Literal:
		return literalPseudo(x.Value)
	case *ast.Var:
		if x.Default != nil {
			return fmt.Sprintf("%s ?? %s", x.Name, f.expr(x.Default))
		}
		return x.Name
	case *ast.Pkg:
		return x.Name
	case *ast.Call:
		return f.callExpr(x)
	case *ast.Array:
		items := make([]string, len(x.Items))
		for i, it := range x.Items {
			items[i] = f.expr(it)
		}
		return fmt.Sprintf("array<%s>[%s]", x.Elem, strings.Join(items, ", "))
	case *ast.MapLit:
		return f.mapExpr(x)
	case *ast.StructLit:
		return f.structExpr(x, 0)
	case *ast.ChanLit:
		if x.Buffer == nil {
			return fmt.Sprintf("chan<%s>()", x.ElemType)
		}
		return fmt.Sprintf("chan<%s>(%s)", x.ElemType, f.expr(x.Buffer))
	case *ast.Match:
		return f.matchExpr(x, 0)
	case *ast.Routine:
		if x.Call != nil {
			return "routine " + f.callExpr(x.Call)
		}
		var b strings.Builder
		b.WriteString("routine {")
		if len(x.Body) > 0 {
			b.WriteByte('\n')
			nested := pseudoFormatter{}
			nested.blockLines(x.Body, 1)
			b.WriteString(nested.buf.String())
		}
		b.WriteString("}")
		return b.String()
	case *ast.IfExpr:
		return "<if>"
	case *ast.SelectStmt:
		return "<select>"
	default:
		return fmt.Sprintf("<%T>", n)
	}
}

func (f *pseudoFormatter) callExpr(c *ast.Call) string {
	if c == nil {
		return "<nil-call>"
	}
	if isInfixOp(c.Op) && len(c.Args) >= 2 {
		parts := make([]string, len(c.Args))
		for i, arg := range c.Args {
			parts[i] = f.expr(arg)
		}
		return strings.Join(parts, " "+c.Op+" ")
	}
	if isPrefixOp(c.Op) && len(c.Args) == 1 {
		return c.Op + f.expr(c.Args[0])
	}
	if (c.Op == "await" || strings.HasPrefix(c.Op, "m.") || strings.HasPrefix(c.Op, "ch.")) && len(c.Args) > 0 {
		return c.Op + "(" + f.exprList(c.Args) + ")"
	}
	if len(c.Args) > 0 {
		if pkg, ok := c.Args[0].(*ast.Pkg); ok {
			return pkg.Name + "." + c.Op + "(" + f.exprList(c.Args[1:]) + ")"
		}
		if recv, ok := c.Args[0].(*ast.Var); ok {
			return recv.Name + "." + c.Op + "(" + f.exprList(c.Args[1:]) + ")"
		}
	}
	return c.Op + "(" + f.exprList(c.Args) + ")"
}

func (f *pseudoFormatter) exprList(nodes []ast.Node) string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = f.expr(n)
	}
	return strings.Join(out, ", ")
}

func (f *pseudoFormatter) mapExpr(x *ast.MapLit) string {
	if len(x.Entries) == 0 {
		return fmt.Sprintf("map<%s,%s>{}", x.KeyType, x.ValType)
	}
	lines := []string{fmt.Sprintf("map<%s,%s> {", x.KeyType, x.ValType)}
	for _, ent := range x.Entries {
		lines = append(lines, "  "+f.expr(ent.Key)+": "+f.expr(ent.Val))
	}
	lines = append(lines, "}")
	return strings.Join(lines, "\n")
}

func (f *pseudoFormatter) structExpr(x *ast.StructLit, indent int) string {
	if len(x.Fields) == 0 {
		return "struct " + x.TypeName + " {}"
	}
	lines := []string{"struct " + x.TypeName + " {"}
	for _, field := range x.Fields {
		lines = append(lines, strings.Repeat("  ", indent+1)+field.Name+": "+f.expr(field.Expr))
	}
	lines = append(lines, strings.Repeat("  ", indent)+"}")
	return strings.Join(lines, "\n")
}

func (f *pseudoFormatter) matchExpr(x *ast.Match, indent int) string {
	lines := []string{"match " + f.expr(x.Value) + " {"}
	for _, c := range x.Cases {
		lines = append(lines, strings.Repeat("  ", indent+1)+"when "+f.expr(c.When)+" {")
		nested := pseudoFormatter{}
		nested.blockLines(c.Do, indent+2)
		lines = append(lines, strings.TrimRight(nested.buf.String(), "\n"))
		lines = append(lines, strings.Repeat("  ", indent+1)+"}")
	}
	if x.Default != nil {
		lines = append(lines, strings.Repeat("  ", indent+1)+"default {")
		nested := pseudoFormatter{}
		nested.blockLines(x.Default, indent+2)
		lines = append(lines, strings.TrimRight(nested.buf.String(), "\n"))
		lines = append(lines, strings.Repeat("  ", indent+1)+"}")
	}
	lines = append(lines, strings.Repeat("  ", indent)+"}")
	return strings.Join(lines, "\n")
}

func literalPseudo(v types.Value) string {
	switch v.TypeName() {
	case types.TString:
		b, _ := json.Marshal(v.Go())
		return string(b)
	case types.TNull:
		return "null"
	case types.TBoolean:
		if v.Go().(bool) {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(v.Go())
	}
}

func isInfixOp(op string) bool {
	switch op {
	case "+", "-", "*", "/", ">", ">=", "<", "<=", "==", "!=", "and", "or", "contains", "startsWith", "endsWith":
		return true
	}
	return false
}

func isPrefixOp(op string) bool {
	return op == "!"
}

func (f *pseudoFormatter) line(indent int, s string) {
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		writeIndent(&f.buf, indent)
		f.buf.WriteString(line)
		f.buf.WriteByte('\n')
	}
}
