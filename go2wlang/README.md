# go2wlang

`go2wlang` translates a deliberately small Go subset into wlang JSON. It is a developer tool for migrating rule and orchestration functions into wlang while keeping complex behavior in registered Go host packages.

## Inputs

The v1 translator accepts one Go source file and one top-level function name. The selected function body becomes a single `wflang/v1` program envelope.

```bash
go run ./cmd/go2wl -func Rule ./rule.go
go run ./cmd/go2wl -func Rule -pseudo ./rule.go
```

Function parameters are treated as existing wlang variables. The translator does not emit declarations for parameters.

## Supported Go Subset

Statements:

- `var x = expr`
- `x := expr`
- `x = expr`
- `a, b := call()` and `a, b = call()` as tuple destructuring
- `return expr`
- `if cond { ... } else { ... }`
- `for i := from; i < to; i++ { ... }`
- `for i := from; i < to; i += step { ... }`
- `for i, item := range xs { ... }`
- `for _, item := range xs { ... }`
- `break`
- `continue`
- `panic(expr)`
- `defer pkg.Call(args...)` and `defer receiver.Call(args...)`
- `go pkg.Call(args...)`
- `go func(){ ... }()`
- `ch <- value`
- `select` with send, receive, and default cases

Expressions:

- identifiers and selector paths
- string, bool, integer, float, and `nil` literals
- unary `!`
- binary `+`, `-`, `*`, `/`, comparisons, `&&`, `||`
- package calls such as `demo.Score(user, total)`
- receiver calls such as `svc.Run(input)`
- composite literals for slices, arrays, maps, and structs
- `make(chan T)` and `make(chan T, n)`
- receive expression `<-ch`

Builtins:

- `close(ch)` emits `ch.close(ch)`
- `panic(v)` emits a wlang `panic` statement
- `make(chan T, n)` emits a wlang channel literal

## Mapping Rules

Package selector calls use Go imports and aliases. Given `import demo "example.com/demo"`, `demo.Score(x)` emits:

```json
{"Score":[{"pkg":"demo"},{"var":"x"}]}
```

Selector calls whose root is a local variable are receiver calls. `svc.Run(x)` emits:

```json
{"Run":[{"var":"svc"},{"var":"x"}]}
```

Go multiple assignment from a call emits wlang tuple destructuring:

```go
risk, err := demo.Score(user, total)
```

```json
{"let":[["risk","err"],{"Score":[{"pkg":"demo"},{"var":"user"},{"var":"total"}]}]}
```

## Unsupported Go

The translator returns `DiagnosticError` with source position and node type for unsupported syntax.

Unsupported in v1:

- function values as ordinary values
- nested function declarations
- interface dispatch, type assertions, and type switches
- reflection, `unsafe`, and cgo
- generics-specific constructs
- pointer address-of, pointer dereference, and complex alias writes
- map/slice index assignment
- `switch` fallthrough and labeled control flow
- `goto`
- named return mutation and `recover`
- complex `for` statements with multiple init/post statements
- package-level variables, `init`, `iota`, and cross-file type resolution
- select cases with complex receive/send left-hand sides

The intended migration pattern is to leave unsupported behavior in Go host functions and call those host functions from the generated wlang program.
