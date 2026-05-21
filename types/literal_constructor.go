package types

import (
	"reflect"
	"sync"

	werr "github.com/donutnomad/wlang/errors"
)

// LiteralConstructor decodes a typed-literal raw string into a Go value.
// It is registered via RegisterLiteralConstructor and consulted by NewLiteral
// when the type name is not a built-in (LANGUAGE.md §4.5 BindOptions.Constructor
// / TC-360).
type LiteralConstructor func(raw string) (any, error)

var (
	constructorsMu sync.RWMutex
	constructors   = map[string]LiteralConstructor{}
	// constructorsByGoType lets the overload picker map a target reflect.Type
	// back to its constructor for implicit string-to-T coercion (TC-648).
	constructorsByGoType = map[reflect.Type]LiteralConstructor{}
)

// RegisterLiteralConstructor associates a custom literal constructor with the
// given type name. The constructor is invoked when NewLiteral encounters a
// typed literal whose type matches name.
func RegisterLiteralConstructor(name string, fn LiteralConstructor) error {
	return RegisterLiteralConstructorTyped(name, fn, nil)
}

// RegisterLiteralConstructorTyped is the typed flavor used by registry's
// BindOptions.Constructor adapter: it also indexes the constructor by its Go
// output type so overload resolution can coerce string args to T at call
// time (LANGUAGE.md §7.4 / TC-648). outType may be nil when the caller has
// no static knowledge of the produced Go type.
func RegisterLiteralConstructorTyped(name string, fn LiteralConstructor, outType reflect.Type) error {
	if name == "" {
		return werr.New(werr.CodeASTShape, "literal constructor name is empty")
	}
	if fn == nil {
		return werr.Newf(werr.CodeASTShape, "literal constructor for %q is nil", name)
	}
	constructorsMu.Lock()
	defer constructorsMu.Unlock()
	constructors[name] = fn
	if outType != nil {
		constructorsByGoType[outType] = fn
	}
	return nil
}

// DeregisterLiteralConstructor removes a constructor (test helper).
func DeregisterLiteralConstructor(name string) {
	constructorsMu.Lock()
	defer constructorsMu.Unlock()
	delete(constructors, name)
	for k, v := range constructorsByGoType {
		if reflect.ValueOf(v).Pointer() == reflect.ValueOf(constructors[name]).Pointer() {
			delete(constructorsByGoType, k)
		}
	}
}

func lookupLiteralConstructor(name string) (LiteralConstructor, bool) {
	constructorsMu.RLock()
	defer constructorsMu.RUnlock()
	fn, ok := constructors[name]
	return fn, ok
}

// LookupConstructorForGoType returns the constructor registered for goType
// (TC-648). Unknown types return (nil, false).
func LookupConstructorForGoType(goType reflect.Type) (LiteralConstructor, bool) {
	constructorsMu.RLock()
	defer constructorsMu.RUnlock()
	fn, ok := constructorsByGoType[goType]
	return fn, ok
}
