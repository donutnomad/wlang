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
- `x += expr`, `x -= expr`, `x *= expr`, `x /= expr`, `x %= expr`
- `x++`、`x--`（变量、字段与元素）
- `a, b := call()`
- `a, b = call()`：已存在的变量（包括 `:=` 复用的变量）被更新，不在内层块里另建同名变量
- `a, b = x, y`、`a, b := x, y`、`a, b = b, a`：先按顺序求值左侧下标与全部右值，再依次赋值
- `const` 声明（使用处折叠成字面量）
- `_ = expr`
- `return expr`
- `return a, b`（翻成 tuple 返回）
- `return`（无返回值函数字面量；命名结果函数中的裸 return）
- 命名返回值函数，例如 `func Rule(...) (err error)`
- 多命名结果，例如 `func Rule(...) (out Out, err error)`、同组命名 `(a, b int64)`、空白 `_` 结果槽位
- 显式 `return out, err`：先求值并赋给命名结果参数，再执行 defer，最后读取最终结果
- `if cond { ... }`
- `if cond { ... } else { ... }`
- `if cond { ... } else if cond { ... }`
- `if init; cond { ... }`：init 声明的变量只在该 `if` 内可见，遮蔽同名外层变量
- 表达式 `switch` 和条件 `switch`：tag 只求值一次；`break` 退出 `switch`
- 类型 switch，生成 `type.is` / `type.assert`
- 三段式循环 `for i := from; cond; post { ... }`：每轮求值 `cond`，`post` 可以是任意简单语句，循环体对 `i` 的写入带入下一轮，闭包捕获每轮自己的 `i`
- range 循环：`for i := range xs`（`i` 是下标）、`for i, item := range xs`、`for _, item := range xs`、`for range xs`；长度在开始时固定，元素在每轮开始时读取
- `for i := range n`（`n` 为 `int`/`int64`）
- `for k, v := range m`：按 key 升序遍历；遍历中删除的未访问 key 被跳过
- `break`、`continue`，包括带标签的形式和 `switch`/`select` 内的 `break`
- `panic(expr)`
- `defer pkg.Call(args...)`
- `defer receiver.Call(args...)`
- `defer func(){ ... }()`
- `go pkg.Call(args...)`
- `go func(){ ... }()`
- `go func(arg T){ ... }(value)`
- channel 发送语句：`ch <- value`
- 包含发送、接收和 default case 的 `select`。

翻译期拒绝（诊断带源码位置与写法名称）：

- `goto`、`fallthrough`。
- 省略 init 或 condition 的 `for`（`for {}`、`for cond {}`）；init 不是 `i := from`。
- `range` 遍历 string、channel、函数、指针，或 `int64` 以外的整数类型；`for k, v = range`（用 `=`）。
- `defer` 非调用表达式。
- 包级变量、`init` 函数。
- `recover`、`min`、`max`、`clear`、`print` 等未列出的内建函数。

## 表达式

已支持：

- 标识符
- 选择器路径，例如 `user.Name`
- 字符串字面量
- 整数字面量
- 浮点数字面量
- 布尔字面量
- `nil`
- 常量表达式（字面量、具名常量、`iota`、`time.Hour` 等包常量、常量运算）按 Go 推定的类型折叠成字面量，例如 `var x int32 = 5` 生成 `int32` 字面量
- 一元 `!`
- 一元 `-`（零值取自操作数类型）、`+`、`^`
- 指针解引用 `*ptr`
- 二元 `+`、`-`、`*`、`/`、`%`（整数溢出按位宽回绕，除法向零截断，同 Go）
- 比较运算 `>`、`>=`、`<`、`<=`、`==`、`!=`
- 布尔运算 `&&`、`||`
- 括号表达式
- 接收表达式：`<-ch`
- slice/array 索引表达式：`xs[i]` 生成 `arr.get(xs, i)`。
- map 单值索引表达式：`dict[k]` 生成 `map.value(dict, k)`。
- map 双值索引表达式：`v, ok := dict[k]` 生成 `map.get(dict, k)`。
- 切片表达式：`xs[a:b]` 生成 `arr.slice(xs, a, b)`。
- 取址输出参数：`&ident` 生成 `out`。
- 取址输出参数：`&selector` 生成带路径的 `out`。
- 类型断言：`x.(T)` 生成 `type.assert`。
- 双值类型断言：`v, ok := x.(T)` 生成 `type.assert.ok`。
- 泛型函数实例化调用会去掉类型实参并翻译普通调用。
- `reflect` 和 `unsafe` 包选择器调用按普通包函数调用翻译。
- 导入包函数值：`pkg.Func` 生成 `symbol`。
- 当前包顶层函数值：`Helper` 生成 `symbol`。
- receiver 方法值：`receiver.Method` 生成 `method`。
- 函数字面量作为值。
- 函数变量调用：`fn(args...)`。
- 不改变 WLang 表示的转换原样传值：`int`↔`int64`、`time.Duration`↔`int64`、具名字符串类型↔`string`、转成接口类型。其余转换（`float64(n)`、`int(f)`、`string(b)`、`[]byte(s)` 等）翻译期拒绝。

翻译期拒绝：

- string 的下标与切片 `s[i]`、`s[a:b]`。
- 完整切片表达式 `xs[a:b:c]`。
- 计算值上的字段访问与赋值：`xs[i].F`、`f().F`、`x.(T).F`（先赋给局部变量）。
- descriptor 结构体的提升字段（嵌入字段本身在 descriptor 里已被拒绝）。
- 移位与位运算 `<<`、`>>`、`&`、`|`、`^`、`&^`（常量运算除外）。
- 表达式里的 `append`；只支持语句 `xs = append(xs, v...)`。
- `new(T)`，当 `T` 是本包声明的结构体。
- `&index` 和复杂取址表达式。
- 泛型类型声明和泛型方法声明。
- 反射结果的静态类型建模。

## 调用

已支持：

- 包函数调用：`demo.Score(user, total)`
- 接收者方法调用：`svc.Run(input)`
- 当前包函数调用：`BuildFailureReason(step, err)`
- 函数值调用：`compensations[i](ctx, reason)`
- 静态函数符号动态调用：`Helper(x)` 在类型信息可用时生成 `call` + `symbol`。
- 链式 receiver 调用：`future.Get(ctx, &out)` 生成嵌套 receiver call。
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
- `len(xs)` 生成 `arr.len(xs)`；map 生成 `map.len`，string 返回字节数
- `append(xs, v...)` 在 `xs = append(xs, v...)` 中生成 `arr.push(xs, v)`；`append(xs, ys...)` 逐个追加
- `delete(dict, k)` 生成 `map.del(dict, k)`
- `cap(ch)` 生成 `ch.cap(ch)`，`cap(xs)` 生成 `arr.cap(xs)`
- `new(T)` 生成 `ptr.new("T")`
- `copy(dst, src)` 生成 `copy(dst, src)`
- `complex`、`real`、`imag` 生成同名内建调用

翻译期拒绝：

- 可变参数展开：`fn(xs...)`（`append(xs, ys...)` 除外）。

## 复合字面量

已支持：

- 省略键的数组和切片字面量：`[]int64{1, 2}`。
- 带键数组元素：`[]int64{2: 99}`。
- 含键值元素的 map 字面量：`map[string]int64{"a": 1}`。
- 包限定结构体字面量：`api.Args{Name: "aaa"}`。
- 当前包结构体字面量：`Args{Name: "aaa"}`。
- 无键结构体字面量：`Args{"aaa"}`，字段名来自 Go 类型信息。
- 省略类型的元素字面量：`[]Point{{X: 1}}`、`map[string]Point{"a": {X: 1}}`，类型来自 Go 类型信息。
- 当前包结构体字面量作为调用参数：`a.Book(ctx, Args{Name: "aaa"})`。
- 使用 `Options.LocalPackageName` 控制当前包结构体字面量的生成类型前缀。

当前范围外：

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
- `go func(arg T){...}(value)`
- `select` 接收 case。
- `select` 发送 case。
- `select` default case。

当前范围外：

- 翻译输出中的定向 channel 类型区分。
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

## 与 Go 的语义一致性

`go2wlang/differential_test.go` 对 `go2wlang/testdata/differential/cases.go` 里的每个函数做差分测试：原生运行 Go 函数，再翻译成 WLang 在解释器里运行，比较 JSON 规范化后的结果（失败只比较是否失败）；标注为拒绝的用例要求翻译期诊断带上写法名称与源码位置。新增翻译能力时同时在该文件里加用例。

运行期语义与 Go 一致的要点：

- 结构体（record）是值：赋值、传参、存入容器时复制。
- 切片共享底层数组：`ys := xs` 与 `xs[a:b]` 之后的元素写入互相可见，`append` 在容量足够时复用底层数组。
- `var xs []T`、`var m map[K]V` 是 nil，JSON 编码为 `null`；写 nil map 是运行时错误。
- map 遍历按 key 升序（Go 不规定顺序，确定的顺序是重放所需）。

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
