# go2wlang Examples

`approval_rule.go` demonstrates the intended v1 migration shape: a Go rule function with host package calls, receiver method calls, explicit error values, and concurrency primitives.

Run:

```bash
go run ./cmd/go2wl -func ApprovalRule -pseudo go2wlang/examples/approval_rule.go
go run ./cmd/go2wl -func ApprovalRule go2wlang/examples/approval_rule.go
```

The important mappings in this example:

- `policy.Normalize(user)` becomes a package function call: `policy.Normalize(user)`.
- `policy.Decision{...}` becomes a registered package struct literal.
- `audit.Close("approval-rule")` becomes a deferred package function call.
- `scorer.Score(normalized, total)` becomes a receiver method call.
- `store.Save(decision)` becomes a receiver method call returning an ordinary `error` value.
- `notify.Publish(status)` becomes a package function routine call.
- `make(chan string, 1)`, `events <- status`, `<-events`, and `select` become wlang channel operations.

The example intentionally leaves domain behavior in host packages and host receiver values. go2wlang translates the orchestration layer into wlang JSON.
