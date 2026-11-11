package go2wlang

import (
	"go/ast"
	"go/constant"
	"go/token"
	gotypes "go/types"
	"strconv"
)

// This file holds the translations that need Go's type information to keep
// Go semantics: constants, conversions, increments and assignments that Go
// evaluates in two phases.

// basicLiteralType is the WLang literal type of a Go basic type, or "" when
// WLang has no matching scalar.
func basicLiteralType(b *gotypes.Basic) string {
	switch b.Kind() {
	case gotypes.Bool, gotypes.UntypedBool:
		return "boolean"
	case gotypes.String, gotypes.UntypedString:
		return "string"
	case gotypes.Int, gotypes.Int64, gotypes.UntypedInt, gotypes.UntypedRune:
		return "int64"
	case gotypes.Int8:
		return "int8"
	case gotypes.Int16:
		return "int16"
	case gotypes.Int32:
		return "int32"
	case gotypes.Uint, gotypes.Uint64:
		return "uint64"
	case gotypes.Uint8:
		return "uint8"
	case gotypes.Uint16:
		return "uint16"
	case gotypes.Uint32:
		return "uint32"
	case gotypes.Float32:
		return "float32"
	case gotypes.Float64, gotypes.UntypedFloat:
		return "float64"
	}
	return ""
}

// scalarType is the WLang scalar type of e, or "" when e is not of a basic
// type WLang represents.
func (t *translator) scalarType(e ast.Expr) string {
	if t.info == nil {
		return ""
	}
	typ := t.info.TypeOf(e)
	if typ == nil {
		return ""
	}
	if b, ok := typ.Underlying().(*gotypes.Basic); ok {
		return basicLiteralType(b)
	}
	return ""
}

// constantLiteral folds a constant expression (literals, named and iota
// constants, time.Hour, constant arithmetic) into a literal of the type Go
// gives it. ok is false when e is not a constant.
func (t *translator) constantLiteral(e ast.Expr) (any, bool, error) {
	if t.info == nil {
		return nil, false, nil
	}
	tv, found := t.info.Types[e]
	if !found || tv.Value == nil || tv.Type == nil {
		return nil, false, nil
	}
	b, isBasic := tv.Type.Underlying().(*gotypes.Basic)
	if !isBasic {
		return nil, false, nil
	}
	typ := basicLiteralType(b)
	value := tv.Value
	switch typ {
	case "boolean":
		return lit(typ, strconv.FormatBool(constant.BoolVal(value))), true, nil
	case "string":
		return lit(typ, constant.StringVal(value)), true, nil
	case "float64", "float32":
		f, _ := constant.Float64Val(constant.ToFloat(value))
		return lit(typ, strconv.FormatFloat(f, 'g', -1, 64)), true, nil
	case "":
		// Complex constants keep their builtin-call translation.
		return nil, false, nil
	default:
		return lit(typ, constant.ToInt(value).ExactString()), true, nil
	}
}

// conversion translates T(x). A conversion that keeps the WLang
// representation (int and int64, time.Duration and int64, a named string
// type and string, any interface) passes the value through; every other
// conversion changes the value in Go and is rejected.
func (t *translator) conversion(call *ast.CallExpr, target gotypes.Type) (any, error) {
	if len(call.Args) != 1 {
		return nil, t.unsupported(call, "conversion requires one argument", "")
	}
	arg := call.Args[0]
	source := t.info.TypeOf(arg)
	if source == nil || source == gotypes.Typ[gotypes.Invalid] {
		// Without type information for the operand (a package that does not
		// type-check), only conversions among the int64-represented types
		// pass through, as before type-aware translation.
		if b, ok := target.Underlying().(*gotypes.Basic); ok && basicLiteralType(b) == "int64" {
			return t.expr(arg)
		}
	}
	if source != nil && t.sameRepresentation(source, target) {
		return t.expr(arg)
	}
	from := "unknown type"
	if source != nil {
		from = source.String()
	}
	return nil, t.unsupported(call, "conversion from "+from+" to "+target.String()+" is not supported", "do the conversion in an activity or host function")
}

func (t *translator) sameRepresentation(source, target gotypes.Type) bool {
	if gotypes.IsInterface(target) {
		return true
	}
	if gotypes.Identical(source, target) {
		return true
	}
	sb, sok := source.Underlying().(*gotypes.Basic)
	tb, tok := target.Underlying().(*gotypes.Basic)
	if sok && tok {
		name := basicLiteralType(sb)
		return name != "" && name == basicLiteralType(tb)
	}
	return false
}

// typedOne is the literal 1 of e's type, used by ++ and --.
func (t *translator) typedOne(e ast.Expr) any {
	typ := t.scalarType(e)
	if typ == "" {
		typ = "int64"
	}
	return lit(typ, "1")
}

// typedZero is the literal 0 of e's type, used to negate e.
func (t *translator) typedZero(e ast.Expr) any {
	typ := t.scalarType(e)
	if typ == "" {
		typ = "int64"
	}
	return lit(typ, "0")
}

// incDec translates x++ and x-- on a variable, field path or element.
func (t *translator) incDec(x *ast.IncDecStmt) ([]any, error) {
	op := "+"
	if x.Tok == token.DEC {
		op = "-"
	}
	one := t.typedOne(x.X)
	switch target := x.X.(type) {
	case *ast.Ident:
		return []any{map[string]any{"set": map[string]any{target.Name: map[string]any{op: []any{varNode(target.Name), one}}}}}, nil
	case *ast.SelectorExpr:
		path, err := t.assignablePath(target)
		if err != nil {
			return nil, err
		}
		return []any{map[string]any{"set": map[string]any{path: map[string]any{op: []any{varNode(path), one}}}}}, nil
	case *ast.IndexExpr:
		return t.indexAssignValue(target, compoundTokens[op], one)
	}
	return nil, t.unsupported(x, "increment target is not supported", "")
}

var compoundTokens = map[string]token.Token{"+": token.ADD_ASSIGN, "-": token.SUB_ASSIGN}

// assignablePath returns the dotted path of a field assignment rooted at a
// variable. Fields of computed values (xs[i].F, f().F) are rejected.
func (t *translator) assignablePath(sel *ast.SelectorExpr) (string, error) {
	if _, ok := selectorRootIdent(sel.X); !ok {
		return "", t.unsupported(sel, "assignment to a field of a computed value is not supported", "copy the value into a local variable, change it and store it back")
	}
	return selectorPath(sel), nil
}

// isNewDefinition reports whether id is declared (not reused) by its :=.
func (t *translator) isNewDefinition(id *ast.Ident) bool {
	if t.info == nil {
		return true
	}
	return t.info.Defs[id] != nil
}

// tupleAssign translates a, b := f() and a, b = f() (also the comma-ok forms).
// Targets the statement declares are bound by the destructuring let; targets
// that already exist (= or reused by :=) receive their values through
// temporaries so the existing variables are updated, not shadowed.
func (t *translator) tupleAssign(x *ast.AssignStmt, rhs any) ([]any, error) {
	if rhs == nil {
		var err error
		if rhs, err = t.expr(x.Rhs[0]); err != nil {
			return nil, err
		}
	}
	names := []any{}
	var stores []any
	for _, lhs := range x.Lhs {
		if id, ok := lhs.(*ast.Ident); ok && id.Name == "_" {
			names = append(names, "_")
			continue
		}
		if id, ok := lhs.(*ast.Ident); ok && x.Tok == token.DEFINE && t.isNewDefinition(id) {
			names = append(names, id.Name)
			continue
		}
		temp := t.tempName()
		names = append(names, temp)
		store, err := t.storeTo(lhs, varNode(temp))
		if err != nil {
			return nil, err
		}
		stores = append(stores, store...)
	}
	if x.Tok == token.DEFINE {
		for _, lhs := range x.Lhs {
			if id, ok := lhs.(*ast.Ident); ok {
				t.locals.add(id.Name)
			}
		}
	}
	return append([]any{map[string]any{"let": []any{names, rhs}}}, stores...), nil
}

// storeTo assigns an already evaluated value to an assignment target.
func (t *translator) storeTo(lhs ast.Expr, value any) ([]any, error) {
	switch target := lhs.(type) {
	case *ast.Ident:
		return []any{map[string]any{"set": map[string]any{target.Name: value}}}, nil
	case *ast.SelectorExpr:
		path, err := t.assignablePath(target)
		if err != nil {
			return nil, err
		}
		return []any{map[string]any{"set": map[string]any{path: value}}}, nil
	case *ast.IndexExpr:
		return t.indexAssignValue(target, token.ASSIGN, value)
	}
	return nil, t.unsupported(lhs, "assignment target is not supported", "assign to a variable, field or element")
}

// multiAssign translates a, b = x, y and a, b := x, y with Go's two phases:
// index operands on the left and every right-hand value are evaluated first,
// left to right, then the assignments happen left to right.
func (t *translator) multiAssign(x *ast.AssignStmt) ([]any, error) {
	if x.Tok != token.ASSIGN && x.Tok != token.DEFINE {
		return nil, t.unsupported(x, "assignment operator is not supported", "")
	}
	var out []any
	targets := make([]ast.Expr, len(x.Lhs))
	for i, lhs := range x.Lhs {
		targets[i] = lhs
		index, ok := lhs.(*ast.IndexExpr)
		if !ok || t.isConstant(index.Index) {
			continue
		}
		key, err := t.expr(index.Index)
		if err != nil {
			return nil, err
		}
		temp := t.tempName()
		out = append(out, map[string]any{"let": map[string]any{temp: key}})
		copied := *index
		copied.Index = &ast.Ident{NamePos: index.Index.Pos(), Name: temp}
		t.locals.add(temp)
		targets[i] = &copied
	}
	values := make([]string, len(x.Rhs))
	for i, rhs := range x.Rhs {
		value, err := t.expr(rhs)
		if err != nil {
			return nil, err
		}
		values[i] = t.tempName()
		out = append(out, map[string]any{"let": map[string]any{values[i]: value}})
	}
	for i, lhs := range targets {
		id, isIdent := lhs.(*ast.Ident)
		switch {
		case isIdent && id.Name == "_":
		case isIdent && x.Tok == token.DEFINE && t.isNewDefinition(id):
			out = append(out, map[string]any{"let": map[string]any{id.Name: varNode(values[i])}})
			t.locals.add(id.Name)
		default:
			store, err := t.storeTo(lhs, varNode(values[i]))
			if err != nil {
				return nil, err
			}
			out = append(out, store...)
		}
	}
	return out, nil
}

// supportedBuiltins are the Go builtins callExpr translates.
var supportedBuiltins = map[string]bool{
	"append": true, "cap": true, "close": true, "complex": true, "copy": true, "delete": true,
	"imag": true, "len": true, "make": true, "new": true, "panic": true, "real": true,
}

// isPromotedField reports whether sel reads a field promoted from an embedded
// struct of a descriptor-backed record, which stores the embedded struct as
// its own field.
func (t *translator) isPromotedField(sel *ast.SelectorExpr) bool {
	if t.info == nil {
		return false
	}
	selection := t.info.Selections[sel]
	return selection != nil && selection.Kind() == gotypes.FieldVal && len(selection.Index()) > 1 &&
		t.usesDescriptorType(selection.Recv())
}

// isLocalStruct reports whether e names a struct type declared in the
// translated package. Such types are WLang records, which have no pointers.
func (t *translator) isLocalStruct(e ast.Expr) bool {
	if t.info == nil {
		return false
	}
	named, ok := t.info.TypeOf(e).(*gotypes.Named)
	if !ok || named.Obj().Pkg() == nil || t.file == nil || named.Obj().Pkg().Name() != t.file.Name.Name {
		return false
	}
	_, isStruct := named.Underlying().(*gotypes.Struct)
	return isStruct
}

// scopeInit keeps the variables of an if/switch init statement inside the
// statement: when the init redeclares a visible name, the translated
// statement is wrapped in a block so the outer variable is shadowed, not
// overwritten.
func (t *translator) scopeInit(init ast.Stmt, translate func() ([]any, error)) ([]any, error) {
	shadows := false
	if assign, ok := init.(*ast.AssignStmt); ok && assign.Tok == token.DEFINE {
		for _, lhs := range assign.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && t.locals.has(id.Name) && t.isNewDefinition(id) {
				shadows = true
			}
		}
	}
	out, err := translate()
	if err != nil || !shadows {
		return out, err
	}
	return []any{map[string]any{"if": map[string]any{"cond": lit("boolean", "true"), "then": out}}}, nil
}

// goScalarName is the Go spelling typeName maps back to the WLang scalar.
func goScalarName(scalar string) string {
	if scalar == "boolean" {
		return "bool"
	}
	return scalar
}
