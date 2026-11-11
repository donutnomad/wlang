package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	werr "github.com/donutnomad/wlang/errors"
	"github.com/donutnomad/wlang/types"
)

// DecodeJSONValue decodes one persisted JSON payload through the registry's
// descriptor graph. Integer widths and named DTO identity remain intact.
func (r *Registry) DecodeJSONValue(typeName string, raw json.RawMessage) (types.Value, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return types.Value{}, codecError("/", "empty JSON payload for %s", typeName)
	}
	id, ok := r.resolveCodecType(typeName)
	if !ok {
		return types.Value{}, codecError("/", "type %q has no JSON descriptor", typeName)
	}
	return r.decodeJSONByID(id, raw, "/")
}

// EncodeJSONValue encodes a runtime value through the same descriptor graph
// used by DecodeJSONValue.
func (r *Registry) EncodeJSONValue(value types.Value) (json.RawMessage, error) {
	id, ok := r.resolveCodecType(value.TypeName())
	if !ok {
		return nil, codecError("/", "type %q has no JSON descriptor", value.TypeName())
	}
	if err := r.validateValueByID(id, value, "/"); err != nil {
		return nil, err
	}
	carrier, err := r.encodeCarrierByID(id, value.Go(), "/")
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(carrier)
	if err != nil {
		return nil, codecError("/", "encode %s: %v", value.TypeName(), err)
	}
	return raw, nil
}

// ValidateValue verifies both a WLang type identity and its host-independent
// carrier graph against the registered descriptor.
func (r *Registry) ValidateValue(typeName string, value types.Value, path string) error {
	id, ok := r.resolveCodecType(typeName)
	if !ok {
		return codecError(path, "type %q has no descriptor", typeName)
	}
	return r.validateValueByID(id, value, path)
}

func (r *Registry) validateValueByID(id string, value types.Value, path string) error {
	if value.TypeName() == types.TNull {
		if r.isNullableTypeID(id) {
			return nil
		}
		return codecError(path, "type %s does not accept null", r.languageNameForTypeID(id))
	}
	expected := r.languageNameForTypeID(id)
	if value.TypeName() != expected {
		return codecError(path, "expected %s, got %s", expected, value.TypeName())
	}
	return r.validateCarrierByID(id, value.Go(), path)
}

func (r *Registry) validateCarrierByID(id string, carrier any, path string) error {
	if isBuiltinTypeID(id) {
		return validateBuiltinCarrier(id, carrier, path)
	}
	descriptor, ok := r.collectionDescriptor(id)
	if !ok {
		return codecError(path, "type descriptor %q is not defined", id)
	}
	switch descriptor.Kind {
	case types.TypeKindAlias:
		return r.validateCarrierByID(descriptor.Underlying, carrier, path)
	case types.TypeKindPointer:
		if carrier == nil {
			return nil
		}
		if _, ok := carrier.(*types.Record); !ok {
			carrier = indirectCarrier(carrier)
		}
		if carrier == nil {
			return nil
		}
		return r.validateCarrierByID(descriptor.Elem, carrier, path)
	case types.TypeKindSlice, types.TypeKindArray:
		carrier = indirectCarrier(carrier)
		if carrier == nil {
			if descriptor.Kind == types.TypeKindSlice {
				return nil
			}
			return codecError(path, "validate %s: null array", descriptor.LanguageName)
		}
		value := reflect.ValueOf(carrier)
		if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
			return codecError(path, "validate %s: expected array carrier, got %T", descriptor.LanguageName, carrier)
		}
		if descriptor.Kind == types.TypeKindArray && int64(value.Len()) != descriptor.Length {
			return codecError(path, "validate %s: array length %d, want %d", descriptor.LanguageName, value.Len(), descriptor.Length)
		}
		for i := 0; i < value.Len(); i++ {
			if err := r.validateCarrierByID(descriptor.Elem, value.Index(i).Interface(), codecIndexPath(path, i)); err != nil {
				return err
			}
		}
		return nil
	case types.TypeKindMap:
		carrier = indirectCarrier(carrier)
		if carrier == nil {
			return nil
		}
		if !r.descriptorHasStringUnderlying(descriptor.Key, map[string]bool{}) {
			return codecError(path, "validate %s: map key %q has non-string underlying type", descriptor.LanguageName, descriptor.Key)
		}
		value := reflect.ValueOf(carrier)
		if value.Kind() != reflect.Map {
			return codecError(path, "validate %s: expected map carrier, got %T", descriptor.LanguageName, carrier)
		}
		iter := value.MapRange()
		for iter.Next() {
			key := indirectReflect(iter.Key())
			if !key.IsValid() || key.Kind() != reflect.String {
				return codecError(path, "validate %s: map key carrier must be string", descriptor.LanguageName)
			}
			if err := r.validateCarrierByID(descriptor.Elem, iter.Value().Interface(), codecFieldPath(path, key.String())); err != nil {
				return err
			}
		}
		return nil
	case types.TypeKindRecord:
		record, ok := carrier.(*types.Record)
		if !ok || record == nil {
			return codecError(path, "validate %s: expected record carrier, got %T", descriptor.LanguageName, carrier)
		}
		if record.TypeName() != descriptor.LanguageName {
			return codecError(path, "validate %s: record carrier has type %s", descriptor.LanguageName, record.TypeName())
		}
		fields := record.Fields()
		known := make(map[string]types.FieldDescriptor, len(descriptor.Fields))
		for _, field := range descriptor.Fields {
			known[field.GoName] = field
			fieldValue, present := fields[field.GoName]
			if !present {
				return codecError(codecFieldPath(path, field.GoName), "record field is missing")
			}
			if err := r.validateValueByID(field.TypeID, fieldValue, codecFieldPath(path, field.GoName)); err != nil {
				return err
			}
		}
		for name := range fields {
			if _, ok := known[name]; !ok {
				return codecError(codecFieldPath(path, name), "record field is not declared")
			}
		}
		return nil
	default:
		return codecError(path, "type descriptor %q has unsupported kind %q", id, descriptor.Kind)
	}
}

func validateBuiltinCarrier(typeName string, carrier any, path string) error {
	if typeName == types.TAny {
		return nil
	}
	if typeName == types.TNull {
		if carrier == nil {
			return nil
		}
		return codecError(path, "null requires a nil carrier, got %T", carrier)
	}
	if typeName == types.TError {
		return codecError(path, "error is outside JSON payload values")
	}
	expected, err := primitiveReflectType(typeName)
	if err != nil {
		return codecError(path, "%v", err)
	}
	if carrier == nil || reflect.TypeOf(carrier) != expected {
		return codecError(path, "%s requires carrier %s, got %T", typeName, expected, carrier)
	}
	return nil
}

func (r *Registry) isNullableTypeID(id string) bool {
	if id == types.TAny || id == types.TNull {
		return true
	}
	descriptor, ok := r.collectionDescriptor(id)
	return ok && (descriptor.Kind == types.TypeKindPointer || descriptor.Kind == types.TypeKindSlice || descriptor.Kind == types.TypeKindMap)
}

func (r *Registry) languageNameForTypeID(id string) string {
	if isBuiltinTypeID(id) {
		return id
	}
	if descriptor, ok := r.collectionDescriptor(id); ok {
		return descriptor.LanguageName
	}
	return id
}

// ImportGoValue converts a materialized Go value into the descriptor-backed
// runtime carrier. Record fields are matched by Go name, which keeps host out
// arguments independent from JSON tag spelling.
func (r *Registry) ImportGoValue(typeName string, value any) (types.Value, error) {
	id, ok := r.resolveCodecType(typeName)
	if !ok {
		return types.Value{}, codecError("/", "type %q has no descriptor", typeName)
	}
	return r.importReflectByID(id, reflect.ValueOf(value), "/")
}

func (r *Registry) resolveCodecType(name string) (string, bool) {
	if isBuiltinTypeID(name) {
		return name, true
	}
	if id, ok := r.DescriptorID(name); ok {
		return id, true
	}
	if _, ok := r.genericCollectionDescriptor(name); ok {
		return name, true
	}
	return "", false
}

func (r *Registry) importReflectByID(id string, value reflect.Value, path string) (types.Value, error) {
	if isBuiltinTypeID(id) {
		return importBuiltinReflect(id, value, path)
	}
	descriptor, ok := r.collectionDescriptor(id)
	if !ok {
		return types.Value{}, codecError(path, "type descriptor %q is not defined", id)
	}
	switch descriptor.Kind {
	case types.TypeKindAlias:
		underlying, err := r.importReflectByID(descriptor.Underlying, value, path)
		if err != nil {
			return types.Value{}, err
		}
		return types.NewValue(descriptor.LanguageName, underlying.Go()), nil
	case types.TypeKindPointer:
		value, isNil := unwrapReflect(value)
		if isNil {
			return types.NewValue(descriptor.LanguageName, nil), nil
		}
		elem, err := r.importReflectByID(descriptor.Elem, value, path)
		if err != nil {
			return types.Value{}, err
		}
		return types.NewValue(descriptor.LanguageName, elem.Go()), nil
	case types.TypeKindSlice, types.TypeKindArray:
		value, isNil := unwrapReflect(value)
		if isNil {
			if descriptor.Kind == types.TypeKindArray {
				return types.Value{}, codecError(path, "import %s: null array", descriptor.LanguageName)
			}
			return types.NewValue(descriptor.LanguageName, nil), nil
		}
		if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
			return types.Value{}, codecError(path, "import %s: expected array, got %s", descriptor.LanguageName, value.Type())
		}
		if descriptor.Kind == types.TypeKindArray && int64(value.Len()) != descriptor.Length {
			return types.Value{}, codecError(path, "import %s: array length %d, want %d", descriptor.LanguageName, value.Len(), descriptor.Length)
		}
		items := make([]any, value.Len())
		for i := 0; i < value.Len(); i++ {
			item, err := r.importReflectByID(descriptor.Elem, value.Index(i), codecIndexPath(path, i))
			if err != nil {
				return types.Value{}, err
			}
			items[i] = item.Go()
		}
		return types.NewValue(descriptor.LanguageName, items), nil
	case types.TypeKindMap:
		value, isNil := unwrapReflect(value)
		if isNil {
			return types.NewValue(descriptor.LanguageName, nil), nil
		}
		if value.Kind() != reflect.Map {
			return types.Value{}, codecError(path, "import %s: expected map, got %s", descriptor.LanguageName, value.Type())
		}
		if !r.descriptorHasStringUnderlying(descriptor.Key, map[string]bool{}) {
			return types.Value{}, codecError(path, "import %s: map key %q has non-string underlying type", descriptor.LanguageName, descriptor.Key)
		}
		entries := make(map[string]any, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			key, isNil := unwrapReflect(iter.Key())
			if isNil || key.Kind() != reflect.String {
				return types.Value{}, codecError(path, "import %s: map key carrier must be string", descriptor.LanguageName)
			}
			name := key.String()
			item, err := r.importReflectByID(descriptor.Elem, iter.Value(), codecFieldPath(path, name))
			if err != nil {
				return types.Value{}, err
			}
			entries[name] = item.Go()
		}
		return types.NewValue(descriptor.LanguageName, entries), nil
	case types.TypeKindRecord:
		value, isNil := unwrapReflect(value)
		if isNil {
			return types.Value{}, codecError(path, "import %s: null record", descriptor.LanguageName)
		}
		if value.Kind() != reflect.Struct {
			return types.Value{}, codecError(path, "import %s: expected struct, got %s", descriptor.LanguageName, value.Type())
		}
		fields := make(map[string]types.Value, len(descriptor.Fields))
		aliases := make(map[string]string, len(descriptor.Fields))
		for _, field := range descriptor.Fields {
			reflected := value.FieldByName(field.GoName)
			if !reflected.IsValid() {
				return types.Value{}, codecError(codecFieldPath(path, field.GoName), "Go field is missing from %s", value.Type())
			}
			fieldValue, err := r.importReflectByID(field.TypeID, reflected, codecFieldPath(path, field.GoName))
			if err != nil {
				return types.Value{}, err
			}
			fields[field.GoName] = fieldValue
			if field.JSONName != "" && field.JSONName != "-" && field.JSONName != field.GoName {
				aliases[field.JSONName] = field.GoName
			}
		}
		return types.NewRecordWithAliases(descriptor.LanguageName, fields, aliases), nil
	default:
		return types.Value{}, codecError(path, "type descriptor %q has unsupported kind %q", id, descriptor.Kind)
	}
}

func importBuiltinReflect(typeName string, value reflect.Value, path string) (types.Value, error) {
	value, isNil := unwrapReflect(value)
	if isNil {
		if typeName == types.TAny || typeName == types.TNull {
			return types.NewValue(typeName, nil), nil
		}
		zero, ok := primitiveZero(typeName)
		if !ok {
			return types.Value{}, codecError(path, "type %q has no Go importer", typeName)
		}
		return zero, nil
	}
	if typeName == types.TAny {
		return types.NewValue(typeName, value.Interface()), nil
	}
	if typeName == types.TNull {
		return types.Value{}, codecError(path, "import null: got %s", value.Type())
	}
	target, err := primitiveReflectType(typeName)
	if err != nil {
		return types.Value{}, codecError(path, "%v", err)
	}
	if value.Type().AssignableTo(target) {
		return types.NewValue(typeName, value.Interface()), nil
	}
	if value.Type().ConvertibleTo(target) {
		return types.NewValue(typeName, value.Convert(target).Interface()), nil
	}
	return types.Value{}, codecError(path, "import %s: cannot convert %s", typeName, value.Type())
}

func unwrapReflect(value reflect.Value) (reflect.Value, bool) {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return reflect.Value{}, true
		}
		value = value.Elem()
	}
	return value, !value.IsValid()
}

func (r *Registry) decodeJSONByID(id string, raw json.RawMessage, path string) (types.Value, error) {
	if isBuiltinTypeID(id) {
		return decodeJSONBuiltin(id, raw, path)
	}
	descriptor, ok := r.collectionDescriptor(id)
	if !ok {
		return types.Value{}, codecError(path, "type descriptor %q is not defined", id)
	}
	switch descriptor.Kind {
	case types.TypeKindAlias:
		value, err := r.decodeJSONByID(descriptor.Underlying, raw, path)
		if err != nil {
			return types.Value{}, err
		}
		return types.NewValue(descriptor.LanguageName, value.Go()), nil
	case types.TypeKindPointer:
		if isJSONNull(raw) {
			return types.NewValue(descriptor.LanguageName, nil), nil
		}
		value, err := r.decodeJSONByID(descriptor.Elem, raw, path)
		if err != nil {
			return types.Value{}, err
		}
		return types.NewValue(descriptor.LanguageName, value.Go()), nil
	case types.TypeKindSlice:
		if isJSONNull(raw) {
			return types.NewValue(descriptor.LanguageName, nil), nil
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return types.Value{}, codecError(path, "decode %s: expected array: %v", descriptor.LanguageName, err)
		}
		values := make([]types.Value, len(items))
		for i, item := range items {
			value, err := r.decodeJSONByID(descriptor.Elem, item, codecIndexPath(path, i))
			if err != nil {
				return types.Value{}, err
			}
			values[i] = value
		}
		array, err := types.NewArray(r.languageNameForTypeID(descriptor.Elem), values)
		if err != nil {
			return types.Value{}, codecError(path, "decode %s: %v", descriptor.LanguageName, err)
		}
		return types.NewValue(descriptor.LanguageName, array.Go()), nil
	case types.TypeKindArray:
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return types.Value{}, codecError(path, "decode %s: expected array: %v", descriptor.LanguageName, err)
		}
		if int64(len(items)) != descriptor.Length {
			return types.Value{}, codecError(path, "decode %s: array length %d, want %d", descriptor.LanguageName, len(items), descriptor.Length)
		}
		out := make([]any, len(items))
		for i, item := range items {
			value, err := r.decodeJSONByID(descriptor.Elem, item, codecIndexPath(path, i))
			if err != nil {
				return types.Value{}, err
			}
			out[i] = value.Go()
		}
		return types.NewValue(descriptor.LanguageName, out), nil
	case types.TypeKindMap:
		if isJSONNull(raw) {
			return types.NewValue(descriptor.LanguageName, nil), nil
		}
		if !r.descriptorHasStringUnderlying(descriptor.Key, map[string]bool{}) {
			return types.Value{}, codecError(path, "decode %s: JSON map key %q has non-string underlying type", descriptor.LanguageName, descriptor.Key)
		}
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			return types.Value{}, codecError(path, "decode %s: expected object: %v", descriptor.LanguageName, err)
		}
		values := make(map[string]types.Value, len(entries))
		for key, item := range entries {
			value, err := r.decodeJSONByID(descriptor.Elem, item, codecFieldPath(path, key))
			if err != nil {
				return types.Value{}, err
			}
			values[key] = value
		}
		out := r.decodedMapCarrier(descriptor.Elem, values)
		return types.NewValue(descriptor.LanguageName, out), nil
	case types.TypeKindRecord:
		if isJSONNull(raw) {
			return r.zeroDescriptor(descriptor, path, map[string]bool{})
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return types.Value{}, codecError(path, "decode %s: expected object: %v", descriptor.LanguageName, err)
		}
		values := make(map[string]types.Value, len(descriptor.Fields))
		aliases := make(map[string]string, len(descriptor.Fields))
		for _, field := range descriptor.Fields {
			if field.JSONName == "-" {
				value, err := r.zeroByID(field.TypeID, codecFieldPath(path, field.GoName), map[string]bool{})
				if err != nil {
					return types.Value{}, err
				}
				values[field.GoName] = value
				continue
			}
			jsonName := field.JSONName
			if jsonName == "" {
				jsonName = field.GoName
			}
			fieldRaw, present := fields[jsonName]
			var value types.Value
			var err error
			if present {
				value, err = r.decodeJSONByID(field.TypeID, fieldRaw, codecFieldPath(path, jsonName))
			} else {
				value, err = r.zeroByID(field.TypeID, codecFieldPath(path, jsonName), map[string]bool{})
			}
			if err != nil {
				return types.Value{}, err
			}
			values[field.GoName] = value
			if jsonName != field.GoName {
				aliases[jsonName] = field.GoName
			}
		}
		return types.NewRecordWithAliases(descriptor.LanguageName, values, aliases), nil
	default:
		return types.Value{}, codecError(path, "type descriptor %q has unsupported kind %q", id, descriptor.Kind)
	}
}

func decodeJSONBuiltin(typeName string, raw json.RawMessage, path string) (types.Value, error) {
	if typeName == types.TAny {
		if isJSONNull(raw) {
			return types.NewValue(types.TAny, nil), nil
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return types.Value{}, codecError(path, "decode any: %v", err)
		}
		value, err := normalizeJSONAny(value, path)
		if err != nil {
			return types.Value{}, err
		}
		return types.NewValue(types.TAny, value), nil
	}
	if typeName == types.TNull {
		if !isJSONNull(raw) {
			return types.Value{}, codecError(path, "decode null: got %s", strings.TrimSpace(string(raw)))
		}
		return types.NewValue(types.TNull, nil), nil
	}
	if typeName == types.TError {
		return types.Value{}, codecError(path, "error is outside JSON payload values")
	}
	if isJSONNull(raw) {
		value, ok := primitiveZero(typeName)
		if !ok {
			return types.Value{}, codecError(path, "type %q has no JSON decoder", typeName)
		}
		return value, nil
	}
	var target any
	switch typeName {
	case types.TString:
		target = new(string)
	case types.TBoolean:
		target = new(bool)
	case types.TInt8:
		target = new(int8)
	case types.TInt16:
		target = new(int16)
	case types.TInt32:
		target = new(int32)
	case types.TInt64:
		target = new(int64)
	case types.TUint8:
		target = new(uint8)
	case types.TUint16:
		target = new(uint16)
	case types.TUint32:
		target = new(uint32)
	case types.TUint64:
		target = new(uint64)
	case types.TFloat32:
		target = new(float32)
	case types.TFloat64:
		target = new(float64)
	default:
		return types.Value{}, codecError(path, "type %q has no JSON decoder", typeName)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return types.Value{}, codecError(path, "decode %s: %v", typeName, err)
	}
	return types.NewValue(typeName, reflect.ValueOf(target).Elem().Interface()), nil
}

func normalizeJSONAny(value any, path string) (any, error) {
	switch current := value.(type) {
	case json.Number:
		raw := current.String()
		if strings.ContainsAny(raw, ".eE") {
			decoded, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, codecError(path, "decode any number %q: %v", raw, err)
			}
			return decoded, nil
		}
		if decoded, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return decoded, nil
		}
		if decoded, err := strconv.ParseUint(raw, 10, 64); err == nil {
			return decoded, nil
		}
		return nil, codecError(path, "decode any integer %q exceeds int64 and uint64", raw)
	case []any:
		out := make([]any, len(current))
		for i, item := range current {
			decoded, err := normalizeJSONAny(item, codecIndexPath(path, i))
			if err != nil {
				return nil, err
			}
			out[i] = decoded
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(current))
		for key, item := range current {
			decoded, err := normalizeJSONAny(item, codecFieldPath(path, key))
			if err != nil {
				return nil, err
			}
			out[key] = decoded
		}
		return out, nil
	default:
		return value, nil
	}
}

func (r *Registry) encodeCarrierByID(id string, carrier any, path string) (any, error) {
	if isBuiltinTypeID(id) {
		if id == types.TError {
			return nil, codecError(path, "error is outside JSON payload values")
		}
		return carrier, nil
	}
	descriptor, ok := r.collectionDescriptor(id)
	if !ok {
		return nil, codecError(path, "type descriptor %q is not defined", id)
	}
	switch descriptor.Kind {
	case types.TypeKindAlias:
		return r.encodeCarrierByID(descriptor.Underlying, carrier, path)
	case types.TypeKindPointer:
		if carrier == nil {
			return nil, nil
		}
		if _, ok := carrier.(*types.Record); !ok {
			carrier = indirectCarrier(carrier)
		}
		if carrier == nil {
			return nil, nil
		}
		return r.encodeCarrierByID(descriptor.Elem, carrier, path)
	case types.TypeKindSlice, types.TypeKindArray:
		carrier = indirectCarrier(carrier)
		if carrier == nil {
			return nil, nil
		}
		value := reflect.ValueOf(carrier)
		if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
			return nil, codecError(path, "encode %s: expected array carrier, got %T", descriptor.LanguageName, carrier)
		}
		if value.Kind() == reflect.Slice && value.IsNil() {
			// A nil slice encodes as null, as encoding/json does.
			return nil, nil
		}
		if descriptor.Kind == types.TypeKindArray && int64(value.Len()) != descriptor.Length {
			return nil, codecError(path, "encode %s: array length %d, want %d", descriptor.LanguageName, value.Len(), descriptor.Length)
		}
		out := make([]any, value.Len())
		for i := 0; i < value.Len(); i++ {
			item, err := r.encodeCarrierByID(descriptor.Elem, value.Index(i).Interface(), codecIndexPath(path, i))
			if err != nil {
				return nil, err
			}
			out[i] = item
		}
		return out, nil
	case types.TypeKindMap:
		carrier = indirectCarrier(carrier)
		if carrier == nil {
			return nil, nil
		}
		if !r.descriptorHasStringUnderlying(descriptor.Key, map[string]bool{}) {
			return nil, codecError(path, "encode %s: JSON map key %q has non-string underlying type", descriptor.LanguageName, descriptor.Key)
		}
		value := reflect.ValueOf(carrier)
		if value.Kind() != reflect.Map {
			return nil, codecError(path, "encode %s: expected map carrier, got %T", descriptor.LanguageName, carrier)
		}
		if value.IsNil() {
			return nil, nil
		}
		out := make(map[string]any, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			key := indirectReflect(iter.Key())
			if key.Kind() != reflect.String {
				return nil, codecError(path, "encode %s: map key carrier %s is not string", descriptor.LanguageName, key.Type())
			}
			name := key.String()
			item, err := r.encodeCarrierByID(descriptor.Elem, iter.Value().Interface(), codecFieldPath(path, name))
			if err != nil {
				return nil, err
			}
			out[name] = item
		}
		return out, nil
	case types.TypeKindRecord:
		record, ok := carrier.(*types.Record)
		if !ok {
			if value, valueOK := carrier.(types.Record); valueOK {
				record = &value
				ok = true
			}
		}
		if !ok {
			return nil, codecError(path, "encode %s: expected record carrier, got %T", descriptor.LanguageName, carrier)
		}
		out := make(map[string]any, len(descriptor.Fields))
		for _, field := range descriptor.Fields {
			if field.JSONName == "-" {
				continue
			}
			value, present := record.Field(field.GoName)
			if !present {
				return nil, codecError(codecFieldPath(path, field.GoName), "record field is missing")
			}
			if field.OmitEmpty && isEmptyJSONValue(value.Go()) {
				continue
			}
			name := field.JSONName
			if name == "" {
				name = field.GoName
			}
			encoded, err := r.encodeCarrierByID(field.TypeID, value.Go(), codecFieldPath(path, name))
			if err != nil {
				return nil, err
			}
			out[name] = encoded
		}
		return out, nil
	default:
		return nil, codecError(path, "type descriptor %q has unsupported kind %q", id, descriptor.Kind)
	}
}

func (r *Registry) descriptorHasStringUnderlying(id string, visiting map[string]bool) bool {
	if id == types.TString {
		return true
	}
	if visiting[id] {
		return false
	}
	descriptor, ok := r.Descriptor(id)
	if !ok || descriptor.Kind != types.TypeKindAlias {
		return false
	}
	visiting[id] = true
	return r.descriptorHasStringUnderlying(descriptor.Underlying, visiting)
}

func (r *Registry) collectionDescriptor(id string) (types.TypeDescriptor, bool) {
	if descriptor, ok := r.Descriptor(id); ok {
		return descriptor, true
	}
	return r.genericCollectionDescriptor(id)
}

func (r *Registry) genericCollectionDescriptor(typeName string) (types.TypeDescriptor, bool) {
	if elemName, ok := splitGenericArrayType(typeName); ok {
		elemID, resolved := r.resolveCodecType(elemName)
		if !resolved {
			return types.TypeDescriptor{}, false
		}
		return types.TypeDescriptor{
			ID: typeName, LanguageName: typeName, Kind: types.TypeKindSlice, Elem: elemID,
		}, true
	}
	keyName, elemName, ok := splitGenericMapType(typeName)
	if !ok {
		return types.TypeDescriptor{}, false
	}
	keyID, keyResolved := r.resolveCodecType(keyName)
	elemID, elemResolved := r.resolveCodecType(elemName)
	if !keyResolved || !elemResolved || !r.descriptorHasStringUnderlying(keyID, map[string]bool{}) {
		return types.TypeDescriptor{}, false
	}
	return types.TypeDescriptor{
		ID: typeName, LanguageName: typeName, Kind: types.TypeKindMap, Key: keyID, Elem: elemID,
	}, true
}

func splitGenericArrayType(typeName string) (string, bool) {
	const prefix = "array<"
	if !strings.HasPrefix(typeName, prefix) || !strings.HasSuffix(typeName, ">") {
		return "", false
	}
	elem := typeName[len(prefix) : len(typeName)-1]
	return elem, elem != ""
}

func splitGenericMapType(typeName string) (string, string, bool) {
	const prefix = "map<"
	if !strings.HasPrefix(typeName, prefix) || !strings.HasSuffix(typeName, ">") {
		return "", "", false
	}
	body := typeName[len(prefix) : len(typeName)-1]
	depth := 0
	for i, char := range body {
		switch char {
		case '<':
			depth++
		case '>':
			depth--
			if depth < 0 {
				return "", "", false
			}
		case ',':
			if depth == 0 && i > 0 && i < len(body)-1 {
				return body[:i], body[i+1:], true
			}
		}
	}
	return "", "", false
}

func (r *Registry) decodedMapCarrier(elemID string, values map[string]types.Value) any {
	elemType, err := primitiveReflectType(elemID)
	if err != nil || elemID == types.TAny {
		out := make(map[string]any, len(values))
		for key, value := range values {
			out[key] = value.Go()
		}
		return out
	}
	out := reflect.MakeMapWithSize(reflect.MapOf(reflect.TypeOf(""), elemType), len(values))
	for key, value := range values {
		out.SetMapIndex(reflect.ValueOf(key), reflect.ValueOf(value.Go()))
	}
	return out.Interface()
}

func indirectCarrier(carrier any) any {
	if carrier == nil {
		return nil
	}
	value := reflect.ValueOf(carrier)
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil
	}
	return value.Interface()
}

func indirectReflect(value reflect.Value) reflect.Value {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return value
		}
		value = value.Elem()
	}
	return value
}

func isEmptyJSONValue(carrier any) bool {
	if carrier == nil {
		return true
	}
	value := reflect.ValueOf(carrier)
	return value.IsValid() && value.IsZero()
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func codecFieldPath(path, field string) string {
	field = strings.ReplaceAll(strings.ReplaceAll(field, "~", "~0"), "/", "~1")
	if path == "/" {
		return "/" + field
	}
	return path + "/" + field
}

func codecIndexPath(path string, index int) string {
	return codecFieldPath(path, fmt.Sprint(index))
}

func codecError(path, format string, args ...any) error {
	return werr.Newf(werr.CodeType, format, args...).WithPath(path)
}
