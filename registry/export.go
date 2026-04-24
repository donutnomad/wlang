package registry

import (
	"reflect"
	"sort"
)

// LanguageSpec is a structural description of everything the Registry exposes
// to the language (LANGUAGE.md §11). It is safe to marshal and diff.
type LanguageSpec struct {
	Packages map[string]PackageInfo `json:"packages"`
	Types    map[string]TypeInfo    `json:"types"`
}

// PackageInfo describes a bound Go package.
type PackageInfo struct {
	Name      string         `json:"name"`
	Functions []FunctionInfo `json:"functions"`
}

// FunctionInfo describes a host-callable function.
type FunctionInfo struct {
	GoName       string   `json:"goName"`
	ParamTypes   []string `json:"paramTypes"`
	ReturnTypes  []string `json:"returnTypes"`
	Pure         bool     `json:"pure"`
	Capabilities []string `json:"capabilities,omitempty"`
	Variadic     bool     `json:"variadic,omitempty"`
}

// TypeInfo describes a bound type's method table.
type TypeInfo struct {
	Name      string                  `json:"name"`
	GoType    string                  `json:"goType"`
	Methods   map[string]FunctionInfo `json:"methods"`
	Overloads map[string][]string     `json:"overloads,omitempty"`
}

// ExportLanguageSpec snapshots the current Registry contents.
func (r *Registry) ExportLanguageSpec() (*LanguageSpec, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec := &LanguageSpec{
		Packages: map[string]PackageInfo{},
		Types:    map[string]TypeInfo{},
	}
	for name, pkg := range r.packages {
		info := PackageInfo{Name: name}
		names := make([]string, 0, len(pkg.funcs))
		for fn := range pkg.funcs {
			names = append(names, fn)
		}
		sort.Strings(names)
		for _, fn := range names {
			info.Functions = append(info.Functions, fnInfo(fn, pkg.funcs[fn]))
		}
		spec.Packages[name] = info
	}
	for name, bt := range r.types {
		ti := TypeInfo{
			Name:    name,
			Methods: map[string]FunctionInfo{},
		}
		for mn, bf := range bt.methods {
			ti.Methods[mn] = fnInfo(mn, bf)
			if !bf.impl.IsValid() {
				continue
			}
			if t := bf.impl.Type(); t.NumIn() > 0 {
				ti.GoType = typeName(t.In(0))
			}
		}
		if len(bt.overloads) > 0 {
			ti.Overloads = map[string][]string{}
			for op, set := range bt.overloads {
				names := make([]string, 0, len(set.cands))
				for _, c := range set.cands {
					names = append(names, c.name)
				}
				ti.Overloads[op] = names
			}
		}
		spec.Types[name] = ti
	}
	return spec, nil
}

func fnInfo(name string, bf *boundFunc) FunctionInfo {
	fi := FunctionInfo{
		GoName:       name,
		Pure:         bf.pure,
		Variadic:     bf.variadic,
		Capabilities: append([]string(nil), bf.capability...),
	}
	for _, t := range bf.inTypes {
		fi.ParamTypes = append(fi.ParamTypes, typeName(t))
	}
	for _, t := range bf.outTypes {
		fi.ReturnTypes = append(fi.ReturnTypes, typeName(t))
	}
	return fi
}

func typeName(t reflect.Type) string {
	if t == nil {
		return ""
	}
	return t.String()
}
