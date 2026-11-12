// Package shapes declares data types the differential cases use from another
// package, the way a workflow uses the DTOs of a contract package.
package shapes

// Forest and Owner are mutually recursive across a pointer and a slice.
type Forest struct {
	Trees []Tree
	Owner *Owner
}

type Owner struct {
	Name    string
	Forests []Forest
}

// Tree is recursive through a slice of values.
type Tree struct {
	Label    string `json:"label"`
	Children []Tree `json:"children,omitempty"`
	Attrs    Attrs  `json:"attrs"`
}

// Attrs is a named map type.
type Attrs map[string]string
