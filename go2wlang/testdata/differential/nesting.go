package differential

import "github.com/donutnomad/wlang/go2wlang/testdata/differential/shapes"

// Data types nested more than five levels deep, mixing structs, slices, maps,
// pointers and arrays. Each type is registered because the program touches
// it, wherever it occurs.

type Level1 struct {
	Name  string
	Inner Level2
	Extra *Level2
}

type Level2 struct {
	Grid  [2]Level3
	Index map[string][]Level3
}

type Level3 struct {
	Items []Level4
}

type Level4 struct {
	ByName map[string]Level5
	Opt    *Level5
}

type Level5 struct {
	Note  string
	Flags [2]bool
	At    *Point
	Tags  Tags
}

// Tags and Ledger are named slice and map types.
type Tags []string

type Ledger map[string]Amounts

type Amounts []int64

type Account struct {
	Tags   Tags
	Ledger Ledger
}

// Node is recursive through a slice, a pointer and a map.
type Node struct {
	Name     string
	Children []Node
	Parent   *Node
	Index    map[string]Node
}

// Dept and Person are mutually recursive.
type Dept struct {
	Name  string
	Staff []Person
}

type Person struct {
	Name    string
	Dept    *Dept
	Reports []Person
	Mentors map[string]Dept
}

// Envelope and its field types occur only inside the composite literal that
// DeepOnlyInsideLiteral passes to a call.
type Envelope struct {
	Route   []Hop
	Payload map[string]Parcel
}

type Hop struct {
	Station string
	Parcels [1]Parcel
	Note    any
}

type Parcel struct {
	Weight int
	Next   *Parcel
}

func DeepNestedLiteral() Level1 {
	leaf := Level5{Note: "deep", Flags: [2]bool{true, false}, Tags: []string{"x", "y"}}
	l4 := Level4{ByName: map[string]Level5{"a": leaf, "b": {Note: "elided"}}}
	l3 := Level3{Items: []Level4{l4, {}}}
	return Level1{
		Name: "root",
		Inner: Level2{
			Grid:  [2]Level3{l3, {}},
			Index: map[string][]Level3{"k": []Level3{l3}, "nil": nil},
		},
	}
}

func DeepNestedZero() Level1 {
	var v Level1
	return v
}

func DeepNestedCopy() []Level1 {
	a := DeepNestedLiteral()
	b := a
	b.Name = "copy"
	return []Level1{a, b}
}

func DeepOnlyInsideLiteral() any {
	send := func(v any) any { return v }
	return send(Envelope{
		Route:   []Hop{{Station: "a", Parcels: [1]Parcel{{Weight: 3}}, Note: true}, {Station: "b", Note: Parcel{Weight: 9}}, {}},
		Payload: map[string]Parcel{"p": {Weight: 1}},
	})
}

func NamedSliceMap() Account {
	return Account{Tags: []string{"a", "b"}, Ledger: map[string]Amounts{"x": []int64{1, 2}, "z": nil}}
}

func NamedSliceMapLiterals() []Account {
	tags := Tags{"a"}
	ledger := Ledger{"x": {1, 2}, "y": Amounts{3}}
	return []Account{{Tags: tags, Ledger: ledger}, {Tags: Tags{}, Ledger: Ledger{}}}
}

func NamedSliceMapZero() Account {
	var a Account
	return a
}

func RecursiveTree() Node {
	return Node{
		Name:     "root",
		Children: []Node{{Name: "a", Children: []Node{{Name: "a1"}}}, {Name: "b"}},
		Index:    map[string]Node{"x": {Name: "x", Children: []Node{{Name: "x1"}}}},
	}
}

func MutuallyRecursive() Dept {
	lead := Person{Name: "lead", Reports: []Person{{Name: "dev"}}, Mentors: map[string]Dept{"ops": {Name: "ops"}}}
	return Dept{Name: "eng", Staff: []Person{lead}}
}

func CrossPackageTypes() shapes.Forest {
	tree := shapes.Tree{Label: "oak", Children: []shapes.Tree{{Label: "branch", Attrs: map[string]string{"leaf": "green"}}}}
	return shapes.Forest{Trees: []shapes.Tree{tree, {Label: "bare"}}}
}

func CrossPackageOnlyInsideLiteral() any {
	keep := func(v any) any { return v }
	return keep(shapes.Owner{Name: "ann", Forests: []shapes.Forest{{Trees: []shapes.Tree{{Label: "elm"}}}}})
}

// Types that cannot cross a workflow boundary are rejected at translation
// time with the path of the offending position.

type Courier struct {
	Hops []Station
}

type Station struct {
	Name  string
	Queue chan int
}

func ChannelInsideNesting() int {
	c := Courier{}
	return len(c.Hops)
}

type Callbacks struct {
	OnDone map[string]func() int
}

func FunctionInsideNesting() int {
	var c Callbacks
	return len(c.OnDone)
}
