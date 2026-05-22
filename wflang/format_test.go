package wflang_test

import (
	"strings"
	"testing"

	"github.com/donutnomad/wlang/wflang"
)

// §12.2: Formatter 必须产生稳定 key 顺序。
func TestFormatJSON_StableKeyOrder(t *testing.T) {
	src := []byte(`{"b":1,"a":2,"c":3}`)
	out, err := wflang.FormatJSON(src)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	got := string(out)
	want := "{\n  \"a\": 2,\n  \"b\": 1,\n  \"c\": 3\n}"
	if got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestFormatPseudoCode_CoreSyntax(t *testing.T) {
	src := []byte(`{
	  "lang":"wflang/v1",
	  "imports":["demo","audit"],
	  "program":[
	    {"let":{"title":{"literal":{"type":"string","value":"demo"}}}},
	    {"defer":{"Close":[{"pkg":"audit"},{"var":"title"}]}},
	    {"let":{"user":{"struct":["demo.User",{
	      "Name":{"literal":{"type":"string","value":"alice"}},
	      "Age":{"literal":{"type":"int64","value":"29"}}
	    }]}}},
	    {"let":{"total":{"literal":{"type":"int64","value":"0"}}}},
	    {"let":[["risk","err"],["int64","error"],{"Score":[{"pkg":"demo"},{"var":"user"},{"var":"total"}]}]},
	    {"if":{
	      "cond":{">":[{"var":"risk"},{"literal":{"type":"int64","value":"10"}}]},
	      "then":[{"set":{"total":{"+":[{"var":"total"},{"var":"risk"}]}}}],
	      "else":[{"panic":{"literal":{"type":"string","value":"low"}}}]
	    }},
	    {"foreach":{"target":{"var":"scores"},"as":"score","index":"i","do":[{"continue":true}]}},
	    {"return":{"struct":["demo.Report",{"Title":{"var":"title"}}]}}
	  ]
	}`)
	out, err := wflang.FormatPseudoCode(src)
	if err != nil {
		t.Fatalf("format pseudo: %v", err)
	}
	got := string(out)
	want := strings.Join([]string{
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
	}, "\n")
	if got != want {
		t.Fatalf("pseudo code mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestFormatPseudoCode_FullFeatureDemoLandmarks(t *testing.T) {
	src := []byte(`{
	  "lang":"wflang/v1",
	  "program":[
	    {"let":{"events":{"chan":["string",{"literal":{"type":"int64","value":"1"}}]}}},
	    {"select":[
	      {"case":{"recv":[{"var":"events"}],"bind":["event","ok"],"do":[{"set":{"title":{"+":[{"var":"title"},{"var":"event"}]}}}]}},
	      {"default":[{"set":{"title":{"literal":{"type":"string","value":"idle"}}}}]}
	    ]},
	    {"let":{"handle":{"routine":{"do":[
	      {"let":[["value","_"],["string","error"],{"Echo":[{"pkg":"worker"},{"literal":{"type":"string","value":"async"}}]}]},
	      {"return":{"var":"value"}}
	    ]}}}},
	    {"let":{"result":{"await":{"var":"handle"}}}}
	  ]
	}`)
	out, err := wflang.FormatPseudoCode(src)
	if err != nil {
		t.Fatalf("format pseudo: %v", err)
	}
	got := string(out)
	for _, needle := range []string{
		"let events = chan<string>(1)",
		"select {",
		"recv events -> event, ok {",
		"default {",
		"let handle = routine {",
		"let value: string, _: error = worker.Echo(\"async\")",
		"let result = await(handle)",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestFormatPseudoCode_ReceiverCallChainUsesDotSyntax(t *testing.T) {
	src := []byte(`{
	  "lang":"wflang/v1",
	  "program":[
	    {"set":{"err":{"Get":[
	      {"ExecuteActivity":[
	        {"pkg":"workflow"},
	        {"var":"ctx"},
	        {"symbol":"workflow.Reserve"},
	        {"var":"input.OrderID"}
	      ]},
	      {"var":"ctx"},
	      {"out":"reserve"}
	    ]}}}
	  ]
	}`)
	out, err := wflang.FormatPseudoCode(src)
	if err != nil {
		t.Fatalf("format pseudo: %v", err)
	}
	got := string(out)
	want := "err = workflow.ExecuteActivity(ctx, symbol workflow.Reserve, input.OrderID).Get(ctx, &reserve)"
	if got != want {
		t.Fatalf("pseudo code mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestFormatPseudoCode_ArrayBuiltinSugar(t *testing.T) {
	src := []byte(`{
	  "lang":"wflang/v1",
	  "program":[
	    {"let":{"n":{"arr.len":[{"var":"scores"}]}}},
	    {"let":{"item":{"arr.get":[{"var":"scores"},{"var":"idx"}]}}},
	    {"expr":{"arr.set":[
	      {"var":"scores"},
	      {"literal":{"type":"int64","value":"0"}},
	      {"var":"n"}
	    ]}},
	    {"let":{"part":{"arr.slice":[
	      {"var":"scores"},
	      {"literal":{"type":"int64","value":"1"}},
	      {"literal":{"type":"int64","value":"3"}}
	    ]}}}
	  ]
	}`)
	out, err := wflang.FormatPseudoCode(src)
	if err != nil {
		t.Fatalf("format pseudo: %v", err)
	}
	got := string(out)
	want := strings.Join([]string{
		"let n = scores.length",
		"let item = scores[idx]",
		"scores[0] = n",
		"let part = scores[1:3]",
	}, "\n")
	if got != want {
		t.Fatalf("pseudo code mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestFormatPseudoCode_MapBuiltinSugar(t *testing.T) {
	src := []byte(`{
	  "lang":"wflang/v1",
	  "program":[
	    {"let":[["val","ok"],{"map.get":[{"var":"labels"},{"literal":{"type":"string","value":"primary"}}]}]},
	    {"let":{"current":{"map.value":[{"var":"labels"},{"literal":{"type":"string","value":"copy"}}]}}},
	    {"expr":{"map.set":[
	      {"var":"labels"},
	      {"literal":{"type":"string","value":"copy"}},
	      {"var":"val"}
	    ]}},
	    {"expr":{"map.del":[
	      {"var":"labels"},
	      {"literal":{"type":"string","value":"primary"}}
	    ]}}
	  ]
	}`)
	out, err := wflang.FormatPseudoCode(src)
	if err != nil {
		t.Fatalf("format pseudo: %v", err)
	}
	got := string(out)
	want := strings.Join([]string{
		"let val, ok = labels[\"primary\"]",
		"let current = labels[\"copy\"]",
		"labels[\"copy\"] = val",
		"delete(labels, \"primary\")",
	}, "\n")
	if got != want {
		t.Fatalf("pseudo code mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}
