# wlang

`wlang` is a Go-hosted JSON programming runtime. It lets you store executable logic as JSON, validate it with a typed compiler, and run it against a controlled set of Go functions, methods, values, and resource limits.

The Go module path is:

```bash
go get github.com/donutnomad/wlang
```

The language envelope is versioned as `wflang/v1`. The public Go package is `github.com/donutnomad/wlang/wflang`.

## Why wlang

wlang is useful when business logic needs to be stored, audited, generated, migrated, or edited outside the main Go binary while still running inside a Go-controlled security boundary.

Typical use cases:

- rule evaluation and policy checks
- workflow orchestration over registered Go host functions
- JSON-backed automation scripts
- business DSL compilation targets
- safe user-editable logic with budgets and capabilities
- Go-to-JSON migration for a supported Go subset through `go2wlang`

## Highlights

- **JSON as AST**: programs are plain JSON, easy to persist, diff, audit, and transport.
- **Go as host**: domain behavior stays in Go packages, methods, and registered types.
- **Typed execution**: literals, arrays, maps, structs, tuples, channels, function values, and host signatures are validated.
- **Explicit error model**: Go `error` returns become ordinary `error` values or tuple slots; panic, context cancellation, and budget exhaustion remain runtime failures.
- **Control flow**: `if`, `match`, `foreach`, `fori`, `break`, `continue`, `return`, `defer`, `routine`, `await`, and `select`.
- **Closures and deferred calls**: function values capture lexical variables by shared cell semantics and can be stored, called, and deferred.
- **Tooling**: `wlfmt` formats JSON into stable JSON or pseudocode; `go2wl` translates a supported Go subset into executable wlang JSON.

## Quick Start

Install the library:

```bash
go get github.com/donutnomad/wlang
```

Embed the runtime in Go:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/donutnomad/wlang/wflang"
)

func strlen(s string) (int64, error) {
	return int64(len(s)), nil
}

func main() {
	reg := wflang.DefaultRegistry()
	err := reg.BindGoPackage("strx", wflang.PackageSpec{
		Functions: []wflang.FuncSpec{
			{GoName: "Len", Impl: strlen},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	engine := wflang.NewEngine(wflang.EngineOptions{Registry: reg})

	programJSON := []byte(`[
	  {"return":{"Len":[
	    {"pkg":"strx"},
	    {"literal":{"type":"string","value":"hello"}}
	  ]}}
	]`)

	program, err := engine.CompileJSON(programJSON)
	if err != nil {
		log.Fatal(err)
	}

	value, err := program.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(value.Go())
}
```

Output:

```text
5
```

## JSON Program Shape

A program can be a bare statement array:

```json
[
  {
    "let": {
      "name": { "literal": { "type": "string", "value": "alice" } }
    }
  },
  {
    "return": {
      "+": [
        { "literal": { "type": "string", "value": "hello " } },
        { "var": "name" }
      ]
    }
  }
]
```

It can also use an envelope:

```json
{
  "lang": "wflang/v1",
  "imports": ["str", "arr", "m"],
  "program": [
    { "return": { "literal": { "type": "boolean", "value": true } } }
  ]
}
```

See [examples/full_feature_demo.json](examples/full_feature_demo.json) for a broad syntax tour.

## Go Host Binding

Host functions are registered as package functions:

```go
reg := wflang.NewRegistry()
err := reg.BindGoPackage("orders", wflang.PackageSpec{
	Functions: []wflang.FuncSpec{
		{GoName: "Reserve", Impl: Reserve},
		{GoName: "Pay", Impl: Pay},
	},
})
```

JSON calls the registered package explicitly:

```json
{
  "Reserve": [
    { "pkg": "orders" },
    { "var": "orderID" }
  ]
}
```

Go types can be bound so exported methods are callable on typed values:

```go
err := reg.AutoBindType((*OrderService)(nil))
```

The runtime supports context-aware functions, capability checks, pure stdlib registration, structured `LangError` diagnostics, and budget limits for steps, arrays, object keys, recursion, and routines.

## Error Semantics

Go return shapes map directly into wlang values:

| Go signature | wlang result |
| --- | --- |
| `func(...) T` | `T` |
| `func(...) error` | `error` value, with `nil` represented as a typed null-like error carrier |
| `func(...) (T, error)` | `tuple<T,error>` |
| `func(...) (T1, T2)` | `tuple<T1,T2>` |
| `func(...) (T1, T2, error)` | `tuple<T1,T2,error>` |

Program logic handles error values explicitly through variables, tuple destructuring, comparisons, and host helper functions. Go panic, context cancellation, and budget exhaustion surface as `LangError`.

## Language Surface

Core values and collections:

- scalar typed literals: `string`, `boolean`, fixed-width integers, floats, `bigInt`, `bigDecimal`, `null`, `error`
- `array<T>` with `arr.push`, `arr.get`, `arr.len`, and related stdlib helpers
- `map<K,V>` literals and `map.get`, `map.set`, `map.del`, `map.has`, `map.keys`, `map.values`, `map.len`
- struct literals for registered Go struct types
- `chan<T>` with `ch.send`, `ch.recv`, `ch.close`, `ch.len`, `ch.cap`
- `tuple<...>` values and destructuring
- first-class `func<...>` values with lexical capture

Statements and control flow:

- `let`, typed `let`, tuple destructuring, and `_` discard
- `set`
- `if`, `match`
- `foreach` over arrays and maps
- `fori`
- `break`, `continue`, `return`, named return
- `defer` over host calls and function calls
- `routine`, `await`
- `select` over channel send, receive, and default cases
- `panic`

The full language reference lives in [LANGUAGE.md](LANGUAGE.md). For a shorter walkthrough of JSON, pseudo output, go2wlang, and built-in namespaces, see [QUICKSTART.md](QUICKSTART.md).

## go2wlang

`go2wlang` translates a deliberately constrained Go subset into wlang JSON. It is designed for rule functions and orchestration functions where domain behavior remains in registered Go packages.

CLI usage:

```bash
go run ./cmd/go2wl -func ApprovalRule go2wlang/examples/approval_rule.go
go run ./cmd/go2wl -func ApprovalRule -pseudo go2wlang/examples/approval_rule.go
go run ./cmd/go2wl -func OrderWorkflow -embed-import-map go2wlang/examples/order_workflow.go
```

Library usage:

```go
jsonProgram, err := go2wlang.TranslateFilePath("rules/rule.go", go2wlang.Options{
	FuncName: "Rule",
})

result, err := go2wlang.TranslateFilePathDetailed("rules/rule.go", go2wlang.Options{
	FuncName:       "Rule",
	EmbedImportMap: true,
})
```

Supported Go patterns include package calls, receiver method calls, struct literals, named returns, `defer func(){...}()`, closures, arrays of function values, `append` to `arr.push`, `len` to `arr.len`, reverse compensation loops, goroutines, channels, and `select`.

Detailed translator documentation:

- [go2wlang/README.md](go2wlang/README.md)
- [go2wlang/SUPPORT.md](go2wlang/SUPPORT.md)
- [go2wlang/examples](go2wlang/examples)

## Formatter

`wlfmt` renders executable JSON as stable JSON or pseudocode:

```bash
go run ./cmd/wlfmt examples/full_feature_demo.json
go run ./cmd/wlfmt -json examples/full_feature_demo.json
```

`go2wl -pseudo` uses the same formatter so generated JSON can be reviewed in a compact, Go-like form.

## Security Model

wlang executes inside the host process, so the host controls the available surface area:

- only registered packages and bound methods are callable
- side effects are exposed through explicit host bindings
- capabilities gate sensitive host functions
- budgets limit steps, recursion, arrays, maps, and routines
- context cancellation stops blocking operations such as channel send/receive and routine waits
- structured diagnostics preserve error code and JSON path

This model keeps the language core small and moves application-specific authority into Go.

## Project Layout

```text
ast/          JSON AST nodes
compiler/     parser, normalizer, type checker, migration, diagnostics
errors/       structured LangError codes
go2wlang/     Go subset translator
registry/     Go host binding and reflection bridge
runtime/      executor, scope, closures, routines, channels
stdlib/       pure built-in packages
types/        typed value model
wflang/       public Go API
cmd/go2wl/    Go-to-wlang CLI
cmd/wlfmt/    formatter CLI
examples/     JSON language examples
```

## Development

Run the full test suite:

```bash
env -u GOROOT -u GOTOOLCHAIN go test ./...
```

Run the formatter on the feature demo:

```bash
go run ./cmd/wlfmt examples/full_feature_demo.json
```

Generate JSON from the compensation-flow example:

```bash
go run ./cmd/go2wl -func OrderWorkflow go2wlang/examples/order_workflow.go
```

## Documentation

- [QUICKSTART.md](QUICKSTART.md): quick walkthrough and built-in namespace reference
- [LANGUAGE.md](LANGUAGE.md): language reference and semantics
- [SPEC_TESTS.md](SPEC_TESTS.md): specification-oriented test coverage
- [examples/README.md](examples/README.md): JSON feature demo notes
- [go2wlang/README.md](go2wlang/README.md): translator usage and API
- [go2wlang/SUPPORT.md](go2wlang/SUPPORT.md): supported and unsupported Go patterns
