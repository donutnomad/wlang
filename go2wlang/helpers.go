package go2wlang

import (
	"go/ast"
	gotypes "go/types"
	"strconv"
)

// This file translates the functions of the translated package that the entry
// function calls (Options.TranslateHelpers): plain functions and methods with
// a value receiver. Each one becomes a WLang function bound to a variable at
// the start of the program; its call sites call that variable. The program
// binds every helper before the entry body runs and closures resolve names
// when they run, so helpers can call each other in any order and recurse.

// helperFunc is one translated same-package function or method.
type helperFunc struct {
	decl *ast.FuncDecl
	// name is the WLang variable that holds the function.
	name string
}

// collectHelpers finds the same-package functions reachable from entry
// through calls, in discovery order, and rejects the ones the translation
// cannot keep faithful.
func (t *translator) collectHelpers(entry *ast.FuncDecl) error {
	t.helpers = map[gotypes.Object]*helperFunc{}
	if t.info == nil {
		return nil
	}
	decls := map[gotypes.Object]*ast.FuncDecl{}
	for _, file := range t.files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if obj := t.info.Defs[fn.Name]; obj != nil {
					decls[obj] = fn
				}
			}
		}
	}
	queue := []*ast.FuncDecl{entry}
	for len(queue) > 0 {
		body := queue[0].Body
		queue = queue[1:]
		var err error
		ast.Inspect(body, func(node ast.Node) bool {
			if err != nil {
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			obj, found, callErr := t.calledHelper(call, decls)
			if callErr != nil || !found {
				err = callErr
				return err == nil
			}
			if _, seen := t.helpers[obj]; seen {
				return true
			}
			decl := decls[obj]
			if err = t.checkHelperDecl(decl); err != nil {
				return false
			}
			t.helpers[obj] = &helperFunc{decl: decl, name: helperVarName(decl)}
			t.helperOrder = append(t.helperOrder, t.helpers[obj])
			queue = append(queue, decl)
			return true
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// calledHelper resolves the callee of call to a function or method declared
// in the translated package.
func (t *translator) calledHelper(call *ast.CallExpr, decls map[gotypes.Object]*ast.FuncDecl) (gotypes.Object, bool, error) {
	switch fun := t.stripCallTypeArgs(call.Fun).(type) {
	case *ast.Ident:
		fn, ok := t.info.Uses[fun].(*gotypes.Func)
		if !ok || fn.Pkg() == nil || fn.Pkg().Path() != t.packagePath {
			return nil, false, nil
		}
		if decls[fn] == nil {
			return nil, false, t.unsupported(call, "function "+fun.Name+" has no Go body to translate", "")
		}
		return fn, true, nil
	case *ast.SelectorExpr:
		selection := t.info.Selections[fun]
		if selection == nil || selection.Kind() != gotypes.MethodVal {
			return nil, false, nil
		}
		method, ok := selection.Obj().(*gotypes.Func)
		if !ok || method.Pkg() == nil || method.Pkg().Path() != t.packagePath {
			return nil, false, nil
		}
		name := fun.Sel.Name
		if decls[method] == nil {
			return nil, false, t.unsupported(call, "method call "+name+" through an interface type declared in this package is not supported", "call a function or a method of a struct type")
		}
		if len(selection.Index()) > 1 {
			return nil, false, t.unsupported(call, "promoted method "+name+" of an embedded type is not supported", "name the embedded field explicitly")
		}
		if _, isPointer := method.Type().(*gotypes.Signature).Recv().Type().(*gotypes.Pointer); isPointer {
			return nil, false, t.unsupported(call, "method "+name+" has a pointer receiver; methods with pointer receivers are not supported", "use a value receiver and return the changed value")
		}
		if _, isPointer := selection.Recv().(*gotypes.Pointer); isPointer {
			return nil, false, t.unsupported(call, "method call "+name+" on a pointer is not supported", "call the method on a struct value")
		}
		return method, true, nil
	}
	return nil, false, nil
}

// checkHelperDecl rejects function shapes WLang functions cannot express.
func (t *translator) checkHelperDecl(decl *ast.FuncDecl) error {
	name := decl.Name.Name
	if decl.Type.TypeParams != nil && decl.Type.TypeParams.NumFields() > 0 {
		return t.unsupported(decl, "generic function "+name+" is not supported", "write a non-generic function for the types the workflow uses")
	}
	if decl.Recv != nil {
		switch decl.Recv.List[0].Type.(type) {
		case *ast.IndexExpr, *ast.IndexListExpr:
			return t.unsupported(decl, "method "+name+" of a generic type is not supported", "")
		}
	}
	for _, field := range decl.Type.Params.List {
		if _, ok := field.Type.(*ast.Ellipsis); ok {
			return t.unsupported(field, "variadic function "+name+" is not supported", "take a slice parameter")
		}
	}
	return nil
}

func helperVarName(decl *ast.FuncDecl) string {
	if decl.Recv == nil {
		return "__wlang_func_" + decl.Name.Name
	}
	recv := decl.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	typeName := ""
	if id, ok := recv.(*ast.Ident); ok {
		typeName = id.Name
	}
	return "__wlang_method_" + typeName + "_" + decl.Name.Name
}

// helperFor returns the helper an identifier or method selector calls.
func (t *translator) helperFor(fun ast.Expr) (*helperFunc, bool) {
	if t.info == nil || len(t.helpers) == 0 {
		return nil, false
	}
	switch x := fun.(type) {
	case *ast.Ident:
		if t.locals.has(x.Name) {
			return nil, false
		}
		helper, ok := t.helpers[t.info.Uses[x]]
		return helper, ok
	case *ast.SelectorExpr:
		selection := t.info.Selections[x]
		if selection == nil || selection.Kind() != gotypes.MethodVal {
			return nil, false
		}
		helper, ok := t.helpers[selection.Obj()]
		return helper, ok
	}
	return nil, false
}

// helperDefinitions binds every helper to its variable.
func (t *translator) helperDefinitions() ([]any, error) {
	out := make([]any, 0, len(t.helperOrder))
	for _, helper := range t.helperOrder {
		fn, err := t.helperLiteral(helper.decl)
		if err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"let": map[string]any{helper.name: fn}})
	}
	return out, nil
}

// helperLiteral translates a declaration as a function literal in a fresh
// scope: a helper sees its parameters, constants and other helpers only. A
// method takes its receiver as the first parameter; blank and unnamed
// parameters get private names.
func (t *translator) helperLiteral(decl *ast.FuncDecl) (any, error) {
	var fields []*ast.Field
	if decl.Recv != nil {
		fields = append(fields, decl.Recv.List[0])
	}
	fields = append(fields, decl.Type.Params.List...)
	params := &ast.FieldList{Opening: decl.Type.Params.Opening, Closing: decl.Type.Params.Closing}
	for i, field := range fields {
		named := &ast.Field{Type: field.Type}
		if len(field.Names) == 0 {
			named.Names = []*ast.Ident{{NamePos: field.Pos(), Name: privateParam(i, 0)}}
		}
		for j, name := range field.Names {
			if name.Name == "_" {
				name = &ast.Ident{NamePos: name.NamePos, Name: privateParam(i, j)}
			}
			named.Names = append(named.Names, name)
		}
		params.List = append(params.List, named)
	}
	literal := &ast.FuncLit{
		Type: &ast.FuncType{Func: decl.Type.Func, Params: params, Results: decl.Type.Results},
		Body: decl.Body,
	}
	outerLocals, outerTargets, outerLabel := t.locals, t.targets, t.pendingLabel
	t.locals, t.targets, t.pendingLabel = newLocalScopes(), nil, ""
	defer func() { t.locals, t.targets, t.pendingLabel = outerLocals, outerTargets, outerLabel }()
	if err := t.rejectPackageVariables(decl.Body); err != nil {
		return nil, err
	}
	return t.funcLit(literal)
}

func privateParam(field, name int) string {
	return "__wlang_param_" + strconv.Itoa(field) + "_" + strconv.Itoa(name)
}

// rejectPackageVariables rejects reads and writes of package-level variables:
// the program has no package scope, every value comes from a parameter, a
// local or a constant.
func (t *translator) rejectPackageVariables(body *ast.BlockStmt) error {
	if t.info == nil || body == nil {
		return nil
	}
	var err error
	ast.Inspect(body, func(node ast.Node) bool {
		if err != nil {
			return false
		}
		id, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		v, ok := t.info.Uses[id].(*gotypes.Var)
		if ok && v.Pkg() != nil && v.Parent() == v.Pkg().Scope() && v.Pkg().Path() == t.packagePath {
			err = t.unsupported(id, "package-level variable "+id.Name+" is not supported", "use a constant or pass the value as a parameter")
		}
		return err == nil
	})
	return err
}
