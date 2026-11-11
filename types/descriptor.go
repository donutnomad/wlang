package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// TypeKind identifies one node in a serializable type graph.
type TypeKind string

const (
	TypeKindAlias   TypeKind = "alias"
	TypeKindRecord  TypeKind = "record"
	TypeKindPointer TypeKind = "pointer"
	TypeKindSlice   TypeKind = "slice"
	TypeKindArray   TypeKind = "array"
	TypeKindMap     TypeKind = "map"
)

// TypeDescriptor is the persisted, host-independent description of a WLang
// data type. References use canonical IDs; LanguageName is the spelling used
// by the program AST.
type TypeDescriptor struct {
	ID           string            `json:"id"`
	LanguageName string            `json:"language_name"`
	Kind         TypeKind          `json:"kind"`
	Underlying   string            `json:"underlying,omitempty"`
	Elem         string            `json:"elem,omitempty"`
	Key          string            `json:"key,omitempty"`
	Length       int64             `json:"length,omitempty"`
	Fields       []FieldDescriptor `json:"fields,omitempty"`
	Fingerprint  string            `json:"fingerprint"`
}

// FieldDescriptor maps one Go field to its deterministic JSON representation.
type FieldDescriptor struct {
	GoName    string `json:"go_name"`
	JSONName  string `json:"json_name"`
	TypeID    string `json:"type_id"`
	OmitEmpty bool   `json:"omitempty,omitempty"`
}

// DescriptorFingerprint returns a stable content hash for one descriptor.
func DescriptorFingerprint(descriptor TypeDescriptor) string {
	descriptor.Fingerprint = ""
	raw, _ := json.Marshal(descriptor)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Record is the runtime carrier for a named, host-independent data record.
// Its field order comes from the associated descriptor; values are keyed by
// Go field name so source-level field access remains stable across JSON tags.
type Record struct {
	typeName string
	fields   map[string]Value
	aliases  map[string]string
}

// NewRecord constructs a record value and takes a defensive copy of fields.
func NewRecord(typeName string, fields map[string]Value) Value {
	return NewRecordWithAliases(typeName, fields, nil)
}

// NewRecordWithAliases also registers alternate field spellings such as JSON
// names while keeping Go names as the canonical storage keys.
func NewRecordWithAliases(typeName string, fields map[string]Value, aliases map[string]string) Value {
	copyFields := make(map[string]Value, len(fields))
	for name, value := range fields {
		copyFields[name] = value
	}
	copyAliases := make(map[string]string, len(aliases))
	for alias, goName := range aliases {
		copyAliases[alias] = goName
	}
	return NewValue(typeName, &Record{typeName: typeName, fields: copyFields, aliases: copyAliases})
}

// AsRecord extracts a record carrier.
func AsRecord(value Value) (*Record, bool) {
	record, ok := value.Go().(*Record)
	return record, ok && record != nil
}

// TypeName returns the record's WLang type name.
func (r *Record) TypeName() string {
	if r == nil {
		return ""
	}
	return r.typeName
}

// Field returns one field by Go name.
func (r *Record) Field(name string) (Value, bool) {
	if r == nil {
		return Value{}, false
	}
	if goName, ok := r.aliases[name]; ok {
		name = goName
	}
	value, ok := r.fields[name]
	return value, ok
}

// SetField updates one field. Descriptor validation remains the caller's
// responsibility because Record deliberately stays independent of registry.
func (r *Record) SetField(name string, value Value) bool {
	if r == nil {
		return false
	}
	if goName, ok := r.aliases[name]; ok {
		name = goName
	}
	if _, ok := r.fields[name]; !ok {
		return false
	}
	r.fields[name] = value
	return true
}

// Fields returns a defensive copy ordered independently by callers.
func (r *Record) Fields() map[string]Value {
	if r == nil {
		return nil
	}
	out := make(map[string]Value, len(r.fields))
	for name, value := range r.fields {
		out[name] = value
	}
	return out
}

// SortedFieldNames is useful for deterministic fallback rendering.
func (r *Record) SortedFieldNames() []string {
	names := make([]string, 0, len(r.fields))
	for name := range r.fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CopyValue gives a record Go value semantics: a non-pointer record is copied
// together with every nested non-pointer record, while slices, maps and
// pointers inside it stay shared, as when Go copies a struct. Other values are
// returned unchanged.
func CopyValue(value Value) Value {
	record, ok := AsRecord(value)
	if !ok || len(value.TypeName()) > 0 && value.TypeName()[0] == '*' {
		return value
	}
	fields := make(map[string]Value, len(record.fields))
	for name, field := range record.fields {
		fields[name] = CopyValue(field)
	}
	return NewValue(value.TypeName(), &Record{typeName: record.typeName, fields: fields, aliases: record.aliases})
}
