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
	visiting    map[string]bool
}

func (t *translator) extractDescriptors(fn *ast.FuncDecl) (map[string]wltypes.TypeDescriptor, EntrySignature, error) {
	extractor := &descriptorExtractor{
		translator: t, descriptors: map[string]wltypes.TypeDescriptor{}, visiting: map[string]bool{},
	}
	if err := extractor.collectFieldList(fn.Type.Params, true, false); err != nil {
		return nil, EntrySignature{}, err
	}
	if err := extractor.collectFieldList(fn.Type.Results, false, false); err != nil {
		return nil, EntrySignature{}, err
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
		if err := extractor.collectFieldList(literal.Type.Params, true, true); err != nil {
			firstErr = err
			return false
		}
		if err := extractor.collectFieldList(literal.Type.Results, false, true); err != nil {
			firstErr = err
			return false
		}
		return true
	})
	if firstErr != nil {
		return nil, EntrySignature{}, firstErr
	}
	// body-local DTO：`var detail ChargeFailure` / `detail := ChargeFailure{}` 这类局部变量
	// 不进入口或契约签名，但翻译出的 zero<T> 仍需要 descriptor，否则运行期报
	// E_SYMBOL: type not registered。这里沿既有 addType 机制补齐。
	if err := extractor.collectBodyLocalTypes(fn.Body); err != nil {
		return nil, EntrySignature{}, err
	}
	// Translated helpers use their own parameter, result and local types.
	for _, helper := range t.helperOrder {
		decl := helper.decl
		for _, fields := range []*ast.FieldList{decl.Recv, decl.Type.Params, decl.Type.Results} {
			if err := extractor.collectFieldList(fields, false, false); err != nil {
				return nil, EntrySignature{}, err
			}
		}
		if err := extractor.collectBodyLocalTypes(decl.Body); err != nil {
			return nil, EntrySignature{}, err
		}
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

func isSetUpdateHandlerCall(call *ast.CallExpr) bool {
	switch target := call.Fun.(type) {
	case *ast.SelectorExpr:
		return target.Sel.Name == "SetUpdateHandler"
	case *ast.Ident:
		return target.Name == "SetUpdateHandler"
	}
	return false
}

func (e *descriptorExtractor) collectFieldList(fields *ast.FieldList, skipFirst, payloadBoundary bool) error {
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
			if typ == nil || isErrorGoType(typ) || isContextLikeType(typ) || (!payloadBoundary && !containsNamedDTO(typ)) {
				continue
			}
			if _, err := e.addType(typ, field.Pos()); err != nil {
				return err
			}
		}
	}
	return nil
}

// entryDescriptorSkipped reports whether collectFieldList would have skipped this
// type at a non-payload boundary. Entry extraction must ask the same question,
// otherwise Entry could name an ID that never made it into Result.Types.
func entryDescriptorSkipped(typ gotypes.Type) bool {
	return typ == nil || isErrorGoType(typ) || isContextLikeType(typ) || !containsNamedDTO(typ)
}

// extractEntrySignature reads the payload boundary of the translated function.
//
// It runs from inside extractDescriptors, after the field lists are collected
// but before fingerprinting, so every ID it returns is a key of the descriptor
// map. It deliberately reuses addType rather than canonicalNamedID, because a
// pointer parameter's descriptor is keyed "*"+elem and canonicalNamedID cannot
// express that at all.
func (e *descriptorExtractor) extractEntrySignature(fn *ast.FuncDecl) (EntrySignature, error) {
	var entry EntrySignature

	name, typ, ok := entryInputParam(e.translator, fn.Type.Params)
	if ok && !entryDescriptorSkipped(typ) {
		id, err := e.addType(typ, fn.Type.Params.Pos())
		if err != nil {
			return EntrySignature{}, err
		}
		// Both halves must survive together: an ID with no variable leaves the
		// host nowhere to bind, a variable with no ID leaves it nothing to decode.
		entry.InputVar = name
		entry.Input = id
	}

	if typ, ok := entryOutputType(e.translator, fn.Type.Results); ok && !entryDescriptorSkipped(typ) {
		id, err := e.addType(typ, fn.Type.Results.Pos())
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
		// Mirror collectFieldList(skipFirst: true): the leading ctx is not payload.
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

func (e *descriptorExtractor) addType(typ gotypes.Type, pos token.Pos) (string, error) {
	typ = gotypes.Unalias(typ)
	switch current := typ.(type) {
	case *gotypes.Basic:
		name := goTypeToWlang(current.Name())
		if isSerializableBuiltin(name) {
			return name, nil
		}
		return "", e.unsupported(pos, "type "+current.String()+" is outside json/plain-v1")
	case *gotypes.Interface:
		if current.NumMethods() == 0 {
			return wltypes.TAny, nil
		}
		return "", e.unsupported(pos, "interface "+current.String()+" is outside json/plain-v1")
	case *gotypes.Named:
		return e.addNamed(current, pos)
	case *gotypes.Pointer:
		elemID, err := e.addType(current.Elem(), pos)
		if err != nil {
			return "", err
		}
		return e.addComposite(wltypes.TypeKindPointer, "*"+elemID, e.translator.languageTypeName(current), "", elemID, 0), nil
	case *gotypes.Slice:
		elemID, err := e.addType(current.Elem(), pos)
		if err != nil {
			return "", err
		}
		return e.addComposite(wltypes.TypeKindSlice, "[]"+elemID, e.translator.languageTypeName(current), "", elemID, 0), nil
	case *gotypes.Array:
		elemID, err := e.addType(current.Elem(), pos)
		if err != nil {
			return "", err
		}
		id := fmt.Sprintf("[%d]%s", current.Len(), elemID)
		return e.addComposite(wltypes.TypeKindArray, id, e.translator.languageTypeName(current), "", elemID, current.Len()), nil
	case *gotypes.Map:
		keyID, err := e.addType(current.Key(), pos)
		if err != nil {
			return "", err
		}
		if !e.hasStringUnderlying(keyID, map[string]bool{}) {
			return "", e.unsupported(pos, "json/plain-v1 map keys must be string")
		}
		elemID, err := e.addType(current.Elem(), pos)
		if err != nil {
			return "", err
		}
		id := "map[string]" + elemID
		return e.addComposite(wltypes.TypeKindMap, id, e.translator.languageTypeName(current), keyID, elemID, 0), nil
	default:
		return "", e.unsupported(pos, "type "+typ.String()+" is outside json/plain-v1")
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

func (e *descriptorExtractor) addNamed(current *gotypes.Named, pos token.Pos) (string, error) {
	id := canonicalNamedID(current, e.translator.packagePath)
	if _, ok := e.descriptors[id]; ok {
		return id, nil
	}
	if e.visiting[id] {
		return "", e.unsupported(pos, "recursive type graph at "+id)
	}
	if hasCustomJSONMethods(current) {
		return "", e.unsupported(pos, "custom JSON codec on "+id+" requires a dedicated payload encoding")
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
			if field.Anonymous() {
				return "", e.unsupported(field.Pos(), "embedded field "+field.Name()+" is outside json/plain-v1")
			}
			if !field.Exported() {
				return "", e.unsupported(field.Pos(), "unexported field "+field.Name()+" is outside json/plain-v1")
			}
			jsonName, omitEmpty, ignored, err := parseJSONFieldTag(underlying.Tag(i), field.Name())
			if err != nil {
				return "", e.unsupported(field.Pos(), err.Error())
			}
			if !ignored && seenJSON[jsonName] {
				return "", e.unsupported(field.Pos(), "duplicate JSON field name "+strconv.Quote(jsonName))
			}
			if !ignored {
				seenJSON[jsonName] = true
			}
			fieldTypeID, err := e.addType(field.Type(), field.Pos())
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
		underlyingID, err := e.addType(current.Underlying(), pos)
		if err != nil {
			return "", err
		}
		descriptor.Kind = wltypes.TypeKindAlias
		descriptor.Underlying = underlyingID
	}
	descriptor.Fingerprint = wltypes.DescriptorFingerprint(descriptor)
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

func (e *descriptorExtractor) unsupported(pos token.Pos, reason string) error {
	position := e.translator.fset.Position(pos)
	return &DiagnosticError{
		Filename: position.Filename, Line: position.Line, Column: position.Column,
		Node: "serializable type", Reason: reason,
	}
}

// collectBodyLocalTypes 为函数体内显式声明的局部 DTO 类型补 descriptor。
//
// 只收集同时满足 containsNamedDTO 的类型（与入口/契约边界同一套过滤），因此标量与
// context/error 不会进入 Types。已有 descriptor 的类型由 addType 幂等复用。
func (e *descriptorExtractor) collectBodyLocalTypes(body *ast.BlockStmt) error {
	if body == nil {
		return nil
	}
	var firstErr error
	collect := func(typeExpr ast.Expr, pos token.Pos) {
		if firstErr != nil || typeExpr == nil {
			return
		}
		typ := e.translator.info.TypeOf(typeExpr)
		if typ == nil || isErrorGoType(typ) || isContextLikeType(typ) || !containsNamedDTO(typ) {
			return
		}
		if err := e.translator.checkForeignType(typ, typeExpr); err != nil {
			firstErr = err
			return
		}
		if _, err := e.addType(typ, pos); err != nil {
			firstErr = err
		}
	}
	ast.Inspect(body, func(node ast.Node) bool {
		if firstErr != nil {
			return false
		}
		switch current := node.(type) {
		case *ast.ValueSpec: // var x T
			collect(current.Type, current.Pos())
		case *ast.AssignStmt: // x := T{...} / x := &T{...}
			if current.Tok != token.DEFINE {
				return true
			}
			for _, rhs := range current.Rhs {
				if literalType := compositeLiteralType(rhs); literalType != nil {
					collect(literalType, rhs.Pos())
				}
			}
		}
		return true
	})
	return firstErr
}

// compositeLiteralType 返回复合字面量（含取地址）的类型表达式。
func compositeLiteralType(expr ast.Expr) ast.Expr {
	switch current := expr.(type) {
	case *ast.CompositeLit:
		return current.Type
	case *ast.UnaryExpr:
		if current.Op == token.AND {
			return compositeLiteralType(current.X)
		}
	}
	return nil
}
