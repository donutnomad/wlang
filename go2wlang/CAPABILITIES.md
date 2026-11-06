# go2wlang 能力权威参考（CAPABILITIES）

> **这是判断「go2wlang 能不能翻译某个 Go 构造」的权威文档。要用直接查这里，不要再写探针试探。**
> 证据来源：`translate.go`（翻译器主体，行号引用它）+ `*_test.go`（能力的最硬证据）。以源码/测试为准。
> 配套 `SUPPORT.md` 是能力矩阵清单；本文档补齐它缺的**边界机制、翻译产物形态、踩坑原因**。

---

## 0. 快速判断速查表（最常问的）

| 问题 | 答案 | 依据 |
|---|---|---|
| **取地址 `&x`？** | 仅 `&ident` / `&selector` 作**输出参数**（`.Get(ctx, &out)`）→ `{"out":name}`。`&rp` 当值传给指针字段 ❌、`&index`（`&xs[i]`）❌、`&CompositeLit{}`（`&T{}`）❌ | :1216-1217, :1842-1848 |
| **指针字段 `F *T`？** | ❌ 运行时 `E_TYPE: cannot assign T to *T`。**改用值字段 `F T` + 内联值字面量** | 见 §3 大坑 |
| **嵌套 struct 值字面量 `T{Inner: U{...}}`？** | ✅ 能翻，含 int64/float64/string/bool/[]string 字段 | :1643-1740 |
| **闭包 `func(...){...}`？** | ✅ 能翻 → `{"fn":{params,returns,do}}`；参数**必须命名**、参数/返回类型必须可翻 | :1539-1590 |
| **duration / time.Duration？** | ❌ 无 duration 类型。**用 string（如 "30s"）在 host 侧 ParseDuration** | 见 §3 |
| **`i = i + 1` 作 for post？** | ❌ 只支持 `i++`/`i--`/`i += step`/`i -= step` | :1011-1030 |
| **`return a, b`（多返回值）？** | ✅ 翻成 `{"return":{"tuple":[a,b]}}`；命名结果另见 `return.named` 列表 | `translate.go` ReturnStmt |
| **可变参数展开 `fn(xs...)`？** | ❌ | SUPPORT.md |
| **`&T{}`（取地址复合字面量）？** | ❌ `address-of expression is not supported`。用局部变量 + `&local`（但 `&local` 也只在输出参数位合法） | :1848 |
| **map/slice/array 字面量？** | ✅ 全支持（省略键、带键、键值元素） | :1643-1696 |
| **panic / defer / go / select / channel？** | ✅ 支持（defer 仅调用表达式，go 支持 func literal） | SUPPORT.md §Channel |
| **goto / recover / init 函数 / 包级变量？** | ❌ | :412, SUPPORT.md |

---

## 1. 取地址 `&` 与解引用 `*` 的确切边界（最大踩坑区）

### 1.1 一元表达式全集（translate.go:1215-1257）
| Go | 翻译产物 | 说明 |
|---|---|---|
| `&x` | `t.outExpr(x)` → `{"out":name}` | **仅输出参数场景**，见 1.2 |
| `+x` | 透传 `x` | |
| `-x` | `{"-":[{"literal":"0"}, x]}` | |
| `^x` | `{"bit.not":[x]}` | |
| `*ptr` | `{"ptr.deref":[ptr]}` | 解引用**支持** |
| `!x` | `{"!":[x]}` | |
| `<-ch` | `{"ch.recv":[ch]}` | |
| 其它一元 | ❌ `unary operator is not supported` | :1257 |

### 1.2 `&`（address-of）走 `outExpr`——只在输出参数位合法
`&` 直接调 `t.outExpr(x.X)`（:1216-1217）。`outExpr`（:~1840-1873）：
- `&ident` → `{"out":name}`（:1873）✅
- `&selector`（`&obj.Field`）→ 带路径的 `out` ✅
- `&index`（`&xs[i]`）→ ❌ `address-of index is not supported`（:1846，hint: 存局部变量再 `&local`）
- `&selector target`（某些选择器）→ ❌ `address-of selector target is not supported`（:1842）
- 一般 `&expr`（如 `&rp`、`&T{}`）→ ❌ `address-of expression is not supported`（:1848）

### 1.3 为什么 `&rp` 失败（踩坑实录）
`rp := workflow.RetryPolicy{...}; ... &rp` —— `&rp` 生成 `{"out":"rp"}`，但 `{"out":...}` 语义是「接收 host 调用的输出参数」（如 `future.Get(ctx, &out)`）。当它被当作**值传给一个指针字段**时，WLang 运行时报 `out is only valid as a call argument`。
**结论**：`{"out":name}` 只在 call 的实参位置合法；不能把 `&local` 当普通值用。指针字段赋值走不通（见 §3）。

---

## 2. 复合字面量（composite literal）翻译产物

### 2.1 三种形态（translate.go:1643-1712）
| Go | 翻译产物 JSON | 行号 |
|---|---|---|
| `[]T{a, b}` / `[]T{2: x}` | `{"array":{"elem":"T","items":[...]}}` | :1673 |
| `map[K]V{k: v}` | `{"map":{"type":["K","V"],"entries":[[k,v],...]}}` | :1696 |
| `pkg.Type{F: v}` | `{"struct":["pkg.Type",{F: v}]}` | :1703, structLiteral :1740 |
| `LocalType{F: v}` | `{"struct":["<LocalPackageName>.LocalType",{...}]}` | :1708（需 `Options.LocalPackageName`，否则 ❌ :1706） |

### 2.2 struct 字面量细节（:1714-1740）
- **带键** `T{Name: "x"}`：字段名取 key ident。
- **无键** `T{"x"}`：字段名从 Go 类型信息推断（`structFieldNames` :1743，靠 `t.info.Types`）。
- 字段值递归走 `t.expr`——**嵌套复合字面量支持**（值字面量，非取地址）。
- 值多于字段 → ❌ `struct literal has more values than fields`（:1721）；key 非 ident → ❌（:1732）。
- **[]string 字段** → 翻成 `{"array":{"elem":"string","items":[...]}}`（已实测，B 任务 RetryPolicy.NonRetryableErrors）。
- **任意字段自动翻译**：给 struct 加字段无需改 sdkgen，只要字段类型可翻（B 任务实证：ActivityOptions 加超时字段自动过线）。

### 2.3 范围外
- `&T{}`（取地址复合字面量）❌（走 §1.2 的 address-of 拒绝）。
- 复合字面量内部嵌套「当前范围外的表达式」❌（SUPPORT.md:198）。

---

## 3. 类型系统：值 vs 指针的大坑

### 3.1 指针类型字段 ❌（重点）
Go struct 里的**指针字段** `F *T`：
- go2wlang 能翻字面量本身，但**运行时**赋值报 `E_TYPE: cannot assign T to *T`（传值 struct 给指针字段）。
- 且 `&rp`（取地址产生指针值）走不通（§1.3）。
- **对齐做法**：marker/DSL struct 用**值字段 `F T`（非指针）+ 内联值字面量**。B 任务 RetryPolicy 就是这样从 `*RetryPolicy` 改成值类型 `RetryPolicy` 才走通的。

### 3.2 duration / time ❌
- 无 duration 类型。**约定用 string**（如 `"30s"`），在 host 侧 `time.ParseDuration` 解析。config raw 层同款（超时字段都是 string）。

### 3.3 typeName 决定「类型能否翻」
`typeName(ast.Expr)` 返回类型名字符串，空串 = 不支持。用于：array elem、map key/value、闭包 params/returns、struct 前缀。返回空则对应处报 unsupported。**支持的类型全集见下方独立 §3.3 表**（int→int64、bool→boolean 等映射）。

---

## 4. 函数 / 闭包

### 4.1 闭包 funcLit（translate.go:1539-1590）
`func(params) returns { body }` → `{"fn":{"params":[[name,type],...],"returns":[type,...],"do":[...body...]}}`
- **参数必须命名**（:1548 `function literal parameters must be named`）。
- 参数类型、返回类型必须 `typeName` 可翻（:1545/:1560）。
- 闭包体走 `stmtList`，参数加入局部作用域（:1574-1584）。
- **捕获**：闭包体内引用外层变量按普通 ident 翻（局部作用域链）。

### 4.2 闭包的使用场景（已知能翻）
- `go func(){...}()`（:88-90 SUPPORT）、`go func(arg T){...}(value)`（参数数量须匹配 :1040）。
- `defer func(){...}()`（:87）。
- workflow.Go / Await / Selector 的回调闭包（sdkgen 层收集，本翻译器翻成 `fn`）。
- **guard-and-bail**：`if err := f.Get(...); err != nil { return ... }`——if-Init 支持（:71 SUPPORT），Init 里的调用正常翻。

---

## 5. 明确不支持清单（每条附报错原文 + 行号）

（先落已确认的，剩余追加）

| 构造 | 报错原文 | 行号 |
|---|---|---|
| 独立 `i++`/`i--`（非 for post） | `standalone inc/dec is only supported as for post` | :410 |
| `&index`（`&xs[i]`） | `address-of index is not supported` | :1846 |
| `&expr`（`&rp`/`&T{}`） | `address-of expression is not supported` | :1848 |
| `&selector target` | `address-of selector target is not supported` | :1842 |
| for 缺 init/cond/post | `for requires init, condition, and post` | :947 |
| for post 非 `i++`/`i--`/`i += step` | `for post must be i++/i-- or compound step` | :1030 |
| 一般一元运算符 | `unary operator is not supported` | :1257 |
| 一般二元运算符 | `binary operator is not supported` | :1204 |
| 可变参数展开 `fn(xs...)` | （SUPPORT.md 范围外） | — |
| goto / 独立 inc-dec / 省略段 for / 多 init-post for | — | SUPPORT.md:96-99 |
| defer 非调用 / go func 带参调用 / 包级变量 / init / recover | — | SUPPORT.md:100-104 |
| 赋值目标非法（复杂写） | `assignment target is not supported` | :740 |
| append 需 `=` | `append assignment requires =` | :751 |
| 局部 struct 字面量缺包名 | `local struct literal requires a package name` | :1706 |
| type switch guard 非 `x.(type)` | `type switch guard must be x.(type)` | :620/:630 |

（完整 ~55 处 unsupported 见 §7 全表，追加中。）

---

## 6. 翻译产物 JSON 形态速查

| 构造 | JSON |
|---|---|
| 局部变量引用 | `{"var":"name"}` |
| 字面量 | `{"literal":{"type":"int64|string|float64|bool|null","value":...}}` |
| 输出参数 `&x` | `{"out":"name"}` |
| struct 字面量 | `{"struct":["pkg.Type",{fields}]}` |
| array/slice 字面量 | `{"array":{"elem":"T","items":[...]}}` |
| map 字面量 | `{"map":{"type":["K","V"],"entries":[[k,v]]}}` |
| 闭包 | `{"fn":{"params":[[n,t]],"returns":[t],"do":[...]}}` |
| 解引用 `*p` | `{"ptr.deref":[p]}` |
| channel 接收 `<-ch` | `{"ch.recv":[ch]}` |
| 调用 | `{"call":{"fn":...,"args":[...]}}` / host: `{"MethodName":[recv, args...]}` |
| 索引 `xs[i]` | `arr.get(xs, i)` |
| map 取值 `m[k]` | `map.value(m,k)` / 双值 `map.get(m,k)` |

---

## 3.3 typeName 支持的类型全集（translate.go:1987-2018 + goTypeToWlang）
`typeName` 返回空串 = 不支持（对应处报 unsupported）。支持：
| Go 类型 | WLang 类型名 | 说明 |
|---|---|---|
| `string` | `string` | |
| `bool` | `boolean` | ⚠️ 名字变了 |
| `int` | `int64` | ⚠️ int 统一映射为 int64 |
| `int64/32/16/8`, `uint64/32/16/8`, `float64/32` | 原样 | |
| `error`, `any` | 原样 | |
| `[]T` | `array<T>` | 元素类型须可翻 |
| `map[K]V` | `map<K,V>` | K/V 须可翻 |
| `*T` | `*T` | **类型名能翻，但赋值运行时炸（§3.1）** |
| `func(...)...` | `func<(...)->...>` | 参数/返回须可翻 |
| `pkg.Type` | selectorPath | 包限定类型 |
- **不认识的自定义 Ident 类型**：`goTypeToWlang` 原样返回名字（不报错，当作已注册 host 类型名透传）。
- 复合类型元素不可翻 → 整体返回空 → 报 unsupported。

---

## 8. 控制流产物形态（translate.go）

| Go | 翻译产物 JSON | 行号 |
|---|---|---|
| `if cond {then} else {else}` | `{"if":{"cond":...,"then":[...],"else":[...]}}` | :480-484 |
| `if init; cond {...}` | Init 语句提到 if 前，然后 if（Init 里的调用/赋值正常翻） | :71 SUPPORT |
| `else if` | 嵌套 `{"if":...}` 进 else（:557-561） | :557 |
| 计数 `for i:=from; i<to; i+=step` | `{from, to, step, body}`（:990-993） | :990 |
| `for i,v := range xs` | `{"foreach": loop}`（:942） | :942 |
| `return expr` | `{"return": expr}`；命名返回 `{"return":{"named":"err"}}` 或 `{"return":{"named":["out","err"],"expr":...}}`；无值 `{"return":{"literal":null}}` | `translate.go` ReturnStmt |
| 表达式/条件 `switch` | case clauses（:532 起） | :530+ |
| 类型 switch | `type.is` / `type.assert`（:73 SUPPORT） | :620+ |
| `select` | 含 send/receive/default case | :1087+ |
| `panic(v)` | `{"panic":[v]}`（须恰好 1 参 :367） | :367 |

### if-Init / guard-and-bail（常用形态，✅ 支持）
`if err := f.Get(ctx, &out); err != nil { return err }` —— Init 里的 `f.Get(...)` 正常翻，`&out`→`{"out":"out"}`（作 call 实参合法），条件+return 正常。这是 workflow 里 activity 调用的惯用形态。

---

## 9. 完整 unsupported 报错全表（translate.go，60 处，Ctrl-F 查报错原文）

| 行 | 报错原文 | 类别 |
|---|---|---|
| 319 | only var declarations are supported | 声明 |
| 362 | branch statement is not supported | goto 等 |
| 367 | panic requires exactly one argument | panic |
| 410 | standalone inc/dec is only supported as for post | **独立 `i++` ❌** |
| 412 | statement is not supported | 兜底 |
| 429 | only value specs are supported | var |
| 432 | var declaration value count is unsupported | var 多值 |
| 532 | switch body must contain case clauses | switch |
| 620/630 | type switch guard must be x.(type) | type switch |
| 626 | type switch assignment guard must have one target and one value | |
| 634 | type switch target must be identifier | |
| 639 | type switch guard is not supported | |
| 653 | type switch body must contain case clauses | |
| 707 | type switch case type is not supported | |
| 740 | assignment target is not supported | 复杂写 |
| 751 | append assignment requires = | append 须 `=` |
| 770/833/863 | assignment operator is not supported | 赋值符 |
| 793 | multi-value assignment requires one RHS expression | 多值赋值须单 RHS（host tuple） |
| 801 | tuple assignment targets must be identifiers | |
| 820 | selector assignment target is not supported | `obj.F = ` ❌ |
| 868 | append requires a slice and at least one value | |
| 872 | append target must match assignment target | |
| 907 | else branch must be block or if | |
| 947 | for requires init, condition, and post | **省略段 for ❌** |
| 951 | for init must be i := from | |
| 955 | for variable must be identifier | |
| 963 | for condition must be i < to or i >= to | |
| 967 | for condition must use loop variable | |
| 1003/1019 | for post must use/update loop variable | |
| 1011 | for post must be i++ or i-- | |
| 1015 | for post must be i += step or i -= step | **`i=i+1` ❌** |
| 1030 | for post must be i++/i-- or compound step | |
| 1040 | go function literal argument count does not match parameters | |
| 1087-1146 | select ... （6 处 case 形态限制） | select |
| 1204 | binary operator is not supported | |
| 1257 | unary operator is not supported | |
| 1313 | expression is not supported | 兜底 |
| 1330 | literal kind is not supported | |
| 1340-1407 | int64/delete/cap/new/close/len/append 内建参数数错 | 内建 |
| 1376 | new type is not supported | |
| 1518 | type assertion target is not supported | |
| 1545 | function parameter type is not supported | **闭包参数类型不可翻 ❌** |
| 1548 | function literal parameters must be named | **闭包参数须命名 ❌** |
| 1560 | function result type is not supported | 闭包返回类型 |
| 1595-1603 | make/channel 限制 | make |
| 1619 | only selector calls are supported | 调用须 pkg.F / recv.M |
| 1648-1710 | 复合字面量各类限制 | composite |
| 1842-1848 | address-of 三种（selector-target/index/expr） | **`&` ❌** |
| 1706 | local struct literal requires a package name | |

**范围外（SUPPORT.md，非 translate.go 报错但同为不支持）**：goto、独立 inc/dec、省略段/多 init-post for、defer 非调用、go func 带参调用、包级变量、init 函数、recover、可变参数展开 `fn(xs...)`、点导入、整包/多函数翻译。

---

## 10. 符号 / 包解析（package_resolver.go）

- 包函数调用 `pkg.Func(args)` → host 调用透传 / `{"symbol":...}`。
- 符号解析支持：显式 import 别名、路径末段匹配、go.work / replace / vendor / GOMODCACHE、项目内导入、局部变量遮蔽 import 别名、block 级作用域跟踪。
- **区分包选择器 vs 接收者选择器**：靠 Go 类型信息（`t.info`）；类型信息缺失时回退启发式。
- host 方法调用 `recv.Method(args)` → `{"Method":[recv, args...]}`（透传给 host 注册的方法）。
- 范围外：`golang.org/x/tools/go/packages` 全模块图、远程下载、点导入、空白导入作可调用。

---

## 11. 测试证据交叉引用（能力的最硬证明）

| 能力 | 测试证据 |
|---|---|
| 闭包翻成 `"fn"` | translate_test.go:255（断言 `"fn": {`），:225 compensations append 闭包 |
| 闭包作参数值 | translate_test.go:372/383（`func(string) string` → `{"var":"fn"}`） |
| `&out` 输出参数 | translate_test.go:612（断言 `"out"`），:683 `.Get(ctx, &out)` |
| go func literal | translate_test.go:74（`go func(){}`），expanded_support_test.go:108（带参 `go func(v string)`） |
| defer 闭包 | translate_test.go:203 |
| 嵌套 struct / 任意字段 | B 任务实证（RetryPolicy 值字面量 + []string 字段翻成 array） |
| 类型感知（包 vs 接收者选择器） | typeaware_test.go 全套 |
| 符号翻译 | symbol_translation_test.go 全套 |
| 语义等价（翻译前后行为一致） | equivalence_test.go 全套 |
| 扩展支持（channel/select/go/defer） | expanded_support_test.go 全套 |

---

## 12. SUPPORT.md 勘误（对照源码）

SUPPORT.md 整体准确，是能力清单；本文档补其缺的**边界机制**。发现的不精确处：
1. **SUPPORT.md:129-130「取址输出参数 `&ident`/`&selector` 生成 `out`」** —— 正确但**没讲边界**：`&` 仅在**输出参数位**（call 实参）合法，`&rp` 当值传给指针字段会运行时 `out is only valid as a call argument`；`&index`/`&T{}` 直接翻译期报 unsupported。本文档 §1 补全。
2. **SUPPORT.md:119「指针解引用 `*ptr`」列为支持** —— 对（`{"ptr.deref"}`），但**指针字段赋值**（`F *T` 传值）运行时炸 `E_TYPE`，SUPPORT.md 未提。本文档 §3.1 补全。
3. **SUPPORT.md 未提 duration** —— 无 duration 类型，约定用 string。本文档 §3.2 补。
4. SUPPORT.md:97「循环 post 外的独立 i++」与 translate.go:410 一致（`standalone inc/dec is only supported as for post`）✓。
5. **无实质错误**——SUPPORT.md 可信，本文档是它的「机制/踩坑」增强层，二者配合查。
