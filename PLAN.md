# Go 闭包函数值与 go2wlang 补偿流程翻译实施计划

**Goal:** 让 wlang 能表达并执行 Go 补偿栈模式：闭包函数值、defer closure、函数数组、数组追加、数组下标调用、命名返回值经 defer 改写。

**Architecture:** 在 wlang runtime 增加一等函数值和 Go 风格引用捕获；go2wlang 识别 Go 子集并生成 `fn/call` JSON。示例和一致性测试使用本地 stub workflow/temporal 包。

**Tech Stack:** Go AST/go/types、现有 compiler/runtime/types/go2wlang/cmd/go2wl。

---

## Key Changes

- 新增 wlang 函数值：
  - 类型 helper：`types.FuncType(params, returns)`，用于 `func<(P1,P2)->R>` 这类类型名。
  - AST：`FuncLit`、`FuncCall`。
  - JSON：
    - `{"fn":{"params":[["ctx","workflow.Context"],["reason","FailureReason"]],"returns":["error"],"do":[...]}}`
    - `{"call":{"fn":{"var":"comp"},"args":[{"var":"ctx"},{"var":"reason"}]}}`
  - runtime 闭包捕获当前 lexical scope，变量按 Go 引用捕获语义共享 cell。

- 扩展 `defer`：
  - `ast.Defer` 接受 host call 或 function call。
  - `defer func(){...}()` 翻译为 deferred `call`。
  - defer 执行顺序保持 LIFO，闭包执行时可读写捕获变量。

- 增加命名返回表达能力：
  - JSON：`{"return":{"named":"err"}}`
  - 语义：block/program/routine 退出时先执行 defer，再读取 `err` 的最终值。
  - go2wlang 对 `func(...)(err error)` 初始化根作用域变量 `err`，把 `return expr` 翻成 `set err=expr` + `return named err`。

- 增加 array 原生方法：
  - `{"arr.push":[arr, value]}`：原地追加，返回 `null`。
  - `{"arr.get":[arr, index]}`：返回元素。
  - `{"arr.len":[arr]}`：返回 `int64`。
  - 受 `Budget.MaxArrayLength` 约束。

- 扩展 go2wlang：
  - `ast.FuncLit` 作为值翻译为 `fn`。
  - `append(xs, v)` 翻译为 `arr.push(xs, v)` 表达式语句或赋值中的等价语句序列。
  - `len(xs)` 翻译为 `arr.len(xs)`。
  - `xs[i]` 翻译为 `arr.get(xs, i)`。
  - `xs[i](args...)` 翻译为 dynamic `call`。
  - 支持 `for i := len(xs)-1; i >= 0; i--` 反向循环。
  - 保留现有包函数、结构体方法、结构体字面量、导入映射能力。

## Docs And Examples

- 更新 `LANGUAGE.md`：函数值、闭包捕获、dynamic call、defer closure、`return named`、`arr.push/get/len`。
- 更新 `go2wlang/README.md` 和 `go2wlang/SUPPORT.md`：新增支持清单、Go 子集限制、参数传递说明、导入元数据说明。
- 新增 go2wlang 示例：
  - 一个接近用户给出的 OrderWorkflow 补偿流程。
  - 使用本地 stub 的 `workflow.ExecuteActivity(...).Get(...)`、`temporal.NewApplicationError(...)`。
  - 包含外部包函数、结构体方法、结构体字面量、闭包捕获 `input/reserve/failedStep/err`。

## Test Plan

- runtime 单元测试：
  - closure 读取和修改外层变量。
  - closure 存入 `array<func<...>>`，`arr.push` 后 `arr.get` 调用。
  - `defer func(){...}()` 修改 `err`，`return named err` 返回修改后的值。
  - deferred closure LIFO 顺序。
  - `arr.push/get/len` 类型错误、越界、预算错误。

- go2wlang 转换测试：
  - `func(...) (err error)` 命名返回转换。
  - `append(compensations, func(...){...})` 输出 `arr.push`。
  - `compensations[i](ctx, reason)` 输出 `arr.get` + `call`。
  - 反向补偿 loop 输出可执行 `fori`。
  - 包函数和结构体方法归属保持准确。

- 行为一致性测试：
  - Go 原函数用 stub workflow 执行一次。
  - go2wlang 生成 JSON 后通过 wlang runtime 执行一次。
  - 对比 activity 调用顺序、补偿调用顺序、最终 error 类型/消息。

- 验证命令：
  - `env -u GOROOT -u GOTOOLCHAIN go test ./...`

## Assumptions

- Temporal 示例使用本地 stub 类型和函数验证翻译能力。
- 闭包变量按 Go 引用捕获。
- wlang 原生 array 添加方法使用 `arr.push` 原地追加。
- Go 的 `append` 在 go2wlang 中作为输入模式识别，wlang JSON 输出使用 `arr.push`。
- 命名返回使用根作用域变量加 `return named` 语义。
