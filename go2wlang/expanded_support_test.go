package go2wlang_test

import (
	"strings"
	"testing"

	"github.com/donutnomad/wlang/go2wlang"
	"github.com/donutnomad/wlang/wflang"
)

func translatePseudo(t *testing.T, src string) string {
	t.Helper()
	out, err := go2wlang.TranslateFile([]byte(src), go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	pseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("pseudo: %v", err)
	}
	return string(pseudo)
}

func TestTranslateFileAdditionalControlFlow(t *testing.T) {
	got := translatePseudo(t, `package rules

func Rule(xs []int64, flag bool) int64 {
	if n := len(xs); n > 0 {
		return n
	}
	switch {
	case flag:
		return 1
	default:
		return 2
	}
}
`)
	for _, needle := range []string{
		"let n = xs.length",
		"if n > 0 {",
		"} else {",
		"if flag {",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestTranslateFileTypeSwitch(t *testing.T) {
	got := translatePseudo(t, `package rules

func Rule(x any) string {
	switch v := x.(type) {
	case string:
		return v
	default:
		return ""
	}
}
`)
	for _, needle := range []string{
		"if type.is(x, \"string\") {",
		"let v = type.assert(x, \"string\")",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestTranslateFileAdditionalExpressionAndCollectionForms(t *testing.T) {
	got := translatePseudo(t, `package rules

func Rule(m map[string]int64, xs []int64, p *int64) int64 {
	v, ok := m["a"]
	if ok {
		m["b"] = -v
	}
	xs[0] = +v
	part := xs[1:3]
	_ = part
	return *p
}
`)
	for _, needle := range []string{
		"let v, ok = m[\"a\"]",
		"m[\"b\"] = 0 - v",
		"xs[0] = v",
		"xs[1:3]",
		"return ptr.deref(p)",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestTranslateFileAdditionalCallAndTypeForms(t *testing.T) {
	got := translatePseudo(t, `package rules

func Rule(x any, ch chan string) string {
	s, ok := x.(string)
	if ok {
		return s
	}
	go func(v string) {
		ch <- v
	}("done")
	delete(map[string]string{"a":"b"}, "a")
	return ""
}
`)
	for _, needle := range []string{
		"let s, ok = type.assert.ok(x, \"string\")",
		"routine {",
		"let v = \"done\"",
		"ch.send(ch, v)",
		"delete(map<string,string> {",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestTranslateFileAddressOfSelector(t *testing.T) {
	got := translatePseudo(t, `package rules

type Result struct{ ID string }

func Rule(get func(*string) error, result Result) error {
	return get(&result.ID)
}
`)
	if !strings.Contains(got, "&result.ID") {
		t.Fatalf("missing selector out argument in:\n%s", got)
	}
}

func TestTranslateFileStructArrayAndGenericCallForms(t *testing.T) {
	got := translatePseudo(t, `package rules

type Pair struct {
	Name string
	Rank int64
}

func Id[T any](v T) T { return v }

func Rule() string {
	p := Pair{"go", 2}
	p.Name = Id[string](p.Name)
	xs := []int64{2: 9}
	_ = xs
	return p.Name
}
`)
	for _, needle := range []string{
		"let p = struct rules.Pair {",
		"Name: \"go\"",
		"Rank: 2",
		"p.Name = call symbol rules.Id(p.Name)",
		"let xs = array<int64>[0, 0, 9]",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}
