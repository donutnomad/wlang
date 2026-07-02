package wflang_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/donutnomad/wlang/wflang"
)

func TestCompileText_PropertyRoundTripsGeneratedASTs(t *testing.T) {
	for _, seed := range roundTripSeeds() {
		t.Run(seed.name, func(t *testing.T) {
			assertPseudoRoundTripAST(t, seed.json)
		})
	}
}

func FuzzCompileTextRoundTripsFormatterAST(f *testing.F) {
	seeds := roundTripSeeds()
	for i := range seeds {
		f.Add(int64(i))
	}
	f.Fuzz(func(t *testing.T, rawSeed int64) {
		r := rand.New(rand.NewSource(rawSeed))
		program := generatedProgram(r, 2+r.Intn(5))
		assertPseudoRoundTripAST(t, mustCanonicalJSON(t, program))
	})
}

type roundTripSeed struct {
	name string
	json []byte
}

func roundTripSeeds() []roundTripSeed {
	return []roundTripSeed{
		{name: "control and calls", json: mustCanonicalJSONNoTest(map[string]any{
			"lang":    "wflang/v1",
			"imports": []any{"demo", "audit"},
			"program": []any{
				let("title", strLit("demo")),
				map[string]any{"defer": call("Close", pkg("audit"), v("title"))},
				let("risk", call("Score", pkg("demo"), v("input"))),
				map[string]any{"if": map[string]any{
					"cond": call(">", v("risk"), intLit(10)),
					"then": []any{
						set("risk", call("+", v("risk"), intLit(1))),
					},
					"else": []any{
						map[string]any{"panic": strLit("low")},
					},
				}},
				map[string]any{"return": v("risk")},
			},
		})},
		{name: "collections and map disambiguation", json: mustCanonicalJSONNoTest(map[string]any{
			"lang": "wflang/v1",
			"program": []any{
				let("xs", map[string]any{"array": map[string]any{"elem": "int64", "items": []any{intLit(1), intLit(2), intLit(3)}}}),
				let("labels", map[string]any{"map": map[string]any{"type": []any{"string", "any"}, "entries": []any{
					[]any{strLit("primary"), intLit(7)},
				}}}),
				map[string]any{"let": []any{[]any{"val", "ok"}, map[string]any{"map.get": []any{v("labels"), strLit("primary")}}}},
				let("current", map[string]any{"map.value": []any{v("labels"), strLit("primary")}}),
				map[string]any{"expr": map[string]any{"map.set": []any{v("m"), v("k"), v("val")}}},
				map[string]any{"expr": map[string]any{"arr.set": []any{v("xs"), intLit(0), v("val")}}},
				let("part", map[string]any{"arr.slice": []any{v("xs"), intLit(0), intLit(2)}}),
				map[string]any{"return": v("part")},
			},
		})},
		{name: "advanced statements", json: mustCanonicalJSONNoTest(map[string]any{
			"lang": "wflang/v1",
			"program": []any{
				let("events", map[string]any{"chan": []any{"string", intLit(1)}}),
				map[string]any{"select": []any{
					map[string]any{"case": map[string]any{"recv": []any{v("events")}, "bind": []any{"_", "ok"}, "do": []any{map[string]any{"continue": map[string]any{}}}}},
					map[string]any{"case": map[string]any{"send": []any{v("events"), strLit("done")}, "do": []any{map[string]any{"break": map[string]any{}}}}},
					map[string]any{"default": []any{set("status", strLit("idle"))}},
				}},
				let("f", map[string]any{"fn": map[string]any{
					"params":  []any{[]any{"delta", "int64"}},
					"returns": []any{"int64"},
					"do":      []any{map[string]any{"return": call("+", v("delta"), intLit(1))}},
				}}),
				map[string]any{"defer": map[string]any{"call": map[string]any{"fn": v("f"), "args": []any{intLit(2)}}}},
				let("next", map[string]any{"call": map[string]any{"fn": v("f"), "args": []any{intLit(1)}}}),
				let("choice", map[string]any{"match": map[string]any{
					"value": v("next"),
					"cases": []any{
						map[string]any{"when": intLit(1), "do": []any{map[string]any{"return": strLit("one")}}},
					},
					"default": []any{map[string]any{"return": strLit("other")}},
				}}),
			},
		})},
		{name: "method package and precedence", json: mustCanonicalJSONNoTest(map[string]any{
			"lang": "wflang/v1",
			"program": []any{
				let("grouped", call("*", call("+", intLit(1), intLit(2)), intLit(3))),
				let("rightGrouped", call("+", map[string]any{"map.get": []any{v("m"), v("k")}}, call("+", map[string]any{"method": []any{map[string]any{"arr.get": []any{v("workers"), intLit(0)}}, "Run"}}, map[string]any{"arr.get": []any{map[string]any{"array": map[string]any{"elem": "int64", "items": []any{intLit(1), intLit(2)}}}, intLit(0)}}))),
				let("defaultedInfix", call("*", map[string]any{"fn": map[string]any{"params": []any{[]any{"n", "int64"}}, "returns": []any{"int64"}, "do": []any{map[string]any{"return": call("+", v("n"), intLit(1))}}}}, map[string]any{"var": []any{"maybe.value", intLit(3)}})),
				let("method", map[string]any{"method": []any{map[string]any{"arr.get": []any{v("workers"), intLit(0)}}, "Run"}}),
				let("pkgResult", call("Run", pkg("Demo"), strLit("x"))),
				map[string]any{"return": map[string]any{"call": map[string]any{"fn": v("method"), "args": []any{v("pkgResult")}}}},
			},
		})},
	}
}

func assertPseudoRoundTripAST(t *testing.T, src []byte) {
	t.Helper()
	pseudo, err := wflang.FormatPseudoCode(src)
	if err != nil {
		t.Fatalf("FormatPseudoCode: %v\njson:\n%s", err, src)
	}
	got, err := wflang.CompileText(pseudo)
	if err != nil {
		t.Fatalf("CompileText: %v\npseudo:\n%s\njson:\n%s", err, pseudo, src)
	}
	want := mustCanonicalJSON(t, mustDecodeJSON(t, src))
	if !jsonEqual(want, got) {
		t.Fatalf("round-trip JSON mismatch\npseudo:\n%s\nwant:\n%s\n\ngot:\n%s", pseudo, want, got)
	}
}

func generatedProgram(r *rand.Rand, n int) any {
	body := make([]any, 0, n+1)
	body = append(body, let("x0", randomExpr(r, 0)))
	for i := 1; i < n; i++ {
		body = append(body, let(fmt.Sprintf("x%d", i), randomExpr(r, 0)))
	}
	body = append(body, map[string]any{"return": v(fmt.Sprintf("x%d", n-1))})
	return map[string]any{"lang": "wflang/v1", "program": body}
}

func randomExpr(r *rand.Rand, depth int) any {
	if depth >= 3 {
		return randomLeafExpr(r)
	}
	switch r.Intn(16) {
	case 0:
		return call("+", randomExpr(r, depth+1), randomExpr(r, depth+1))
	case 1:
		return call("*", randomExpr(r, depth+1), randomExpr(r, depth+1))
	case 2:
		return call("and", call(">", randomExpr(r, depth+1), intLit(0)), boolLit(true))
	case 3:
		return map[string]any{"array": map[string]any{"elem": "int64", "items": []any{intLit(int64(r.Intn(9))), intLit(int64(r.Intn(9)))}}}
	case 4:
		return map[string]any{"arr.get": []any{map[string]any{"array": map[string]any{"elem": "int64", "items": []any{intLit(1), intLit(2)}}}, intLit(0)}}
	case 5:
		return map[string]any{"arr.slice": []any{map[string]any{"array": map[string]any{"elem": "int64", "items": []any{intLit(1), intLit(2), intLit(3)}}}, intLit(0), intLit(2)}}
	case 6:
		return map[string]any{"map": map[string]any{"type": []any{"string", "any"}, "entries": []any{[]any{strLit("k"), randomLeafExpr(r)}}}}
	case 7:
		return map[string]any{"map.value": []any{v("labels"), strLit("primary")}}
	case 8:
		return map[string]any{"map.get": []any{v("labels"), strLit("primary")}}
	case 9:
		return map[string]any{"map.get": []any{v("m"), v("k")}}
	case 10:
		return map[string]any{"chan": []any{"string", intLit(1)}}
	case 11:
		return map[string]any{"struct": []any{"demo.Item", map[string]any{"Name": strLit("item"), "Count": intLit(1)}}}
	case 12:
		return map[string]any{"fn": map[string]any{"params": []any{[]any{"n", "int64"}}, "returns": []any{"int64"}, "do": []any{map[string]any{"return": call("+", v("n"), intLit(1))}}}}
	case 13:
		return map[string]any{"call": map[string]any{"fn": v("f"), "args": []any{intLit(1)}}}
	case 14:
		return map[string]any{"method": []any{map[string]any{"arr.get": []any{v("workers"), intLit(0)}}, "Run"}}
	default:
		return randomLeafExpr(r)
	}
}

func randomLeafExpr(r *rand.Rand) any {
	switch r.Intn(6) {
	case 0:
		return intLit(int64(r.Intn(100) - 50))
	case 1:
		return strLit(fmt.Sprintf("s%d", r.Intn(10)))
	case 2:
		return boolLit(r.Intn(2) == 0)
	case 3:
		return v(fmt.Sprintf("x%d", r.Intn(3)))
	case 4:
		return map[string]any{"var": []any{"maybe.value", intLit(3)}}
	default:
		return map[string]any{"zero": "demo.Item"}
	}
}

func let(name string, expr any) any {
	return map[string]any{"let": map[string]any{name: expr}}
}

func set(name string, expr any) any {
	return map[string]any{"set": map[string]any{name: expr}}
}

func call(op string, args ...any) any {
	return map[string]any{op: args}
}

func v(name string) any {
	return map[string]any{"var": name}
}

func pkg(name string) any {
	return map[string]any{"pkg": name}
}

func intLit(v int64) any {
	return map[string]any{"literal": map[string]any{"type": "int64", "value": fmt.Sprint(v)}}
}

func strLit(v string) any {
	return map[string]any{"literal": map[string]any{"type": "string", "value": v}}
}

func boolLit(v bool) any {
	return map[string]any{"literal": map[string]any{"type": "boolean", "value": v}}
}

func mustDecodeJSON(t *testing.T, src []byte) any {
	t.Helper()
	var v any
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, src)
	}
	return v
}

func mustCanonicalJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	out, err := wflang.FormatJSON(raw)
	if err != nil {
		t.Fatalf("FormatJSON: %v\n%s", err, raw)
	}
	return out
}

func mustCanonicalJSONNoTest(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	out, err := wflang.FormatJSON(raw)
	if err != nil {
		panic(err)
	}
	return out
}

func jsonEqual(a, b []byte) bool {
	var av, bv any
	adec := json.NewDecoder(bytes.NewReader(a))
	adec.UseNumber()
	if err := adec.Decode(&av); err != nil {
		return false
	}
	bdec := json.NewDecoder(bytes.NewReader(b))
	bdec.UseNumber()
	if err := bdec.Decode(&bv); err != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}
