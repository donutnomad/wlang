package runtime

import (
	"context"
	"testing"

	"github.com/wflang/wflang/compiler"
	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/types"
)

func runSrcWithScope(t *testing.T, src string, scope *Scope) (types.Value, error) {
	t.Helper()
	prog, err := compiler.ParseProgram([]byte(src))
	if err != nil {
		return types.Value{}, err
	}
	exec := NewExecutor(context.Background(), scope, nil, nil, Budget{})
	return exec.RunProgram(prog)
}

func TestFunctionClosureCapturesOuterVariableByReference(t *testing.T) {
	src := `[
		{"let":{"x":{"literal":{"type":"int64","value":"1"}}}},
		{"let":{"f":{"fn":{
			"params":[],
			"returns":["int64"],
			"do":[
				{"set":{"x":{"literal":{"type":"int64","value":"2"}}}},
				{"return":{"var":"x"}}
			]
		}}}},
		{"expr":{"call":{"fn":{"var":"f"},"args":[]}}},
		{"return":{"var":"x"}}
	]`
	v, err := runSrc(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != types.TInt64 || v.Go().(int64) != 2 {
		t.Fatalf("want int64=2, got %s=%v", v.TypeName(), v.Go())
	}
}

func TestDeferredClosureCanUpdateNamedReturn(t *testing.T) {
	src := `[
		{"let":{"err":{"literal":{"type":"string","value":"original"}}}},
		{"defer":{"call":{"fn":{"fn":{
			"params":[],
			"returns":[],
			"do":[
				{"set":{"err":{"literal":{"type":"string","value":"deferred"}}}}
			]
		}},"args":[]}}},
		{"return":{"named":"err"}}
	]`
	v, err := runSrc(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != types.TString || v.Go().(string) != "deferred" {
		t.Fatalf("want string=deferred, got %s=%v", v.TypeName(), v.Go())
	}
}

func TestFunctionArrayPushGetAndCall(t *testing.T) {
	src := `[
		{"let":{"stack":{"array":{"elem":"func<(string)->string>","items":[]}}}},
		{"expr":{"arr.push":[
			{"var":"stack"},
			{"fn":{
				"params":[["s","string"]],
				"returns":["string"],
				"do":[{"return":{"+":[
					{"var":"s"},
					{"literal":{"type":"string","value":"!"}}
				]}}]
			}}
		]}},
		{"let":{"f":{"arr.get":[
			{"var":"stack"},
			{"literal":{"type":"int64","value":"0"}}
		]}}},
		{"return":{"call":{"fn":{"var":"f"},"args":[
			{"literal":{"type":"string","value":"go"}}
		]}}}
	]`
	v, err := runSrc(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != types.TString || v.Go().(string) != "go!" {
		t.Fatalf("want string=go!, got %s=%v", v.TypeName(), v.Go())
	}
}

func TestFunctionCallRejectsInvalidShape(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
	}{
		{
			name: "call non function",
			src: `[
				{"let":{"f":{"literal":{"type":"string","value":"x"}}}},
				{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
			]`,
			code: werr.CodeType,
		},
		{
			name: "arg count mismatch",
			src: `[
				{"let":{"f":{"fn":{"params":[["x","string"]],"returns":["string"],"do":[
					{"return":{"var":"x"}}
				]}}}},
				{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
			]`,
			code: werr.CodeType,
		},
		{
			name: "arg type mismatch",
			src: `[
				{"let":{"f":{"fn":{"params":[["x","string"]],"returns":["string"],"do":[
					{"return":{"var":"x"}}
				]}}}},
				{"return":{"call":{"fn":{"var":"f"},"args":[
					{"literal":{"type":"int64","value":"1"}}
				]}}}
			]`,
			code: werr.CodeType,
		},
		{
			name: "return type mismatch",
			src: `[
				{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
					{"return":{"literal":{"type":"int64","value":"1"}}}
				]}}}},
				{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
			]`,
			code: werr.CodeType,
		},
		{
			name: "function expression eval error",
			src: `[
				{"return":{"call":{"fn":{"var":"missing"},"args":[]}}}
			]`,
			code: werr.CodeSymbol,
		},
		{
			name: "argument eval error",
			src: `[
				{"let":{"f":{"fn":{"params":[["x","string"]],"returns":["string"],"do":[
					{"return":{"var":"x"}}
				]}}}},
				{"return":{"call":{"fn":{"var":"f"},"args":[{"var":"missing"}]}}}
			]`,
			code: werr.CodeSymbol,
		},
		{
			name: "body lang error propagates",
			src: `[
				{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
					{"set":{"missing":{"literal":{"type":"string","value":"x"}}}}
				]}}}},
				{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
			]`,
			code: werr.CodeSymbol,
		},
		{
			name: "named return missing variable",
			src: `[
				{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
					{"return":{"named":"missing"}}
				]}}}},
				{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
			]`,
			code: werr.CodeSymbol,
		},
		{
			name: "deferred function error propagates",
			src: `[
				{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
					{"defer":{"call":{"fn":{"fn":{"params":[],"returns":["string"],"do":[
						{"return":{"literal":{"type":"int64","value":"1"}}}
					]}},"args":[]}}},
					{"return":{"literal":{"type":"string","value":"ok"}}}
				]}}}},
				{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
			]`,
			code: werr.CodeType,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runSrc(t, tc.src)
			assertLangCode(t, err, tc.code)
		})
	}

	t.Run("max call depth is enforced for function calls", func(t *testing.T) {
		prog, err := compiler.ParseProgram([]byte(`[
			{"let":{"f":{"fn":{"params":[],"returns":["string"],"do":[
				{"return":{"literal":{"type":"string","value":"ok"}}}
			]}}}},
			{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
		]`))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		exec := NewExecutor(context.Background(), NewScope(), nil, nil, Budget{MaxCallDepth: 1})
		exec.callDepth = 1
		_, err = exec.RunProgram(prog)
		assertLangCode(t, err, werr.CodeBudget)
	})
}

func TestFunctionReturnShapesAndErrors(t *testing.T) {
	t.Run("no returns discards body value as null", func(t *testing.T) {
		src := `[
			{"let":{"f":{"fn":{"params":[],"returns":[],"do":[
				{"return":{"literal":{"type":"int64","value":"9"}}}
			]}}}},
			{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
		]`
		v, err := runSrc(t, src)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if v.TypeName() != types.TNull || v.Go() != nil {
			t.Fatalf("want null, got %s=%v", v.TypeName(), v.Go())
		}
	})

	t.Run("error return accepts null", func(t *testing.T) {
		src := `[
			{"let":{"f":{"fn":{"params":[],"returns":["error"],"do":[
				{"return":{"literal":{"type":"null","value":null}}}
			]}}}},
			{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
		]`
		v, err := runSrc(t, src)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if v.TypeName() != types.TNull || v.Go() != nil {
			t.Fatalf("want null, got %s=%v", v.TypeName(), v.Go())
		}
	})

	t.Run("multi returns require tuple", func(t *testing.T) {
		src := `[
			{"let":{"f":{"fn":{"params":[],"returns":["int64","string"],"do":[
				{"return":{"literal":{"type":"int64","value":"1"}}}
			]}}}},
			{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
		]`
		_, err := runSrc(t, src)
		assertLangCode(t, err, werr.CodeType)
	})

	t.Run("multi return tuple matches declared shape", func(t *testing.T) {
		scope := NewScope()
		scope.Let("pair", types.NewValue("tuple<int64,string>", []any{int64(7), "ok"}), "")
		src := `[
			{"let":{"f":{"fn":{"params":[],"returns":["int64","string"],"do":[
				{"return":{"var":"pair"}}
			]}}}},
			{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
		]`
		v, err := runSrcWithScope(t, src, scope)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if v.TypeName() != "tuple<int64,string>" {
			t.Fatalf("want tuple<int64,string>, got %s", v.TypeName())
		}
	})

	t.Run("multi return tuple arity mismatch", func(t *testing.T) {
		scope := NewScope()
		scope.Let("pair", types.NewValue("tuple<int64>", []any{int64(7)}), "")
		src := `[
			{"let":{"f":{"fn":{"params":[],"returns":["int64","string"],"do":[
				{"return":{"var":"pair"}}
			]}}}},
			{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
		]`
		_, err := runSrcWithScope(t, src, scope)
		assertLangCode(t, err, werr.CodeType)
	})

	t.Run("multi return tuple type mismatch", func(t *testing.T) {
		scope := NewScope()
		scope.Let("pair", types.NewValue("tuple<string,string>", []any{"bad", "ok"}), "")
		src := `[
			{"let":{"f":{"fn":{"params":[],"returns":["int64","string"],"do":[
				{"return":{"var":"pair"}}
			]}}}},
			{"return":{"call":{"fn":{"var":"f"},"args":[]}}}
		]`
		_, err := runSrcWithScope(t, src, scope)
		assertLangCode(t, err, werr.CodeType)
	})
}

func TestNilCarrierValuesCompareEqualToNull(t *testing.T) {
	nullV, err := types.NewNull()
	if err != nil {
		t.Fatalf("NewNull: %v", err)
	}
	nilErr := types.NewValue(types.TError, nil)
	if !equalValues(nilErr, nullV) {
		t.Fatal("want nil error value to equal null")
	}
	if !equalValues(nullV, nilErr) {
		t.Fatal("want null to equal nil error value")
	}
}

func TestDeferredClosureRunsInLIFOOrder(t *testing.T) {
	src := `[
		{"let":{"out":{"literal":{"type":"string","value":""}}}},
		{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
			{"set":{"out":{"+":[{"var":"out"},{"literal":{"type":"string","value":"1"}}]}}}
		]}},"args":[]}}},
		{"defer":{"call":{"fn":{"fn":{"params":[],"returns":[],"do":[
			{"set":{"out":{"+":[{"var":"out"},{"literal":{"type":"string","value":"2"}}]}}}
		]}},"args":[]}}},
		{"return":{"named":"out"}}
	]`
	v, err := runSrc(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.TypeName() != types.TString || v.Go().(string) != "21" {
		t.Fatalf("want string=21, got %s=%v", v.TypeName(), v.Go())
	}
}

func TestDeferredClosureErrorSurfacesFromBlockReturn(t *testing.T) {
	src := `[
		{"if":{
			"cond":{"literal":{"type":"boolean","value":"true"}},
			"then":[
				{"defer":{"call":{"fn":{"fn":{"params":[],"returns":["string"],"do":[
					{"return":{"literal":{"type":"int64","value":"1"}}}
				]}},"args":[]}}},
				{"return":{"literal":{"type":"string","value":"ok"}}}
			]
		}}
	]`
	_, err := runSrc(t, src)
	assertLangCode(t, err, werr.CodeType)
}

func TestArrayHelpersRejectInvalidAccessAndBudget(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
	}{
		{
			name: "get out of range",
			src: `[
				{"let":{"xs":{"array":{"elem":"int64","items":[]}}}},
				{"return":{"arr.get":[{"var":"xs"},{"literal":{"type":"int64","value":"0"}}]}}
			]`,
			code: werr.CodeRuntime,
		},
		{
			name: "push type mismatch",
			src: `[
				{"let":{"xs":{"array":{"elem":"int64","items":[]}}}},
				{"expr":{"arr.push":[{"var":"xs"},{"literal":{"type":"string","value":"bad"}}]}}
			]`,
			code: werr.CodeType,
		},
		{
			name: "push target must be variable",
			src: `[
				{"let":{"xs":{"array":{"elem":"int64","items":[]}}}},
				{"expr":{"arr.push":[{"arr.get":[{"var":"xs"},{"literal":{"type":"int64","value":"0"}}]},{"literal":{"type":"int64","value":"1"}}]}}
			]`,
			code: werr.CodeASTShape,
		},
		{
			name: "push dotted variable rejected",
			src: `[
				{"expr":{"arr.push":[{"var":"box.items"},{"literal":{"type":"int64","value":"1"}}]}}
			]`,
			code: werr.CodeASTShape,
		},
		{
			name: "push wrong arg count",
			src: `[
				{"let":{"xs":{"array":{"elem":"int64","items":[]}}}},
				{"expr":{"arr.push":[{"var":"xs"}]}}
			]`,
			code: werr.CodeASTShape,
		},
		{
			name: "push target type mismatch",
			src: `[
				{"let":{"xs":{"literal":{"type":"string","value":"bad"}}}},
				{"expr":{"arr.push":[{"var":"xs"},{"literal":{"type":"string","value":"x"}}]}}
			]`,
			code: werr.CodeType,
		},
		{
			name: "push target eval error",
			src: `[
				{"expr":{"arr.push":[{"var":"xs"},{"literal":{"type":"int64","value":"1"}}]}}
			]`,
			code: werr.CodeSymbol,
		},
		{
			name: "push item eval error",
			src: `[
				{"let":{"xs":{"array":{"elem":"int64","items":[]}}}},
				{"expr":{"arr.push":[{"var":"xs"},{"var":"missing"}]}}
			]`,
			code: werr.CodeSymbol,
		},
		{
			name: "get index type mismatch",
			src: `[
				{"let":{"xs":{"array":{"elem":"int64","items":[]}}}},
				{"return":{"arr.get":[{"var":"xs"},{"literal":{"type":"string","value":"0"}}]}}
			]`,
			code: werr.CodeType,
		},
		{
			name: "get target eval error",
			src: `[
				{"return":{"arr.get":[{"var":"xs"},{"literal":{"type":"int64","value":"0"}}]}}
			]`,
			code: werr.CodeSymbol,
		},
		{
			name: "get index eval error",
			src: `[
				{"let":{"xs":{"array":{"elem":"int64","items":[]}}}},
				{"return":{"arr.get":[{"var":"xs"},{"var":"missing"}]}}
			]`,
			code: werr.CodeSymbol,
		},
		{
			name: "get wrong arg count",
			src: `[
				{"let":{"xs":{"array":{"elem":"int64","items":[]}}}},
				{"return":{"arr.get":[{"var":"xs"}]}}
			]`,
			code: werr.CodeASTShape,
		},
		{
			name: "len target eval error",
			src: `[
				{"return":{"arr.len":[{"var":"xs"}]}}
			]`,
			code: werr.CodeSymbol,
		},
		{
			name: "len target type mismatch",
			src: `[
				{"return":{"arr.len":[{"literal":{"type":"string","value":"bad"}}]}}
			]`,
			code: werr.CodeType,
		},
		{
			name: "len wrong arg count",
			src: `[
				{"let":{"xs":{"array":{"elem":"int64","items":[]}}}},
				{"return":{"arr.len":[{"var":"xs"},{"literal":{"type":"int64","value":"1"}}]}}
			]`,
			code: werr.CodeASTShape,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runSrc(t, tc.src)
			if err == nil {
				t.Fatal("want error, got nil")
			}
			le, ok := err.(*werr.LangError)
			if !ok {
				t.Fatalf("want LangError, got %T: %v", err, err)
			}
			if le.Code != tc.code {
				t.Fatalf("want %s, got %s", tc.code, le.Code)
			}
		})
	}

	prog, err := compiler.ParseProgram([]byte(`[
		{"let":{"xs":{"array":{"elem":"int64","items":[
			{"literal":{"type":"int64","value":"1"}}
		]}}}},
		{"expr":{"arr.push":[{"var":"xs"},{"literal":{"type":"int64","value":"2"}}]}}
	]`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	exec := NewExecutor(context.Background(), NewScope(), nil, nil, Budget{MaxArrayLength: 1})
	_, err = exec.RunProgram(prog)
	if err == nil {
		t.Fatal("want budget error, got nil")
	}
	le, ok := err.(*werr.LangError)
	if !ok {
		t.Fatalf("want LangError, got %T: %v", err, err)
	}
	if le.Code != werr.CodeBudget {
		t.Fatalf("want %s, got %s", werr.CodeBudget, le.Code)
	}

	scope := NewScope()
	scope.LetReadOnly("xs", mustInt64ArrayValue(t, 1))
	_, err = runSrcWithScope(t, `[
		{"expr":{"arr.push":[{"var":"xs"},{"literal":{"type":"int64","value":"2"}}]}}
	]`, scope)
	assertLangCode(t, err, werr.CodeReadonlyVar)

	corrupt := NewScope()
	corrupt.Let("xs", types.NewValue("array<int64>", []any{"bad-carrier"}), "")
	_, err = runSrcWithScope(t, `[
		{"expr":{"arr.push":[{"var":"xs"},{"literal":{"type":"int64","value":"2"}}]}}
	]`, corrupt)
	assertLangCode(t, err, werr.CodeType)
}

func TestArrayHelpersHandleNullAndPointers(t *testing.T) {
	t.Run("array any can push and read null", func(t *testing.T) {
		src := `[
			{"let":{"xs":{"array":{"elem":"any","items":[]}}}},
			{"expr":{"arr.push":[{"var":"xs"},{"literal":{"type":"null","value":null}}]}},
			{"return":{"arr.get":[{"var":"xs"},{"literal":{"type":"int64","value":"0"}}]}}
		]`
		v, err := runSrc(t, src)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if v.TypeName() != types.TAny || v.Go() != nil {
			t.Fatalf("want any nil, got %s=%v", v.TypeName(), v.Go())
		}
	})

	t.Run("array pointer can read nil pointer", func(t *testing.T) {
		scope := NewScope()
		scope.Let("ptrs", types.NewValue("array<*int>", []any{(*int)(nil)}), "")
		src := `[
			{"return":{"arr.get":[{"var":"ptrs"},{"literal":{"type":"int64","value":"0"}}]}}
		]`
		v, err := runSrcWithScope(t, src, scope)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		ptr, ok := v.Go().(*int)
		if v.TypeName() != "*int" || !ok || ptr != nil {
			t.Fatalf("want *int nil, got %s=%v", v.TypeName(), v.Go())
		}
	})

	t.Run("array len supports typed host slice", func(t *testing.T) {
		scope := NewScope()
		scope.Let("flags", types.NewValue("array<boolean>", []bool{true, false}), "")
		src := `[
			{"return":{"arr.len":[{"var":"flags"}]}}
		]`
		v, err := runSrcWithScope(t, src, scope)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if v.TypeName() != types.TInt64 || v.Go().(int64) != 2 {
			t.Fatalf("want int64 2, got %s=%v", v.TypeName(), v.Go())
		}
	})
}

func assertLangCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want %s, got nil", code)
	}
	le, ok := err.(*werr.LangError)
	if !ok {
		t.Fatalf("want LangError, got %T: %v", err, err)
	}
	if le.Code != code {
		t.Fatalf("want %s, got %s", code, le.Code)
	}
}

func mustInt64ArrayValue(t *testing.T, values ...int64) types.Value {
	t.Helper()
	items := make([]types.Value, len(values))
	for i, value := range values {
		items[i] = types.NewValue(types.TInt64, value)
	}
	v, err := types.NewArray(types.TInt64, items)
	if err != nil {
		t.Fatalf("NewArray: %v", err)
	}
	return v
}
