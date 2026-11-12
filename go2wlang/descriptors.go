package go2wlang

import (
	"fmt"
	"go/ast"
	"go/token"
	gotypes "go/types"
	"reflect"
	"sort"
	"strconv"
	"strings"

	wltypes "github.com/donutnomad/wlang/types"
)

func (t *translator) languageTypeName(typ gotypes.Type) string {
	switch current := typ.(type) {
	case *gotypes.Basic:
		if current.Kind() == gotypes.Invalid {
			return ""
		}
		return goTypeToWlang(current.Name())
	case *gotypes.Named:
		prefix := t.packageLanguageName(current.Obj().Pkg())
		if prefix == "" {
			return current.Obj().Name()
		}
		return prefix + "." + current.Obj().Name()
	case *gotypes.Alias:
		prefix := t.packageLanguageName(current.Obj().Pkg())
		if prefix == "" {
			return current.Obj().Name()
		}
		return prefix + "." + current.Obj().Name()
	case *gotypes.Pointer:
		elem := t.languageTypeName(current.Elem())
		if elem == "" {
			return ""
		}
		return "*" + elem
	case *gotypes.Slice:
		elem := t.languageTypeName(current.Elem())
		if elem == "" {
			return ""
		}
		return "array<" + elem + ">"
	case *gotypes.Array:
		elem := t.languageTypeName(current.Elem())
		if elem == "" {
			return ""
		}
		return "array<" + elem + ">"
	case *gotypes.Map:
		key := t.languageTypeName(current.Key())
		elem := t.languageTypeName(current.Elem())
		if key == "" || elem == "" {
			return ""
		}
		return "map<" + key + "," + elem + ">"
	case *gotypes.Signature:
		params := tupleTypeNames(t, current.Params())
		results := tupleTypeNames(t, current.Results())
		for _, name := range append(append([]string{}, params...), results...) {
			if name == "" {
				return ""
			}
		}
		return "func<(" + strings.Join(params, ",") + ")->" + strings.Join(results, ",") + ">"
	case *gotypes.Interface:
		if current.NumMethods() == 0 {
			return wltypes.TAny
		}
	}
	return ""
}

func tupleTypeNames(t *translator, tuple *gotypes.Tuple) []string {
	if tuple == nil {
		return nil
	}
	out := make([]string, tuple.Len())
	for i := 0; i < tuple.Len(); i++ {
		out[i] = t.languageTypeName(tuple.At(i).Type())
	}
	return out
}

func (t *translator) packageLanguageName(pkg *gotypes.Package) string {
	if pkg == nil {
		return ""
	}
	if pkg.Path() == t.packagePath || pkg.Name() == t.localPackageName {
		return t.localPackageName
	}
	aliases := make([]string, 0, len(t.imports))
	for alias, importPath := range t.imports {
		if importPath == pkg.Path() {
			aliases = append(aliases, alias)
		}
	}
	if len(aliases) > 0 {
		sort.Strings(aliases)
		return aliases[0]
	}
	return pkg.Name()
}

type descriptorExtractor struct {
	translator  *translator
	descriptors map[string]wltypes.TypeDescriptor
	// visiting holds the named types whose descriptor is being built; meeting
	// one again is a recursive reference, which the descriptor graph keeps as
	// a plain ID.
	visiting map[string]bool
}

// extractDescriptors builds the program's type table: a descriptor for every
// data type the translated code can touch, at any nesting depth.
//
// The roots are the types of every expression and declaration in the entry
// function and in each translated helper (collectProgramTypes); addType then
// walks each root's type graph to a fixed point. The payload boundaries, the
// entry signature and update handler parameters and results, additionally
// register types without a program-declared named type in them, such as
// []string, because the host decodes those payloads by descriptor.
func (t *translator) extractDescriptors(fn *ast.FuncDecl) (map[string]wltypes.TypeDescriptor, EntrySignature, error) {
	extractor := &descriptorExtractor{
		translator: t, descriptors: map[string]wltypes.TypeDescriptor{}, visiting: map[string]bool{},
	}
	if err := extractor.collectProgramTypes(fn); err != nil {
		return nil, EntrySignature{}, err
	}
	for _, helper := range t.helperOrder {
		if err := extractor.collectProgramTypes(helper.decl); err != nil {
			return nil, EntrySignature{}, err
		}
	}
	// Before fingerprinting: addType may still register descriptors, and every
	// one of them has to be stamped by the loop below.
	entry, err := extractor.extractEntrySignature(fn)
	if err != nil {
		return nil, EntrySignature{}, err
	}
	var firstErr error
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if firstErr != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok || !isSetUpdateHandlerCall(call) || len(call.Args) < 3 {
			return true
		}
		literal, ok := call.Args[2].(*ast.FuncLit)
		if !ok {
			return true
		}
		if err := extractor.collectPayloadFields(literal.Type.Params, true); err != nil {
			firstErr = err
			return false
		}
		if err := extractor.collectPayloadFields(literal.Type.Results, false); err != nil {
			firstErr = err
			return false
		}
		return true
	})
	if firstErr != nil {
		return nil, EntrySignature{}, firstErr
	}
	for id, descriptor := range extractor.descriptors {
		descriptor.Fingerprint = wltypes.DescriptorFingerprint(descriptor)
		extractor.descriptors[id] = descriptor
	}
	t.descriptorNames = map[string]bool{}
	for _, descriptor := range extractor.descriptors {
		t.descriptorNames[descriptor.LanguageName] = true
	}
	return extractor.descriptors, entry, nil
}

// collectProgramTypes registers every program data type that node's code
// touches: the type of each expression (call arguments and results, composite
// literals and their elided elements, conversions, closures, selectors, host
// calls of every kind) and of each declared identifier and type expression.
//
// A root is a program data type when a named type declared by the translated
// package or by another non-host, non-standard package occurs in it
// (programDataRoot). Host and standard-library types are bound by the host
// and get no descriptor of their own; function, tuple and channel types are
// looked through for the data types they carry.
func (e *descriptorExtractor) collectProgramTypes(node ast.Node) error {
	info := e.translator.info
	if info == nil || node == nil {
		return nil
	}
	var firstErr error
	ast.Inspect(node, func(n ast.Node) bool {
		if firstErr != nil {
			return false
		}
		switch current := n.(type) {
		case *ast.ValueSpec:
			if current.Type != nil {
				firstErr = e.translator.checkForeignType(info.TypeOf(current.Type), current.Type)
			}
		case *ast.CompositeLit:
			if current.Type != nil {
				firstErr = e.translator.checkForeignType(info.TypeOf(current.Type), current.Type)
			}
		}
		if firstErr != nil {
			return false
		}
		if expr, ok := n.(ast.Expr); ok {
			if typ := info.TypeOf(expr); typ != nil {
				firstErr = e.collectRoot(typ, expr.Pos())
			}
		}
		return firstErr == nil
	})
	return firstErr
}

// collectRoot registers the program data types carried by typ.
func (e *descriptorExtractor) collectRoot(typ gotypes.Type, pos token.Pos) error {
	typ = gotypes.Unalias(typ)
	if e.programDataRoot(typ) {
		_, err := e.addType(typ, pos, e.typeString(typ))
		return err
	}
	switch current := typ.(type) {
	case *gotypes.Tuple:
		for i := 0; i < current.Len(); i++ {
			if err := e.collectRoot(current.At(i).Type(), pos); err != nil {
				return err
			}
		}
	case *gotypes.Signature:
		if err := e.collectRoot(current.Params(), pos); err != nil {
			return err
		}
		return e.collectRoot(current.Results(), pos)
	case *gotypes.Chan:
		return e.collectRoot(current.Elem(), pos)
	case *gotypes.Pointer:
		return e.collectRoot(current.Elem(), pos)
	case *gotypes.Slice:
		return e.collectRoot(current.Elem(), pos)
	case *gotypes.Array:
		return e.collectRoot(current.Elem(), pos)
	case *gotypes.Map:
		if err := e.collectRoot(current.Key(), pos); err != nil {
			return err
		}
		return e.collectRoot(current.Elem(), pos)
	case *gotypes.Struct:
		for i := 0; i < current.NumFields(); i++ {
			if err := e.collectRoot(current.Field(i).Type(), pos); err != nil {
				return err
			}
		}
	case *gotypes.Named:
		// A host or standard-library generic such as a typed channel handle
		// carries program data in its type arguments.
		args := current.TypeArgs()
		for i := 0; i < args.Len(); i++ {
			if err := e.collectRoot(args.At(i), pos); err != nil {
				return err
			}
		}
	}
	return nil
}

// programDataRoot reports whether typ is a data shape, a tree of pointers,
// slices, arrays and maps over data leaves, in which a program named type
// occurs. Such a type gets a descriptor of its own: zero values and literals
// of it, such as zero<map<string,pkg.T>>, resolve through it.
func (e *descriptorExtractor) programDataRoot(typ gotypes.Type) bool {
	program := false
	var shape func(gotypes.Type) bool
	shape = func(typ gotypes.Type) bool {
		switch current := gotypes.Unalias(typ).(type) {
		case *gotypes.Named:
			if e.programNamed(current) {
				program = true
				return true
			}
			_, basic := current.Underlying().(*gotypes.Basic)
			return basic
		case *gotypes.Basic:
			return true
		case *gotypes.Interface:
			return current.NumMethods() == 0
		case *gotypes.Pointer:
			return shape(current.Elem())
		case *gotypes.Slice:
			return shape(current.Elem())
		case *gotypes.Array:
			return shape(current.Elem())
		case *gotypes.Map:
			return shape(current.Key()) && shape(current.Elem())
		}
		return false
	}
	return shape(typ) && program
}

// programNamed reports whether named is a data type the program itself
// declares: in the translated package or in a package that is neither the
// standard library nor bound by the host (a contract or DTO package). Error
// and context types are host values whatever their package.
func (e *descriptorExtractor) programNamed(named *gotypes.Named) bool {
	pkg := named.Obj().Pkg()
	if pkg == nil || isContextLikeType(named) || isErrorGoType(named) {
		return false
	}
	if pkg.Path() != e.translator.packagePath && (isStdlibPath(pkg.Path()) || e.translator.hostPackages[pkg.Path()]) {
		return false
	}
	switch named.Underlying().(type) {
	case *gotypes.Struct, *gotypes.Basic, *gotypes.Pointer, *gotypes.Slice, *gotypes.Array, *gotypes.Map:
		return true
	}
	return false
}

func (e *descriptorExtractor) typeString(typ gotypes.Type) string {
	return gotypes.TypeString(typ, func(pkg *gotypes.Package) string { return pkg.Name() })
}

func isSetUpdateHandlerCall(call *ast.CallExpr) bool {
	switch target := call.Fun.(type) {
	case *ast.SelectorExpr:
		return target.Sel.Name == "SetUpdateHandler"
	case *ast.Ident:
		return target.Name == "SetUpdateHandler"
	}
	return false
}

// collectPayloadFields registers the payload types of an update handler's
// parameters or results, data shapes without a program named type included.
func (e *descriptorExtractor) collectPayloadFields(fields *ast.FieldList, skipFirst bool) error {
	if fields == nil {
		return nil
	}
	index := 0
	for _, field := range fields.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			if skipFirst && index == 0 {
				index++
				continue
			}
			index++
			typ := e.translator.info.TypeOf(field.Type)
			if typ == nil || isErrorGoType(typ) || isContextLikeType(typ) {
				continue
			}
			if _, err := e.addType(typ, field.Pos(), e.typeString(typ)); err != nil {
				return err
			}
		}
	}
	return nil
}

// entryDescriptorSkipped reports whether the entry boundary has no descriptor
// for typ: plain data such as int64 or []string travels untyped.
func entryDescriptorSkipped(typ gotypes.Type) bool {
	return typ == nil || isErrorGoType(typ) || isContextLikeType(typ) || !containsNamedDTO(typ)
}

// extractEntrySignature reads the payload boundary of the translated function.
//
// It runs from inside extractDescriptors, after the program types are collected
// but before fingerprinting, so every ID it returns is a key of the descriptor
// map. It deliberately reuses addType rather than canonicalNamedID, because a
// pointer parameter's descriptor is keyed "*"+elem and canonicalNamedID cannot
// express that at all.
func (e *descriptorExtractor) extractEntrySignature(fn *ast.FuncDecl) (EntrySignature, error) {
	var entry EntrySignature

	name, typ, ok := entryInputParam(e.translator, fn.Type.Params)
	if ok && !entryDescriptorSkipped(typ) {
		id, err := e.addType(typ, fn.Type.Params.Pos(), e.typeString(typ))
		if err != nil {
			return EntrySignature{}, err
		}
		// Both halves must survive together: an ID with no variable leaves the
		// host nowhere to bind, a variable with no ID leaves it nothing to decode.
		entry.InputVar = name
		entry.Input = id
	}

	if typ, ok := entryOutputType(e.translator, fn.Type.Results); ok && !entryDescriptorSkipped(typ) {
		id, err := e.addType(typ, fn.Type.Results.Pos(), e.typeString(typ))
		if err != nil {
			return EntrySignature{}, err
		}
		entry.Output = id
	}
	return entry, nil
}

// entryInputParam returns the business input parameter: the first one after the
// leading context, and only when the body can actually reference it by name.
//
// An unnamed parameter, or several parameters sharing one type, yield no usable
// identifier — the translator never seeds those into locals, so the body has no
// way to mention them. Rather than guess, report no input and let the caller
// fall back to the untyped path.
func entryInputParam(t *translator, params *ast.FieldList) (string, gotypes.Type, bool) {
	if params == nil {
		return "", nil, false
	}
	index := 0
	for _, field := range params.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		if count > 1 {
			return "", nil, false
		}
		// The leading ctx is not payload.
		if index == 0 {
			index += count
			continue
		}
		if len(field.Names) == 0 {
			return "", nil, false
		}
		return field.Names[0].Name, t.info.TypeOf(field.Type), true
	}
	return "", nil, false
}

// entryOutputType returns the first non-error result, i.e. the business value a
// workflow hands back. A lone error result yields nothing, which is what makes
// Output empty for error-only workflows.
func entryOutputType(t *translator, results *ast.FieldList) (gotypes.Type, bool) {
	if results == nil {
		return nil, false
	}
	for _, field := range results.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			typ := t.info.TypeOf(field.Type)
			if typ == nil || isErrorGoType(typ) {
				continue
			}
			return typ, true
		}
	}
	return nil, false
}

func containsNamedDTO(typ gotypes.Type) bool {
	typ = gotypes.Unalias(typ)
	switch current := typ.(type) {
	case *gotypes.Named:
		if current.Obj().Name() == "Context" {
			return false
		}
		switch current.Underlying().(type) {
		case *gotypes.Struct, *gotypes.Basic:
			return true
		}
		return false
	case *gotypes.Pointer:
		return containsNamedDTO(current.Elem())
	case *gotypes.Slice:
		return containsNamedDTO(current.Elem())
	case *gotypes.Array:
		return containsNamedDTO(current.Elem())
	case *gotypes.Map:
		return containsNamedDTO(current.Key()) || containsNamedDTO(current.Elem())
	}
	return false
}

func isErrorGoType(typ gotypes.Type) bool {
	errorType := gotypes.Universe.Lookup("error").Type().Underlying().(*gotypes.Interface)
	return gotypes.Implements(typ, errorType)
}

func isContextLikeType(typ gotypes.Type) bool {
	named, ok := gotypes.Unalias(typ).(*gotypes.Named)
	return ok && named.Obj().Name() == "Context"
}

// addType registers the descriptor of typ and, transitively, of every type
// it reaches: struct fields, pointer, slice, array and map elements, named
// types' underlying types. Recursive types terminate: a named type met again
// while its descriptor is being built is referenced by ID. path names the
// position inside the root type, such as Input.Items[].Meta, for diagnostics.
func (e *descriptorExtractor) addType(typ gotypes.Type, pos token.Pos, path string) (string, error) {
	typ = gotypes.Unalias(typ)
	switch current := typ.(type) {
	case *gotypes.Basic:
		name := goTypeToWlang(current.Name())
		if isSerializableBuiltin(name) {
			return name, nil
		}
		return "", e.unsupportedAt(pos, path, typ, "type "+current.String()+" is outside json/plain-v1")
	case *gotypes.Interface:
		if current.NumMethods() == 0 {
			return wltypes.TAny, nil
		}
		return "", e.unsupportedAt(pos, path, typ, "interfaces with methods cannot cross workflow boundaries")
	case *gotypes.Named:
		return e.addNamed(current, pos, path)
	case *gotypes.Pointer:
		elemID, err := e.addType(current.Elem(), pos, path)
		if err != nil {
			return "", err
		}
		return e.addComposite(wltypes.TypeKindPointer, "*"+elemID, e.translator.languageTypeName(current), "", elemID, 0), nil
	case *gotypes.Slice:
		elemID, err := e.addType(current.Elem(), pos, path+"[]")
		if err != nil {
			return "", err
		}
		return e.addComposite(wltypes.TypeKindSlice, "[]"+elemID, e.translator.languageTypeName(current), "", elemID, 0), nil
	case *gotypes.Array:
		elemID, err := e.addType(current.Elem(), pos, path+"[]")
		if err != nil {
			return "", err
		}
		id := fmt.Sprintf("[%d]%s", current.Len(), elemID)
		return e.addComposite(wltypes.TypeKindArray, id, e.translator.languageTypeName(current), "", elemID, current.Len()), nil
	case *gotypes.Map:
		keyID, err := e.addType(current.Key(), pos, path+"[key]")
		if err != nil {
			return "", err
		}
		if !e.hasStringUnderlying(keyID, map[string]bool{}) {
			return "", e.unsupportedAt(pos, path, typ, "json/plain-v1 map keys must be string")
		}
		elemID, err := e.addType(current.Elem(), pos, path+"[]")
		if err != nil {
			return "", err
		}
		id := "map[string]" + elemID
		return e.addComposite(wltypes.TypeKindMap, id, e.translator.languageTypeName(current), keyID, elemID, 0), nil
	case *gotypes.Chan:
		return "", e.unsupportedAt(pos, path, typ, "channels cannot cross workflow boundaries")
	case *gotypes.Signature:
		return "", e.unsupportedAt(pos, path, typ, "functions cannot cross workflow boundaries")
	case *gotypes.Struct:
		return "", e.unsupportedAt(pos, path, typ, "anonymous struct types are not supported; declare a named type")
	default:
		return "", e.unsupportedAt(pos, path, typ, "type "+typ.String()+" is outside json/plain-v1")
	}
}

func (e *descriptorExtractor) hasStringUnderlying(id string, visiting map[string]bool) bool {
	if id == wltypes.TString {
		return true
	}
	if visiting[id] {
		return false
	}
	descriptor, ok := e.descriptors[id]
	if !ok || descriptor.Kind != wltypes.TypeKindAlias {
		return false
	}
	visiting[id] = true
	return e.hasStringUnderlying(descriptor.Underlying, visiting)
}

func (e *descriptorExtractor) addNamed(current *gotypes.Named, pos token.Pos, path string) (string, error) {
	id := canonicalNamedID(current, e.translator.packagePath)
	if _, ok := e.descriptors[id]; ok {
		return id, nil
	}
	if e.visiting[id] {
		return id, nil
	}
	if current.TypeArgs().Len() > 0 {
		return "", e.unsupportedAt(pos, path, current, "generic type instantiations are not supported; declare a non-generic type")
	}
	if hasCustomJSONMethods(current) {
		return "", e.unsupportedAt(pos, path, current, "custom JSON codec on "+id+" requires a dedicated payload encoding")
	}
	e.visiting[id] = true
	defer delete(e.visiting, id)
	descriptor := wltypes.TypeDescriptor{ID: id, LanguageName: e.translator.languageTypeName(current)}
	switch underlying := current.Underlying().(type) {
	case *gotypes.Struct:
		descriptor.Kind = wltypes.TypeKindRecord
		seenJSON := map[string]bool{}
		for i := 0; i < underlying.NumFields(); i++ {
			field := underlying.Field(i)
			fieldPath := path + "." + field.Name()
			if field.Anonymous() {
				return "", e.unsupportedAt(field.Pos(), fieldPath, field.Type(), "embedded field "+field.Name()+" is outside json/plain-v1")
			}
			if !field.Exported() {
				return "", e.unsupportedAt(field.Pos(), fieldPath, field.Type(), "unexported field "+field.Name()+" is outside json/plain-v1")
			}
			jsonName, omitEmpty, ignored, err := parseJSONFieldTag(underlying.Tag(i), field.Name())
			if err != nil {
				return "", e.unsupportedAt(field.Pos(), fieldPath, field.Type(), err.Error())
			}
			if !ignored && seenJSON[jsonName] {
				return "", e.unsupportedAt(field.Pos(), fieldPath, field.Type(), "duplicate JSON field name "+strconv.Quote(jsonName))
			}
			if !ignored {
				seenJSON[jsonName] = true
			}
			fieldTypeID, err := e.addType(field.Type(), field.Pos(), fieldPath)
			if err != nil {
				return "", err
			}
			if ignored {
				jsonName = "-"
			}
			descriptor.Fields = append(descriptor.Fields, wltypes.FieldDescriptor{
				GoName: field.Name(), JSONName: jsonName, TypeID: fieldTypeID, OmitEmpty: omitEmpty,
			})
		}
	default:
		underlyingID, err := e.addType(current.Underlying(), pos, path)
		if err != nil {
			return "", err
		}
		descriptor.Kind = wltypes.TypeKindAlias
		descriptor.Underlying = underlyingID
	}
	e.descriptors[id] = descriptor
	return id, nil
}

func (e *descriptorExtractor) addComposite(kind wltypes.TypeKind, id, languageName, key, elem string, length int64) string {
	if _, ok := e.descriptors[id]; !ok {
		descriptor := wltypes.TypeDescriptor{
			ID: id, LanguageName: languageName, Kind: kind, Key: key, Elem: elem, Length: length,
		}
		descriptor.Fingerprint = wltypes.DescriptorFingerprint(descriptor)
		e.descriptors[id] = descriptor
	}
	return id
}

func canonicalNamedID(named *gotypes.Named, localPackagePath string) string {
	pkg := named.Obj().Pkg()
	path := localPackagePath
	if pkg != nil && pkg.Path() != "" {
		path = pkg.Path()
	}
	if path == "" {
		return named.Obj().Name()
	}
	return path + "." + named.Obj().Name()
}

func isSerializableBuiltin(name string) bool {
	switch name {
	case wltypes.TString, wltypes.TBoolean,
		wltypes.TInt8, wltypes.TInt16, wltypes.TInt32, wltypes.TInt64,
		wltypes.TUint8, wltypes.TUint16, wltypes.TUint32, wltypes.TUint64,
		wltypes.TFloat32, wltypes.TFloat64, wltypes.TAny:
		return true
	}
	return false
}

func hasCustomJSONMethods(named *gotypes.Named) bool {
	for _, typ := range []gotypes.Type{named, gotypes.NewPointer(named)} {
		methods := gotypes.NewMethodSet(typ)
		for i := 0; i < methods.Len(); i++ {
			switch methods.At(i).Obj().Name() {
			case "MarshalJSON", "UnmarshalJSON":
				return true
			}
		}
	}
	return false
}

func parseJSONFieldTag(raw, fallback string) (name string, omitEmpty, ignored bool, err error) {
	tag, ok := reflect.StructTag(raw).Lookup("json")
	if !ok || tag == "" {
		return fallback, false, false, nil
	}
	parts := strings.Split(tag, ",")
	if parts[0] == "-" {
		return "-", false, true, nil
	}
	name = parts[0]
	if name == "" {
		name = fallback
	}
	for _, option := range parts[1:] {
		switch option {
		case "omitempty":
			omitEmpty = true
		case "":
		default:
			return "", false, false, fmt.Errorf("json option %q requires a dedicated codec", option)
		}
	}
	return name, omitEmpty, false, nil
}

// unsupportedAt reports a type the program touches that has no json/plain-v1
// representation, at the source position that brought it in and with its
// path inside the root type: "Input.Items[].Meta (chan int): channels ...".
func (e *descriptorExtractor) unsupportedAt(pos token.Pos, path string, typ gotypes.Type, reason string) error {
	if name := e.typeString(typ); name != path {
		path += " (" + name + ")"
	}
	return e.unsupported(pos, path+": "+reason)
}

func (e *descriptorExtractor) unsupported(pos token.Pos, reason string) error {
	position := e.translator.fset.Position(pos)
	return &DiagnosticError{
		Filename: position.Filename, Line: position.Line, Column: position.Column,
		Node: "serializable type", Reason: reason,
	}
}
