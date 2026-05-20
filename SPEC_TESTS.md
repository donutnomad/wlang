# wflang 验收测试用例

本文件把 `LANGUAGE.md` 拆解为可独立验证的测试用例 / 验收标准（TC）。每条 TC 给出：

- **Spec**：对应 `LANGUAGE.md` 章节
- **Given**：前置条件 / 注册 / 输入
- **When**：执行动作（Compile / Run / AppendRun / await 等）
- **Then**：期望结果（typed value、错误码、状态、副作用）

错误码以 `LANGUAGE.md §9.3` 为准。所有常量必须用 typed literal。

---

## §1 定位

### TC-001 语言不允许定义 function
- Spec: §1.1
- Given: 任意包含 `{"function": ...}` / `{"def": ...}` / `{"lambda": ...}` 的 JSON 程序
- When: `engine.CompileJSON(data)`
- Then: 返回 `E_AST_SHAPE` 或 `E_SYMBOL`，message 指出语言不支持用户函数定义

### TC-002 所有可调用能力来自 Registry
- Spec: §1.1
- Given: 空 Registry（无 `BindGoPackage` / `AutoBindType`）
- When: 编译 `{"Foo": [{"pkg":"bar"}]}`
- Then: 返回 `E_SYMBOL`，无法解析 `pkg:bar`

---

## §2.1 类型系统

### TC-010 内置整数类型完整覆盖
- Spec: §2.1
- Given: 对每个类型 ∈ {`uint8`,`uint16`,`uint32`,`uint64`,`int8`,`int16`,`int32`,`int64`} 构造 typed literal
- When: 求值 `{"literal":{"type":T,"value":"1"}}`
- Then: 结果 typed value 的 `TypeName()==T`，Go 承载类型与表 §2.1 一致

### TC-011 浮点类型映射
- Spec: §2.1
- Given: `float32`/`float64` typed literal
- When: 求值
- Then: Go 承载分别为 `float32`/`float64`

### TC-012 boolean / string / null / error 映射
- Spec: §2.1
- Given: `boolean`/`string`/`null`/`error` typed literal（`null.value` 必须为 JSON `null`）
- When: 求值
- Then: 类型名与 Go 承载匹配；`null` 的 Go 值为 `nil`

### TC-013 bigInt / bigDecimal 映射
- Spec: §2.1
- Given: `{"literal":{"type":"bigInt","value":"100000000000000000000"}}` 与 `bigDecimal` `"1.000"`
- When: 求值
- Then: Go 承载分别为 `*big.Int` / `*big.Rat`，数值精确无截断

### TC-014 array<T> 元素类型保持
- Spec: §2.1
- Given: `array<int64>` typed literal 含三个 int64 元素
- When: 求值
- Then: Go 承载为 `[]int64`（或 wrapper），元素类型校验通过

### TC-015 禁止使用 int / uint 平台依赖宽度
- Spec: §2.1
- Given: typed literal `{"type":"int","value":"1"}`
- When: 编译
- Then: 返回 `E_TYPE`（语言层不接受 `int`/`uint`）

### TC-016 自动宿主类型名格式
- Spec: §2.1
- Given: 注册了一个返回 `*github.com/acme/books.Book` 的包函数，但未 `AutoBindType("book", ...)`
- When: 调用并取结果类型名
- Then: 类型名 == `*github.com/acme/books.Book`

### TC-017 自动宿主类型可继续传递
- Spec: §2.1
- Given: 接 TC-016 结果存入 `let x`，再传给 `func PrintBook(*Book)`
- When: 编译并执行
- Then: 编译期通过类型匹配，运行期成功调用

### TC-018 操作符覆盖：数字
- Spec: §2.1
- Given: 对 `+ - * / > >= < <= == !=` 构造典型 int64 / float64 表达式
- When: 求值
- Then: 结果与 Go 同名运算结果一致

### TC-019 操作符覆盖：字符串
- Spec: §2.1
- Given: `+`、`==`、`!=`、`contains`、`endsWith` 表达式
- When: 求值
- Then: 等价于 Go `string` 拼接 / 比较 / `strings.Contains` / `strings.HasSuffix`

### TC-020 操作符覆盖：bigInt / bigDecimal
- Spec: §2.1
- Given: bigInt 加减乘除与比较；bigDecimal 同上
- When: 求值
- Then: 结果与 `math/big` 等价；除法精度策略由 Registry 决定，确定可重现

---

## §2.2 表达式求值

### TC-030 字面量加法
- Spec: §2.2
- Given: `{"+": [{"literal":{"type":"int64","value":"1"}}, {"literal":{"type":"int64","value":"2"}}]}`
- When: 求值
- Then: typed value `int64=3`

### TC-031 var 读取嵌套路径
- Spec: §2.2
- Given: `Vars={user:{name:"alice",status:"active"}}`，表达式 `{"==":[{"var":"user.status"},{"literal":{"type":"string","value":"active"}}]}`
- When: 求值
- Then: typed value `boolean=true`

### TC-032 var 缺省值（路径缺失）
- Spec: §2.2
- Given: `Vars={user:{}}`，表达式 `{"var":["user.name", {"literal":{"type":"string","value":"anonymous"}}]}`
- When: 求值
- Then: typed value `string="anonymous"`

### TC-033 typed literal int64 / bigInt
- Spec: §2.2
- Given: 同表中 typed literal 示例
- When: 求值
- Then: 类型与值一致

### TC-034 逻辑 and / or / !
- Spec: §2.2
- Given: 三组真值组合
- When: 求值
- Then: 与 Go bool 短路语义一致；`and` 在第一个 `false` 后不再求值后续表达式

### TC-035 if 表达式（对象形）
- Spec: §2.2 / §16.4
- Given: `{"if":{"cond":expr,"then":[stmts],"else":[stmts]}}`
- When: 求值
- Then: 仅执行命中分支

### TC-036 let / set 基本语义
- Spec: §2.2
- Given: `let x=1; set x=2`
- When: 顺序执行后 `return x`
- Then: 返回 int64=2

### TC-037 操作符调用是 JSONLogic 单键
- Spec: §3.2
- Given: `{"+": [...], "-": [...]}` 同对象多键
- When: 编译
- Then: `E_AST_SHAPE`

### TC-038 包函数调用：Len(str,"hello")
- Spec: §2.2
- Given: 注册 `str.Len`
- When: 求值 `{"Len":[{"pkg":"str"},{"literal":{"type":"string","value":"hello"}}]}`
- Then: int64=5

---

## §2.3 作用域

### TC-050 PushScope / PopScope 嵌套
- Spec: §2.3
- Given: 嵌套两层 `block`（或 `if.then`），内层 `let x`
- When: 退出内层
- Then: 外层 `var x` 不可见，返回 `E_SYMBOL` 或缺省值

### TC-051 SetVar 向外查找
- Spec: §2.3
- Given: 外层 `let x=1`，内层 `set x=2`
- When: 退出内层后 `return x`
- Then: int64=2

### TC-052 LookupPath 支持 struct json tag
- Spec: §2.3
- Given: Go struct `User{Name string \`json:"name"\`}`，注入 `Vars={u:&User{Name:"a"}}`
- When: `{"var":"u.name"}`
- Then: string="a"

### TC-053 LookupPath 支持数组下标
- Spec: §2.3
- Given: `Vars={items:[10,20,30]}`
- When: `{"var":"items.1"}`
- Then: int64=20（或对应注入类型）

### TC-054 顶级变量默认只读
- Spec: §2.3 / §3.5
- Given: `Vars={input:...}` 未在 `VarOptions` 中开启 Writable
- When: `{"set":{"input":...}}`
- Then: `E_READONLY_VAR`

### TC-055 顶级变量可显式声明可写
- Spec: §3.5
- Given: `VarOptions={counter:{Writable:true}}`，`Vars={counter:0}`
- When: `set counter=1` 后 `return counter`
- Then: int64=1

### TC-056 pkg 与 var 命名空间隔离
- Spec: §3.5
- Given: 同名 `risk` 既是包又是顶级变量
- When: `{"var":"risk"}` 与 `{"pkg":"risk"}`
- Then: 各自命中对应命名空间，不相互污染

---

## §2.4 程序执行器

### TC-070 语句数组顺序执行 + return
- Spec: §2.4
- Given: §2.4 的 sum prices 程序
- When: `program.Run`
- Then: 返回 typed value bigDecimal=∑item.price

### TC-071 已具备语句矩阵
- Spec: §2.4
- Given: 单测覆盖 `let / set / if / foreach / return`
- When: 各自最小程序
- Then: 全部通过

### TC-072 规划中语句的占位
- Spec: §2.4
- Given: `fori / break / continue / panic / routine` 任一
- When: 编译当前未实现版本
- Then: 返回 `E_SYMBOL` 或对应"规划中"诊断；实现后转入 §3.3 系列 TC

---

## §2.5 Go 宿主桥接

### TC-080 BindGoPackage 注册并调用
- Spec: §2.5
- Given: `registry.BindGoPackage("books", booksPackage)`，包内 `FindByID(int64)(*Book,error)`
- When: 调用 `{"FindByID":[{"pkg":"books"},{"literal":{"type":"int64","value":"1001"}}]}`
- Then: 成功返回 *Book typed value

### TC-081 包名重复注册报错
- Spec: §2.5
- Given: 同名包二次 `BindGoPackage`
- When: 注册
- Then: 返回诊断错误（启动期）

### TC-082 函数名严格大小写
- Spec: §2.5 / §7.4
- Given: 包函数 `FindByID`
- When: 调用 `{"findById":[...]}`
- Then: `E_SYMBOL`，不做驼峰转换

### TC-083 私有 Go 函数不可调用
- Spec: §2.5
- Given: 包内 `findByID`（小写）
- When: 注册时反射扫描
- Then: 不出现在符号表；调用返回 `E_SYMBOL`

### TC-084 Receiver 分派：pkg vs var
- Spec: §2.5
- Given: 同名 operator 同时存在包函数表和某类型方法表
- When: receiver 分别为 `{"pkg":...}` 与 `{"var":...}`
- Then: 各自走不同分派路径，无歧义

### TC-085 第一个参数为 null 时的方法调用
- Spec: §2.5 / §9.1.2
- Given: `Vars={u:nil}`，调用 `{"Run":[{"var":"u"},...]}`
- When: 执行
- Then: `E_NIL_RECEIVER`，不进入反射调用

### TC-086 AutoBindType 反射方法集
- Spec: §2.5
- Given: `*User` 含 `Run(float64)(string,error)`
- When: `AutoBindType("user", reflect.TypeFor[*User]())`
- Then: 类型方法表生成 `Run`，调用 `{"Run":[{"var":"user"},{"literal":{"type":"float64","value":"10"}}]}` 返回 string

### TC-087 BindMethodOverloads 多 Go 名称合并
- Spec: §2.5
- Given: `AddInt8/AddInt64/AddFloat64` 合并到 operator `Add`
- When: 编译 `{"Add":[{"var":"counter"},{"literal":{"type":"int64","value":"1"}}]}`
- Then: 命中 `AddInt64`

### TC-088 重载分派优先级
- Spec: §2.5 重载分派表
- Given: 同 operator 候选集 `[int8, int64, bigInt, any]`，调用方传入 `int8` typed literal
- When: 解析
- Then: 命中精确 `int8`（优先级 100）

### TC-089 重载安全数值提升
- Spec: §2.5
- Given: 候选 `[int64, float64]`，传入 `int8`
- When: 解析
- Then: 命中 `int64`（优先级 80），不命中 `float64`

### TC-090 any 兜底匹配
- Spec: §2.5
- Given: 唯一候选 `(any)`，传入任意类型
- When: 解析
- Then: 命中 any 候选，优先级 10

### TC-091 重载歧义触发 E_AMBIGUOUS_OVERLOAD
- Spec: §2.5
- Given: 两个同分候选（如显式注册 `int8`/`int8` 别名）
- When: 解析
- Then: `E_AMBIGUOUS_OVERLOAD`

### TC-092 Go error 自动暴露 Error 方法
- Spec: §2.5
- Given: `try` 捕获到 `error` typed value
- When: `{"Error":[{"var":"err"}]}`
- Then: string=err.Error()

### TC-093 前台调用 host error 按普通 error 处理
- Spec: §2.5 / §5.3
- Given: 前台宿主调用返回 Go error
- When: 执行
- Then: 走默认错误短路

---

## §2.5.1 字面量类型规则

### TC-100 裸 JSON 数字非法
- Spec: §2.5.1
- Given: `{"Add":[{"var":"counter"}, 1]}`
- When: 编译
- Then: `E_AST_SHAPE`，提示需要 typed literal

### TC-101 string typed literal
- Spec: §2.5.1
- Given: `{"literal":{"type":"string","value":"hello"}}`
- When: 求值
- Then: string="hello"

### TC-102 boolean typed literal
- Spec: §2.5.1
- Given: `{"literal":{"type":"boolean","value":"true"}}`
- When: 求值
- Then: boolean=true

### TC-103 float64 typed literal
- Spec: §2.5.1
- Given: `"1.0"`
- When: 求值
- Then: float64=1.0

### TC-104 null typed literal
- Spec: §2.5.1
- Given: `{"literal":{"type":"null","value":null}}`
- When: 求值
- Then: typed value 类型名=`null`，Go=nil

### TC-105 typed literal value 类型/格式错误
- Spec: §2.5.1 / §14.1
- Given: `{"literal":{"type":"int64","value":"abc"}}`
- When: 编译或加载
- Then: 立即报错（编译期/加载期），不延迟到运行期

### TC-106 重载歧义 typed literal 收敛
- Spec: §2.5.1
- Given: 候选 `AddInt64/AddFloat`，传入 int64 typed literal `"1"`
- When: 解析
- Then: 唯一命中 `AddInt64`

---

## §2.6 静态校验

### TC-120 函数缺失返回 E1001
- Spec: §2.6 错误码表
- Given: 调用未注册 operator
- When: 编译
- Then: `E1001` / `E_SYMBOL`（视实现统一）

### TC-121 类型匹配失败 E1002
- Spec: §2.6
- Given: `+` int64 与 string
- When: 编译
- Then: `E1002` / `E_TYPE`

### TC-122 变量缺失 E1003
- Spec: §2.6
- Given: `{"var":"unknown"}` 且无缺省值
- When: 编译/运行
- Then: `E1003`

### TC-123 参数数量错误 E1004
- Spec: §2.6
- Given: 函数签名 2 个参数，传 1 个
- When: 编译
- Then: `E1004`

### TC-124 AST 结构异常 E1005
- Spec: §2.6
- Given: `{"+":"not-array"}`
- When: 编译
- Then: `E1005`

### TC-125 typed literal 不支持的类型 E1007
- Spec: §2.6
- Given: `{"literal":{"type":"unknown_t","value":"x"}}`
- When: 编译
- Then: `E1007`

### TC-126 LangError 携带 path / hint
- Spec: §2.6 / §9.2
- Given: 任一编译错误
- When: 编译
- Then: 返回的 `LangError` 含 `Code/Message/Path/Hint`，`Path` 为 JSON Pointer

---

## §3.1 程序形态

### TC-150 envelope 形态可执行
- Spec: §3.1
- Given: `{"lang":"wflang/v1","imports":[],"program":[stmts]}`
- When: `CompileJSON`+`Run`
- Then: 与裸 `[stmts]` 等价

### TC-151 最小程序形态：语句数组
- Spec: §3.1
- Given: `[{"return":{"literal":{"type":"int64","value":"1"}}}]`
- When: 执行
- Then: 返回 int64=1

### TC-152 渐进式执行三段
- Spec: §3.1
- Given: 顺序 AppendRun: ① `let x=1` ② `set x=x+2` ③ `return x`
- When: 三次 AppendRun
- Then: 第三次返回 int64=3

### TC-153 渐进式：return 之后追加报错
- Spec: §3.1
- Given: 已执行 `return`
- When: 再次 AppendRun
- Then: 诊断错误（session completed）

### TC-154 SessionOptions.Vars 写入根作用域
- Spec: §3.1 渐进式作用域
- Given: `SessionOptions.Vars={x:1}`
- When: AppendRun `{"return":{"var":"x"}}`
- Then: 返回注入值

### TC-155 片段中 let 落到根作用域
- Spec: §3.1
- Given: 片段 ① `let y=2`
- When: 片段 ② `return y`
- Then: 返回 int64=2

### TC-156 片段中 set 命中只读顶级 Vars
- Spec: §3.1
- Given: 顶级 `Vars={input:...}` 默认只读
- When: 片段 `set input=...`
- Then: `E_READONLY_VAR`

### TC-157 嵌套块 let 不写入根作用域
- Spec: §3.1
- Given: 片段 `if cond then let z=...`
- When: 后续片段 `var z`
- Then: `E_SYMBOL`（z 已销毁）

### TC-158 lang 版本冲突
- Spec: §3.1
- Given: session 创建为 `wflang/v1`，后续片段 envelope 声明 `wflang/v2`
- When: AppendRun
- Then: `E_LANG_VERSION_CONFLICT`

### TC-159 imports 取并集
- Spec: §3.1
- Given: 首片段 imports=[A]，后续片段 imports=[A,B]
- When: AppendRun
- Then: 注册集 = {A,B}；冲突项报错

### TC-160 routine handle 可 await
- Spec: §3.1
- Given: 片段启动 routine 并保存 handle
- When: await handle
- Then: 返回 routine 的 typed value

---

## §3.2 表达式形态

### TC-170 单键对象表达单一操作
- Spec: §3.2
- Given: `{"+":[...]}`
- When: 编译
- Then: 通过

### TC-171 多键对象作为操作符表达式非法
- Spec: §3.2
- Given: `{"+":[...], "-":[...]}`
- When: 编译
- Then: `E_AST_SHAPE`

### TC-172 array<T> 元素仍然 typed
- Spec: §3.2
- Given: `array<int64>` 元素必须是 typed literal
- When: 编译含裸数字元素的 array<int64>
- Then: `E_AST_SHAPE` / `E_TYPE`

### TC-173 包函数表达式 Len(str,"hello")
- Spec: §3.2
- Given: 同 TC-038
- When: 求值
- Then: int64=5

---

## §3.3 语句形态

### TC-190 return 立即结束程序
- Spec: §3.3 return
- Given: `[stmtA, return v, stmtB]`
- When: 执行
- Then: 返回 v；stmtB 不执行

### TC-191 return 出现在 if.then
- Spec: §3.3
- Given: if.then 含 return
- When: cond=true
- Then: 程序立即结束

### TC-192 foreach 元素绑定
- Spec: §3.3 foreach
- Given: `target=[10,20,30]`, `as=item`，`do` 内 sum+=item
- When: 执行
- Then: 返回 60

### TC-193 foreach 含 index
- Spec: §3.3
- Given: `as=item, index=i`，`do` 累加 i
- When: target 长度=3
- Then: i 从 0..2 递增；累加=3

### TC-194 foreach as 与 index 同名非法
- Spec: §3.3
- Given: `as="x", index="x"`
- When: 编译
- Then: `E_AST_SHAPE`

### TC-195 foreach target 非数组
- Spec: §3.3
- Given: `target` 求值为 int64
- When: 编译
- Then: `E_TYPE`

### TC-196 foreach 每轮新 block
- Spec: §3.3
- Given: 内 `let tmp=...`
- When: 退出 foreach 后 `var tmp`
- Then: `E_SYMBOL`

### TC-197 foreach break 退出最近一层
- Spec: §3.3 / §16.9
- Given: 嵌套 foreach，内层 break
- When: 执行
- Then: 仅退出内层；外层继续

### TC-198 fori 基本范围 [from,to)
- Spec: §3.3 fori
- Given: from=0,to=5,step=1，累加 i
- When: 执行
- Then: 累加=0+1+2+3+4=10

### TC-199 fori step=0 报错
- Spec: §3.3
- Given: step=0
- When: 编译/执行
- Then: 诊断错误

### TC-200 fori 类型不一致报错
- Spec: §3.3
- Given: from=int64, to=float64
- When: 编译
- Then: `E_TYPE`

### TC-201 break / continue 在循环外报错
- Spec: §3.3
- Given: 顶层直接 `break`
- When: 编译/执行
- Then: `E_INVALID_CONTROL_FLOW`

### TC-202 return 优先级高于 break/continue
- Spec: §3.3
- Given: 循环内同步含 return 与后续 break
- When: 执行
- Then: 返回 return 值；break 不再生效

### TC-203 panic 转 E_PANIC
- Spec: §3.3 panic
- Given: `{"panic":{"literal":{"type":"string","value":"x"}}}`
- When: 默认 EngineOptions
- Then: 返回 `E_PANIC`，message 含 "x"

### TC-204 panic 透传到 Go 宿主
- Spec: §3.3
- Given: `EngineOptions.PropagatePanic=true`
- When: panic
- Then: 执行器把 panic 抛回 Go 宿主

### TC-205 expr 丢弃返回值
- Spec: §3.3 expr
- Given: `{"expr":{"Publish":[{"pkg":"events"},...]}}`
- When: 执行
- Then: 调用产生副作用；不写入任何变量；不影响作用域

### TC-206 expr 内部错误按短路冒泡
- Spec: §3.3 / §9.1.1
- Given: `expr` 内宿主调用返回 error
- When: 执行
- Then: 中止当前语句并向外冒泡

### TC-207 routine 必须包单个宿主调用
- Spec: §3.3 routine
- Given: `{"routine":{"do":[...]}}` 或 `{"routine":[stmt1,stmt2]}`
- When: 编译
- Then: `E_AST_SHAPE`

### TC-208 routine 单调用立即返回 null
- Spec: §3.3
- Given: `{"routine":{"Publish":[{"pkg":"events"},{"var":"input.event"}]}}`
- When: 执行
- Then: 当前线程立即得到 null typed value；后台 goroutine 已启动

### TC-209 routine error 进入 RoutineErrorHandler
- Spec: §3.3 / §9.1.1
- Given: 注册 `RoutineErrorHandler`
- When: routine 内宿主调用返回 error
- Then: handler 收到该 error；不冒泡到主流程

### TC-210 routine await 获取返回值
- Spec: §3.3
- Given: routine 内调用成功返回
- When: 执行
- Then: await handle 得到返回值

### TC-211 routine 受 capability 与 MaxRoutines 限制
- Spec: §3.3 / §10.1
- Given: 未授予 `routine:spawn` 或当前已达 `MaxRoutines`
- When: 执行
- Then: `E_CAPABILITY` 或 `E_BUDGET`

---

## §3.4 变量模型

### TC-230 词法作用域：内层不影响外层 set
- Spec: §3.4
- Given: 外 let x=1；内 let x=2（shadow）；退出后 var x
- When: 执行
- Then: 外层 x=1（shadow 不污染外层）

### TC-231 解构式 let
- Spec: §3.4
- Given: `let { price: var item.price, count: var item.count default 1 }`
- When: 执行
- Then: 同时创建 price/count 两变量

### TC-232 带类型声明的 let
- Spec: §3.4
- Given: `let total bigDecimal = 0`
- When: 后续 `set total=1.0`（类型不匹配）
- Then: `E_TYPE`

---

## §3.5 顶级运行上下文

### TC-250 Vars / Packages 双命名空间
- Spec: §3.5
- Given: 同名 `risk` 既包又变量
- When: 各自语法访问
- Then: 互不干扰（同 TC-056）

### TC-251 同命名空间重复注入报错
- Spec: §3.5
- Given: SessionOptions 中 Vars 重复或 Packages 重复同名
- When: NewSession
- Then: 诊断错误

### TC-252 局部 let 可遮蔽顶级 var
- Spec: §3.5
- Given: 顶级 `Vars={x:1}`，子块 `let x=2`
- When: 子块内 `var x`
- Then: int64=2；退出子块 var x=1

### TC-253 后续片段继承顶级上下文
- Spec: §3.5 / §3.1
- Given: SessionOptions 注入 input
- When: 第 N 个 AppendRun 仍可访问 input
- Then: 通过

---

## §3.6 数组索引

### TC-270 var 路径数字段 = 数组下标
- Spec: §3.6
- Given: `Vars={items:[{price:10}]}`
- When: `{"var":"items.0.price"}`
- Then: int64=10

### TC-271 越界返回缺省 / 诊断
- Spec: §3.6
- Given: items 长度=1，访问 items.5
- When: 无缺省值
- Then: 诊断错误；带缺省值时返回缺省

### TC-272 路径段对 map 数字仍是字符串 key
- Spec: §3.6
- Given: `Vars={stats:map[string]int{"2024":7}}`
- When: `{"var":"stats.2024"}`
- Then: 命中 `map["2024"]`，结果=7

### TC-273 slice 前驱时数字段为下标
- Spec: §3.6
- Given: `Vars={arr:[10,20]}`
- When: `{"var":"arr.1"}`
- Then: 20

### TC-274 字段名包含 `.` 必须用 path.Get
- Spec: §3.6
- Given: map key=`"a.b"`
- When: `{"var":"a.b"}`
- Then: 解析为 a→b，命中失败；用 `path.Get([{"literal":{"type":"string","value":"a.b"}}])` 才能取到

### TC-275 struct 字段大小写敏感
- Spec: §3.6
- Given: `User{Name string \`json:"name"\`}`
- When: `{"var":"u.Name"}` 与 `{"var":"u.name"}`
- Then: 仅 `name`（json tag）和 `Name`（字段名）二者匹配；其他大小写形态失败

---

## §4.1 类型映射模型

### TC-300 同一类型名映射稳定
- Spec: §4.1
- Given: 注册 `money -> Money`
- When: 同一程序多次引用 money
- Then: 同一 reflect.Type；方法集稳定

### TC-301 友好别名底层 Type 必须一致
- Spec: §4.2.2
- Given: 自动类型 `*github.com/acme/books.Book` 已存在；之后宿主显式 `AutoBindType("book", reflect.TypeFor[*Book]())`
- When: 注册
- Then: 接受为别名；底层 reflect.Type 不一致时报错

---

## §4.2.3 多返回值映射

### TC-320 单业务返回值
- Spec: §4.2.3
- Given: `(R, error)`
- When: 调用成功
- Then: result 为 R typed value

### TC-321 多业务返回值 → tuple
- Spec: §4.2.3
- Given: `(*Book, int64, bool, string, error)`
- When: 调用成功
- Then: result 类型 = `tuple<*github.com/acme/books.Book,int64,boolean,string>`

### TC-322 仅 error 返回
- Spec: §4.2.3
- Given: `func F() error`，error=nil
- When: 调用
- Then: result 为 null typed value

### TC-323 业务返回值未注册类型 → 自动类型名
- Spec: §4.2.3
- Given: 未注册 `*X`
- When: 调用返回
- Then: result 类型名按 §4.2.2 自动生成

### TC-324 error != nil 默认中断
- Spec: §4.2.3 / §9.1.1
- Given: 默认模式
- When: 函数返回 error
- Then: 走错误短路

---

## §4.3 / §4.4 类型推断与检查

### TC-340 typed literal 推断
- Spec: §4.3
- Given: int64 literal
- When: 编译
- Then: 表达式静态类型=int64

### TC-341 var 路径类型推断
- Spec: §4.3
- Given: 注入类型 `User{Name string}`
- When: `{"var":"u.Name"}`
- Then: 推断 string

### TC-342 if 两分支类型不一致告警
- Spec: §4.3
- Given: then 返回 int64，else 返回 string
- When: 编译
- Then: `E_TYPE` 或合并到 `any` 视实现策略

### TC-343 array 元素类型推断 array<T>
- Spec: §4.3
- Given: array<int64> literal
- When: 编译
- Then: 类型=array<int64>

### TC-344 set 类型检查
- Spec: §4.4
- Given: `let x int64=1; set x="s"`
- When: 编译
- Then: `E_TYPE`

### TC-345 foreach.target 非 array
- Spec: §4.4
- Given: target 推断为 string
- When: 编译
- Then: `E_TYPE`

### TC-346 if.cond 必须为 boolean
- Spec: §4.4
- Given: cond 推断为 int64
- When: 编译
- Then: `E_TYPE`

### TC-347 struct 未导出字段不可见
- Spec: §4.4 / §10.3
- Given: `User{name string}`（小写）
- When: `{"var":"u.name"}`
- Then: `E_SYMBOL`

### TC-348 json:"-" 字段屏蔽
- Spec: §10.3
- Given: 字段带 `json:"-"`
- When: var 访问
- Then: 不可见

---

## §4.5 类型自动绑定 API

### TC-360 AutoBindType + Constructor
- Spec: §4.5
- Given: `BindOptions{Constructor: NewMoney}`
- When: typed literal `{"type":"money","value":"1.23"}`
- Then: 由 NewMoney 构造 Money 值

### TC-361 include / exclude 白名单
- Spec: §4.5
- Given: BindOptions 排除 `internalMethod`
- When: 调用
- Then: `E_SYMBOL`

### TC-362 注册期签名校验失败聚合
- Spec: §4.5
- Given: 多个不可映射方法
- When: 启动注册
- Then: 一次性返回多错误集

### TC-363 方法元数据：capability/timeout/cost
- Spec: §4.5
- Given: 注册带元数据
- When: 编译/执行
- Then: capability check 与预算控制按元数据生效

---

## §5.1 / §5.2 宿主 API

### TC-400 NewEngine + CompileJSON + Run
- Spec: §5.2
- Given: 完整 EngineOptions（含 Budget），合法程序
- When: CompileJSON → Run
- Then: 返回 typed value，无 error

### TC-401 Strict 模式拒绝宽松输入
- Spec: §5.2
- Given: Strict=true，含裸 JSON 数字的程序
- When: CompileJSON
- Then: `E_AST_SHAPE` / `E_TYPE`

### TC-402 NewSession + AppendRun + await 完整链路
- Spec: §5.2
- Given: 注册返回业务值的方法
- When: AppendRun 启动 routine 并 await handle
- Then: session 返回 routine 结果

### TC-403 routine handle 可重复 await
- Spec: §5.2 / §8.1
- Given: 已成功 await 一次的 handle
- When: 再次 await 同 handle
- Then: 返回缓存结果

### TC-404 await 非 handle 报错
- Spec: §5.2
- Given: await 参数不是 routineHandle
- When: await
- Then: `E_TYPE`

### TC-405 await 多业务返回值 → tuple
- Spec: §5.2
- Given: routine 返回 [A,B]
- When: await handle
- Then: 返回 tuple<A,B>

### TC-406 await 仅 error 返回的成功路径
- Spec: §5.2
- Given: routine 函数仅返回 error 且 error=nil
- When: await handle
- Then: 返回 null typed value

### TC-407 await routine error
- Spec: §5.2
- Given: routine 函数返回 Go error
- When: await handle
- Then: await 按普通错误路径返回

---

## §5.3 函数注册签名覆盖

### TC-420 (a,b)(R,error)
- Spec: §5.3
- Given: 普通 2 参函数
- When: 调用
- Then: 通过

### TC-421 (ctx, a)(R,error)
- Spec: §5.3
- Given: 首参为 context.Context
- When: 调用
- Then: ctx 来自 program.Run；不出现在语言参数列表

### TC-422 (env, a)(R,error)
- Spec: §5.3
- Given: 首参为 wflang.Env
- When: 调用
- Then: env 注入；语言参数列表去掉 env

### TC-423 可变参数 (...T)(R,error)
- Spec: §5.3
- Given: `func F(args ...int64)`
- When: 语言传入 N 个 int64 typed literal
- Then: 全部映射到 args

### TC-424 (ctx, ...T)(R,error)
- Spec: §5.3
- Given: ctx + variadic
- When: 调用
- Then: ctx 注入 + variadic 展开

---

## §5.4 / §5.5 capability + 事务边界

### TC-440 capability 缺失拒绝调用
- Spec: §5.5 / §10.2
- Given: 函数声明 `Capabilities=["net:http"]`，引擎未授予
- When: 编译/执行
- Then: `E_CAPABILITY`

### TC-441 capability 授予后通过
- Spec: §5.5
- Given: `CapabilitySet={"net:http":true}`
- When: 调用
- Then: 通过

### TC-442 ctx 跨 Run 的所有前台调用共享
- Spec: §5.5
- Given: program 多次前台宿主调用
- When: Run 一次
- Then: 所有调用收到同一 ctx 对象

### TC-443 routine 内调用共享派生 ctx
- Spec: §5.5
- Given: routine 启动点 ctx=A
- When: routine 内多次调用
- Then: 所有调用 ctx 派生自 A（cancel 联动）

### TC-444 短路不回滚副作用
- Spec: §5.5 / §9.1.1
- Given: 已完成的 Go 调用产生外部副作用，后续语句报错
- When: error 冒泡
- Then: 已完成调用不被自动回滚；事务回滚由宿主 Run 返回后决定

### TC-445 context cancel 中断执行
- Spec: §5.5 / §15.3
- Given: ctx 被宿主 cancel
- When: Run 进行中
- Then: 在下一次检查点（语句 / 调用边界）中止；返回 `context.Canceled` 或 `E_RUNTIME`

---

## §5.6 / §5.7 Builder + 解析执行闭环

### TC-460 Builder 输出严格 JSONLogic operator 形态
- Spec: §5.6
- Given: Builder.Call(pkg, "Score", ...)
- When: JSON()
- Then: 输出 `{"Score":[{"pkg":"..."}, ...]}` 单键结构

### TC-461 Builder 包函数 receiver
- Spec: §5.6
- Given: builder.Pkg("risk")
- When: JSON()
- Then: 生成 `{"pkg":"risk"}`

### TC-462 Builder 类型方法 receiver
- Spec: §5.6
- Given: builder.Var("user")
- When: JSON()
- Then: 生成 `{"var":"user"}`

### TC-463 Builder 强制 typed literal
- Spec: §5.6
- Given: builder.Lit("int64","1")
- When: JSON()
- Then: 输出 typed literal；缺失类型时返回构造错误

### TC-464 Builder 阶段做符号 / 参数数量检查
- Spec: §5.6
- Given: 调用未注册的 operator
- When: builder.Call(...)
- Then: 立即返回错误（不到 CompileJSON）

### TC-465 Builder int / uint 必须显式选宽度
- Spec: §5.6
- Given: builder.Lit 接 Go int
- When: 调用
- Then: 拒绝；要求 IntN / UintN 函数

---

## §5.8 round-trip

### TC-480 Builder→JSON→Compile→Run 闭环
- Spec: §5.8
- Given: §5.8 例
- When: 跑通 round-trip
- Then: result 类型与 Builder 推断一致

### TC-481 CallPlan 指向 Registry 同一 Go 函数
- Spec: §5.8
- Given: 编译后的 CallPlan
- When: 反射比对
- Then: GoFunc / GoMethod 与 Registry 注册值同一引用

### TC-482 Explain 列出 Go symbol → JSON path → CallPlan
- Spec: §5.8 / §11.2
- Given: 编译后的 program
- When: Explain()
- Then: 报告含三者对应

### TC-483 Conformance 覆盖矩阵
- Spec: §5.8
- Given: package call、method call、typed literal、tuple、routine handle、await、auto host type
- When: 跑 conformance suite
- Then: 全部通过

---

## §6 标准库

### TC-500 str.Len / Trim / Lower / Upper
- Spec: §6.2 str
- Given: 各函数典型输入
- When: 求值
- Then: 与 Go strings 等价

### TC-501 str.Contains / StartsWith / EndsWith
- Spec: §6.2 str
- Given: 命中 / 不命中样本
- When: 求值
- Then: boolean 正确

### TC-502 str.Replace / Split / Join
- Spec: §6.2 str
- Given: 典型样本
- When: 求值
- Then: 与 Go 等价

### TC-503 str.Format 模板格式化
- Spec: §6.2 str
- Given: 模板与参数
- When: 求值
- Then: 渲染结果稳定

### TC-510 num.Abs/Round/Floor/Ceil
- Spec: §6.2 num
- Given: 正负、边界
- When: 求值
- Then: 与 math 包等价

### TC-511 num.Min/Max/Clamp
- Spec: §6.2 num
- Given: 三参数 Clamp
- When: 求值
- Then: 区间裁剪正确

### TC-520 arr.Len / Contains / Sort / Distinct / Flatten
- Spec: §6.2 arr
- Given: array<int64> 样本
- When: 求值
- Then: 长度/包含/有序/去重/展平正确

### TC-530 path.Get / Set / Has / Keys / Values
- Spec: §6.2 path / §3.6
- Given: 含特殊 key（含 `.`/空字符串）
- When: path.Get 显式段数组
- Then: 命中正确，无法用 var 访问的也能取到

### TC-540 to.* 数字转换
- Spec: §6.2 to
- Given: 各源类型 → 目标类型
- When: 求值
- Then: 越界 / 精度丢失返回诊断错误

### TC-541 to.JSON 序列化
- Spec: §6.2 to
- Given: 任意 typed value
- When: to.JSON
- Then: 输出稳定 JSON 字符串

### TC-550 json.Parse / Stringify round-trip
- Spec: §6.2 json
- Given: 任意 JSON 字符串
- When: Parse → Stringify
- Then: 结构等价

### TC-560 val.TypeOf / IsNull / IsEmpty / Coalesce / Assert / IsError
- Spec: §6.2 val
- Given: 对应输入
- When: 求值
- Then: 行为与说明一致；Assert 失败触发诊断错误

### TC-570 不支持匿名函数 / lambda
- Spec: §6.2 末尾
- Given: 任意尝试声明 lambda 的 JSON
- When: 编译
- Then: `E_AST_SHAPE`

---

## §7.1 / §7.2 编译阶段 + Normalize

### TC-600 编译阶段顺序可观察
- Spec: §7.1
- Given: 启用 Trace
- When: CompileJSON
- Then: 阶段顺序 = Decode→Normalize→Parse→Resolve→TypeCheck→Capability→Lower

### TC-601 Normalize：单参数函数转数组
- Spec: §7.2
- Given: `{"-": expr}`（单参）
- When: Normalize
- Then: 转为 `{"-": [expr]}`

### TC-602 Normalize：typed literal value 构造为 Go 值
- Spec: §7.2
- Given: bigInt typed literal
- When: Normalize
- Then: value 转为 *big.Int 缓存到 AST

### TC-603 Normalize：缺省字段补默认
- Spec: §7.2
- Given: foreach 缺 index 字段
- When: Normalize
- Then: index 字段缺省，不报错

### TC-604 Normalize：旧版本语法迁移
- Spec: §7.2 / §13.2
- Given: 标记 deprecated 的旧形式
- When: Normalize
- Then: 转为当前 AST；输出 deprecation 提示

---

## §7.3 Parse AST

### TC-620 每节点带 JSON Pointer
- Spec: §7.3 / §9.4
- Given: 任意编译后的 AST
- When: 遍历节点
- Then: 每节点含 path 字段，能定位到原 JSON 节点

### TC-621 source path 用于错误定位
- Spec: §7.3
- Given: 多文件 import / envelope
- When: 报错
- Then: LangError.Path 指向具体 source

---

## §7.4 Resolve

### TC-640 Resolve 包 receiver
- Spec: §7.4
- Given: 注册 books
- When: `{"pkg":"books"}`
- Then: 解析为包 receiver；未注册返回 `E_SYMBOL`

### TC-641 Resolve 类型方法名
- Spec: §7.4
- Given: 注册 user.Run
- When: `{"Run":[{"var":"user"},...]}`
- Then: 解析到 user.Run

### TC-642 Receiver = pkgRef 走包函数表
- Spec: §7.4 分派流程
- Given: receiver 为 pkgRef
- When: 解析 operator
- Then: 在该包函数表查找

### TC-643 Receiver = typed value 走类型方法表
- Spec: §7.4
- Given: receiver 为 var
- When: 解析 operator
- Then: 按 var 静态类型查方法表

### TC-644 Receiver = null 报错
- Spec: §7.4
- Given: 静态可证明 receiver 为 null
- When: 编译
- Then: `E_NIL_RECEIVER`（编译期检测到的；运行期检测见 TC-085）

### TC-645 重载分派完全在编译期完成
- Spec: §7.4 重载解析规则
- Given: 所有参数类型静态可知
- When: 编译
- Then: 输出唯一 CallPlan；不延迟到运行期

### TC-646 精确类型优先级
- Spec: §7.4
- Given: 候选 [int64, float64]，参数 int64
- When: 解析
- Then: 命中 int64

### TC-647 安全数值提升
- Spec: §7.4
- Given: 候选 [int64]，参数 int8
- When: 解析
- Then: 提升后命中 int64

### TC-648 显式可构造转换
- Spec: §7.4
- Given: 候选 [bigDecimal]，参数 string，已注册 string→bigDecimal 构造
- When: 解析
- Then: 命中 bigDecimal

### TC-649 any 兜底
- Spec: §7.4
- Given: 唯一候选 any
- When: 解析
- Then: 命中

### TC-650 同分歧义 → E_AMBIGUOUS_OVERLOAD
- Spec: §7.4
- Given: 多候选同优先级
- When: 解析
- Then: `E_AMBIGUOUS_OVERLOAD`

### TC-651 无候选 → E_OPERATOR_NOT_FOUND
- Spec: §7.4
- Given: 参数类型不在任何候选范围
- When: 解析
- Then: `E_OPERATOR_NOT_FOUND`

### TC-652 用户处理路径仅二选一
- Spec: §7.4
- Given: 已得 ambiguous / not found
- When: 修复
- Then: ① 调整参数类型 ② 宿主层改绑定；语言不接受 `Add@int64` 形式

### TC-653 any 静态类型在重载中触发歧义
- Spec: §7.4 例
- Given: 参数为 any 值的变量
- When: 编译
- Then: `E_AMBIGUOUS_OVERLOAD`，提示在 let 上游使用精确类型方法

---

## §7.5 / §7.6 / §7.7 / §7.8 Type check / Capability / CallPlan / 优化

### TC-670 Type check 推断 + 校验完整覆盖
- Spec: §7.5 / §4.4
- Given: 含 var/let/set/return/if/foreach/fori 的程序
- When: 编译
- Then: 任一类型错误均在编译期返回，且带 path

### TC-671 Capability 报告
- Spec: §7.6 / §11.2
- Given: 程序使用 net:http
- When: Explain
- Then: 报告列出所需 capability

### TC-672 CallPlan 字段完备
- Spec: §7.7
- Given: 任一调用
- When: 编译
- Then: CallPlan 含 Operator/ReceiverKind/PackageName/GoFunc 或 GoMethod/ParamTypes/ReturnTypes/ErrorIndex/ResultKind/Capabilities

### TC-673 ResultKind：单值 / tuple / null
- Spec: §7.7 运行规则
- Given: 三种返回组合
- When: Run
- Then: 分别得到 typed value / tuple / null

### TC-674 routine 调用进入 CallPlan
- Spec: §7.7
- Given: routine 包装一个宿主调用
- When: 编译 routine 调用
- Then: CallPlan 含该宿主调用及返回类型

### TC-675 常量折叠
- Spec: §7.8
- Given: 纯算术常量表达式
- When: 编译
- Then: 编译后节点为常量值

### TC-676 纯函数常量参数预求值
- Spec: §7.8
- Given: 纯函数 + 全部 typed literal 参数
- When: 编译
- Then: 节点替换为常量

### TC-677 死分支裁剪
- Spec: §7.8
- Given: `if false then A else B`
- When: 编译
- Then: 仅保留 B

---

## §8 routine handle 与 await

### TC-700 routineHandle 接口
- Spec: §8.1
- Given: routine 表达式
- When: 执行
- Then: 返回类型为 routineHandle

### TC-701 await 多 handle 保持输入顺序
- Spec: §8.1
- Given: 多个 routine handle
- When: await [h2,h1]
- Then: 返回 array<any> 顺序为 [h2结果,h1结果]

### TC-702 handle 可跨语句保存
- Spec: §8.1
- Given: let 绑定 routine handle
- When: 后续语句 await
- Then: 命中同一个 handle

### TC-703 await 未知变量
- Spec: §8.1
- Given: await 未定义变量
- When: 执行
- Then: `E_SYMBOL`

### TC-704 handle 缓存结果
- Spec: §8.2
- Given: routine 已完成
- When: 多次 await 同一 handle
- Then: 返回同一结果

### TC-705 await 单业务返回值
- Spec: §8.3
- Given: routine 返回 T
- When: await
- Then: 返回单 typed value

### TC-706 await 多业务返回值 → tuple
- Spec: §8.3
- Given: routine 返回 A,B
- When: await
- Then: 注入 tuple<A,B>

### TC-707 await routine Err
- Spec: §8.3
- Given: routine 返回 error
- When: await
- Then: await 返回错误

### TC-708 自动宿主类型返回
- Spec: §8.3
- Given: routine 返回未注册 Go 类型
- When: await
- Then: 按 §4.2.2 自动生成类型名

---

## §9 错误模型

### TC-720 error == nil 表达式得到 T
- Spec: §9.1
- Given: 函数返回 (T, nil)
- When: 求值
- Then: typed value T

### TC-721 error != nil 默认中断
- Spec: §9.1
- Given: 函数返回 (zero, err)
- When: 求值
- Then: 错误短路

### TC-722 try 启用后 error 转 typed value
- Spec: §9.1
- Given: try 包裹该调用
- When: 求值
- Then: 得到 typed value，类型=error；可调用 Error 方法

### TC-723 错误冒泡 6 步链
- Spec: §9.1.1
- Given: foreach.do 内宿主调用 error
- When: 执行
- Then: 中断当前调用 → 中断当前语句 → 中断 foreach 剩余迭代 → 冒泡到顶层（无 try 时）→ Run 返回该 error

### TC-724 try 捕获停止冒泡
- Spec: §9.1.1
- Given: 外层 try 包含失败语句
- When: 执行
- Then: error 转 typed value 进入 catch；冒泡停止

### TC-725 routine error 不冒泡到主流程
- Spec: §9.1.1
- Given: routine 内宿主调用失败
- When: 执行
- Then: 进入 RoutineErrorHandler；主流程继续

### TC-726 副作用不会自动回滚
- Spec: §9.1.1
- Given: 已完成的写入
- When: 之后语句报错并冒泡到 Run 返回
- Then: 已完成写入仍存在，由宿主基于 ctx.Tx 决定回滚

### TC-727 error 与 null 严格区分
- Spec: §9.1.1
- Given: 函数 (T, error)：error=nil 且 T=null vs error=非空
- When: 求值
- Then: 前者得 null typed value；后者走错误短路，绝不返回 error typed value（除非 try）

### TC-728 null receiver 运行期检查
- Spec: §9.1.2
- Given: var 求值为 null
- When: 调用方法
- Then: 不进入反射；返回 `E_NIL_RECEIVER`

### TC-729 E_NIL_RECEIVER 可被 try 捕获
- Spec: §9.1.2
- Given: 包在 try 内
- When: 调用 null receiver
- Then: 走 catch，得到 error typed value

### TC-730 LangError 字段完备
- Spec: §9.2
- Given: 任一诊断错误
- When: 序列化
- Then: 含 code/message/path/expected/actual/hint

### TC-731 错误码分类齐全
- Spec: §9.3
- Given: 触发各分类
- When: 编译/执行
- Then: 错误码命中：`E_JSON_DECODE/E_AST_SHAPE/E_SYMBOL/E_TYPE/E_CAPABILITY/E_RUNTIME/E_BUDGET/E_HOST/E_NIL_RECEIVER/E_PANIC/E_ROUTINE`

### TC-732 多错误聚合
- Spec: §9.4 / §15.2
- Given: 程序含多处编译错误
- When: CompileJSON
- Then: 一次性返回多 LangError 集合

---

## §10 安全与资源控制

### TC-800 MaxSteps 触发 E_BUDGET
- Spec: §10.1
- Given: Budget.MaxSteps=100，程序需 1000 步
- When: Run
- Then: `E_BUDGET`

### TC-801 MaxCallDepth 触发 E_BUDGET
- Spec: §10.1
- Given: 深度递归宿主调用
- When: Run
- Then: `E_BUDGET`

### TC-802 MaxLoopIterations
- Spec: §10.1
- Given: 长 fori
- When: Run
- Then: `E_BUDGET`

### TC-803 MaxRoutines
- Spec: §10.1
- Given: 已用满 routines
- When: 再启动一个 routine
- Then: `E_BUDGET`

### TC-804 MaxArrayLength / MaxObjectKeys / MaxStringBytes
- Spec: §10.1
- Given: 超大输入
- When: Decode / 求值
- Then: `E_BUDGET`

### TC-805 MaxAllocBytes
- Spec: §10.1
- Given: 估算超过预算
- When: Run
- Then: `E_BUDGET`

### TC-806 Timeout
- Spec: §10.1
- Given: Budget.Timeout=10ms，长跑程序
- When: Run
- Then: 在边界中止，返回 `E_BUDGET` 或 ctx.DeadlineExceeded

### TC-807 默认 Registry 仅含纯函数标准库
- Spec: §10.2
- Given: `wflang.DefaultRegistry()`
- When: 列出
- Then: 不含 net/file/db 等副作用包

### TC-808 panic 默认转诊断错误
- Spec: §10.2
- Given: 默认 EngineOptions
- When: 触发 panic
- Then: `E_PANIC`，不抛出 Go panic

### TC-809 反射调用前完成参数检查
- Spec: §10.2
- Given: 参数数量不足
- When: 编译
- Then: `E1004`，不进入反射

### TC-810 数据边界：未导出字段不可见
- Spec: §10.3
- Given: struct 内未导出字段
- When: var 访问
- Then: 不可见

### TC-811 json:"-" 字段屏蔽
- Spec: §10.3
- Given: 字段带 `json:"-"`
- When: var
- Then: 不可见

### TC-812 宿主类型只暴露自动绑定方法
- Spec: §10.3
- Given: include 列表为空
- When: 调用任意方法
- Then: `E_SYMBOL`

---

## §11 可观测性

### TC-830 Trace 字段完备
- Spec: §11.1
- Given: 启用 trace
- When: Run
- Then: 每事件含 path/op/args/result/type/duration_us/error

### TC-831 Explain 报告完整
- Spec: §11.2
- Given: 任一程序
- When: Explain()
- Then: 报告含变量集、函数集、capability 集、表达式类型、可能错误、运行预算估算

### TC-832 DumpAST / DumpTypedAST
- Spec: §11.3
- Given: 编译后的程序
- When: 调用 dump
- Then: 输出 JSON 序列化稳定

### TC-833 EvalAt 单点求值
- Spec: §11.3
- Given: 编译后的程序与 path
- When: EvalAt(path, input)
- Then: 返回该节点 typed value，不影响外层执行

---

## §12 工具链

### TC-850 wflang-program.schema.json 校验示例
- Spec: §12.1
- Given: §16 所有示例
- When: JSON Schema 校验
- Then: 全部通过

### TC-851 schema 拒绝非 typed literal 常量
- Spec: §12.1
- Given: 含裸数字的程序
- When: schema 校验
- Then: 失败

### TC-852 Formatter 稳定输出
- Spec: §12.2
- Given: 同一程序两次 format
- When: format → format
- Then: 字节级一致

### TC-853 Formatter 单参数函数标准化
- Spec: §12.2
- Given: 单参输入
- When: format
- Then: 转为数组形式

### TC-854 Linter 表达式形状
- Spec: §12.3
- Given: 多键 operator 对象
- When: lint
- Then: 报告

### TC-855 Linter capability 检查
- Spec: §12.3
- Given: 程序使用 capability X，部署目标无该 cap
- When: lint
- Then: 报告

### TC-856 Linter 复杂度 / 循环预算
- Spec: §12.3
- Given: 高复杂度或大循环
- When: lint
- Then: 报告

### TC-857 文档自动生成
- Spec: §12.4
- Given: Registry 元数据
- When: 生成
- Then: 输出类型/操作符/函数/capability/示例/版本变更文档

### TC-858 Test runner JSON 用例
- Spec: §12.5
- Given: §12.5 示例 case
- When: runner 执行
- Then: 对比 want；失败输出 diff 与错误码

### TC-859 Config Builder round-trip
- Spec: §12.6 / §5.8
- Given: Builder 构造程序
- When: JSON → Compile → Run → 反向 dump
- Then: 与原 Builder AST 一致

---

## §13 版本兼容

### TC-880 lang 版本声明强制
- Spec: §13.1
- Given: envelope 缺 lang
- When: Compile
- Then: 视实现：警告或 `E_AST_SHAPE`

### TC-881 同大版本语义稳定
- Spec: §13.2
- Given: v1.x 全部 minor 版本
- When: 同程序执行
- Then: 结果一致

### TC-882 deprecation 表
- Spec: §13.2
- Given: 使用 deprecated 语法
- When: Compile
- Then: 输出 deprecation 提示但仍执行

### TC-883 迁移器
- Spec: §13.2
- Given: 旧版本程序
- When: 迁移器
- Then: 输出当前版本等价 AST

### TC-884 features 灰度开关
- Spec: §13.3
- Given: `features.typedArray=false`
- When: 程序使用 array<T> typed literal
- Then: `E_AST_SHAPE` / `E_SYMBOL`

---

## §14 改善项验收

### TC-900 null 独立类型
- Spec: §14.1
- Given: 任意 null typed value
- When: TypeOf
- Then: 返回 `null`，与 `any` / `error` 区分

### TC-901 参数数量严格校验
- Spec: §14.1
- Given: 多/少参数
- When: 编译
- Then: `E1004`

### TC-902 反射调用安全检查
- Spec: §14.1
- Given: 类型错配
- When: 运行前
- Then: 转 `E_TYPE`，不调用 Go

### TC-903 typed literal 构造错误直返
- Spec: §14.1
- Given: bigInt value="abc"
- When: 加载
- Then: 立即报错

### TC-904 array<T> 元素类型校验
- Spec: §14.1
- Given: array<int64> 含 string 元素
- When: 编译
- Then: `E_TYPE`

### TC-905 match 表达式
- Spec: §14.2
- Given: match 多分支值匹配
- When: 编译/运行
- Then: 命中分支求值；缺省分支兜底；类型合并规则一致

### TC-906 编译后 Program 复用
- Spec: §14.3
- Given: program 多次 Run
- When: 多次执行
- Then: 不重复 Decode/Resolve；性能基准明显高于一次性 Compile+Run

### TC-907 typed AST 执行一致性
- Spec: §14.3 / §15.2
- Given: typed AST vs 动态执行
- When: 同输入
- Then: 结果一致

### TC-908 顶级注入：变量+包
- Spec: §14.3
- Given: SessionOptions 同时注入 Vars 与 Packages
- When: AppendRun
- Then: 两者均可用

### TC-909 Go 桥接：context cancel
- Spec: §14.4
- Given: ctx 注入并 cancel
- When: Run
- Then: 中断

### TC-910 注册错误聚合
- Spec: §14.4
- Given: 多个无效注册
- When: 启动期
- Then: 一次性返回所有错误

### TC-911 静态分析：let 类型传播
- Spec: §14.5
- Given: `let x = call ...`
- When: 后续使用 x
- Then: x 的静态类型被推断为 call 返回类型

### TC-912 set 类型检查
- Spec: §14.5
- Given: 类型不匹配赋值
- When: 编译
- Then: `E_TYPE`

### TC-913 条件布尔检查
- Spec: §14.5
- Given: cond 非布尔
- When: 编译
- Then: `E_TYPE`

### TC-914 分支返回类型合并
- Spec: §14.5
- Given: if 两分支返回类型不同
- When: 编译
- Then: 按合并规则得到联合类型或 `E_TYPE`

### TC-915 JSON Pointer 错误路径
- Spec: §14.5 / §9.4
- Given: 任一编译错误
- When: 报错
- Then: Path 是 JSON Pointer，可定位到原始 JSON 节点

---

## §15 路线图阶段验收

### TC-950 阶段一：单引入即可运行
- Spec: §15.1 验收
- Given: 仅 import wflang package
- When: 编译执行 §16 示例
- Then: 全通过

### TC-951 阶段一：诊断错误三件套
- Spec: §15.1
- Given: 任一错误
- When: 输出
- Then: 含 path/code/message

### TC-952 阶段二：类型错误编译期返回
- Spec: §15.2
- Given: 各类类型错配
- When: Compile
- Then: 全部在编译期暴露

### TC-953 阶段二：Explain 列出元数据
- Spec: §15.2
- Given: 任一程序
- When: Explain()
- Then: 列出变量/函数/能力/返回类型

### TC-954 阶段三：Builder→Compile 闭环
- Spec: §15.3
- Given: Builder 输出
- When: CompileJSON
- Then: 通过

### TC-955 阶段三：缺 capability 在执行前报告
- Spec: §15.3
- Given: 程序需 cap，未授予
- When: Compile / Capability check
- Then: 报告

### TC-956 阶段四：常见数据转换免新增宿主函数
- Spec: §15.4
- Given: 常见 JSON 转换需求
- When: 仅用 stdlib
- Then: 完成

### TC-957 阶段五：format/lint/test/explain 全可用
- Spec: §15.5
- Given: 任一程序
- When: 工具链
- Then: 全部可执行无崩溃

### TC-958 阶段五：conformance 通过
- Spec: §15.5
- Given: 当前实现
- When: 跑 conformance suite
- Then: 全通过

### TC-959 阶段五：基准性能
- Spec: §15.5
- Given: benchmark suite
- When: 跑 bench
- Then: 输出基准数据，建立回归基线

---

## §16 推荐示例验收

### TC-980 §16.1 多项加法
- Spec: §16.1
- Given: 1+2+3
- When: 求值
- Then: int64=6

### TC-981 §16.2 嵌套 var
- Spec: §16.2
- Given: input.user.age
- When: 求值
- Then: typed value 与注入一致

### TC-982 §16.3 缺省值
- Spec: §16.3
- Given: 缺失 input.user.name
- When: 求值
- Then: string="guest"

### TC-983 §16.4 if 对象形
- Spec: §16.4
- Given: age=20
- When: 求值
- Then: string="adult"；age=10 时返回 "minor"

### TC-984 §16.5 bigDecimal literal
- Spec: §16.5
- Given: "123.456"
- When: 求值
- Then: 精确等于 123.456（big.Rat）

### TC-985 §16.6 宿主值构造
- Spec: §16.6
- Given: 注册 books.NewQuery(string,int64)
- When: 求值
- Then: 返回 *Query typed value

### TC-986 §16.7 数组构造
- Spec: §16.7
- Given: array<int64> 含 var first/second
- When: 求值
- Then: []int64{first,second}

### TC-987 §16.7.1 数组索引
- Spec: §16.7.1
- Given: input.items 长度=3
- When: 取 items.0 / items.0.price / 缺省
- Then: 行为符合 §3.6

### TC-988 §16.8 sum prices 程序
- Spec: §16.8
- Given: input.items 含 price 字段
- When: Run
- Then: bigDecimal=∑price

### TC-989 §16.9 fori + break + continue
- Spec: §16.9
- Given: from=0,to=10,step=1，i==5 continue，i>=8 break
- When: Run
- Then: sum = 0+1+2+3+4+6+7 = 23

### TC-990 §16.10 panic
- Spec: §16.10
- Given: panic "invalid state"
- When: Run（默认）
- Then: `E_PANIC`，message 含 "invalid state"

### TC-991 §16.10 routine
- Spec: §16.10
- Given: routine 启动 events.Publish
- When: Run
- Then: 主流程立即返回 null；后台执行 Publish

### TC-992 §16.11 包函数 risk.Score
- Spec: §16.11
- Given: 注册 risk.Score(float64,string)
- When: Run
- Then: 返回 float64

### TC-993 §16.12 类型方法 user.Run
- Spec: §16.12
- Given: 注册 *User.Run
- When: Run
- Then: 返回 string

### TC-994 §16.13 Builder→Compile→Run 闭环
- Spec: §16.13
- Given: Builder 构造 risk.Score 程序
- When: JSON→CompileJSON→Run
- Then: 与生成的 JSON 等价；返回 float64

---

## §17 极致目标画像 → 全局回归

### TC-1000 语言核心可独立 import
- Spec: §17.1
- Given: 单包引入
- When: 编译运行
- Then: 不依赖任何外部副作用

### TC-1001 任意 Go 类型可安全映射
- Spec: §17.2
- Given: 复杂 Go 类型矩阵（指针、嵌入、别名、外部库类型）
- When: AutoBindType
- Then: 全部可映射并可调用

### TC-1002 类型反馈早
- Spec: §17.3
- Given: 任一类型错误
- When: Compile
- Then: 编译期即可见

### TC-1003 错误定位准
- Spec: §17.4
- Given: 任一错误
- When: 报错
- Then: JSON Pointer 精准

### TC-1004 执行可控
- Spec: §17.5
- Given: 各预算开关
- When: Run
- Then: 全部生效

### TC-1005 工具完善
- Spec: §17.6
- Given: schema/formatter/linter/test runner/trace/explain
- When: 工具链
- Then: 全部可用

### TC-1006 版本稳定 + 迁移
- Spec: §17.7 / §13
- Given: 旧版程序
- When: 迁移
- Then: 等价运行

### TC-1007 文档自动化
- Spec: §17.8 / §12.4
- Given: Registry
- When: 文档生成
- Then: 类型/操作符/函数齐备

### TC-1008 测试体系
- Spec: §17.9
- Given: 单测/golden/fuzz/bench/conformance
- When: 跑全套
- Then: 全部存在并通过

### TC-1009 上层 DSL 可降级到 wflang
- Spec: §17.10
- Given: 任一上层 DSL → wflang AST 编译器
- When: 编译
- Then: 输出合法 wflang JSON 并通过 round-trip
