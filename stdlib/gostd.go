package stdlib

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/types"
)

// goStdlib binds deterministic Go standard-library functions under their import
// path, so go2wlang can call strings.ToUpper as {"ToUpper":[{"pkg":"strings"},…]}
// whatever the import alias. Each entry calls the Go function itself (slice
// results are wrapped as WLang arrays), so results, edge cases and error texts
// are those of the Go toolchain the host is built with.
//
// Excluded on purpose: functions whose result can differ between Go releases
// or CPU architectures (strconv.Quote and %q follow the Unicode tables of the
// release; math.Pow and the other non-exact float functions may be compiled
// with fused multiply-add on arm64), and everything with IO, time, randomness
// or mutable receivers (strings.Builder).
var goStdlib = map[string][]registry.FuncSpec{
	"strings": {
		{GoName: "Contains", Impl: strings.Contains},
		{GoName: "Count", Impl: strings.Count},
		{GoName: "EqualFold", Impl: strings.EqualFold},
		{GoName: "Fields", Impl: func(s string) types.Value { return stringArray(strings.Fields(s)) }},
		{GoName: "HasPrefix", Impl: strings.HasPrefix},
		{GoName: "HasSuffix", Impl: strings.HasSuffix},
		{GoName: "Index", Impl: strings.Index},
		{GoName: "Join", Impl: strings.Join},
		{GoName: "LastIndex", Impl: strings.LastIndex},
		{GoName: "Repeat", Impl: strings.Repeat},
		{GoName: "Replace", Impl: strings.Replace},
		{GoName: "ReplaceAll", Impl: strings.ReplaceAll},
		{GoName: "Split", Impl: func(s, sep string) types.Value { return stringArray(strings.Split(s, sep)) }},
		{GoName: "ToLower", Impl: strings.ToLower},
		{GoName: "ToUpper", Impl: strings.ToUpper},
		{GoName: "Trim", Impl: strings.Trim},
		{GoName: "TrimLeft", Impl: strings.TrimLeft},
		{GoName: "TrimPrefix", Impl: strings.TrimPrefix},
		{GoName: "TrimRight", Impl: strings.TrimRight},
		{GoName: "TrimSpace", Impl: strings.TrimSpace},
		{GoName: "TrimSuffix", Impl: strings.TrimSuffix},
	},
	"strconv": {
		{GoName: "Atoi", Impl: strconv.Atoi},
		{GoName: "FormatBool", Impl: strconv.FormatBool},
		{GoName: "FormatFloat", Impl: strconv.FormatFloat},
		{GoName: "FormatInt", Impl: strconv.FormatInt},
		{GoName: "Itoa", Impl: strconv.Itoa},
		{GoName: "ParseBool", Impl: strconv.ParseBool},
		{GoName: "ParseFloat", Impl: strconv.ParseFloat},
		{GoName: "ParseInt", Impl: strconv.ParseInt},
	},
	"fmt": {
		{GoName: "Errorf", Impl: fmt.Errorf},
		{GoName: "Sprint", Impl: fmt.Sprint},
		{GoName: "Sprintf", Impl: fmt.Sprintf},
		{GoName: "Sprintln", Impl: fmt.Sprintln},
	},
	"math": {
		{GoName: "Abs", Impl: math.Abs},
		{GoName: "Ceil", Impl: math.Ceil},
		{GoName: "Floor", Impl: math.Floor},
		{GoName: "Inf", Impl: math.Inf},
		{GoName: "IsInf", Impl: math.IsInf},
		{GoName: "IsNaN", Impl: math.IsNaN},
		{GoName: "Max", Impl: math.Max},
		{GoName: "Min", Impl: math.Min},
		{GoName: "Mod", Impl: math.Mod},
		{GoName: "NaN", Impl: math.NaN},
		{GoName: "Round", Impl: math.Round},
		{GoName: "Sqrt", Impl: math.Sqrt},
		{GoName: "Trunc", Impl: math.Trunc},
	},
}

// stringArray keeps a []string result an array<string>, the type go2wlang
// gives a Go []string.
func stringArray(items []string) types.Value {
	return types.NewValue(types.ArrayType(types.TString), items)
}

func registerGoStdlib(r *registry.Registry) error {
	for path, functions := range goStdlib {
		pure := make([]registry.FuncSpec, len(functions))
		for i, function := range functions {
			function.Pure = true
			pure[i] = function
		}
		if err := r.BindGoPackage(path, registry.PackageSpec{Functions: pure}); err != nil {
			return err
		}
	}
	return nil
}

// GoFunction reports whether the Go standard-library function pkgPath.name is
// bound, under the package name pkgPath.
func GoFunction(pkgPath, name string) bool {
	for _, function := range goStdlib[pkgPath] {
		if function.GoName == name {
			return true
		}
	}
	return false
}

// GoFunctions lists the bound functions of a standard-library package in
// alphabetical order; it is empty for a package with none.
func GoFunctions(pkgPath string) []string {
	names := make([]string, 0, len(goStdlib[pkgPath]))
	for _, function := range goStdlib[pkgPath] {
		names = append(names, function.GoName)
	}
	slices.Sort(names)
	return names
}

// GoPackages lists the standard-library packages with bound functions.
func GoPackages() []string {
	paths := make([]string, 0, len(goStdlib))
	for path := range goStdlib {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}
