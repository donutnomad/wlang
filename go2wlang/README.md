# go2wlang

`go2wlang` 将一个刻意收窄的 Go 子集翻译成 wlang JSON。它面向开发者，用于把规则函数和编排函数迁移到 wlang，同时把复杂行为保留在已注册的 Go 宿主包中。

## 输入

v1 翻译器接收一个 Go 源文件和一个顶层函数名。选中的函数体会生成一个单独的 `wflang/v1` 程序封套。

```bash
go run ./cmd/go2wl -func Rule ./rule.go
go run ./cmd/go2wl -func Rule -pseudo ./rule.go
go run ./cmd/go2wl -func Rule -manifest rule.imports.json ./rule.go
go run ./cmd/go2wl -func Rule -embed-import-map ./rule.go
```

函数参数会被视为已有的 wlang 变量。参数声明由调用环境提供。

Go 调用方可以使用以下 API：

```go
jsonProgram, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
jsonProgram, err := go2wlang.TranslateFilePath("rules/rule.go", go2wlang.Options{FuncName: "Rule"})
result, err := go2wlang.TranslateFilePathDetailed("rules/rule.go", go2wlang.Options{FuncName: "Rule"})
```

`TranslateFilePath` 会使用源文件目录启用类型感知的选择器解析。当 Go 类型检查能够解析该文件时，它可以区分导入包和局部变量。

`TranslateFilePath` 还会从导入路径解析真实包名。当导入包可通过当前模块、`go.work`、本地 `replace`、`vendor`、`GOMODCACHE` 或 `GOPATH/src` 访问时，它会读取导入包目录。这样可以处理路径末段与声明包名不同的导入：

```go
import "example.com/app/shared/approvalkit"

func Rule(req approvals.Request) int64 {
	return approvals.Score(req)
}
```

生成的调用会使用 `{"pkg":"approvals"}`。外部依赖缺失时会回退到导入路径末段，从而仍可为选中函数生成诊断信息。

详细 API 会返回生成的 JSON 以及导入清单：

```go
type Result struct {
	JSON     []byte
	Imports  map[string]string
	FuncName string
	Source   string
}
```

`Options{EmbedImportMap: true}` 会向 JSON 封套添加 `importMap`。`Options{LocalPackageName: "policy"}` 控制当前包结构体字面量的生成类型前缀，例如 `Args{Name: "aaa"}`。

## API 选项和元数据

`Options.FuncName` 是必填项。它用于选择要翻译的顶层 Go 函数：

```go
jsonProgram, err := go2wlang.TranslateFilePath("rules/rule.go", go2wlang.Options{
	FuncName: "Rule",
})
```

`Options.LocalPackageName` 控制当前包结构体字面量名称。当 Go 代码使用 `Args{Name: "aaa"}`，且宿主注册表期望特定类型名时，这个选项很有用：

```go
jsonProgram, err := go2wlang.TranslateFilePath("rules/rule.go", go2wlang.Options{
	FuncName:         "Rule",
	LocalPackageName: "policy",
})
```

输入：

```go
a.Book(ctx, Args{Name: "aaa"})
```

生成的类型名：

```json
{"struct":["policy.Args",{"Name":{"literal":{"type":"string","value":"aaa"}}}]}
```

`Options.EmbedImportMap` 控制依赖元数据是否嵌入生成的 JSON：

```go
result, err := go2wlang.TranslateFilePathDetailed("rules/rule.go", go2wlang.Options{
	FuncName:       "Rule",
	EmbedImportMap: true,
})
```

默认 JSON 只保留包根名称：

```json
{
  "lang": "wflang/v1",
  "imports": ["policy"],
  "program": []
}
```

嵌入式 JSON 包含包根名称到导入路径的元数据：

```json
{
  "lang": "wflang/v1",
  "imports": ["policy"],
  "importMap": {
    "policy": "example.com/app/policy"
  },
  "program": []
}
```

详细 API 始终通过 `Result.Imports` 单独返回元数据：

```go
result, err := go2wlang.TranslateFilePathDetailed("rules/rule.go", go2wlang.Options{
	FuncName: "Rule",
})
jsonProgram := result.JSON
importManifest := result.Imports
```

当可执行 JSON 与依赖元数据拥有不同生命周期时，使用单独的元数据存储：

```text
rules
- id
- name
- wlang_json

rule_imports
- rule_id
- pkg_name
- import_path
```

当一个 JSON 文档需要包含检查或迁移所需的全部信息时，使用嵌入式元数据：

```go
result, err := go2wlang.TranslateFilePathDetailed("rules/rule.go", go2wlang.Options{
	FuncName:       "Rule",
	EmbedImportMap: true,
})
```

对应的 CLI 用法：

```bash
go run ./cmd/go2wl -func Rule -manifest rule.imports.json ./rule.go
go run ./cmd/go2wl -func Rule -embed-import-map ./rule.go
```

`-manifest` 会把 `{"policy":"example.com/app/policy"}` 写入单独文件。`-embed-import-map` 会把同一映射写入生成的 JSON。

## 已支持的 Go 子集

语句：

- `var x = expr`
- `x := expr`
- `x = expr`
- `var x T`
- `a, b := call()` 和 `a, b = call()` 作为元组解构
- `return expr`
- `return`
- 命名返回值函数，例如 `func Rule(...) (err error)`
- `if cond { ... } else { ... }`
- `for i := from; i < to; i++ { ... }`
- `for i := from; i < to; i += step { ... }`
- `for i := len(xs)-1; i >= 0; i-- { ... }`
- `for i, item := range xs { ... }`
- `for _, item := range xs { ... }`
- `break`
- `continue`
- `panic(expr)`
- `defer pkg.Call(args...)` 和 `defer receiver.Call(args...)`
- `defer func(){ ... }()`
- `go pkg.Call(args...)`
- `go func(){ ... }()`
- `ch <- value`
- 包含发送、接收和 default case 的 `select`

表达式：

- 标识符和选择器路径
- 字符串、布尔、整数、浮点数和 `nil` 字面量
- 一元 `!`
- 二元 `+`、`-`、`*`、`/`、比较运算、`&&`、`||`
- 包调用，例如 `demo.Score(user, total)`
- 当前包调用，例如 `BuildFailureReason(step, err)`
- 接收者调用，例如 `svc.Run(input)`
- 函数字面量作为值
- 函数值调用，例如 `compensations[i](ctx, reason)`
- 索引表达式 `xs[i]`
- 切片、数组、map 和结构体复合字面量
- 当前包结构体字面量，例如 `Args{Name: "aaa"}`
- `make(chan T)` 和 `make(chan T, n)`
- 接收表达式 `<-ch`

内建函数：

- `close(ch)` 生成 `ch.close(ch)`
- `panic(v)` 生成 wlang `panic` 语句
- `make(chan T, n)` 生成 wlang channel 字面量
- `len(xs)` 生成 `arr.len(xs)`
- `xs = append(xs, v)` 生成 `arr.push(xs, v)`

## 映射规则

包选择器调用使用 Go 导入和别名。给定 `import demo "example.com/demo"`，`demo.Score(x)` 会生成：

```json
{"Score":[{"pkg":"demo"},{"var":"x"}]}
```

根对象为局部变量的选择器调用会映射为接收者调用。`svc.Run(x)` 会生成：

```json
{"Run":[{"var":"svc"},{"var":"x"}]}
```

使用 `TranslateFilePath` 时，选择器归属由 `go/types` 数据和已解析的导入包名共同决定。它可以处理项目内导入、`go.work` 模块、遮蔽导入别名的局部变量，以及声明包名与路径末段不同的导入。当类型检查无法解析依赖时，翻译器会回退到上面的语法级别别名规则。

Go 中来自调用的多重赋值会生成 wlang 元组解构：

```go
risk, err := demo.Score(user, total)
```

```json
{"let":[["risk","err"],{"Score":[{"pkg":"demo"},{"var":"user"},{"var":"total"}]}]}
```

Go 闭包和补偿栈会生成 wlang 函数值与 array 原生操作：

```go
compensations = append(compensations, func(ctx workflow.Context, reason FailureReason) error {
	return workflow.MarkReserveFailed(ctx, reason)
})
compErr := compensations[i](ctx, reason)
```

```json
[
  {"expr":{"arr.push":[{"var":"compensations"},{"fn":{"params":[["ctx","workflow.Context"],["reason","FailureReason"]],"returns":["error"],"do":[...]}}]}},
  {"let":{"compErr":{"call":{"fn":{"arr.get":[{"var":"compensations"},{"var":"i"}]},"args":[{"var":"ctx"},{"var":"reason"}]}}}}
]
```

## Go 暂缺能力

遇到当前范围外的语法时，翻译器会返回带有源码位置和节点类型的 `DiagnosticError`。

v1 当前范围外：

- 嵌套函数声明
- 接口分派、类型断言和类型 switch
- 反射、`unsafe` 和 cgo
- 泛型专属结构
- 指针取址、指针解引用和复杂别名写入
- map/slice 索引赋值
- `switch` fallthrough 和带标签控制流
- `goto`
- `recover`
- 含多个 init/post 语句的复杂 `for` 语句
- 包级变量、`init` 和 `iota`
- 接收/发送左侧较复杂的 select case

推荐迁移模式：把当前范围外的行为保留在 Go 宿主函数中，并从生成的 wlang 程序调用这些宿主函数。
