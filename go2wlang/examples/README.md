# go2wlang 示例

`approval_rule.go` 展示 v1 推荐迁移形态：一个包含宿主包调用、接收者方法调用、显式错误值和并发原语的 Go 规则函数。

`order_workflow.go` 展示补偿流程形态：命名返回值、`defer func(){...}()`、闭包捕获、函数数组、`append` 到 `arr.push`、`len` 到 `arr.len`、`compensations[i](...)` 到函数值动态调用。

运行：

```bash
go run ./cmd/go2wl -func ApprovalRule -pseudo go2wlang/examples/approval_rule.go
go run ./cmd/go2wl -func ApprovalRule go2wlang/examples/approval_rule.go
go run ./cmd/go2wl -func OrderWorkflow -pseudo go2wlang/examples/order_workflow.go
go run ./cmd/go2wl -func OrderWorkflow go2wlang/examples/order_workflow.go
```

本示例中的关键映射：

- `policy.Normalize(user)` 生成包函数调用：`policy.Normalize(user)`。
- `policy.Decision{...}` 生成已注册的包结构体字面量。
- `audit.Close("approval-rule")` 生成延迟执行的包函数调用。
- `scorer.Score(normalized, total)` 生成接收者方法调用。
- `store.Save(decision)` 生成返回普通 `error` 值的接收者方法调用。
- `notify.Publish(status)` 生成包函数协程调用。
- `make(chan string, 1)`、`events <- status`、`<-events` 和 `select` 生成 wlang channel 操作。
- `defer func(){...}()` 生成 deferred 函数值调用。
- `append(compensations, fn)` 生成 `arr.push(compensations, fn)`。
- `compensations[i](ctx, reason)` 生成 `arr.get` 和 `call`。
- `func(...) error { ... }` 生成 wlang `fn` 函数值。

本示例有意将领域行为保留在宿主包和宿主接收者值中。go2wlang 会把编排层翻译为 wlang JSON。
