# wflang JSON Feature Demo

`full_feature_demo.json` is a syntax-level feature tour for the current JSON AST.

It demonstrates:

- envelope fields: `lang`, `imports`, `program`
- scalar literals, array literals, map literals, struct literals, and channel literals
- `let`, typed `let`, tuple destructuring, `_` discard, and `set`
- `if`, `foreach`, `fori`, `continue`, `match`, and `return`
- builtin calls including arithmetic, comparison, string `+`, `m.*`, and `ch.*`
- explicit host error handling through tuple values
- `defer`
- routine block form, legacy routine call form, and `await`
- channel `send`, `recv`, `close`, and `select`

The file uses example host packages and types so the JSON can show the full surface area in one program. A runnable host setup needs these registry entries:

- package `demo`
  - struct type `demo.User` with fields `Name string`, `Age int64`, `Active bool`
  - struct type `demo.Report` with fields matching the final return object
  - function `Score(demo.User, int64) (int64, error)`
- package `worker`
  - function `Echo(string) (string, error)`
- package `audit`
  - function `Close(string) error`

The standard library packages used here are `arr`, `ch`, `m`, `num`, `str`, `to`, and `val`.

Host `error` values are ordinary wflang values in this example. Calls returning `(T, error)` are consumed with tuple destructuring, and calls returning `error` are suitable for `defer` cleanup where deferred errors are handled by the runtime cleanup policy.
