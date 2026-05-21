# go2wlang 支持矩阵

本文档记录 Go 到 wlang 翻译器当前支持的能力以及后续计划。翻译器面向调用已注册 Go 宿主包的规则函数和编排函数。

## 入口点

已支持：

- `TranslateFile(src, Options{FuncName: "Rule"})`
- `TranslateFilePath(path, Options{FuncName: "Rule"})`
- `TranslateFileDetailed(src, Options{FuncName: "Rule"})`
- `TranslateFilePathDetailed(path, Options{FuncName: "Rule"})`
- CLI JSON 输出：`go run ./cmd/go2wl -func Rule ./rule.go`
- CLI 伪代码输出：`go run ./cmd/go2wl -func Rule -pseudo ./rule.go`
- CLI 清单输出：`go run ./cmd/go2wl -func Rule -manifest rule.imports.json ./rule.go`
- CLI 嵌入式导入映射：`go run ./cmd/go2wl -func Rule -embed-import-map ./rule.go`
- 每次翻译一个具名顶层函数。

当前范围外：

- 整包翻译。
- 整仓库翻译。
- 单次调用翻译多个函数。
- 将方法作为入口点翻译。

## 导入和包选择器

已支持：

- 显式导入别名：`import pol "example.com/app/policy"`。
- 路径末段与包名匹配的普通导入。
- 包源码可访问时，声明包名与路径末段不同的导入。
- 当前模块中的项目内导入。
- 来自 `go.work` 的工作区导入。
- 本地 `replace` 导入。
- vendor 导入。
- 通过 `GOMODCACHE` 查询本地模块缓存。
- 导入包源码缺失时回退到导入路径末段。
- 当 Go 类型信息能解析文件时，区分包选择器和接收者选择器。
- 函数作用域内局部变量遮蔽导入别名。
- 导入清单：包根名称到导入路径。
- 感知 build tag 的包名发现。
- 用于选择器解析的块级作用域变量跟踪。

当前范围外：

- 使用 `golang.org/x/tools/go/packages` 加载完整模块图。
- 翻译期间下载远程模块。
- 与 `go list` 一致的版本选择语义。
- 点导入。
- 将空白导入作为可调用包。

## 语句

已支持：

- `var x = expr`
- `var x T`
- `x := expr`
- `x = expr`
- `x += expr`, `x -= expr`, `x *= expr`, `x /= expr`
- `a, b := call()`
- `a, b = call()`
- `_ = expr`
- `return expr`
- `return`（用于无返回值函数字面量）
- 命名返回值函数，例如 `func Rule(...) (err error)`
- `if cond { ... }`
- `if cond { ... } else { ... }`
- `if cond { ... } else if cond { ... }`
- 计数循环：`for i := from; i < to; i++ { ... }`
- 带步长的计数循环：`for i := from; i < to; i += step { ... }`
- 反向计数循环：`for i := len(xs)-1; i >= 0; i-- { ... }`
- range 循环：`for i, item := range xs { ... }`
- range 循环：`for _, item := range xs { ... }`
- `break`
- `continue`
- `panic(expr)`
- `defer pkg.Call(args...)`
- `defer receiver.Call(args...)`
- `defer func(){ ... }()`
- `go pkg.Call(args...)`
- `go func(){ ... }()`
- channel 发送语句：`ch <- value`
- 包含发送、接收和 default case 的 `select`。

当前范围外：

- `if init; cond { ... }`
- `switch`
- 类型 switch
- `goto`
- 标签
- 带标签的 `break`
- 带标签的 `continue`
- 循环 post 子句外的独立 `i++` 或 `i--`。
- 省略 init、condition 和 post 的 `for`。
- 含多个 init 或 post 语句的 `for`。
- `defer` 非调用表达式。
- 带参数的 `go` 函数字面量调用。
- 包级变量。
- `init` 函数。
- `recover`。

## 表达式

已支持：

- 标识符
- 选择器路径，例如 `user.Name`
- 字符串字面量
- 整数字面量
- 浮点数字面量
- 布尔字面量
- `nil`
- 一元 `!`
- 二元 `+`、`-`、`*`、`/`
- 比较运算 `>`、`>=`、`<`、`<=`、`==`、`!=`
- 布尔运算 `&&`、`||`
- 括号表达式
- 接收表达式：`<-ch`
- 索引表达式：`xs[i]` 生成 `arr.get(xs, i)`。
- 函数字面量作为值。
- 函数变量调用：`fn(args...)`。
- 函数风格的 `int64(expr)` 转换。

当前范围外：

- 一元 `-`、`+`、`^`、`&`、`*`
- 指针解引用
- 指针取址
- map 索引表达式：`m[k]`
- 切片表达式：`xs[a:b]`
- 类型断言
- 方法值。
- 泛型专属表达式。
- 反射和 `unsafe`。

## 调用

已支持：

- 包函数调用：`demo.Score(user, total)`
- 接收者方法调用：`svc.Run(input)`
- 当前包函数调用：`BuildFailureReason(step, err)`
- 函数值调用：`compensations[i](ctx, reason)`
- 作为表达式使用的调用。
- 作为语句使用的调用。
- `defer` 使用的调用。
- `go` 使用的调用。
- 来自调用的元组解构：`v, err := demo.Load(id)`。

已支持的内建函数：

- `make(chan T)`
- `make(chan T, n)`
- `close(ch)`
- `panic(v)`
- `int64(v)`
- `len(xs)` 生成 `arr.len(xs)`
- `append(xs, v)` 在 `xs = append(xs, v)` 中生成 `arr.push(xs, v)`

当前范围外：

- 可变参数展开：`fn(xs...)`。
- `copy`、`delete`、`cap`、`new`、`complex`、`real`、`imag` 等内建函数。

## 复合字面量

已支持：

- 省略键的数组和切片字面量：`[]int64{1, 2}`。
- 含键值元素的 map 字面量：`map[string]int64{"a": 1}`。
- 包限定结构体字面量：`api.Args{Name: "aaa"}`。
- 当前包结构体字面量：`Args{Name: "aaa"}`。
- 当前包结构体字面量作为调用参数：`a.Book(ctx, Args{Name: "aaa"})`。
- 使用 `Options.LocalPackageName` 控制当前包结构体字面量的生成类型前缀。

当前范围外：

- 无键结构体字面量：`api.Args{"aaa"}`。
- 带键数组元素：`[]int64{2: 99}`。
- 复合字面量内部嵌套当前范围外的表达式。

## Channel 和并发

已支持：

- `make(chan T)`
- `make(chan T, n)`
- `ch <- value`
- `<-ch`
- `close(ch)`
- `go pkg.Call(args...)`
- `go func(){ ... }()`
- `select` 接收 case。
- `select` 发送 case。
- `select` default case。

当前范围外：

- 翻译输出中的定向 channel 类型区分。
- `go func(arg T){...}(value)`。
- select case 中的复杂接收目标。

## 输出形状

已支持：

- 默认 JSON 封套：

```json
{
  "lang": "wflang/v1",
  "imports": ["demo"],
  "program": []
}
```

- 通过 `wflang.FormatPseudoCode` 格式化伪代码。
- 详细结果：

```go
type Result struct {
	JSON     []byte
	Imports  map[string]string
	FuncName string
	Source   string
}
```

- 可选的嵌入式导入映射：

```json
{
  "lang": "wflang/v1",
  "imports": ["demo"],
  "importMap": {
    "demo": "example.com/demo"
  },
  "program": []
}
```

## 选项语义

已支持：

- `FuncName`：选择要翻译的顶层函数。
- `PackageAliases`：在源导入缺失或由调用方合成时，提供包根名称到导入路径的映射。
- `Lang`：覆盖 JSON 封套中的语言字符串。
- `Imports`：覆盖 JSON 封套中的导入包根列表。
- `Filename`：设置诊断信息使用的源文件名。
- `Dir`：设置用于导入解析和类型感知解析的目录。
- `EmbedImportMap`：将 `importMap` 写入生成的 JSON。
- `LocalPackageName`：控制当前包结构体字面量类型名，例如 `policy.Args`。

元数据存储选择：

- JSON 和依赖元数据分开存储时，使用 `Result.Imports`。
- 单个 JSON 文档需要包含依赖元数据时，使用 `EmbedImportMap`。
- 使用 CLI `-manifest` 将依赖元数据写入单独文件。
- 使用 CLI `-embed-import-map` 将依赖元数据包含在 JSON 输出中。

## 诊断

已支持：

- 当前范围外语法会返回 `DiagnosticError`。
- 诊断信息包含源码位置和 AST 节点类型。

## 推荐的 Go 翻译风格

使用这种形态：

```go
func Rule(ctx context.Context, svc Service, user policy.User) policy.Decision {
	normalized := policy.Normalize(user)
	result, err := svc.Book(ctx, api.Args{Name: normalized.Name})
	if err != nil {
		panic(err)
	}
	return result
}
```

把复杂行为保留在已注册的宿主包中，并从被翻译函数调用它。
