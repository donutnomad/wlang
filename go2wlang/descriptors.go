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

func (t *translator) extractDescriptors(fn *ast.FuncDecl) (map[string]wltypes.TypeDescriptor, error) {
	extractor := &descriptorExtractor{
		translator: t, descriptors: map[string]wltypes.TypeDescriptor{}, visiting: map[string]bool{},
	}
	if err := extractor.collectFieldList(fn.Type.Params, true, false); err != nil {
		return nil, err
	}
	if err := extractor.collectFieldList(fn.Type.Results, false, false); err != nil {
		return nil, err
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
		return nil, firstErr
	}
	for id, descriptor := range extractor.descriptors {
		descriptor.Fingerprint = wltypes.DescriptorFingerprint(descriptor)
		extractor.descriptors[id] = descriptor
	}
	t.descriptorNames = map[string]bool{}
	for _, descriptor := range extractor.descriptors {
		t.descriptorNames[descriptor.LanguageName] = true
	}
	return extractor.descriptors, nil
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
