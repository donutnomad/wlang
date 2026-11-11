package go2wlang

import (
	"go/ast"
	"go/constant"
	gotypes "go/types"
	"strconv"
	"strings"

	"github.com/donutnomad/wlang/stdlib"
)

// This file decides which package functions translated code may call. A
// standard-library function runs only if stdlib binds it (stdlib/gostd.go):
// the call names the package by its import path, whatever the import alias,
// and every other standard-library function is rejected. Functions and
// methods of other packages are host bindings; Options.HostPackages, when
// set, lists the packages the host binds.

// isStdlibPath reports whether importPath belongs to the Go standard library.
func isStdlibPath(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return !strings.Contains(first, ".")
}

// packageCallName checks a call of a package-level function and returns the
// package name the program calls it through.
func (t *translator) packageCallName(call *ast.CallExpr, sel *ast.SelectorExpr, alias string) (string, error) {
	fn, ok := t.packageObject(sel).(*gotypes.Func)
	if !ok || fn.Pkg() == nil {
		t.markImport(alias)
		return alias, nil
	}
	path, name := fn.Pkg().Path(), fn.Name()
	if isStdlibPath(path) {
		if !stdlib.GoFunction(path, name) {
			return "", t.unsupported(call, path+"."+name+" is not supported: WLang binds no such standard-library function", boundStdlibHint(path))
		}
		if path == "fmt" {
			if err := t.checkFmtCall(call, name); err != nil {
				return "", err
			}
		}
		t.markImport(path)
		if t.imports[path] == "" {
			t.imports[path] = path
		}
		return path, nil
	}
	if !t.hostPackage(path) {
		return "", t.unsupported(call, "call of "+path+"."+name+" is not supported: the package is not bound by the host", t.hostPackagesHint())
	}
	t.markImport(alias)
	return alias, nil
}

// packageValue checks a package-level function or variable used as a value.
// Standard-library functions are only callable and their variables are not
// bound; other packages' symbols are host bindings.
func (t *translator) packageValue(sel *ast.SelectorExpr) error {
	obj := t.packageObject(sel)
	if obj == nil || obj.Pkg() == nil || !isStdlibPath(obj.Pkg().Path()) {
		return nil
	}
	name := obj.Pkg().Path() + "." + obj.Name()
	switch obj.(type) {
	case *gotypes.Func:
		return t.unsupported(sel, "function value "+name+" is not supported", "call it directly")
	case *gotypes.Var:
		return t.unsupported(sel, "package variable "+name+" is not supported", "")
	}
	return nil
}

func (t *translator) packageObject(sel *ast.SelectorExpr) gotypes.Object {
	if t.info == nil {
		return nil
	}
	return t.info.Uses[sel.Sel]
}

// checkMethodCall rejects calls of methods declared in a package the host
// does not bind, such as strings.Builder.WriteString.
func (t *translator) checkMethodCall(call *ast.CallExpr, sel *ast.SelectorExpr) error {
	if t.info == nil || t.hostPackages == nil {
		return nil
	}
	selection := t.info.Selections[sel]
	if selection == nil || selection.Kind() != gotypes.MethodVal {
		return nil
	}
	pkg := selection.Obj().Pkg()
	if pkg == nil || pkg.Path() == t.packagePath || t.hostPackages[pkg.Path()] {
		return nil
	}
	return t.unsupported(call, "method call "+gotypes.TypeString(selection.Recv(), nil)+"."+sel.Sel.Name+" is not supported: the package "+pkg.Path()+" is not bound by the host", t.hostPackagesHint())
}

// checkForeignType rejects a local variable of a named type from a standard
// package the host does not bind, such as strings.Builder: WLang has no value
// of it.
func (t *translator) checkForeignType(typ gotypes.Type, at ast.Node) error {
	if t.hostPackages == nil {
		return nil
	}
	named, ok := gotypes.Unalias(typ).(*gotypes.Named)
	if !ok || named.Obj().Pkg() == nil {
		return nil
	}
	path := named.Obj().Pkg().Path()
	if path == t.packagePath || t.hostPackages[path] || !isStdlibPath(path) {
		return nil
	}
	return t.unsupported(at, "type "+path+"."+named.Obj().Name()+" is not supported: the package "+path+" is not bound by the host", t.hostPackagesHint())
}

func (t *translator) hostPackage(path string) bool {
	return t.hostPackages == nil || t.hostPackages[path]
}

func (t *translator) hostPackagesHint() string {
	paths := make([]string, 0, len(t.hostPackages))
	for path := range t.hostPackages {
		paths = append(paths, path)
	}
	sortStrings(paths)
	hint := "callable are the bound functions of " + strings.Join(stdlib.GoPackages(), ", ")
	if len(paths) > 0 {
		hint += " and the functions and methods of " + strings.Join(paths, ", ")
	}
	return hint
}

func boundStdlibHint(path string) string {
	if names := stdlib.GoFunctions(path); len(names) > 0 {
		return "bound functions of " + path + ": " + strings.Join(names, ", ")
	}
	return "packages with bound functions: " + strings.Join(stdlib.GoPackages(), ", ")
}

// fmt operands format exactly as in Go when the value WLang passes has the
// same formatting as the Go value: booleans, integers, floats and strings of
// any width, named or not, as long as the type has no methods (fmt would call
// String, Error or Format), and error values. The Go dynamic type can still
// differ (Go int arrives as int64), so the verbs that print type names (%T,
// and every verb-operand mismatch, which prints %!d(type=value)) are rejected.
const (
	fmtBool   = "bool"
	fmtInt    = "integer"
	fmtFloat  = "float"
	fmtString = "string"
	fmtError  = "error"
)

// fmtVerbClasses lists the operand classes each supported verb accepts.
var fmtVerbClasses = map[byte][]string{
	'v': {fmtBool, fmtInt, fmtFloat, fmtString, fmtError},
	'd': {fmtInt}, 'b': {fmtInt}, 'o': {fmtInt},
	'x': {fmtInt, fmtString}, 'X': {fmtInt, fmtString},
	's': {fmtString, fmtError},
	't': {fmtBool},
	'e': {fmtFloat}, 'E': {fmtFloat}, 'f': {fmtFloat}, 'F': {fmtFloat}, 'g': {fmtFloat}, 'G': {fmtFloat},
}

func (t *translator) checkFmtCall(call *ast.CallExpr, name string) error {
	if t.info == nil {
		return t.unsupported(call, "fmt."+name+" needs type information", "")
	}
	switch name {
	case "Sprint", "Sprintln":
		for _, arg := range call.Args {
			if _, err := t.fmtOperand(arg); err != nil {
				return err
			}
		}
		return nil
	}
	tv := t.info.Types[call.Args[0]]
	if tv.Value == nil || tv.Value.Kind() != constant.String {
		return t.unsupported(call.Args[0], "fmt."+name+" format must be a constant string", "write the format as a string literal or constant")
	}
	verbs, err := parseFmtVerbs(constant.StringVal(tv.Value))
	if err != nil {
		return t.unsupported(call.Args[0], "fmt."+name+" format "+strconv.Quote(constant.StringVal(tv.Value))+": "+err.Error(), "supported verbs: %v %d %b %o %x %X %s %t %e %E %f %F %g %G, %w in fmt.Errorf; flags + - space 0, # with %x %X %o %b; width and precision as digits")
	}
	operands := call.Args[1:]
	if len(verbs) != len(operands) {
		return t.unsupported(call, "fmt."+name+" format has "+strconv.Itoa(len(verbs))+" verbs for "+strconv.Itoa(len(operands))+" operands", "use one operand per verb")
	}
	for i, verb := range verbs {
		class, err := t.fmtOperand(operands[i])
		if err != nil {
			return err
		}
		accepted := fmtVerbClasses[verb]
		if verb == 'w' {
			if name != "Errorf" {
				return t.unsupported(call.Args[0], "fmt."+name+" does not take %w", "use %v, or fmt.Errorf to wrap an error")
			}
			accepted = []string{fmtError}
		}
		if !containsString(accepted, class) {
			return t.unsupported(operands[i], "fmt."+name+" verb %"+string(verb)+" does not accept "+class+" operand of type "+t.info.TypeOf(operands[i]).String(), "")
		}
	}
	return nil
}

// fmtOperand returns the formatting class of an operand.
func (t *translator) fmtOperand(arg ast.Expr) (string, error) {
	typ := t.info.TypeOf(arg)
	if typ == nil {
		return "", t.unsupported(arg, "fmt operand has no type information", "")
	}
	if gotypes.Identical(typ, gotypes.Universe.Lookup("error").Type()) {
		return fmtError, nil
	}
	reject := func(reason string) (string, error) {
		return "", t.unsupported(arg, "fmt operand of type "+typ.String()+" is not supported: "+reason, "format booleans, numbers, strings and errors; format struct fields one by one")
	}
	if named, ok := gotypes.Unalias(typ).(*gotypes.Named); ok {
		if gotypes.NewMethodSet(named).Len() > 0 || gotypes.NewMethodSet(gotypes.NewPointer(named)).Len() > 0 {
			return reject("the type has methods, which fmt would call")
		}
	}
	basic, ok := typ.Underlying().(*gotypes.Basic)
	if !ok {
		return reject("its formatting depends on the Go type")
	}
	switch {
	case basic.Info()&gotypes.IsBoolean != 0:
		return fmtBool, nil
	case basic.Info()&gotypes.IsString != 0:
		return fmtString, nil
	case basic.Info()&gotypes.IsInteger != 0 && basic.Kind() != gotypes.Uintptr:
		return fmtInt, nil
	case basic.Info()&gotypes.IsFloat != 0:
		return fmtFloat, nil
	}
	return reject("WLang has no value of this type")
}

// parseFmtVerbs returns the verbs of a format string in operand order.
func parseFmtVerbs(format string) ([]byte, error) {
	var verbs []byte
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		flags := ""
		for i < len(format) && strings.IndexByte("+- #0", format[i]) >= 0 {
			flags += string(format[i])
			i++
		}
		for i < len(format) && (format[i] >= '0' && format[i] <= '9' || format[i] == '.') {
			i++
		}
		if i >= len(format) {
			return nil, errorString("ends with an incomplete verb")
		}
		verb := format[i]
		switch {
		case verb == '%':
			if flags != "" {
				return nil, errorString("%% takes no flags")
			}
			continue
		case verb == '*':
			return nil, errorString("* width or precision is not supported")
		case verb == '[':
			return nil, errorString("explicit argument indexes are not supported")
		case verb == 'q':
			return nil, errorString("%q is not supported: its output follows the Unicode tables of the Go release")
		case verb == 'T':
			return nil, errorString("%T is not supported: WLang values do not carry Go type names")
		case verb == 'w':
		case fmtVerbClasses[verb] == nil:
			return nil, errorString("verb %" + string(rune(verb)) + " is not supported")
		}
		if strings.Contains(flags, "#") && strings.IndexByte("xXob", verb) < 0 {
			return nil, errorString("flag # is supported with %x %X %o %b only")
		}
		verbs = append(verbs, verb)
	}
	return verbs, nil
}

type errorString string

func (e errorString) Error() string { return string(e) }

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
