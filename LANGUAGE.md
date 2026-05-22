# wflang 语言设计文档

wflang 是一门依附于 Go 运行时的 JSON 宿主调用语言。JSON 承载程序结构，Go 提供解析、类型系统、执行器、可调用函数、可调用类型方法、安全边界和工具链。

本文定义语言目标、当前能力、能力缺口、演进路线和工程标准。本文只描述语言自身：表达式、语句、类型、宿主桥接、运行时、安全、工具链、测试和版本兼容。

---

## 1. 定位

### 1.1 核心定位

wflang 的目标是把 JSON 从配置格式提升为可执行程序格式：

- JSON 负责表达程序 AST。
- Go 负责执行 AST。
- Go 类型系统负责承接语言类型。
- Go 函数和 Go 类型方法负责提供全部可调用能力。
- Go 宿主应用负责注入上下文、权限和外部能力。

语言本体保持小而稳定，本身不能定义任何 function。wflang 只能调用 Go 宿主通过 Registry 注入的包函数和类型方法。

### 1.2 典型使用场景

- 规则表达：布尔条件、比较、聚合、分支判断。
- 策略执行：根据输入上下文计算动作、参数和结果。
- 数据转换：从嵌套对象中取值、拼接、转换、构造输出对象。
- 自动化脚本：按 JSON 程序执行步骤、循环、条件、宿主函数调用。
- 业务 DSL 内核：上层领域语言可编译到 wflang JSON AST。
- 安全脚本引擎：只暴露宿主授权函数，控制运行资源和副作用。

### 1.3 设计原则

1. **JSON 即 AST**：程序结构直接可序列化、可持久化、可传输、可审计。
2. **Go 即宿主**：语言运行依附 Go 对象、Go 函数、Go 错误、Go 上下文。
3. **强类型内核**：类型推断、操作符重载、函数签名校验形成核心安全网。
4. **小核心、大扩展**：核心只包含通用语法；领域能力全部通过 Registry 注入。
5. **确定性优先**：相同输入、相同函数实现、相同版本产生相同结果。
6. **可解释性优先**：每次执行可追踪表达式路径、变量来源、函数调用和错误位置。
7. **安全默认值**：宿主函数按能力授权，运行按预算限制，Go 错误按 `error` 类型映射。
8. **版本化演进**：语言版本、标准库版本、宿主扩展版本独立声明。

---

## 2. 当前已有能力

### 2.1 类型系统

当前已经具备基础强类型框架：

| 能力 | 状态 | 位置 |
|------|------|------|
| 语言类型枚举 | 已具备 | `types/types.go` |
| 运行时值包装 `Value` | 已具备 | `types/types.go` |
| 注册表 `Registry` | 已具备 | `registry/registry.go` |
| 内置类型注册 | 已具备 | `types/types.go` |
| 内置操作符 | 已具备 | `runtime/eval.go` |
| 自定义操作符 / 方法重载 | 已具备 | `registry/registry.go` |
| 包函数注册 | 已具备 | `registry/registry.go` |
| Go 函数反射绑定 | 已具备 | `registry/registry.go` |
| 类型构造与拆包 | 已具备 | `types/types.go` + `registry/registry.go` |
| tuple 解构 | 已具备 | `compiler/parse.go` + `runtime/exec.go` |
| map / struct / chan 字面量 | 已具备 | `compiler/parse.go` + `runtime/literals.go` |

内置类型也是映射类型：左侧是 wflang 中使用的类型名，右侧是对应的 Go 类型。语言运行时只认识类型名；实际值、构造、方法调用和错误承载都由映射的 Go 类型完成。

| 语言类型 | Go 承载 | 用途 |
|----------|---------|------|
| `uint8` | `uint8` | 8 位无符号整数 |
| `uint16` | `uint16` | 16 位无符号整数 |
| `uint32` | `uint32` | 32 位无符号整数 |
| `uint64` | `uint64` | 64 位无符号整数 |
| `int8` | `int8` | 8 位有符号整数 |
| `int16` | `int16` | 16 位有符号整数 |
| `int32` | `int32` | 32 位有符号整数 |
| `int64` | `int64` | 64 位有符号整数 |
| `float32` | `float32` | 32 位浮点数 |
| `float64` | `float64` | 64 位浮点数 |
| `boolean` | `bool` | 布尔值 |
| `string` | `string` | 字符串 |
| `bigInt` | `*big.Int` / wrapper | 任意精度整数 |
| `bigDecimal` | `*big.Rat` / wrapper | 任意精度小数 |
| `array<T>` | `[]T` / `[]any` | 泛型数组 |
| `any` | `any` | 宿主边界值 |
| `null` | `nil` | 空值 |
| `error` | `error` | Go `error` 接口映射 |

语言内置集合只采用 Go 语义稳定、跨平台宽度明确的类型。`int` 和 `uint` 由宿主 Go 平台决定宽度，语言层采用固定宽度整数类型表达数值语义。

业务类型遵循同一模型：宿主把 Go 类型注册为 wflang 类型名，例如 `user -> *User`、`money -> Money`、`address -> common.Address`。`bigInt` 和 `bigDecimal` 只是预置的宿主映射类型。

Go 函数返回未显式注册的类型时，wflang 必须保留该值并允许后续代码继续传递、调用和输入。运行时为这类类型生成稳定的宿主类型名，格式基于 Go 的完整类型身份：`pkgPath.TypeName`，指针类型保留指针标记。

| Go 返回类型 | 自动类型名 |
|-------------|------------|
| `github.com/acme/books.Book` | `github.com/acme/books.Book` |
| `*github.com/acme/books.Book` | `*github.com/acme/books.Book` |
| `github.com/ethereum/go-ethereum/common.Address` | `github.com/ethereum/go-ethereum/common.Address` |

自动类型名用于精确判断后续方法兼容性、参数兼容性和重载分派。

当前操作符覆盖：

- 数字：`+`、`-`、`*`、`/`、`>`、`>=`、`<`、`<=`、`==`、`!=`
- 字符串：`+`、`==`、`!=`、`contains`、`endsWith`
- 布尔：`==`、`!=`
- 大整数、大精度小数：算术与比较操作

### 2.2 表达式求值

当前已经具备 JSONLogic 风格表达式求值：

```json
{
  "+": [
    { "literal": { "type": "int64", "value": "1" } },
    { "literal": { "type": "int64", "value": "2" } }
  ]
}
```

```json
{
  "==": [
    { "var": "user.status" },
    { "literal": { "type": "string", "value": "active" } }
  ]
}
```

当前表达式能力：

| 表达式 | 状态 | 示例 |
|--------|------|------|
| 变量读取 | 已具备 | `{ "var": "user.name" }` |
| 缺省值 | 已具备 | `{ "var": ["user.name", {"literal": {"type": "string", "value": "anonymous"}}] }` |
| 静态 Go 符号值 | 已具备 | `{ "symbol": "activities.Reserve" }` |
| receiver-bound 方法值 | 已具备 | `{ "method": [{"var": "worker"}, "Reserve"] }` |
| Go 输出参数 | 已具备 | `{ "out": "reserve" }` |
| Go typed zero | 已具备 | `{ "zero": "orders.ReserveResult" }` |
| typed literal | 已具备 | `{ "literal": {"type": "int64", "value": "1"} }` |
| typed literal | 已具备 | `{ "literal": {"type": "bigInt", "value": "100"} }` |
| 逻辑与 | 已具备 | `{ "and": [expr1, expr2] }` |
| 逻辑或 | 已具备 | `{ "or": [expr1, expr2] }` |
| 逻辑取反 | 已具备 | `{ "!": [expr] }` |
| 条件分支 | 已具备 | `{ "if": { "cond": expr, "then": [...], "else": [...] } }` |
| 变量定义 | 已具备 | `{ "let": {"x": {"literal": {"type": "int64", "value": "1"}}} }` |
| 变量修改 | 已具备 | `{ "set": {"x": {"literal": {"type": "int64", "value": "2"}}} }` |
| 操作符调用 | 已具备 | `{ "+": [{"literal": {"type": "int64", "value": "1"}}, {"literal": {"type": "int64", "value": "2"}}] }` |
| Go 包函数调用 | 已具备 | `{ "Len": [{"pkg": "str"}, {"literal": {"type": "string", "value": "hello"}}] }` |

### 2.3 作用域

当前已经具备作用域栈：

- `PushScope()` 创建新作用域。
- `PopScope()` 退出当前作用域。
- `LetVar()` 在当前作用域定义变量。
- `SetVar()` 从当前作用域向外层查找并修改变量。
- `LookupPath()` 支持点分路径、数组下标、Go struct 导出字段。

示例：

```json
{ "var": "order.items.0.price" }
```

Go struct 字段可通过 `json` 或 `yaml` tag 暴露给语言读取。

顶级运行上下文由宿主注入：

- `Vars` 注入顶级变量，可通过 `{ "var": "name" }` 和 `{ "var": "name.path" }` 读取。
- `Packages` 注入包 receiver，可通过 `{ "pkg": "books" }` 读取。
- 顶级变量默认只读，宿主可显式声明可写变量。
- 包 receiver 只用于分派 Go 包级函数。

### 2.4 程序执行器

当前已经具备语句数组执行器：

```json
[
  { "let": { "sum": { "literal": { "type": "bigDecimal", "value": "0" } } } },
  {
    "foreach": {
      "target": { "var": "items" },
      "as": "item",
      "do": [
        { "set": { "sum": { "+": [{ "var": "sum" }, { "var": "item.price" }] } } }
      ]
    }
  },
  { "return": { "var": "sum" } }
]
```

当前语句能力：

| 语句 | 状态 | 说明 |
|------|------|------|
| `let` | 已具备 | 定义局部变量 |
| `set` | 已具备 | 修改已有变量 |
| `if` | 已具备 | 块级条件分支 |
| `foreach` | 已具备 | 遍历数组 |
| `fori` | 已具备 | 按整数下标循环 |
| `return` | 已具备 | 返回程序结果 |
| `break` | 已具备 | 退出当前循环 |
| `continue` | 已具备 | 跳过当前循环剩余语句 |
| `panic` | 已具备 | 映射 Go `panic` 关键字 |
| `routine` | 已具备 | 映射 Go `go` 关键字 |
| `await` | 已具备 | 等待 `routineHandle` |
| `expr` | 已具备 | 执行表达式并丢弃结果 |
| `defer` | 已具备 | 作用域退出时 LIFO 执行调用 |
| `match` | 已具备 | 多分支值匹配 |
| `select` | 已具备 | channel send / recv 多路选择 |

### 2.5 Go 宿主桥接

宿主桥接采用 receiver 分派模型。包级函数调用使用 `pkg` intrinsic，类型方法调用使用 typed value receiver。`pkg`、`var`、`symbol`、`method`、`out` 和 `zero` 是特殊引用表达式：`var` 读取运行时 scope 变量，`pkg` 读取 Go 宿主包 receiver，`symbol` 读取注册表里的静态 Go 符号，`method` 生成绑定 receiver 的方法值，`out` 表示 Go `&name` 输出参数，`zero` 生成注册类型的 Go 零值。

Go 包级函数：

```go
package books

func FindByID(id int64) (*Book, error) { ... }
```

宿主注册：

```go
registry.BindGoPackage("books", booksPackage)
```

语言调用：

```json
{
  "FindByID": [
    { "pkg": "books" },
    { "literal": { "type": "int64", "value": "1001" } }
  ]
}
```

包级函数和类型方法共用同一个分派模型：operator 是方法名或函数名，第一个参数是 receiver。`{ "pkg": "books" }` 产生包 receiver，`{ "var": "user" }` 产生对象 receiver。

分派规则：

1. 求值第一个参数。
2. 如果第一个参数是 `{ "pkg": "books" }`，在 `books` 包函数表中查找 `FindByID`。
3. 如果第一个参数是 `{ "var": "user" }`，按 `user` 的语言类型查找类型方法。
4. 剩余参数按 typed literal 和变量结果进行类型检查。

包注册规则：

- `BindGoPackage("books", booksPackage)` 注册一个包 receiver。
- `SessionOptions.Packages` 可为单次运行注入顶级包 receiver。
- 包名全局唯一，重复注册同一个包名返回诊断错误。
- 函数名与 Go 包级函数名完全一致，语言层不做大小写转换、别名转换或驼峰转换。
- 只有 Go 反射能访问到的导出函数可以注册，小写开头的包级函数不可调用。
- `booksPackage` 是宿主提供的包描述对象，包含该包可暴露的 Go 包级函数集合。
- 包函数与类型方法在同一个 operator 分派模型下运行；第一个参数的 receiver 决定查包函数表或类型方法表。

静态符号注册：

```go
registry.BindSymbol("activities.Reserve", activities.Reserve)
```

静态符号值：

```json
{ "symbol": "activities.Reserve" }
```

`symbol` 可作为普通参数传给宿主函数，也可通过 `call` 动态调用：

```json
{
  "call": {
    "fn": { "symbol": "activities.Reserve" },
    "args": [{ "var": "ctx" }, { "var": "input" }]
  }
}
```

receiver-bound 方法值：

```json
{
  "method": [{ "var": "worker" }, "Compensate"]
}
```

链式调用用嵌套 receiver call 表达。`a.B(x).C(y)` 对应：

```json
{
  "C": [
    { "B": [{ "var": "a" }, { "var": "x" }] },
    { "var": "y" }
  ]
}
```

Go 输出参数使用 `out`，当前支持 `&ident` 形态：

```json
[
  { "let": { "reserve": { "zero": "orders.ReserveResult" } } },
  {
    "let": {
      "err": {
        "Get": [
          { "ExecuteActivity": [{ "pkg": "workflow" }, { "var": "ctx" }, { "symbol": "activities.Reserve" }] },
          { "var": "ctx" },
          { "out": "reserve" }
        ]
      }
    }
  }
]
```

`out` 在调用参数求值时读取当前变量，按目标 Go 参数类型创建临时 pointer。宿主调用返回后，临时 pointer 的值写回原变量。写回发生在 Go 返回值映射之前，因此 `Get(ctx, &reserve)` 返回 error 时，`reserve` 已经携带宿主写入的值。

typed zero 使用注册表类型信息：

```json
{ "zero": "orders.ReserveResult" }
```

`zero` 会生成 `orders.ReserveResult{}` 的 Go carrier，并保留语言类型名 `orders.ReserveResult`。

静态符号错误模型：

- 未绑定 `symbol` 返回 `E_SYMBOL`。
- `method` receiver 为 null 返回 `E_NIL_RECEIVER`。
- `method` 名称缺失返回 `E_SYMBOL`。
- `out` 用在调用参数之外返回 `E_AST_SHAPE`。
- `out` 变量缺失返回 `E_SYMBOL`。
- `out` 写回只读变量返回 `E_READONLY_VAR`。
- `out` 与 Go 参数目标类型冲突返回 `E_TYPE`。
- `zero` 类型未注册返回 `E_SYMBOL`。

当前签名约定：

- 参数类型通过反射映射到语言类型。
- 返回类型可自动推断，也可显式指定。
- 可变参数可映射为语言函数可变参数。
- Go 返回的 `error` 映射为语言内置 `error` 类型，语义与 Go `error` 接口一致。
- `error` 类型只自动暴露 Go 方法集；Go `error` 接口只有 `Error() string`，语言中只能调用 `Error` 映射出来的 operator。
- `func(...)` 返回 `null`。
- `func(...) error` 返回 `error` typed value，nil error 的 Go 承载值为 `nil`。
- `func(...) T` 返回 `T`。
- `func(...) (T, error)` 返回 `tuple<T,error>`。
- `func(...) (T1, T2, ..., Tn)` 返回 `tuple<T1,T2,...,Tn>`。
- `func(...) (T1, T2, ..., Tn, error)` 返回 `tuple<T1,T2,...,Tn,error>`。
- Go panic、context cancel、预算耗尽和语言诊断错误以 `LangError` 形式中断执行。

目标桥接模型以 Go 类型自动绑定为主：宿主注册 Go 类型后，Registry 通过反射扫描该类型的可导出方法，自动生成语言方法表。

```go
type User struct {
    Name string
}

func (u *User) Run(speed float64) (string, error) {
    return fmt.Sprintf("%s runs at %.1f", u.Name, speed), nil
}

registry.AutoBindType("user", reflect.TypeFor[*User]())
```

语言调用保持 JSONLogic 形态：operator 是方法名，第一个参数是 receiver。

```json
{
  "Run": [
    { "var": "user" },
    { "literal": { "type": "float64", "value": "10" } }
  ]
}
```

自动绑定规则：

- 扫描 Go 类型的导出方法。
- 接收者成为语言方法的 receiver。
- 方法参数映射为语言参数。
- 方法返回值映射为语言返回类型。
- 方法签名采用 `(T, error)`、`(context.Context, ..., T) (R, error)`、`(wflang.Env, ..., T) (R, error)` 等宿主签名。
- 方法返回的 `error` 使用内置 `error` 类型承载，方法集来自 Go `error` 接口。
- 方法名与 Go 导出方法名完全一致。
- 显式 overload 映射可以把多个 Go 方法绑定到同一个语言 operator。
- 语言调用统一使用 JSONLogic operator 形态：`{ "GoMethodName": [receiver, ...args] }`。
- 运行时只根据第一个参数的语言类型选择 Go 方法。
- 自动生成的方法进入类型方法表，静态校验和运行时分派共用同一份元数据。

Go 方法集合按名称唯一，语言层重载通过 Registry 的 overload table 实现。同一个语言 operator 可以对应多个 Go 方法，每个 Go 方法有不同的 Go 名称和不同的参数签名。

```go
type Counter struct{}

func (Counter) AddInt8(v int8) (int8, error)       { return v + 1, nil }
func (Counter) AddInt64(v int64) (int64, error)   { return v + 1, nil }
func (Counter) AddFloat64(v float64) (float64, error) {
    return v + 1, nil
}

registry.BindMethodOverloads("counter", "Add", []types.GoMethodOverload{
    {GoMethod: "AddInt8"},
    {GoMethod: "AddInt64"},
    {GoMethod: "AddFloat64"},
})
```

语言调用始终保持一个 operator：

```json
{ "Add": [{ "var": "counter" }, { "literal": { "type": "int64", "value": "1" } }] }
```

重载分派规则：

1. 取 operator 名称，如 `Add`。
2. 求值第一个参数，得到 receiver 类型，如 `counter`。
3. 从 overload table 中取出 `(operator=Add, receiver=counter)` 的候选方法。
4. 按剩余参数类型过滤候选方法。
5. 按匹配优先级选择最具体的方法。
6. 多个候选分数相同则返回 `E_AMBIGUOUS_OVERLOAD`。
7. 找到唯一候选后调用对应 Go 方法。

推荐匹配优先级：

| 优先级 | 匹配类型 | 示例 |
|--------|----------|------|
| 100 | 精确语言类型匹配 | `int64` → `int64` |
| 80 | 安全数值提升 | `int8` → `int64` |
| 60 | 精度型提升 | `int64` → `bigInt` |
| 40 | 显式可构造转换 | `string` → `bigDecimal` |
| 10 | `any` 兜底 | 任意值 → `any` |

### 2.5.1 字面量类型规则

语言层所有常量值必须使用 typed literal。裸 JSON primitive 仅作为承载格式语法元素；语言常量入口统一为 typed literal。

语法位置里的字符串属于 AST 元数据，如 operator 名称、`var` 路径、字段名、`type` 名称、`as` 变量名；这些字符串按语法解析。程序数据值统一通过 typed literal 表达。

非法示例：

```json
{ "Add": [{ "var": "counter" }, 1] }
```

合法示例：

```json
{ "Add": [{ "var": "counter" }, { "literal": { "type": "int64", "value": "1" } }] }
```

统一 typed literal 规则：

- 字符串必须写成 `{ "literal": { "type": "string", "value": "hello" } }`。
- 布尔必须写成 `{ "literal": { "type": "boolean", "value": "true" } }`。
- 整数必须写成 `{ "literal": { "type": "int64", "value": "1" } }` 或宿主注册的整数类型。
- 无符号整数必须写成 `{ "literal": { "type": "uint64", "value": "1" } }`。
- 浮点数必须写成 `{ "literal": { "type": "float64", "value": "1.0" } }`。
- 大整数必须写成 `{ "literal": { "type": "bigInt", "value": "100000000000000000000" } }`。
- 高精度小数必须写成 `{ "literal": { "type": "bigDecimal", "value": "1.000" } }`。
- 空值必须写成 `{ "literal": { "type": "null", "value": null } }`。

```json
{ "literal": { "type": "uint64", "value": "1" } }
```

```json
{ "literal": { "type": "float64", "value": "1.0" } }
```

```json
{ "literal": { "type": "bigDecimal", "value": "1.000" } }
```

typed literal 是语言唯一常量入口：

- 解析阶段直接得到明确语言类型。
- 类型检查阶段无需猜测默认类型。
- 重载分派阶段只做明确签名匹配。
- 配置审计时能直接看出业务语义。
- Go 宿主构造失败能在编译期或加载期暴露。

重载歧义示例：

```go
func (Counter) AddInt64(v int64) (int64, error) { ... }
func (Counter) AddFloat(v float64) (float64, error) { ... }
```

```json
{ "Add": [{ "var": "counter" }, { "literal": { "type": "int64", "value": "1" } }] }
```

typed literal 指定 `int64` 后只匹配 `int64` 或明确允许从 `int64` 转换的候选方法。存在多个同分候选时，返回 `E_AMBIGUOUS_OVERLOAD`。

### 2.6 静态校验

当前已经具备表达式校验器：

- 推断字面量类型。
- 校验操作符是否存在。
- 校验函数参数数量。
- 校验函数参数类型。
- 返回诊断 `LangError`。
- 支持 `TypeContext` 传入变量类型上下文。

当前错误码：

| 错误码 | 含义 |
|--------|------|
| `E1001` | 函数或操作符缺失 |
| `E1002` | 类型匹配失败 |
| `E1003` | 变量缺失 |
| `E1004` | 参数数量匹配失败 |
| `E1005` | AST 结构异常 |
| `E1006` | 运行时异常 |
| `E1007` | literal 类型尚未支持 |

### 2.7 已有测试覆盖

当前测试覆盖了：

- 类型构造、推断、JSON 序列化。
- 内置数值、字符串、布尔、大整数、大精度小数操作。
- Go 函数绑定。
- 表达式求值。
- 程序语句执行。
- 作用域变量。
- 自定义操作符。
- 部分错误路径。

---

## 3. 语言核心规格

### 3.1 程序形态

推荐最终程序形态采用版本化 envelope：

```json
{
  "lang": "wflang/v1",
  "imports": [],
  "program": [
    { "let": { "x": { "literal": { "type": "int64", "value": "1" } } } },
    { "return": { "+": [{ "var": "x" }, { "literal": { "type": "int64", "value": "2" } }] } }
  ]
}
```

最小程序形态保留为语句数组：

```json
[
  { "return": { "literal": { "type": "int64", "value": "1" } } }
]
```

同时支持渐进式输入：宿主可以每次传入一段 JSON 语句或表达式，wflang 在同一个执行 session 中增量编译并顺序执行。

```json
{ "let": { "x": { "literal": { "type": "int64", "value": "1" } } } }
```

下一次输入：

```json
{ "set": { "x": { "+": [{ "var": "x" }, { "literal": { "type": "int64", "value": "2" } }] } } }
```

下一次输入：

```json
{ "return": { "var": "x" } }
```

渐进式执行规则：

- 每次输入可以是一条语句、一组语句或一个 envelope。
- 同一个 session 共享变量作用域、类型上下文、包注册表、类型注册表和能力配置。
- 每段 JSON 进入同一套 Decode → Parse AST → Resolve → Type check → CallPlan 流程。
- 新片段只能追加到当前执行序列尾部。
- 已执行片段的语义保持稳定。
- `return` 后 session 进入 completed 状态，继续追加返回诊断错误。
- `routine` 后台执行返回 `routineHandle`，后续片段可通过 `await` 等待并读取结果。
- 渐进式执行不要求宿主一次性构造完整 JSON 程序。

渐进式作用域规则：

- session 创建时由 `SessionOptions.Vars` 写入**根作用域**。
- 所有 append 进来的顶层语句都直接执行在同一个根作用域（不会为每个片段单独开 block）。
- 片段中的 `let` 在根作用域中创建新变量，可被后续片段 `var`/`set` 引用和修改。
- 片段中的 `set` 按词法作用域规则向外查找最近的同名可写变量；命中只读顶级 `Vars` 返回 `E_READONLY_VAR`。
- 片段中 `let` 与既有顶级 `Vars` 或之前 `let` 变量同名时，按词法 shadowing 规则创建新变量；推荐宿主在此场景发出 lint 警告但不强制报错。
- `if`/`foreach`/`fori`/`block`/`routine.do` 等控制结构在各自片段内部创建嵌套 block，嵌套 block 内的 `let` 变量在该块结束时被销毁，不写入根作用域。
- `imports` 和 `lang` 仅在 session 创建时或首个 envelope 片段中生效；后续片段再次声明 `lang` 且与 session 版本不一致时返回 `E_LANG_VERSION_CONFLICT`；后续片段的 `imports` 与 session 已注册集合取并集，重复项忽略，冲突项报错。

版本化 envelope 适合长期演进：

- `lang` 声明语言版本。
- `imports` 声明标准库和宿主扩展。
- `program` 承载语句数组。
- `metadata` 可承载作者、说明、校验指纹、生成器信息。

### 3.2 表达式形态

表达式分为 4 类：

1. **typed literal**：字符串、数字、布尔、数组、空值都必须携带类型。
2. **intrinsic 表达式**：语言内建特殊语义，如 `var`、`if`、`let`。
3. **操作符表达式**：`+`、`==`、`contains` 等注册操作符。
4. **宿主调用表达式**：Go 宿主注册的包函数和类型方法。

宿主调用表达式格式：

```json
{ "Operator": [receiver, arg1, arg2] }
```

包函数调用示例：

```json
{ "Len": [{ "pkg": "str" }, { "literal": { "type": "string", "value": "hello" } }] }
```

建议最终规格采用严格表达式对象规则：

- 单键对象表达一个操作。
- 多键对象只用于语句 payload 或宿主规定的 AST 结构。
- 数组常量使用 `array<T>` typed literal，元素自身仍然是 typed value。

推荐数组常量：

```json
{
  "literal": {
    "type": "array<int64>",
    "value": [
      { "literal": { "type": "int64", "value": "1" } },
      { "literal": { "type": "int64", "value": "2" } },
      { "literal": { "type": "int64", "value": "3" } }
    ]
  }
}
```

宿主结构体或领域对象由 Go 包函数构造并返回 typed value。

### 3.3 语句形态

语句是会改变程序控制流或作用域的顶层节点。

推荐稳定语句集：

| 语句 | 目标状态 | 说明 |
|------|----------|------|
| `let` | 核心语句 | 定义变量 |
| `set` | 核心语句 | 修改变量 |
| `if` | 核心语句 | 条件分支 |
| `foreach` | 核心语句 | 遍历数组或 map |
| `fori` | 核心语句 | 按整数下标循环 |
| `return` | 核心语句 | 返回结果 |
| `break` | 核心语句 | 退出最近一层循环 |
| `continue` | 核心语句 | 进入最近一层循环的下一次迭代 |
| `panic` | 核心语句 | 触发 Go `panic(value)` |
| `routine` | 核心语句 | 通过 Go `go` 关键字启动宿主调用或语句块 |
| `await` | 核心语句 | 等待 routine handle |
| `expr` | 核心语句 | 显式执行表达式并丢弃结果 |
| `defer` | 核心语句 | 作用域退出时执行宿主调用或函数值调用 |
| `match` | 核心语句 | 多分支值匹配 |
| `select` | 核心语句 | channel 多路选择 |

#### `return`

`return` 立即结束当前程序并返回一个 typed value。`return` 可以出现在顶层、`if` 分支、`foreach`、`fori` 或 `block` 内部。

```json
{ "return": { "var": "total" } }
```

命名返回读取当前作用域中的变量，并在 defer 执行后返回该变量的最终值：

```json
{ "return": { "named": "err" } }
```

#### `foreach`

`foreach` 遍历数组。`as` 绑定元素变量，`index` 可选，绑定当前下标变量。

```json
{
  "foreach": {
    "target": { "var": "items" },
    "as":    "item",
    "index": "i",
    "do": [
      { "set": { "sum": { "+": [{ "var": "sum" }, { "var": "item.price" }] } } }
    ]
  }
}
```

语义规则：

- `target` 求值结果必须是 `array<T>` 或 Go slice / array。
- `as` 是当前元素变量名，每轮迭代写入当前 block 作用域，类型为 `T`。
- `index` 可选，是当前下标变量名，类型为 `int64`，从 0 开始。
- `as` 与 `index` 位于同一个迭代 block，不允许同名。
- 未提供 `index` 时只写入 `as` 变量。
- 省略 `index` 时，语义与不需要下标的场景一致。
- 每轮迭代创建新的 block 作用域，`as` 和 `index` 是该 block 的局部变量。
- `break` 退出最近一层 `foreach`，`continue` 跳到下一轮。

#### `fori`

`fori` 表达固定整数范围循环，所有边界值必须是 typed literal 或表达式结果。

```json
{
  "fori": {
    "var": "i",
    "from": { "literal": { "type": "int64", "value": "0" } },
    "to": { "var": "count" },
    "step": { "literal": { "type": "int64", "value": "1" } },
    "do": [
      { "expr": { "visit": [{ "var": "i" }] } }
    ]
  }
}
```

语义规则：

- `var` 是循环变量名，写入每轮循环作用域。
- `from` 是起始值，包含该值。
- `to` 是结束值，默认排除该值，即 `[from, to)`。
- `step` 默认由宿主配置决定，建议要求显式提供。
- `from`、`to`、`step` 的类型必须一致，推荐 `int64`。
- `step` 为 0 返回诊断错误。
- 循环每轮按顺序执行 `do` 语句。

#### `break` 与 `continue`

`break` 和 `continue` 只在 `foreach` 或 `fori` 内有效。

```json
{ "break": {} }
```

```json
{ "continue": {} }
```

语义规则：

- `break` 退出最近一层循环。
- `continue` 跳过当前循环剩余语句，进入下一次迭代。
- 在循环外使用返回 `E_INVALID_CONTROL_FLOW`。
- 嵌套循环中只影响最近一层循环。
- `return` 优先级高于 `break` 和 `continue`，会直接结束整个程序。

#### `panic`

`panic` 是内建控制操作符，映射 Go `panic` 关键字。它先求值参数，再把参数转换为 Go 值并执行 `panic(value)`。

```json
{
  "panic": {
    "literal": { "type": "string", "value": "invalid state" }
  }
}
```

语义规则：

- `panic` 接收一个 typed value。
- `panic` 的参数类型可以是任意 wflang 类型。
- 执行器边界通过 `recover` 捕获 panic，并转换为 `E_PANIC` 诊断错误。
- `EngineOptions.PropagatePanic` 开启时，执行器把 panic 继续抛给 Go 宿主。
- `panic` 触发后当前程序结束。

#### `expr`

`expr` 把一个表达式作为顶层语句执行，并丢弃其结果。用于只需要副作用、不关心返回值的宿主调用。

```json
{ "expr": { "Publish": [{ "pkg": "events" }, { "var": "input.event" }] } }
```

语义规则：

- `expr` 接受任意表达式。
- 表达式按正常求值流程执行。
- 求值产生的 typed value 被丢弃，不写入任何变量。
- host call 返回的 Go `error` 按第 9 节进入返回值形态；语言诊断错误继续中断执行。
- `expr` 不影响作用域，也不创建 block。

#### `routine`

`routine` 映射 Go `go` 关键字，把宿主调用或 wflang 语句块放入新的 goroutine 执行，并立即返回 `routineHandle`。

```json
{
  "routine": {
    "Publish": [
      { "pkg": "events" },
      { "var": "input.event" }
    ]
  }
}
```

语义规则：

- 单调用形态：`{"routine":{"GoName":[receiver,...args]}}`。
- 语句块形态：`{"routine":{"do":[stmt1,stmt2,...]}}`。
- 单调用形态先在当前 goroutine 中求值 receiver 和参数，启动后执行捕获到的调用快照。
- 语句块形态使用 child Executor 和独立 root scope 执行 body。
- `routine` 自身立即返回 `routineHandle`。
- goroutine 内的最终 typed value 写入 handle。
- goroutine 内的 `LangError` 写入 handle；fire-and-forget 使用中也进入宿主 `RoutineErrorHandler`。
- `routine` 需要 `routine:spawn` capability。
- `routine` 受 `MaxRoutines` 预算限制。

#### `await`

`await` 等待一个或多个 `routineHandle` 完成。

```json
[
  { "let": { "h": { "routine": { "Fetch": [ { "pkg": "books" }, { "var": "id" } ] } } } },
  { "return": { "await": { "var": "h" } } }
]
```

语义规则：

- `await` 单个 handle 时，返回该 routine 的业务返回值。
- `await` 多个 handle 时，按输入顺序返回 `array<any>`。
- 无返回值时返回 `null`。
- 仅 `error` 返回值时返回 `error` typed value，nil error 的 Go 承载值为 `nil`。
- 单业务返回值返回单个 typed value。
- 多返回值返回 `tuple<T1,T2,...>`，包含最后一位 `error` slot。
- routine 内部产生的 `LangError` 由 `await` 返回给宿主。
- 同一个 handle 可重复 `await`，结果或错误会缓存并复用。

#### `defer`

`defer` 记录一个宿主调用或函数值调用，在当前作用域退出时按 LIFO 顺序执行。receiver、函数值和参数在 `defer` 语句执行时求值并捕获。

```json
{ "defer": { "Close": [{ "pkg": "audit" }, { "var": "id" }] } }
```

```json
{
  "defer": {
    "call": {
      "fn": {
        "fn": {
          "params": [],
          "returns": [],
          "do": [
            { "set": { "err": { "literal": { "type": "string", "value": "deferred" } } } }
          ]
        }
      },
      "args": []
    }
  }
}
```

语义规则：

- `defer` 接受单个宿主调用或单个函数值调用。
- 顶层 program 也是隐式作用域，顶层 defer 在 program 结束时执行。
- `foreach`、`fori`、`if`、`select`、`routine.do` 的块作用域都会运行各自 defer。
- deferred 调用返回的 Go `error` 仍按 host call 返回形态产生 typed value；调用自身产生的 `LangError` 会在原执行路径无错误时作为该作用域错误返回。

#### 函数值

`fn` 创建一等函数值，`symbol` 和 `method` 可产生 Go 函数 carrier，`call` 调用函数值。函数字面量按引用捕获当前 lexical scope，因此函数体内的 `set` 会修改被捕获变量。

```json
{
  "fn": {
    "params": [["ctx", "workflow.Context"], ["reason", "FailureReason"]],
    "returns": ["error"],
    "do": [
      { "return": { "MarkFailed": [{ "pkg": "workflow" }, { "var": "ctx" }, { "var": "reason" }] } }
    ]
  }
}
```

```json
{
  "call": {
    "fn": { "var": "comp" },
    "args": [{ "var": "ctx" }, { "var": "reason" }]
  }
}
```

语义规则：

- 函数类型写作 `func<(T1,T2)->R>`；多返回使用 `func<(T1)->R1,R2>`。
- 参数在调用时创建新的函数调用作用域。
- 函数体内的 `return` 结束当前函数调用。
- 返回值类型必须匹配 `returns`；`returns` 为空时调用结果为 `null`。
- `error` 返回允许 `null` 表示 nil error。
- `call.fn` 可接收 wlang `fn` closure、`symbol` 解析得到的 Go 函数值、`method` 解析得到的绑定方法值，以及宿主注入变量中的 Go 函数值。
- Go 函数 carrier 调用复用宿主调用的参数 coercion、`out` 写回、返回值映射和 panic 保护。

### 3.4 变量模型

当前变量模型是作用域栈：

- `let` 在当前 block 创建变量。
- `set` 修改最近的同名变量。
- `foreach` 每次迭代创建新 block。
- 宿主调用结果通过 `let`、`set`、`return` 或 `expr` 承接。
- 变量路径读取只读视图，复杂路径操作通过宿主包函数完成。

单 binding：

```json
{
  "let": {
    "price": { "var": "item.price" },
    "count": { "var": ["item.count", { "literal": { "type": "int64", "value": "1" } }] }
  }
}
```

变量声明类型：

```json
{
  "let": {
    "total": {
      "type": "bigDecimal",
      "value": { "literal": { "type": "bigDecimal", "value": "0" } }
    }
  }
}
```

tuple 解构：

```json
{
  "let": [
    ["value", "err"],
    ["string", "error"],
    { "Fetch": [{ "pkg": "books" }, { "var": "id" }] }
  ]
}
```

解构规则：

- 右侧表达式必须求值为 `tuple<T1,...,Tn>`。
- 目标数量必须与 tuple arity 一致。
- 目标名 `_` 会丢弃对应位置。
- 可选类型数组用于校验对应位置的类型。

### 3.5 顶级运行上下文

顶级运行上下文是宿主注入到程序根作用域的环境。程序片段、渐进式执行 session 和一次性 `Run` 都共享同一套上下文模型。

```go
session := engine.NewSession(wflang.SessionOptions{
    Vars: map[string]any{
        "input":   input,
        "user":    user,
        "request": requestEnv,
    },
    VarOptions: map[string]wflang.VarOptions{
        "input":   {Writable: false},
        "user":    {Writable: false},
        "request": {Writable: false},
    },
    Packages: map[string]wflang.PackageSpec{
        "books": booksPackage,
        "risk":  riskPackage,
    },
})
```

读取顶级变量：

```json
{ "var": "input.amount" }
```

调用注入包函数：

```json
{
  "Score": [
    { "pkg": "risk" },
    { "var": "input.amount" },
    { "var": "input.country" }
  ]
}
```

上下文规则：

- `Vars` 写入根作用域，进入变量符号表和类型推断。
- `Packages` 写入包 receiver 表，进入 `pkg` 解析和函数签名解析。
- `Vars` 与 `Packages` 位于两个命名空间，访问语法分别为 `var` 和 `pkg`。
- 同一个命名空间内名称唯一，重复注入返回诊断错误。
- 顶级注入变量默认只读，宿主可通过 `VarOptions` 标记可写。
- `let` 在当前 block 创建局部变量，局部变量可遮蔽顶级变量。
- `set` 修改最近的可写变量；命中只读顶级变量时返回 `E_READONLY_VAR`。
- 渐进式执行中，后续片段继承同一个顶级上下文和已注册包 receiver。

### 3.6 数组索引

数组访问采用 JSONLogic 风格的 `var` 路径语法。路径段中的数字表示数组下标。

读取数组元素：

```json
{ "var": "items.0" }
```

读取数组元素的字段：

```json
{ "var": "order.items.0.price" }
```

带缺省值的数组读取：

```json
{
  "var": [
    "items.0",
    { "literal": { "type": "null", "value": null } }
  ]
}
```

语义规则：

- `var` 路径字符串是 AST 元数据，路径里的 `0`、`1`、`2` 按数组下标解析。
- 数组索引从 0 开始。
- 下标必须是非负十进制整数路径段。
- `array<T>` 的索引读取结果类型是 `T`。
- 越界、负数下标、非整数路径段命中数组时返回诊断错误或触发 `var` 缺省值。
- 路径读取 Go slice、Go array 和 wflang `array<T>` 使用同一套语义。

路径段歧义与 Go 字段匹配规则：

- 路径按 `.` 切分为段，段内不再转义；因此 `var` 路径无法表达 "字段名本身包含 `.`" 的 map key。
- 命中 Go `struct` 时，每段按字段名或 `json` / `yaml` tag 的首个名字匹配导出字段；匹配大小写敏感。
- 命中 Go `map` 时，每段按字符串 key 匹配：全数字段仍然作为字符串 key 使用，例如 `{ "var": "stats.2024" }` 读取 `map["2024"]`；只有段前驱类型是 slice / array / wflang `array<T>` 时，整数段才被当下标解析。
- 命中 Go `slice` / `array` / wflang `array<T>` 时，段必须是非负十进制整数，否则触发 `var` 缺省值或返回诊断错误。
- 无法用 `var` 表达的 map key（包含 `.`、空字符串、二进制数据等）必须通过宿主 `path.Get` 函数读取；`path.Get` 接受显式的路径段数组。

数组内建操作：

| 操作 | JSON | 结果 |
|------|------|------|
| 追加 | `{ "arr.push": [ { "var": "xs" }, value ] }` | 原地追加，返回 `null` |
| 读取 | `{ "arr.get": [ xs, index ] }` | 返回元素 `T` |
| 长度 | `{ "arr.len": [ xs ] }` | 返回 `int64` |

`arr.push` 的第一个参数必须是数组变量，追加后更新该变量。元素类型必须匹配 `array<T>` 的 `T`，数组长度受 `Budget.MaxArrayLength` 约束。

---

## 4. 类型系统目标

### 4.1 类型映射模型

wflang 类型系统本质是类型名到 Go 类型的映射表。所有类型都按同一套机制注册、构造、推断、绑定方法和参与重载分派。

```text
wflang type name  ->  Go reflect.Type / constructor / method set
```

内置类型是预注册映射，业务类型是宿主注入映射。

### 4.2 内置类型映射

| 类型 | Go 类型 | 用途 |
|------|---------|------|
| `uint8` | `uint8` | 固定宽度无符号整数 |
| `uint16` | `uint16` | 固定宽度无符号整数 |
| `uint32` | `uint32` | 固定宽度无符号整数 |
| `uint64` | `uint64` | 固定宽度无符号整数 |
| `int8` | `int8` | 固定宽度有符号整数 |
| `int16` | `int16` | 固定宽度有符号整数 |
| `int32` | `int32` | 固定宽度有符号整数 |
| `int64` | `int64` | 固定宽度有符号整数 |
| `float32` | `float32` | 32 位浮点数 |
| `float64` | `float64` | 64 位浮点数 |
| `boolean` | `bool` | 布尔逻辑 |
| `string` | `string` | 文本、格式化、模板、大小写、裁剪 |
| `bigInt` | `*big.Int` | 任意精度整数 |
| `bigDecimal` | `*big.Rat` | 任意精度小数 |
| `array<T>` | `[]T` | 元素类型约束 |
| `any` | `any` | 边界兼容类型 |
| `null` | `nil` | 空值 |
| `error` | `error` | Go `error` 接口映射 |

### 4.2.1 宿主类型映射

宿主类型使用相同格式注册：

```go
registry.AutoBindType("user", reflect.TypeFor[*User]())
registry.AutoBindType("money", reflect.TypeFor[Money]())
registry.AutoBindType("address", reflect.TypeFor[common.Address]())
```

映射规则：

- wflang 类型名由宿主指定，建议使用小写驼峰或业务语义名。
- Go 类型可以是值类型、指针类型、结构体、别名类型或外部库类型。
- 构造器负责把 typed literal 的 `value` 转成 Go 值。
- 自动绑定负责扫描 Go 导出方法，并生成语言 operator。
- Go 方法返回的 `error` 映射为内置 `error` 类型。
- `error` 和其他映射类型相同：wflang 类型名为 `error`，Go 类型为 `error`，可调用方法来自 Go 方法集。
- Go `error` 作为普通值进入函数返回形态；语言诊断错误使用 `LangError` 中断执行。

### 4.2.2 自动宿主类型映射

Go 函数或方法返回值中出现未注册类型时，wflang 自动创建宿主类型映射：

```text
auto type name = Go package path + "." + Go type name
```

规则：

- 自动映射类型不要求提前注册。
- 自动映射类型保留原始 Go `reflect.Type`。
- 自动映射类型可作为后续函数或方法的参数。
- 自动映射类型可参与重载分派。
- 自动映射类型的方法集按 Go 反射规则获取。
- 导出方法可调用，小写方法不可调用。
- 如果宿主后续显式注册同一个 Go 类型，注册名成为友好别名，底层 `reflect.Type` 必须一致。
- 如果两个类型名映射到同一个 `reflect.Type`，方法兼容性按 `reflect.Type` 判断。

示例：

```go
func LoadBook(id int64) (*books.Book, error) { ... }
func PrintBook(book *books.Book) (string, error) { ... }
```

第一次调用返回未注册的 `*books.Book`：

```json
{
  "LoadBook": [
    { "pkg": "books" },
    { "literal": { "type": "int64", "value": "1001" } }
  ]
}
```

运行时结果类型记录为 `*github.com/acme/books.Book`。后续调用可直接把该值传给需要 `*books.Book` 的 Go 函数。

### 4.2.3 多返回值映射

Go 函数可以返回多个业务值和一个 `error`：

```go
func QueryBook(id int64) (*books.Book, int64, bool, string, error) { ... }
```

映射规则：

- 无返回值时结果是 `null`。
- 单返回值 `T` 的结果是 `T`。
- 单返回值 `error` 的结果是 `error` typed value，nil error 的 Go 承载值为 `nil`。
- 多返回值全部组成 `tuple<T1,T2,...>`。
- 最后一位返回值如果实现 `error`，它也作为 tuple 末位保留。
- 业务返回值类型未注册时，按自动宿主类型映射规则生成类型名。

示例 result：

```text
tuple<*github.com/acme/books.Book,int64,boolean,string,error>
```

### 4.3 类型推断

目标推断能力：

- 从 typed literal 推断明确类型。
- 从变量上下文推断路径类型。
- 从函数签名推断返回类型。
- 从操作符重载推断返回类型。
- 从 `if` 两个分支检查类型是否一致。
- 从 `array` 元素推断 `array<T>`。

### 4.4 类型检查

目标检查能力：

- 操作符参数类型检查。
- 函数参数数量和类型检查。
- 可选参数、默认参数和可变参数检查。
- `set` 赋值类型检查。
- `return` 类型检查。
- `foreach.target` 数组类型检查。
- `if.cond` 布尔类型检查。
- Go struct 导出字段和 tag 暴露字段访问检查。
- 数组下标访问检查。
- 宿主类型方法能力检查。

### 4.5 类型自动绑定 API

目标 Go API 以自动绑定为默认路径：

```go
registry.AutoBindType("money", reflect.TypeFor[Money](), types.BindOptions{
    Constructor: NewMoney,
})
```

自动绑定会完成：

- 注册语言类型 `money`。
- 建立 Go 类型 `Money` 与语言类型 `money` 的映射。
- 扫描 `Money` 的导出方法。
- 从方法签名推导语言方法签名。
- 把 `Add`、`Format` 等 Go 方法映射为语言方法。
- 为静态校验器生成方法签名表。
- 为运行时生成反射调用计划。

示例 Go 类型：

```go
type Money struct {
    Amount   *big.Rat
    Currency string
}

func (m Money) Add(other Money) (Money, error) { ... }
func (m Money) Format(style string) (string, error) { ... }
```

语言调用保持 JSONLogic 形态：operator 是自动绑定出来的方法名，第一个参数是 receiver。

```json
{
  "Format": [
    { "var": "price" },
    { "literal": { "type": "string", "value": "plain" } }
  ]
}
```

显式 overload 映射保留为高级覆盖能力：

```go
registry.BindMethodOverloads("money", "Add", []types.GoMethodOverload{
    {GoMethod: "Add"},
    {GoMethod: "AddBigDecimal"},
})
```

目标能力补强：

- 自动扫描 Go 类型导出方法。
- 支持 include / exclude 方法白名单。
- 支持显式 overload 映射。
- 支持参数名来源：Go 源码、tag、显式 override。
- 注册时校验方法签名是否可映射。
- 注册失败返回 error，启动阶段统一收集错误。
- 支持泛型类型描述，如 `array<money>`。
- 支持类型文档和示例，用于自动生成语言文档。
- 支持方法元数据：纯函数、副作用、capability、timeout、cost。

---

## 5. Go 宿主运行时

### 5.1 宿主职责

Go 宿主负责：

- 创建 Registry。
- 注册内置包和领域包。
- 注入顶级运行上下文。
- 设置运行选项。
- 执行程序。
- 接收返回值和错误。
- 采集 trace、metric、log。
- 控制权限和资源预算。

### 5.2 推荐 Go API

```go
engine := wflang.NewEngine(wflang.EngineOptions{
    Registry: registry,
    Strict: true,
    Budget: wflang.Budget{
        MaxSteps: 10000,
        MaxCallDepth: 64,
        MaxAllocBytes: 8 << 20,
    },
})

program, err := engine.CompileJSON(data)
if err != nil {
    return err
}

result, err := program.Run(ctx, wflang.RunOptions{
    Vars: map[string]any{
        "input": input,
        "user":  user,
    },
    VarOptions: map[string]wflang.VarOptions{
        "input": {Writable: false},
        "user":  {Writable: false},
    },
    Packages: map[string]wflang.PackageSpec{
        "books": booksPackage,
    },
})
```

渐进式执行 API：

```go
session := engine.NewSession(wflang.SessionOptions{
    Vars: map[string]any{
        "input": input,
        "user":  user,
    },
    VarOptions: map[string]wflang.VarOptions{
        "input": {Writable: false},
        "user":  {Writable: false},
    },
    Packages: map[string]wflang.PackageSpec{
        "books": booksPackage,
    },
})

result, err := session.AppendRun(ctx, json.RawMessage(`{
    "let": {"x": {"literal": {"type": "int64", "value": "1"}}}
}`))

result, err = session.AppendRun(ctx, json.RawMessage(`{
    "return": {"var": "x"}
}`))
```

`AppendRun` 语义：

- 解析当前片段。
- 编译当前片段为追加的 `CallPlan`。
- 从上次停止位置继续执行。
- 返回当前 session 状态、返回值或诊断错误。
- 保留变量、作用域、顶级上下文、包 receiver 和调用计划。

routine handle / await：

```go
session := engine.NewSession(wflang.SessionOptions{
    RoutineErrorHandler: func(ctx context.Context, err error) {
        log.Printf("background routine failed: %v", err)
    },
})

result, err := session.AppendRun(ctx, json.RawMessage(`[
    {"let":{"h":{"routine":{"RunAsync":[
        {"var":"user"},
        {"literal":{"type":"int64","value":"1001"}}
    ]}}}},
    {"return":{"await":{"var":"h"}}}
]`))
```

`await` 语义：

- `routine` 启动后台 host call 并返回 `routineHandle`。
- `await` 等待 handle 完成并读取结果。
- 多返回值按 `tuple<T1,T2,...>` 返回，包含最后一位 `error` slot。
- 多 handle await 按输入顺序返回 `array<any>`。
- routine 内部产生 `LangError` 时，`await` 将该错误返回给宿主。

### 5.3 函数注册目标

包函数和类型方法绑定支持 `(T, error)`。目标绑定应覆盖：

```go
func(a A, b B) (R, error)
func(ctx context.Context, a A) (R, error)
func(env wflang.Env, a A) (R, error)
func(args ...T) (R, error)
func(ctx context.Context, args ...T) (R, error)
```

wflang 按语句和表达式顺序调用函数。Go `error` 返回值作为 `error` typed value 进入返回形态。routine 内部产生的 `LangError` 写入 `routineHandle`；裸 routine 同时把该错误交给 `RoutineErrorHandler`。

包函数元数据：

```go
registry.BindGoPackage("http", wflang.PackageSpec{
    Functions: []wflang.FuncSpec{
        {
            GoName: "Get",
            Params: []wflang.ParamSpec{
                {Name: "url", Type: types.TString},
            },
            ReturnTypes: []types.Type{types.TAny},
            Pure: false,
            Deterministic: false,
            Capabilities: []string{"net:http"},
            Impl: httpGet,
        },
    },
})
```

### 5.4 纯函数与副作用函数

建议函数分为两类：

| 类型 | 特征 | 运行策略 |
|------|------|----------|
| 纯函数 | 相同输入产生相同输出 | 可缓存、可重排、可预求值 |
| 副作用函数 | 访问时间、随机数、网络、数据库、文件等 | 由宿主权限控制 |

函数元数据应明确标注：

- `Pure`
- `Deterministic`
- `Capabilities`
- `Timeout`
- `Cost`
- `Description`

### 5.5 宿主能力模型

语言通过 capability 控制外部能力：

```go
engine := wflang.NewEngine(wflang.EngineOptions{
    Capabilities: wflang.CapabilitySet{
        "time:read": true,
        "net:http": false,
    },
})
```

函数调用前检查 capability：

```json
{ "Now": [{ "pkg": "time" }] }
```

如果宿主授予 `time:read`，函数执行。权限缺失时返回诊断错误。

事务与副作用编排由宿主负责，语言本体不提供事务语句：

- wflang 只描述"调哪些函数、以什么顺序调、返回值如何承接"，事务边界由宿主决定。
- 事务上下文通过 `context.Context` 从 `program.Run` 或 `AppendRun` 的 ctx 注入到 Go 宿主函数。需要事务时，宿主应在进入 `Run` 前 `ctx = txutil.WithTx(ctx, tx)`，所有相关宿主函数从 ctx 取出同一个事务对象。
- 一次 `Run` 或 `AppendRun` 的所有前台宿主调用共享同一个 ctx；`routine` 内部宿主调用共享派生自启动点的 ctx。
- 已完成的副作用保留；事务提交或回滚由宿主根据显式返回值和 `LangError` 判断。

Go 到 wflang 的转换对象是 Registry 中绑定的包函数、类型方法、类型映射和由 Go Builder 描述的调用图。普通 Go 业务逻辑先通过 `BindGoPackage`、`AutoBindType`、`BindMethodOverloads` 暴露为语言可调用能力，再由配置生成器输出 JSON AST。

闭环目标：

```text
Go package / Go type / Go method
        ↓ Bind / AutoBind / Overload
Registry metadata
        ↓ ConfigBuilder / ExportLanguageSpec
wflang JSON config
        ↓ CompileJSON
CallPlan
        ↓ Run / await
Go reflect call result
```

Registry 导出语言规格：

```go
spec, err := registry.ExportLanguageSpec()
if err != nil {
    return err
}
```

`spec` 至少包含：

- 包 receiver 名称和 Go package identity。
- 包函数名称、参数类型、返回类型、error 位置、capability。
- 类型名到 Go `reflect.Type` 的映射。
- 类型方法名称、receiver 类型、参数类型、返回类型。
- overload table。
- typed literal 构造器。

Go Config Builder 生成 JSON AST：

```go
builder := wflang.NewConfigBuilder(registry)

program := builder.Program().
    Let("score", builder.Call(
        builder.Pkg("risk"),
        "Score",
        builder.Var("input.amount"),
        builder.Var("input.country"),
    )).
    Return(builder.Var("score"))

data, err := program.JSON()
```

生成结果：

```json
[
  {
    "let": {
      "score": {
        "Score": [
          { "pkg": "risk" },
          { "var": "input.amount" },
          { "var": "input.country" }
        ]
      }
    }
  },
  { "return": { "var": "score" } }
]
```

生成规则：

- Builder 只生成 JSONLogic operator 形态：`{ "GoName": [receiver, ...args] }`。
- 包函数 receiver 统一生成 `{ "pkg": "name" }`。
- 类型方法 receiver 统一生成表达式结果或 `{ "var": "name" }`。
- Go 常量统一生成 typed literal。
- Go `int` 和 `uint` 常量通过显式 Builder 类型函数选择 `int64` 或 `uint64`。
- 函数名、方法名、包名按 Registry 元数据精确输出。
- Builder 在生成阶段执行符号解析、参数数量检查和类型检查。
- 生成后的 JSON 可直接交给 `CompileJSON`。

### 5.7 Go 解析与执行 wflang 配置

Go 执行端使用同一份 Registry 解析和执行生成出来的配置。

```go
registry := wflang.NewRegistry()
registry.BindGoPackage("risk", riskPackage)
registry.AutoBindType("user", reflect.TypeFor[*User]())

engine := wflang.NewEngine(wflang.EngineOptions{
    Registry: registry,
    Strict:   true,
})

program, err := engine.CompileJSON(data)
if err != nil {
    return err
}

result, err := program.Run(ctx, wflang.RunOptions{
    Vars: map[string]any{
        "input": input,
        "user":  user,
    },
    Packages: map[string]wflang.PackageSpec{
        "risk": riskPackage,
    },
})
```

执行规则：

- `CompileJSON` 使用 Registry 解析 `pkg`、类型、方法、函数和 overload。
- typed literal 通过 Registry 构造 Go 值。
- `CallPlan` 缓存反射调用目标和返回类型。
- `Run` 按 JSON AST 顺序执行，并通过 Go reflection 调用宿主函数或方法。
- Go 返回值映射为 wflang typed value。
- Go `error` 映射为内置 `error` typed value。
- routine 返回 `routineHandle`，`await` 读取后台 Go 调用结果。

### 5.8 Round-trip 验收

Go 生成配置和 Go 执行配置必须满足 round-trip 验收：

```go
data, err := builder.Program().
    Return(builder.Call(
        builder.Pkg("risk"),
        "Score",
        builder.Lit("float64", "10"),
        builder.Lit("string", "US"),
    )).
    JSON()
if err != nil {
    return err
}

program, err := engine.CompileJSON(data)
if err != nil {
    return err
}

result, err := program.Run(ctx, wflang.RunOptions{
    Packages: map[string]wflang.PackageSpec{"risk": riskPackage},
})
```

验收标准：

- Builder 生成的 JSON 通过 JSON Schema 校验。
- Builder 生成的 JSON 通过 `CompileJSON`。
- `CompileJSON` 生成的 `CallPlan` 指向 Registry 中同一个 Go 函数或方法。
- `Run` 返回值类型与 Builder 推断类型一致。
- `Explain` 能列出从 Go symbol 到 JSON path 再到 `CallPlan` 的映射。
- Conformance test 覆盖 package call、method call、typed literal、tuple、routine handle、await、auto host type。

---

## 6. 标准库目标

### 6.1 标准库分层

建议标准库分为：

1. **核心标准库**：纯函数、确定性、默认启用。
2. **可选标准库**：需要宿主明确开启。
3. **领域标准库**：业务方注入。

### 6.2 核心标准库清单

#### 字符串包 `str`

| 函数 | 说明 |
|------|------|
| `Len` | 字符串长度 |
| `Trim` | 去除两端空白 |
| `Lower` | 转小写 |
| `Upper` | 转大写 |
| `Contains` | 包含判断 |
| `StartsWith` | 前缀判断 |
| `EndsWith` | 后缀判断 |
| `Replace` | 替换 |
| `Split` | 分割 |
| `Join` | 拼接数组 |
| `Format` | 模板格式化 |

调用示例：

```json
{ "Len": [{ "pkg": "str" }, { "var": "input.name" }] }
```

#### 数字包 `num`

| 函数 | 说明 |
|------|------|
| `Abs` | 绝对值 |
| `Round` | 四舍五入 |
| `Floor` | 向下取整 |
| `Ceil` | 向上取整 |
| `Min` | 最小值 |
| `Max` | 最大值 |
| `Clamp` | 区间裁剪 |

#### 数组包 `arr`

| 函数 | 说明 |
|------|------|
| `Len` | 长度 |
| `Contains` | 包含判断 |
| `Sort` | 排序 |
| `Distinct` | 去重 |
| `Flatten` | 展平 |

#### 路径包 `path`

| 函数 | 说明 |
|------|------|
| `Get` | 从 Go struct、map 或宿主对象读取路径 |
| `Set` | 返回写入路径后的新值 |
| `Has` | 判断路径是否存在 |
| `Keys` | 读取键列表 |
| `Values` | 读取值列表 |

#### 类型转换包 `to`

| 函数 | 说明 |
|------|------|
| `String` | 转字符串 |
| `Int8` | 转 `int8` |
| `Int16` | 转 `int16` |
| `Int32` | 转 `int32` |
| `Int64` | 转 `int64` |
| `Uint8` | 转 `uint8` |
| `Uint16` | 转 `uint16` |
| `Uint32` | 转 `uint32` |
| `Uint64` | 转 `uint64` |
| `Float32` | 转 `float32` |
| `Float64` | 转 `float64` |
| `Boolean` | 转布尔 |
| `BigInt` | 转大整数 |
| `BigDecimal` | 转高精度小数 |
| `JSON` | 序列化 JSON 字符串 |

#### JSON 包 `json`

| 函数 | 说明 |
|------|------|
| `Parse` | 解析 JSON 字符串 |
| `Stringify` | 序列化 JSON 值 |

#### 值包 `val`

| 函数 | 说明 |
|------|------|
| `TypeOf` | 返回语言类型 |
| `IsNull` | 空值判断 |
| `IsEmpty` | 空集合或空字符串判断 |
| `Coalesce` | 返回第一个有效值 |
| `Assert` | 断言 |
| `IsError` | 判断是否为 Go `error` 映射值 |

wflang 支持一等函数值和闭包捕获。数组映射、过滤、聚合等批量处理逻辑可以通过 `foreach`、`fori`、函数值和宿主注入函数组合完成。

---

## 7. 静态分析与编译管线

### 7.1 推荐编译阶段

```text
JSON bytes
  → Decode
  → Normalize
  → Parse AST
  → Resolve symbols
  → Type check
  → Capability check
  → Lower / Optimize
  → Executable Program
```

### 7.2 Normalize

Normalize 负责把宽松输入统一成规范 AST：

- 单参数函数转数组参数。
- typed literal 的 `value` 按声明类型构造为 Go 值。
- typed literal 标准化。
- 缺省字段补默认值。
- 旧版本语法迁移到当前版本语法。

### 7.3 Parse AST

Parse AST 负责：

- 校验 JSON 节点形状。
- 构造内部 AST struct。
- 为每个节点生成 JSON Pointer 路径。
- 保留原始 source path，用于错误定位。

### 7.4 Resolve symbols

Resolve symbols 负责：

- 解析顶级注入变量和局部变量名。
- 解析 `pkg` 包 receiver。
- 解析包级函数名。
- 解析类型方法名。
- 解析 operator 重载。
- 解析类型名。
- 解析顶级注入上下文。
- 解析 import。

#### Receiver 分派规则

所有调用表达式统一为：

```json
{ "Operator": [receiver, arg1, arg2] }
```

分派流程：

1. 先解析 operator 是否为 intrinsic，如 `var`、`pkg`、`literal`、`if`。
2. 普通 operator 先解析第一个参数作为 receiver。
3. receiver 为 `pkgRef` 时，在包函数表查找同名 Go 包级函数。
4. receiver 为 typed value 时，在该类型的方法表查找同名 Go 方法。
5. receiver 为 `null` 时返回诊断错误。
6. 找到候选集后按参数类型进行重载分派。
7. 找到唯一候选后生成 `CallPlan`。

包级函数示例：

```json
{
  "FindByID": [
    { "pkg": "books" },
    { "literal": { "type": "int64", "value": "1001" } }
  ]
}
```

类型方法示例：

```json
{
  "Run": [
    { "var": "user" },
    { "literal": { "type": "float64", "value": "10" } }
  ]
}
```

Go 名称规则：

- 包级函数名与 Go 函数名完全一致。
- 类型方法名与 Go 方法名完全一致。
- wflang 不做大小写转换。
- 小写开头的 Go 函数或方法无法通过反射绑定。

#### 重载解析规则

- 同一个 receiver 下允许多个 Go 函数或方法映射到同一个 operator。
- Go 本身的方法名不可重载，语言重载通过宿主显式提供多个 Go 名称映射到同一 operator。
- 精确类型匹配优先。
- 安全数值提升次之，如 `int8` 到 `int64`。
- 显式可构造转换再次之。
- `any` 兜底优先级最低。
- 多个候选同分返回 `E_AMBIGUOUS_OVERLOAD`。
- 没有候选返回 `E_OPERATOR_NOT_FOUND`。

重载解析完全在**编译期**完成，依赖静态类型信息：

- typed literal 的类型在 AST 中已确定，直接参与匹配。
- 变量类型由类型推断提供（`let` 承接的表达式结果、顶级 `Vars` 注入类型、循环变量类型）。
- receiver 类型由第一个参数求值路径的静态类型决定。
- 所有参数类型齐备后，编译器按 overload table 过滤候选；结果只有 0 / 1 / 多三种。

命中 `E_AMBIGUOUS_OVERLOAD` 或 `E_OPERATOR_NOT_FOUND` 时，用户的处理方式只有两条：

1. **调整参数类型**：修改 typed literal 声明的类型，或在上游 `let` 中使用更精确的 Go 宿主函数产生期望类型，使候选集收敛到唯一匹配。
2. **宿主层改绑定**：在 Go 端把冲突的重载拆成不同 operator 名称（例如 `AddInt64` / `AddFloat64`），取消该 operator 的重载合并。

语言层不提供运行期强制选择重载的语法（如 `Add@int64`）。重载合并由宿主决定，歧义由宿主或参数类型修正解决，语言本体保持"编译期类型匹配 → 唯一 `CallPlan`"的简单分派模型。

示例：宿主注册

```go
func (Counter) AddInt64(v int64) (int64, error)     { return v + 1, nil }
func (Counter) AddFloat64(v float64) (float64, error) { return v + 1, nil }

registry.BindMethodOverloads("counter", "Add", []types.GoMethodOverload{
    {GoMethod: "AddInt64"},
    {GoMethod: "AddFloat64"},
})
```

编译期检查：`{ "Add": [{ "var": "c" }, X] }` 中 `X` 的静态类型必须可匹配到 `int64` 或 `float64` 之一。

- `X` 是 `{ "literal": { "type": "int64", "value": "1" } }` → 唯一命中 `AddInt64`。
- `X` 是 `{ "literal": { "type": "float64", "value": "1.0" } }` → 唯一命中 `AddFloat64`。
- `X` 是 `{ "literal": { "type": "string", "value": "1" } }` → `E_OPERATOR_NOT_FOUND`（没有候选）。
- `X` 是一个运行期才知类型的 `any` 值 → 视分派器规则可能返回 `E_AMBIGUOUS_OVERLOAD`，用户应在上游 `let` 里用精确类型方法承接后再调用。

### 7.5 Type check

Type check 负责：

- 推断表达式类型。
- 校验变量读取。
- 校验赋值。
- 校验函数调用。
- 校验分支返回类型。
- 生成 typed AST。

### 7.6 Capability check

Capability check 负责：

- 收集程序使用的宿主函数。
- 收集函数所需 capabilities。
- 对比 EngineOptions 中授权能力。
- 生成执行前权限报告。

### 7.7 CallPlan

`CallPlan` 是编译期产物，运行期按计划顺序执行，减少反射查找和重载分派成本。

```go
type CallPlan struct {
    Operator     string
    ReceiverKind ReceiverKind
    ReceiverType types.Type
    PackageName  string
    GoFunc       reflect.Value
    GoMethod     reflect.Method
    ParamTypes   []types.Type
    ReturnTypes   []types.Type
    GoReturnTypes []reflect.Type
    ErrorIndex    int
    ResultKind    ResultKind
    Capabilities []string
}
```

运行规则：

- 按表达式参数从左到右求值。
- 根据 `CallPlan` 构造 Go 参数。
- 调用 Go 函数或方法。
- 无返回值时返回 `null`。
- 单返回值时返回单个 typed value；单 `error` 返回值也是 `error` typed value。
- 多返回值时返回 `tuple<T1,T2,...>`；最后一位 Go `error` 保留为 tuple 的 `error` slot。
- routine 调用将 typed value 或 `LangError` 写入 `routineHandle`。
- `await` 读取 routine 的 typed value，或返回 routine 内部产生的 `LangError`。

### 7.8 Lower / Optimize

优化阶段建议实现：

- 常量折叠。
- 纯函数常量参数预求值。
- 死分支裁剪。
- 操作符分派缓存。
- 函数反射调用计划缓存。
- 变量路径访问计划缓存。

---

## 8. routine handle 与 await

### 8.1 routineHandle

`routineHandle` 是 `routine` 返回的内部 future-like 值。它保存后台 host call 的完成状态、返回值、错误和调用路径。

规则：

- handle 只由 `routine` 创建。
- handle 可存入变量并跨后续语句使用。
- handle 完成后缓存结果或错误。
- 同一 handle 可重复 `await`。

### 8.2 await

`await` 等待 handle 完成并读取结果：

```json
[
  {"let":{"h":{"routine":{"Load":[{"pkg":"books"},{"var":"id"}]}}}},
  {"return":{"await":{"var":"h"}}}
]
```

多个 handle 可一次等待：

```json
{
  "await": [
    {"var":"a"},
    {"var":"b"}
  ]
}
```

返回规则：

- 单 handle 返回该 routine 的 typed value。
- 多 handle 返回 `array<any>`。
- 多返回值使用 `tuple<T1,T2,...>`，包含最后一位 `error` slot。
- 只有 `error` 返回值时返回 `error` typed value，nil error 的 Go 承载值为 `nil`。
- routine 内部产生的 `LangError` 由 `await` 返回给宿主。
- `await` 非 `routineHandle` 返回 `E_TYPE`。

## 9. 错误模型

### 9.1 Go error 映射

wflang 的 `error` 类型与 Go `error` 接口语义一致。它是一个普通映射类型：类型名是 `error`，Go 类型是 `error`。

```go
type error interface {
    Error() string
}
```

语言中能调用的方法来自 Go 方法集。内置 `error` 只有 `Error() string`，自动绑定为 JSONLogic operator `Error`：

```json
{ "Error": [{ "var": "err" }] }
```

语义等价于：

```go
err.Error()
```

Go 函数返回 `(T, error)` 时：

- 表达式结果为 `tuple<T,error>`。
- nil error 的 tuple 末位是 Go 承载值为 `nil` 的 `error` typed value。
- 非 nil error 的 tuple 末位是承载原始 Go error 的 `error` typed value。

### 9.1.1 Host 返回值形态

Registry 按 Go 函数签名构造 wflang typed value：

| Go 返回签名 | wflang 结果 |
|-------------|-------------|
| `()` | `null` |
| `(error)` | `error` typed value，nil error 的 Go 承载值为 `nil` |
| `(T)` | `T` |
| `(T, error)` | `tuple<T,error>` |
| `(T1, T2, ..., Tn)` | `tuple<T1,T2,...,Tn>` |
| `(T1, T2, ..., Tn, error)` | `tuple<T1,T2,...,Tn,error>` |

Go `error` 在上述形态中保持为普通语言值。程序需要通过 tuple 解构、变量传递或调用 `Error` 方法显式处理它。

Host panic、context cancel、预算耗尽、类型错误、权限错误和 nil receiver 错误使用 `LangError` 报告，直接中断当前执行路径。已完成的副作用不会自动回滚；事务语义由宿主基于 `context.Context` 和返回结果处理。

### 9.1.2 `null` receiver 调用

调用方法时 receiver 求值结果为 `null`（或 Go 侧的 nil 指针/nil 接口）的处理：

- 编译期无法判定 receiver 非 null 时，生成运行期 null 检查。
- 运行期 receiver 为 null 时，跳过 Go 反射调用，直接产生 `E_NIL_RECEIVER` 诊断错误。
- `E_NIL_RECEIVER` 是 `LangError`，会中断当前执行路径。
- 宿主若希望方法接受 nil receiver，应将该方法改为包级函数（`{"pkg":...}` receiver 不会为 null）。

### 9.2 语言诊断错误

语言解析、类型检查、权限检查、资源预算等内部诊断错误使用 `LangError`。`LangError` 是执行器和工具链的诊断结构，并非语言里的 `error` 类型。

```go
type LangError struct {
    Code      string
    Message   string
    Path      string
    Function  string
    Expected  string
    Actual    string
    Hint      string
    Cause     error
}
```

诊断错误 JSON 输出：

```json
{
  "code": "E_TYPE_MISMATCH",
  "message": "type mismatch",
  "path": "/program/2/return/+ /1",
  "expected": "int64",
  "actual": "string",
  "hint": "function '+' expects int64 arguments"
}
```

### 9.3 诊断错误分类

| 分类 | 说明 | 典型阶段 |
|------|------|----------|
| `E_JSON_DECODE` | JSON 解码失败 | Decode |
| `E_AST_SHAPE` | AST 形状异常 | Parse |
| `E_SYMBOL` | 函数、变量、类型解析失败 | Resolve |
| `E_TYPE` | 类型匹配失败 | Type check |
| `E_CAPABILITY` | 宿主能力授权失败 | Capability check |
| `E_RUNTIME` | 运行期错误 | Execute |
| `E_BUDGET` | 运行预算耗尽 | Execute |
| `E_HOST` | 宿主函数执行失败 | Execute |
| `E_NIL_RECEIVER` | 方法调用 receiver 为 null | Execute |
| `E_PANIC` | Go panic 或 wflang `panic` 操作符触发 | Execute |
| `E_ROUTINE` | `routine` 启动或后台执行失败 | Execute |

### 9.4 错误定位

错误定位目标：

- 每个 AST 节点携带 JSON Pointer。
- 每个函数参数携带参数 index 或参数名。
- 每个变量路径携带 path segment。
- 每个宿主函数错误携带函数名和调用路径。
- 多错误聚合一次性返回，提升配置修复效率。

---

## 10. 安全与资源控制

### 10.1 运行预算

语言运行必须支持预算：

| 预算 | 说明 |
|------|------|
| `MaxSteps` | 最大求值步数 |
| `MaxCallDepth` | 最大调用深度 |
| `MaxLoopIterations` | 最大循环次数 |
| `MaxRoutines` | 最大 goroutine 启动数量 |
| `MaxArrayLength` | 最大数组长度 |
| `MaxObjectKeys` | 最大对象键数量 |
| `MaxStringBytes` | 最大字符串大小 |
| `MaxAllocBytes` | 最大估算分配 |
| `Timeout` | 最大运行时长 |

### 10.2 沙箱原则

- 语言本体只执行 JSON AST。
- 外部能力只通过宿主函数进入。
- 宿主函数按 capability 授权。
- 所有宿主函数接收 `context.Context`。
- panic 默认转为诊断错误。
- `routine` 启动受 capability 和预算控制。
- 反射调用前完成参数数量和类型检查。
- 默认 Registry 只包含纯函数标准库。

### 10.3 数据边界

数据输入建议：

- JSON 输入通过 decoder 限制大小。
- Go 对象输入通过 schema 显式暴露字段。
- struct 字段读取只允许导出字段。
- tag 为 `json:"-"` 的字段屏蔽。
- 宿主类型只通过自动绑定的导出方法访问。

---

## 11. 可观测性与调试

### 11.1 Trace

推荐执行时产生 trace：

```json
{
  "path": "/program/1/return",
  "op": "+",
  "args": [
    { "type": "int64", "value": "1" },
    { "type": "int64", "value": "2" }
  ],
  "result": { "type": "int64", "value": "3" },
  "type": "int64",
  "duration_us": 12
}
```

Trace 目标：

- 记录表达式路径。
- 记录操作符和函数名。
- 记录参数类型。
- 记录返回类型。
- 记录耗时。
- 记录错误。

### 11.2 Explain

`Explain(program)` 输出静态分析报告：

- 使用了哪些变量。
- 使用了哪些函数。
- 使用了哪些 capabilities。
- 每个表达式推断类型。
- 可能出现哪些错误。
- 运行预算估算。

### 11.3 Debug API

建议提供：

```go
program.DumpAST()
program.DumpTypedAST()
program.Explain()
program.Trace(ctx, input)
program.EvalAt(path, input)
```

---

## 12. 工具链

### 12.1 JSON Schema

语言需要提供 schema：

- `wflang-program.schema.json`
- `wflang-expression.schema.json`
- `wflang-standard-library.schema.json`

用途：

- IDE 自动补全。
- 配置平台表单校验。
- CI 校验。
- 文档示例校验。

### 12.2 Formatter

Formatter 目标：

- 稳定 key 顺序。
- 稳定数组缩进。
- 格式化 typed literal。
- 标准化单参数函数格式。
- 生成可 diff 的 JSON。

### 12.3 Linter

Linter 规则：

- 表达式对象形状检查。
- 变量命名检查。
- 函数命名检查。
- 宿主 capability 检查。
- 复杂度检查。
- 循环预算检查。
- 废弃语法提示。

### 12.4 文档生成

从 Registry 自动生成：

- 类型文档。
- 操作符文档。
- 函数签名文档。
- capability 文档。
- 语言规格 JSON。
- Config Builder 类型辅助函数。
- 示例文档。
- 版本变更文档。

### 12.5 测试工具

推荐测试文件格式：

```json
{
  "name": "sum prices",
  "program": [
    { "return": { "+": [
      { "literal": { "type": "int64", "value": "1" } },
      { "literal": { "type": "int64", "value": "2" } }
    ] } }
  ],
  "input": {},
  "want": { "type": "int64", "value": "3" }
}
```

测试 runner 目标：

- 执行 JSON case。
- 对比结果。
- 对比错误码。
- 生成 trace。
- 支持 golden file。

### 12.6 Go 配置生成器

Go 配置生成器目标：

- 从 Registry metadata 生成 Builder API。
- 从 Builder AST 输出 wflang JSON。
- 在生成阶段执行符号和类型检查。
- 把 Go typed value 转成 typed literal。
- 把 Go package receiver 转成 `{ "pkg": "name" }`。
- 把 Go 方法调用转成 `{ "MethodName": [receiver, ...args] }`。
- 生成 round-trip 测试样例。

---

## 13. 版本兼容

### 13.1 版本声明

推荐：

```json
{
  "lang": "wflang/v1",
  "stdlib": "stdlib/v1",
  "program": []
}
```

### 13.2 兼容策略

- 语义版本按 `v1`, `v2` 管理。
- 同一大版本保持执行语义稳定。
- 新函数加入标准库通过 minor 版本记录。
- 废弃语法进入 deprecation 表。
- 编译器提供迁移器，将旧语法 normalize 到当前 AST。

### 13.3 功能开关

```json
{
  "lang": "wflang/v1",
  "features": {
    "typedArray": true
  },
  "program": []
}
```

功能开关适合实验性语法灰度。

---

## 14. 当前维护清单

当前实现已经覆盖语言内核、Go 宿主桥接、tuple 解构、`defer`、map、struct literal、channel/select、routine block、formatter、linter、test runner 和 go2wlang 可翻译 Go 子集。后续维护重点集中在工程化深度和标准库广度。

| 领域 | 当前后续项 |
|------|------------|
| 语言 schema | 补齐 IDE / 平台校验 schema |
| 标准库 | 增加日期时间、JSON、Regex、error 辅助函数 |
| 文档生成 | 从 Registry metadata 生成标准库和宿主 API 文档 |
| Conformance | 固化第三方宿主一致性测试套件 |
| 性能 | 增加 benchmark suite 和热点回归测试 |
| 迁移 | 完善版本迁移器和 changelog 生成 |

---

## 15. 演进原则

- 新语法必须有 parser、runtime、文档和 SPEC_TESTS 条目同步更新。
- breaking 语义变更需要版本门控或迁移器。
- host `error` 保持普通值语义；语言诊断继续使用 `LangError`。
- 标准库新增能力优先通过 `DefaultRegistry()` 暴露纯函数。
- go2wlang 继续采用受限 Go 子集，遇到不支持语法返回明确错误。

---

## 16. 推荐语法示例

### 16.1 简单表达式

```json
{
  "+": [
    { "literal": { "type": "int64", "value": "1" } },
    { "literal": { "type": "int64", "value": "2" } },
    { "literal": { "type": "int64", "value": "3" } }
  ]
}
```

### 16.2 变量读取

```json
{ "var": "input.user.age" }
```

### 16.3 缺省值

```json
{ "var": ["input.user.name", { "literal": { "type": "string", "value": "guest" } }] }
```

### 16.4 条件分支

```json
{
  "if": {
    "cond": { ">=": [{ "var": "input.age" }, { "literal": { "type": "int64", "value": "18" } }] },
    "then": [ { "return": { "literal": { "type": "string", "value": "adult" } } } ],
    "else": [ { "return": { "literal": { "type": "string", "value": "minor" } } } ]
  }
}
```

### 16.5 typed literal

```json
{
  "literal": {
    "type": "bigDecimal",
    "value": "123.456"
  }
}
```

### 16.6 宿主值构造

宿主对象由 Go 包函数构造，返回值携带 Go 类型映射。

```json
{
  "NewQuery": [
    { "pkg": "books" },
    { "var": "input.name" },
    { "+": [{ "var": "input.base" }, { "literal": { "type": "int64", "value": "10" } }] }
  ]
}
```

### 16.7 数组构造

```json
{
  "literal": {
    "type": "array<int64>",
    "value": [
      { "var": "input.first" },
      { "var": "input.second" }
    ]
  }
}
```

### 16.7.1 数组索引

读取数组元素：

```json
{ "var": "input.items.0" }
```

读取数组元素字段：

```json
{ "var": "input.items.0.price" }
```

带缺省值读取：

```json
{
  "var": [
    "input.items.0",
    { "literal": { "type": "null", "value": null } }
  ]
}
```

### 16.8 程序

```json
[
  { "let": { "total": { "literal": { "type": "bigDecimal", "value": "0" } } } },
  {
    "foreach": {
      "target": { "var": "input.items" },
      "as": "item",
      "do": [
        {
          "set": {
            "total": {
              "+": [
                { "var": "total" },
                { "var": "item.price" }
              ]
            }
          }
        }
      ]
    }
  },
  { "return": { "var": "total" } }
]
```

### 16.9 `fori`、`break`、`continue`

```json
[
  { "let": { "sum": { "literal": { "type": "int64", "value": "0" } } } },
  {
    "fori": {
      "var": "i",
      "from": { "literal": { "type": "int64", "value": "0" } },
      "to": { "literal": { "type": "int64", "value": "10" } },
      "step": { "literal": { "type": "int64", "value": "1" } },
      "do": [
        {
          "if": {
            "cond": { "==": [{ "var": "i" }, { "literal": { "type": "int64", "value": "5" } }] },
            "then": [{ "continue": {} }]
          }
        },
        {
          "if": {
            "cond": { ">=": [{ "var": "i" }, { "literal": { "type": "int64", "value": "8" } }] },
            "then": [{ "break": {} }]
          }
        },
        {
          "set": {
            "sum": {
              "+": [{ "var": "sum" }, { "var": "i" }]
            }
          }
        }
      ]
    }
  },
  { "return": { "var": "sum" } }
]
```

### 16.10 `panic` 与 `routine`

`panic`：

```json
{
  "panic": {
    "literal": { "type": "string", "value": "invalid state" }
  }
}
```

`routine`：

```json
{
  "routine": {
    "Publish": [
      { "pkg": "events" },
      { "var": "input.event" }
    ]
  }
}
```

### 16.11 宿主包函数调用

Go 包级函数：

```go
package risk

func Score(amount float64, country string) (float64, error) {
    return amountRisk(amount) + countryRisk(country), nil
}
```

宿主注册：

```go
registry.BindGoPackage("risk", riskPackage)
```

语言调用：

```json
{
  "Score": [
    { "pkg": "risk" },
    { "var": "input.amount" },
    { "var": "input.country" }
  ]
}
```

### 16.12 宿主类型方法调用

Go 类型方法：

```go
func (u *User) Run(speed float64) (string, error) { ... }
```

语言调用：

```json
{
  "Run": [
    { "var": "user" },
    { "literal": { "type": "float64", "value": "10" } }
  ]
}
```

### 16.13 Go Builder 生成配置并执行

```go
registry := wflang.NewRegistry()
registry.BindGoPackage("risk", riskPackage)

builder := wflang.NewConfigBuilder(registry)

data, err := builder.Program().
    Return(builder.Call(
        builder.Pkg("risk"),
        "Score",
        builder.Lit("float64", "10"),
        builder.Lit("string", "US"),
    )).
    JSON()
if err != nil {
    return err
}

engine := wflang.NewEngine(wflang.EngineOptions{Registry: registry})
program, err := engine.CompileJSON(data)
if err != nil {
    return err
}

result, err := program.Run(ctx, wflang.RunOptions{
    Packages: map[string]wflang.PackageSpec{"risk": riskPackage},
})
```

生成的配置：

```json
[
  {
    "return": {
      "Score": [
        { "pkg": "risk" },
        { "literal": { "type": "float64", "value": "10" } },
        { "literal": { "type": "string", "value": "US" } }
      ]
    }
  }
]
```

---

## 17. 极致目标画像

一门做到极致的 Go-hosted JSON embedded language 应具备以下特征：

1. **语言核心小**：表达式、语句、类型、函数调用清晰稳定。
2. **宿主扩展强**：任何 Go 类型和函数都能安全映射到语言。
3. **类型反馈早**：运行前发现函数名、参数、变量、返回类型问题。
4. **错误定位准**：每个错误都能定位到 JSON Pointer。
5. **执行可控**：时间、步数、内存、调用深度、外部能力全部受控。
6. **工具完善**：schema、formatter、linter、test runner、trace、explain 全齐。
7. **版本稳定**：语言升级有迁移路径和兼容承诺。
8. **文档自动化**：Registry 自动生成函数、类型、操作符文档。
9. **测试体系强**：单测、golden、fuzz、benchmark、conformance 全覆盖。
10. **上层 DSL 友好**：任何领域配置都可以编译到 wflang AST。

最终形态：wflang 成为 Go 应用中可嵌入、可限制、可解释、可测试、可扩展的 JSON 程序执行内核。
