package wflang_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/donutnomad/wlang/wflang"
)

func TestCompileText_RoundTripsCorePseudoSyntax(t *testing.T) {
	src := []byte(strings.Join([]string{
		"import demo",
		"import audit",
		"",
		"let title = \"demo\"",
		"defer audit.Close(title)",
		"let user = struct demo.User {",
		"  Age: 29",
		"  Name: \"alice\"",
		"}",
		"let total = 0",
		"let risk: int64, err: error = demo.Score(user, total)",
		"if risk > 10 {",
		"  total = total + risk",
		"} else {",
		"  panic(\"low\")",
		"}",
		"foreach score, i in scores {",
		"  continue",
		"}",
		"return struct demo.Report {",
		"  Title: title",
		"}",
	}, "\n"))

	out, err := wflang.CompileText(src)
	if err != nil {
		t.Fatalf("CompileText: %v", err)
	}
	gotPseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("FormatPseudoCode: %v\n%s", err, out)
	}
	if string(gotPseudo) != string(src) {
		t.Fatalf("pseudo mismatch\nwant:\n%s\n\ngot:\n%s\n\njson:\n%s", src, gotPseudo, out)
	}
}

func TestCompileText_RoundTripsAdvancedPseudoSyntax(t *testing.T) {
	src := []byte(strings.Join([]string{
		"let events = chan<string>(1)",
		"select {",
		"  recv events -> event, ok {",
		"    title = title + event",
		"  }",
		"  send events <- \"done\" {",
		"    break",
		"  }",
		"  default {",
		"    title = \"idle\"",
		"  }",
		"}",
		"let handle = routine {",
		"  let value: string, _: error = worker.Echo(\"async\")",
		"  return value",
		"}",
		"let result = await(handle)",
		"let f = fn(delta: int64) -> int64 {",
		"  return delta + 1",
		"}",
		"defer call f(2)",
		"let next = call f(result)",
		"let labels = map<string,any> {",
		"  \"reservation_id\": reserve[\"reservation_id\"]",
		"}",
		"labels[\"copy\"] = labels[\"reservation_id\"]",
		"delete(labels, \"reservation_id\")",
		"let decision = match next {",
		"  when 1 {",
		"    return \"one\"",
		"  }",
		"  default {",
		"    return \"other\"",
		"  }",
		"}",
	}, "\n"))

	out, err := wflang.CompileText(src)
	if err != nil {
		t.Fatalf("CompileText: %v", err)
	}
	gotPseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("FormatPseudoCode: %v\n%s", err, out)
	}
	if string(gotPseudo) != string(src) {
		t.Fatalf("pseudo mismatch\nwant:\n%s\n\ngot:\n%s\n\njson:\n%s", src, gotPseudo, out)
	}
}

func TestCompileText_ReportsLineAndColumn(t *testing.T) {
	_, err := wflang.CompileText([]byte("let x = fn(a: int64) {\n  return a\n"))
	if err == nil {
		t.Fatal("expected syntax error")
	}
	if got := err.Error(); !strings.Contains(got, "line 2") && !strings.Contains(got, "line 3") {
		t.Fatalf("want line information, got %v", err)
	}
}

func TestCompileText_RoundTripsSymbolsOutZeroArraysDefaultsAndFori(t *testing.T) {
	src := []byte(strings.Join([]string{
		"let fallback = zero orders.ReserveResult",
		"let activity = symbol activities.Reserve",
		"let xs = array<int64>[0, 0, 9]",
		"let n = maybe.value ?? 3",
		"for i := 0; i < 3; step 1 {",
		"  xs[i] = n",
		"}",
		"let err = workflow.ExecuteActivity(ctx, activity, input).Get(ctx, &reserve)",
		"let method = method worker.Compensate",
		"return call method(ctx, &reserve)",
	}, "\n"))

	out, err := wflang.CompileText(src)
	if err != nil {
		t.Fatalf("CompileText: %v", err)
	}
	gotPseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("FormatPseudoCode: %v\n%s", err, out)
	}
	if string(gotPseudo) != string(src) {
		t.Fatalf("pseudo mismatch\nwant:\n%s\n\ngot:\n%s\n\njson:\n%s", src, gotPseudo, out)
	}
}

func TestCompileText_ParsesZeroBeforeLessThanAsComparison(t *testing.T) {
	tests := []struct {
		name string
		src  string
		typ  string
	}{
		{name: "scalar", src: "let ok = zero int64 < 1", typ: "int64"},
		{name: "generic", src: "let ok = zero map<string,any> < 1", typ: "map<string,any>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := wflang.CompileText([]byte(tt.src))
			if err != nil {
				t.Fatalf("CompileText: %v", err)
			}

			var doc struct {
				Program []map[string]any `json:"program"`
			}
			if err := json.Unmarshal(out, &doc); err != nil {
				t.Fatalf("json.Unmarshal: %v\n%s", err, out)
			}
			if len(doc.Program) != 1 {
				t.Fatalf("program length = %d, want 1\n%s", len(doc.Program), out)
			}
			let, ok := doc.Program[0]["let"].(map[string]any)
			if !ok {
				t.Fatalf("missing let node:\n%s", out)
			}
			expr, ok := let["ok"].(map[string]any)
			if !ok {
				t.Fatalf("missing ok binding:\n%s", out)
			}
			args, ok := expr["<"].([]any)
			if !ok || len(args) != 2 {
				t.Fatalf("missing less-than comparison:\n%s", out)
			}
			zero, ok := args[0].(map[string]any)["zero"].(string)
			if !ok || zero != tt.typ {
				t.Fatalf("zero type = %v, want %s\n%s", args[0], tt.typ, out)
			}
			lit, ok := args[1].(map[string]any)["literal"].(map[string]any)
			if !ok || lit["type"] != "int64" || lit["value"] != "1" {
				t.Fatalf("literal = %v, want int64 literal 1\n%s", args[1], out)
			}
		})
	}
}

func TestCompileText_RoundTripsMultilineCallArguments(t *testing.T) {
	src := []byte(strings.Join([]string{
		"let results, err = workflow.Go(ctx,",
		"  fn(ctx: workflow.Context) -> map<string,any> {",
		"    let in = map<string,any> {",
		"      \"wallet_address\": \"0xHOT1\"",
		"      \"token\": \"ETH\"",
		"    }",
		"    let out = zero map<string,any>",
		"    return out",
		"  },",
		"  fn(ctx: workflow.Context) -> map<string,any> {",
		"    let in = map<string,any> {",
		"      \"wallet_address\": \"0xHOT2\"",
		"      \"token\": \"USDT\"",
		"    }",
		"    let out = zero map<string,any>",
		"    return out",
		"  })",
		"return null",
	}, "\n"))

	out, err := wflang.CompileText(src)
	if err != nil {
		t.Fatalf("CompileText: %v", err)
	}
	gotPseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("FormatPseudoCode: %v\n%s", err, out)
	}
	if string(gotPseudo) != string(src) {
		t.Fatalf("pseudo mismatch\nwant:\n%s\n\ngot:\n%s\n\njson:\n%s", src, gotPseudo, out)
	}
}

func TestCompileText_RoundTripsFormatterEdgeCases(t *testing.T) {
	json := []byte(`{
	  "lang":"wflang/v1",
	  "program":[
	    {"let":{"grouped":{"*":[{"+":[{"literal":{"type":"int64","value":"1"}},{"literal":{"type":"int64","value":"2"}}]},{"literal":{"type":"int64","value":"3"}}]}}},
	    {"let":{"negative":{"literal":{"type":"int64","value":"-1"}}}},
	    {"select":[{"case":{"recv":[{"var":"ch"}],"bind":["","ok"],"do":[{"continue":{}}]}}]},
	    {"let":{"method":{"method":[{"arr.get":[{"var":"workers"},{"literal":{"type":"int64","value":"0"}}]},"Run"]}}},
	    {"let":{"pkgResult":{"Run":[{"pkg":"Demo"},{"literal":{"type":"string","value":"x"}}]}}}
	  ]
	}`)
	pseudo, err := wflang.FormatPseudoCode(json)
	if err != nil {
		t.Fatalf("FormatPseudoCode: %v", err)
	}
	out, err := wflang.CompileText(pseudo)
	if err != nil {
		t.Fatalf("CompileText: %v\npseudo:\n%s", err, pseudo)
	}
	gotPseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("FormatPseudoCode compiled: %v\n%s", err, out)
	}
	if string(gotPseudo) != string(pseudo) {
		t.Fatalf("pseudo mismatch\nwant:\n%s\n\ngot:\n%s\n\njson:\n%s", pseudo, gotPseudo, out)
	}
}

func TestCompileText_RoundTripsMapBuiltinsWithNonStringKeys(t *testing.T) {
	src := []byte(`{
	  "lang":"wflang/v1",
	  "program":[
	    {"let":[["val","ok"],{"map.get":[{"var":"labels"},{"literal":{"type":"string","value":"primary"}}]}]},
	    {"let":{"got":{"map.get":[{"var":"m"},{"var":"k"}]}}},
	    {"expr":{"map.set":[{"var":"m"},{"var":"k"},{"var":"v"}]}}
	  ]
	}`)
	pseudo, err := wflang.FormatPseudoCode(src)
	if err != nil {
		t.Fatalf("FormatPseudoCode: %v", err)
	}
	out, err := wflang.CompileText(pseudo)
	if err != nil {
		t.Fatalf("CompileText: %v\npseudo:\n%s", err, pseudo)
	}
	gotPseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("FormatPseudoCode compiled: %v\n%s", err, out)
	}
	if string(gotPseudo) != string(pseudo) {
		t.Fatalf("pseudo mismatch\nwant:\n%s\n\ngot:\n%s\n\njson:\n%s", pseudo, gotPseudo, out)
	}
	if !strings.Contains(string(out), `"map.get"`) || !strings.Contains(string(out), `"map.set"`) {
		t.Fatalf("compiled JSON lost map ops:\n%s\npseudo:\n%s", out, pseudo)
	}
}

func TestCompileText_RejectsForiConditionVariableMismatch(t *testing.T) {
	_, err := wflang.CompileText([]byte(strings.Join([]string{
		"for i := 0; j < 3 {",
		"  return i",
		"}",
	}, "\n")))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if got := err.Error(); !strings.Contains(got, "for condition variable must match i") {
		t.Fatalf("unexpected diagnostic: %v", err)
	}
}

func TestFormatPseudoCode_RejectsExpressionOnlyNodes(t *testing.T) {
	src := []byte(`{
	  "lang":"wflang/v1",
	  "program":[
	    {"let":{"value":{"if":{
	      "cond":{"literal":{"type":"boolean","value":true}},
	      "then":[{"return":{"literal":{"type":"int64","value":"1"}}}],
	      "else":[{"return":{"literal":{"type":"int64","value":"0"}}}]
	    }}}}
	  ]
	}`)
	_, err := wflang.FormatPseudoCode(src)
	if err == nil {
		t.Fatal("expected unsupported expression error")
	}
	if got := err.Error(); !strings.Contains(got, "unsupported pseudocode expression") {
		t.Fatalf("unexpected diagnostic: %v", err)
	}
}
