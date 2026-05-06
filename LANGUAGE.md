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

### 2.5 Go 宿主桥接

宿主桥接采用 receiver 分派模型。包级函数调用使用 `pkg` intrinsic，类型方法调用使用 typed value receiver。`pkg` 和 `var` 同级，都是特殊引用表达式：`var` 引用变量，`pkg` 引用 Go 宿主注册的包。

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

当前签名约定：

- Go 函数返回 `(T, error)`。
- 参数类型通过反射映射到语言类型。
- 返回类型可自动推断，也可显式指定。
- 可变参数可映射为语言函数可变参数。
- Go 返回的 `error` 映射为语言内置 `error` 类型，语义与 Go `error` 接口一致。
- `error` 类型只自动暴露 Go 方法集；Go `error` 接口只有 `Error() string`，语言中只能调用 `Error` 映射出来的 operator。
- 默认执行模式中，`error != nil` 会中断当前表达式；`try` 启用后，`error` 可作为普通语言值返回给程序处理。
- 前台宿主调用按普通 Go 调用处理返回值和 `error`。
- `yield` 只在 `routine` 内部宿主调用中被执行器识别。
- 前台宿主调用返回 `yield` error 时，按普通 Go `error` 处理。

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
- 方法在前台调用中返回宿主定义的 `yield` error 时，执行器按普通 Go `error` 处理。
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
- `routine` 后台执行中产生的 yield 进入 routine yielded 状态，宿主通过 `ResumeYield` 注入对应 routine 调用的返回值后继续执行该 routine。
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
| `foreach` | 核心语句 | 遍历数组 |
| `fori` | 核心语句 | 按整数下标循环 |
| `return` | 核心语句 | 返回结果 |
| `break` | 核心语句 | 退出最近一层循环 |
| `continue` | 核心语句 | 进入最近一层循环的下一次迭代 |
| `panic` | 核心语句 | 触发 Go `panic(value)` |
| `routine` | 核心语句 | 通过 Go `go` 关键字启动宿主调用 |
| `expr` | 核心语句 | 显式执行表达式并丢弃结果 |
| `block` | 推荐新增 | 显式创建局部作用域 |
| `try` | 推荐新增 | 捕获语言错误并转为值 |
| `assert` | 推荐新增 | 程序内断言 |

#### `return`

`return` 立即结束当前程序并返回一个 typed value。`return` 可以出现在顶层、`if` 分支、`foreach`、`fori` 或 `block` 内部。

```json
{ "return": { "var": "total" } }
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
- 表达式求值过程中产生的 Go `error` 按第 9 节"错误短路语义"处理。
- `expr` 不影响作用域，也不创建 block。

#### `routine`

`routine` 映射 Go `go` 关键字，把**一个宿主调用**放入新的 goroutine 执行。

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

- `routine` 的参数**必须是单个宿主调用表达式**：`{ "GoName": [receiver, ...args] }`。
- `routine` 先在当前 goroutine 中求值 receiver 和参数。
- 求值完成后，执行器用 Go `go` 关键字启动该宿主调用。
- `routine` 自身立即返回 `{ "literal": { "type": "null", "value": null } }`。
- goroutine 内的普通 Go 返回值由 Go 语义丢弃。
- goroutine 内的 Go `error` 进入宿主 `RoutineErrorHandler`。
- goroutine 内的 panic 进入宿主 `RoutinePanicHandler`。
- goroutine 内的 yield 生成 routine yielded 状态，并进入宿主 `RoutineYieldHandler`。
- `RoutineYieldHandler` 保存 token 后，宿主通过 `ResumeYield` 注入返回值。
- `routine` 需要 `routine:spawn` capability。
- `routine` 受 `MaxRoutines` 预算限制。

设计约束（**为什么 routine 不能包一段语句序列**）：

- wflang 的定位是 JSON 宿主调用语言，负责流程编排和调用分派，不负责管理并发运行时。
- 允许 `routine` 内嵌 `let`/`set`/`foreach` 等语句，会引入：子 goroutine 的独立作用域栈、闭包变量快照 vs 引用的抉择、父子作用域竞态、跨 goroutine 的 `return` / `break` 语义、跨 goroutine 的错误冒泡路径。这些属于真正编程语言的运行时责任，与语言定位冲突。
- 需要"在 goroutine 里跑一段复杂流程"时，正确做法是在 Go 宿主侧定义一个函数封装该流程，再用 `routine` 启动这个函数调用。并发的复杂性留在 Go，wflang 只负责"启动哪个调用、传什么参数"。

### 3.4 变量模型

当前变量模型是动态作用域栈，推荐固化为词法作用域栈：

- `let` 在当前 block 创建变量。
- `set` 修改最近的同名变量。
- `foreach` 每次迭代创建新 block。
- 宿主调用结果通过 `let`、`set`、`return` 或 `expr` 承接。
- 变量路径读取只读视图，复杂路径操作通过宿主包函数完成。

推荐新增解构：

```json
{
  "let": {
    "price": { "var": "item.price" },
    "count": { "var": ["item.count", { "literal": { "type": "int64", "value": "1" } }] }
  }
}
```

推荐新增变量声明类型：

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
- 执行模式可选择：error 直接中断执行，或通过 `try` 捕获为 `error` 值。

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

- 最后一个返回值如果实现 `error`，它是错误通道。
- 最后一个 `error` 前面的所有返回值组成 result。
- 只有一个业务返回值时，result 是该 typed value。
- 有多个业务返回值时，result 是 `tuple<T1,T2,...>`。
- 只有 `error` 返回值时，`error == nil` 的 result 是 `null`。
- 业务返回值类型未注册时，按自动宿主类型映射规则生成类型名。
- `error != nil` 时默认执行模式停止；`try` 启用后返回 `error` typed value。

示例 result：

```text
tuple<*github.com/acme/books.Book,int64,boolean,string>
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
- 返回当前 session 状态、返回值、已记录的 routine yielded 状态或诊断错误。
- 保留变量、作用域、顶级上下文、包 receiver 和调用计划。

Yield 恢复 API：

```go
session := engine.NewSession(wflang.SessionOptions{
    RoutineYieldHandler: func(ctx context.Context, y wflang.YieldState) {
        asyncYieldStore.Save(y.Token, y)
    },
})

_, err := session.AppendRun(ctx, json.RawMessage(`{
    "routine": {
        "RunAsync": [
            {"var": "user"},
            {"literal": {"type": "int64", "value": "1001"}}
        ]
    }
}`))
if err != nil {
    return err
}

yielded := asyncYieldStore.Wait()

result, err := session.ResumeYield(ctx, wflang.ResumeInput{
    Token: yielded.Token,
    Results: []wflang.Value{
        wflang.MustValue("string", "done"),
    },
})
```

`ResumeYield` 语义：

- `Token` 必须匹配当前 session 的 routine yielded 调用点。
- `Token` 是一次性恢复凭证，成功恢复后失效。
- `Results` 是被挂起 routine 函数调用的业务返回值。
- `Results` 的数量和类型必须匹配 routine 挂起调用点的 `ReturnTypes`。
- `ResumeInput.Err` 可注入该调用的 Go `error`，执行规则与普通 Go 函数返回 `error` 一致。
- 多业务返回值按 `tuple<T1,T2,...>` 注入。
- 只有 `error` 返回值的函数在成功恢复时注入 `null`。
- 恢复成功后，执行器把注入值写回 routine 表达式结果槽，并从 routine 挂起调用点之后继续执行。

### 5.3 函数注册目标

包函数和类型方法绑定支持 `(T, error)`。目标绑定应覆盖：

```go
func(a A, b B) (R, error)
func(ctx context.Context, a A) (R, error)
func(env wflang.Env, a A) (R, error)
func(args ...T) (R, error)
func(ctx context.Context, args ...T) (R, error)
```

wflang 按语句和表达式顺序调用函数。前台函数调用返回宿主定义的 `yield` error 时，执行器按普通 Go `error` 处理。`routine` 内部函数调用返回 `yield` error 时，执行器挂起该 routine 调用点，并把 yield token、调用路径、期望返回类型返回给宿主。宿主异步完成后调用 `ResumeYield` 注入该调用的返回值，routine 从挂起点继续执行。

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

- wflang 只描述"调哪些函数、以什么顺序调、出错怎么冒泡"，不决定这些调用是否在同一个 Go 事务里执行。
- 事务上下文通过 `context.Context` 从 `program.Run` 或 `AppendRun` 的 ctx 注入到 Go 宿主函数。需要事务时，宿主应在进入 `Run` 前 `ctx = txutil.WithTx(ctx, tx)`，所有相关宿主函数从 ctx 取出同一个事务对象。
- 一次 `Run` 或一次 `ResumeYield` 的所有前台宿主调用共享同一个 ctx；`routine.do` 内部宿主调用共享同一个 ctx（派生自启动点的 ctx）。
- 语言层默认短路（9.1.1）只影响执行流，不会回滚已发生的副作用；是否在 error 时回滚事务由宿主在 `Run` 返回后根据 error 决定。

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
        ↓ Run / ResumeYield
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
- Go `error` 映射为内置 `error` 或执行错误路径。
- routine 内部 Go yield error 进入 routine yielded 状态，并通过 `ResumeYield` 注入异步返回值。

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
- Conformance test 覆盖 package call、method call、typed literal、tuple、routine yield resume、auto host type。

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

wflang 本身不支持定义匿名函数、lambda 或用户函数。数组映射、过滤、聚合等逻辑通过 `foreach`、`fori` 和宿主注入函数组合完成。

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
    YieldAware   bool
}

// 运行期 routine yielded 状态由 routine 内部 yield error 产生。
type YieldState struct {
    Token       string
    Payload     any
    Path        string
    CallPlanID  uint64
    ReturnTypes []types.Type
}
```

运行规则：

- 按表达式参数从左到右求值。
- 根据 `CallPlan` 构造 Go 参数。
- 调用 Go 函数或方法。
- `error == nil` 且业务返回值数量为 1 时返回单个 typed value。
- `error == nil` 且业务返回值数量大于 1 时返回 `tuple<T1,T2,...>`。
- `error == nil` 且只有 error 返回值时返回 `null` typed value。
- `error != nil` 且是普通 Go error 时按普通 Go error 处理。
- 前台调用中 `error != nil` 时按普通 Go error 处理，yield error 也走普通 error 路径。
- routine 调用中 `error != nil` 且是 yield error 时保存 routine 挂起调用点并返回 routine yielded 状态。
- `ResumeYield` 注入成功结果时，按 `ResultKind` 生成 typed value 或 tuple。
- `ResumeYield` 注入 Go `error` 时，按普通 Go error 处理。

### 7.8 Lower / Optimize

优化阶段建议实现：

- 常量折叠。
- 纯函数常量参数预求值。
- 死分支裁剪。
- 操作符分派缓存。
- 函数反射调用计划缓存。
- 变量路径访问计划缓存。

---

## 8. Yield 挂起与恢复

### 8.1 Yield error

Yield 是宿主通过 Go `error` 返回的 routine 控制信号。`routine` 内部宿主调用返回 yield error 时，执行器进入 routine yielded 状态。前台普通调用返回 yield error 时，它就是一个普通 Go `error`。

推荐接口：

```go
type YieldError interface {
    error
    Token() string
    Payload() any
}
```

`Token` 是一次 routine yield 挂起点的唯一恢复凭证，用于把外部异步结果注入回正确的 routine 函数调用位置。`Payload` 由宿主保存异步任务信息。

Token 规则：

- token 由产生 yield 的 Go 宿主函数创建。
- token 在当前 session 的 routine yielded 状态中唯一。
- token 可直接采用外部异步任务 ID、消息 ID、future ID 或数据库记录 ID。
- `ResumeYield` 必须携带同一个 token。
- token 匹配成功后只能消费一次，恢复完成后当前 routine yielded 状态清除。
- token 匹配失败返回 `E_YIELD_TOKEN_MISMATCH`。

示例：

```go
func (b Books) LoadAsync(id int64) (*Book, error) {
    token := uuid.NewString()

    asyncStore.Save(token, AsyncTask{
        Kind: "books.LoadAsync",
        BookID: id,
    })

    return nil, wflang.NewYield(token, AsyncPayload{
        TaskID: token,
    })
}
```

### 8.2 routine 挂起调用点

routine 挂起调用点保存：

- JSON Pointer path。
- 当前 `CallPlan`。
- receiver 和已求值参数。
- 期望返回类型 `ReturnTypes`。
- Go 返回类型 `GoReturnTypes`。
- 当前作用域栈和程序计数器。
- routine yield token 和 payload。

### 8.3 恢复注入

宿主异步完成后调用 `ResumeYield`：

```go
result, err := session.ResumeYield(ctx, wflang.ResumeInput{
    Token: token,
    Results: []wflang.Value{value},
})
```

外部任务完成后也可以直接使用任务 ID 恢复：

```go
task := asyncStore.Get(taskID)

result, err := session.ResumeYield(ctx, wflang.ResumeInput{
    Token: task.ID,
    Results: []wflang.Value{bookValue},
})
```

恢复规则：

- `Results` 作为被挂起 routine 函数调用的业务返回值。
- 单业务返回值恢复为单个 typed value。
- 多业务返回值恢复为 `tuple<T1,T2,...>`。
- 注入类型需要满足挂起调用点的 `ReturnTypes` 和 `GoReturnTypes`。
- 注入未注册 Go 类型时，按自动宿主类型映射生成完整类型名。
- `ResumeInput.Err` 注入 Go `error`，执行器按普通错误路径处理。
- 恢复完成后，routine 从挂起表达式之后继续顺序执行。

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

- `error == nil`：表达式结果为 `T`。
- `error != nil`：默认执行模式中断当前表达式。
- `try` 启用后：`error` 作为普通 `error` 类型值返回，后续只能调用其 Go 方法集。

### 9.1.1 错误短路语义

默认模式下，一个 Go 调用返回 `error != nil` 时的冒泡规则：

1. 中断当前**宿主调用表达式**的求值。
2. 该 error 作为当前语句的失败原因，**中止当前语句**（`let`/`set`/`expr`/`return`/`panic` 等）。
3. 继续冒泡到包含该语句的最近一层**控制结构**：
   - 在 `if.then` / `if.else` / `foreach.do` / `fori.do` / `block` 内，中止整个该控制结构块的剩余语句。
   - 冒泡穿过循环时，不执行后续迭代。
4. 继续冒泡到程序顶层。
5. 冒泡过程中遇到最近一层的 `try` 语句时被捕获：此时 error 被转化为 typed value `error`，交给 `try` 的 catch 分支处理，冒泡停止。
6. 没有 `try` 捕获且冒泡到顶层：`program.Run` 返回该 Go `error`，执行终止。

`routine` 只包单个宿主调用，其 error 直接进入宿主 `RoutineErrorHandler`，不走上述冒泡流程（routine 内没有可冒泡的语句序列）。

冒泡过程中被跳过的语句完全不执行，其副作用也不会产生。已执行的副作用（已经完成的 Go 调用）不会被回滚；如需原子性由宿主函数自身处理（例如基于 `context.Context` 传入的事务）。

`error` 值和 `null` 值通过类型区分：一个返回 `(T, error)` 的 Go 调用在 `error == nil` 时产生 `T`（可能是 `null`），在 `error != nil` 时触发上述冒泡流程，而不是产生一个 `error` 类型 typed value。只有 `try` 捕获后才会得到 `error` 类型 typed value。

### 9.1.2 `null` receiver 调用

调用方法时 receiver 求值结果为 `null`（或 Go 侧的 nil 指针/nil 接口）的处理：

- 编译期无法判定 receiver 非 null 时，生成运行期 null 检查。
- 运行期 receiver 为 null 时，**不**进入 Go 反射调用，直接产生 `E_NIL_RECEIVER` 诊断错误。
- `E_NIL_RECEIVER` 按 9.1.1 错误短路语义冒泡，可被 `try` 捕获。
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

## 14. 当前需要改善的功能

### 14.1 类型系统改善

| 改善项 | 目标 |
|--------|------|
| `null` 独立类型 | 空值语义清晰化 |
| 参数数量严格校验 | 编译期捕获函数调用错误 |
| 反射调用安全检查 | 类型错配转诊断错误 |
| typed literal 构造错误直返 | 配置错误尽早暴露 |
| `array<T>` | 数组元素类型检查 |

### 14.2 表达式改善

| 改善项 | 目标 |
|--------|------|
| `array<T>` typed literal | 支持数组常量和元素类型校验 |
| path 包函数 | 统一读取、写入、存在判断 |
| `match` 表达式 | 多分支值匹配 |

### 14.3 程序执行改善

| 改善项 | 目标 |
|--------|------|
| 编译后 Program | 运行阶段复用解析结果 |
| typed AST | 执行阶段减少动态判断 |
| 预算控制 | 防止复杂配置耗尽资源 |
| `block` | 显式作用域 |
| `try` | 错误值化 |
| `assert` | 程序内契约 |
| `panic` | 映射 Go `panic` 关键字 |
| `routine` | 映射 Go `go` 关键字 |
| 顶级上下文 | 宿主注入变量和包 receiver |

### 14.4 Go 桥接改善

| 改善项 | 目标 |
|--------|------|
| context 参数注入 | 支持取消、超时、trace |
| Env 参数注入 | 传递当前路径、registry、logger |
| 函数元数据 | 支持文档、权限、成本 |
| capability 检查 | 宿主能力授权 |
| 反射计划缓存 | 降低调用成本 |
| 注册错误聚合 | 启动阶段发现全部注册问题 |
| 顶级包注入 | session 级包 receiver 可参与分派 |

### 14.5 静态分析改善

| 改善项 | 目标 |
|--------|------|
| 变量声明跟踪 | `let` 后续引用获得类型 |
| `set` 类型检查 | 赋值安全 |
| 条件布尔检查 | 分支条件语义稳定 |
| 分支返回类型合并 | 返回类型可预测 |
| JSON Pointer 错误路径 | 配置定位精确 |
| 多错误聚合 | 一次修复多处错误 |

### 14.6 文档与工具改善

| 改善项 | 目标 |
|--------|------|
| 语言 schema | IDE 和平台校验 |
| 标准库文档生成 | Registry 即文档源 |
| 示例测试联动 | 文档示例全部可运行 |
| Formatter | 稳定格式化 |
| Linter | 配置质量把关 |
| Conformance suite | 第三方宿主可验证实现一致性 |

---

## 15. 需要完成的功能路线图

### 15.1 第一阶段：语言内核稳定

目标：让语言内核可作为独立 Go package 使用。

任务：

1. 建立 `LANGUAGE.md` 与 `README.md` 的职责分工。
2. 固化 JSON 表达式语法。
3. 固化 `array<T>` typed literal。
4. 完成 `null` 类型。
5. 让 `DefaultRegistry()` 可选择携带核心 intrinsic。
6. 完成函数调用参数数量严格校验。
7. 完成 reflect 调用 panic 保护。
8. 完成 `panic` 操作符。
9. 完成 `routine` 操作符。
10. typed literal 构造失败返回诊断错误。
11. 统一诊断错误码和 JSON Pointer path。
12. 建立 compile/run API。
13. 建立顶级上下文注入 API。

验收标准：

- 单独引入 wflang package 即可编译并运行 JSON 程序。
- 诊断错误都带 path、code、message。
- 函数调用类型错配返回错误。
- 标准示例全部进入测试。

### 15.2 第二阶段：强类型编译器

目标：编译期发现绝大多数配置问题。

任务：

1. typed AST。
2. 变量符号表。
3. `let/set` 类型传播。
4. `if`、`foreach`、`return` 类型检查。
5. `array<T>`。
6. 函数签名支持可选参数和可变参数。
7. 多错误聚合。
8. Explain 静态报告。

验收标准：

- 类型错误在 `Compile` 阶段返回。
- `Explain` 能列出变量、函数、能力、返回类型。
- typed AST 执行结果和动态执行结果一致。

### 15.3 第三阶段：宿主桥接完善

目标：Go 宿主能安全、清晰、低成本地扩展语言。

任务：

1. `FuncSpec` 元数据注册。
2. context / Env 参数注入。
3. capability 授权。
4. 函数调用 timeout。
5. 反射计划缓存。
6. 自动宿主类型方法导出。
7. Registry 文档自动生成。
8. Registry 语言规格导出。
9. Go Config Builder。
10. 宿主函数单测工具。

验收标准：

- 宿主函数注册即可生成语言文档和 Builder 元数据。
- Builder 生成的 JSON 可直接通过 `CompileJSON`。
- 缺少 capability 的程序在编译阶段或执行前报告。
- context cancel 能中断执行。

### 15.4 第四阶段：宿主标准库能力

目标：常见 JSON 逻辑直接使用标准库完成。

任务：

1. 字符串、数字、数组、路径、类型转换标准库。
2. `match` 表达式。
3. `try/assert/error/panic/routine`。
4. 日期时间标准库，以 capability 控制当前时间读取。
5. JSON 标准库。
6. Regex 标准库。

验收标准：

- 常见数据转换无需宿主新增函数。
- 标准库全部有文档和测试。

### 15.5 第五阶段：工具链极致化

目标：语言具备工程化体验。

任务：

1. JSON Schema。
2. Formatter。
3. Linter。
4. Test runner。
5. Trace viewer 数据格式。
6. Conformance test suite。
7. Benchmark suite。
8. Fuzz suite。
9. 版本迁移器。
10. 变更日志生成。

验收标准：

- 任何 JSON 程序都可 format、lint、test、explain。
- 新版本通过 conformance suite。
- 核心执行性能有基准数据。

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
