# wlang Quickstart

本文档用于快速看懂 wlang JSON、go2wlang 输出和内建命名空间。

## 1. 运行 go2wlang 示例

生成 Temporal 风格补偿 workflow 的伪代码：

```bash
GOCACHE=/private/tmp/wlang-gocache go run ./cmd/go2wl -func OrderWorkflow -pseudo go2wlang/examples/order_workflow.go
```

生成综合能力示例：

```bash
GOCACHE=/private/tmp/wlang-gocache go run ./cmd/go2wl -func FeatureShowcase -pseudo go2wlang/examples/feature_showcase.go
```

生成可执行 JSON：

```bash
GOCACHE=/private/tmp/wlang-gocache go run ./cmd/go2wl -func FeatureShowcase go2wlang/examples/feature_showcase.go
```

## 2. JSON 和 pseudo 的关系

wlang 的可执行形态是 JSON。pseudo 是 review/debug 视图，由 formatter 从同一份 JSON 生成。

Go:

```go
if n := len(scores); n > 0 {
	scores[0] = n
}
```

JSON:

```json
[
  {"let":{"n":{"arr.len":[{"var":"scores"}]}}},
  {"if":{
    "cond":{">":[{"var":"n"},{"literal":{"type":"int64","value":"0"}}]},
    "then":[{"expr":{"arr.set":[
      {"var":"scores"},
      {"literal":{"type":"int64","value":"0"}},
      {"var":"n"}
    ]}}]
  }}
]
```

pseudo:

```go
let n = scores.length
if n > 0 {
  scores[0] = n
}
```

`arr.len`、`arr.set` 这类名字由 runtime 直接识别，pseudo 会显示成 `scores.length`、`scores[0] = n` 这类语法糖。

## 3. 内建命名空间速查

### `arr.*`

数组和 slice 风格操作。

| 操作 | 含义 |
| --- | --- |
| `arr.len(xs)` | 长度，返回 `int64` |
| `arr.get(xs, i)` | 读取元素 |
| `arr.set(xs, i, v)` | 写入元素，返回 `null` |
| `arr.slice(xs, a, b)` | 切片 |
| `arr.push(xs, v)` | 追加元素，返回 `null` |

go2wlang 常见映射：

```go
len(xs)        // arr.len(xs)
xs[i]          // arr.get(xs, i)
xs[i] = v      // arr.set(xs, i, v)
xs[a:b]        // arr.slice(xs, a, b)
xs = append(xs, v)
```

### `map.*`

map 操作。

| 操作 | 含义 |
| --- | --- |
| `map.get(dict, k)` | 读取并返回 `tuple<V, boolean>` |
| `map.value(dict, k)` | 单值读取 |
| `map.set(dict, k, v)` | 写入 |
| `map.del(dict, k)` | 删除 |
| `map.has(dict, k)` | key 是否存在 |
| `map.len(dict)` | 长度 |
| `map.keys(dict)` | key 数组 |
| `map.values(dict)` | value 数组 |

go2wlang 常见映射：

```go
v, ok := dict[k]  // map.get(dict, k)
v := dict[k]      // map.value(dict, k)
dict[k] = v       // map.set(dict, k, v)
delete(dict, k)   // map.del(dict, k)
```

### `ch.*`

channel 操作。

| 操作 | 含义 |
| --- | --- |
| `ch.send(ch, v)` | 发送 |
| `ch.recv(ch)` | 接收 |
| `ch.close(ch)` | 关闭 |
| `ch.len(ch)` | 缓冲区长度 |
| `ch.cap(ch)` | 缓冲区容量 |

go2wlang 常见映射：

```go
ch <- v
v := <-ch
close(ch)
cap(ch)
select { ... }
```

### `ptr.*`

指针操作。

| 操作 | 含义 |
| --- | --- |
| `ptr.new("T")` | 创建 `*T` |
| `ptr.deref(p)` | 解引用 `*p` |

go2wlang 常见映射：

```go
new(T)
*p
```

### `type.*`

类型判断和类型断言。

| 操作 | 含义 |
| --- | --- |
| `type.assert(x, "T")` | 单值类型断言 |
| `type.assert.ok(x, "T")` | 双值类型断言，返回 `tuple<T, boolean>` |
| `type.is(x, "T")` | 类型判断 |

go2wlang 常见映射：

```go
x.(T)
v, ok := x.(T)
switch v := x.(type) { ... }
```

### `bit.*`

位运算。

| 操作 | 含义 |
| --- | --- |
| `bit.not(x)` | Go 的 `^x` |

## 4. 顶层内建操作

这些操作没有命名空间前缀：

| 操作 | 含义 |
| --- | --- |
| `+`, `-`, `*`, `/` | 算术 |
| `==`, `!=`, `>`, `>=`, `<`, `<=` | 比较 |
| `and`, `or`, `!` | 逻辑 |
| `contains`, `startsWith`, `endsWith` | 字符串判断 |
| `call` | 调用函数值或 Go function carrier |
| `routine` | 启动并发 routine |
| `await` | 等待 routine handle |
| `copy` | Go `copy(dst, src)` |
| `complex`, `real`, `imag` | 复数相关内建 |

## 5. Go 静态符号和输出参数

go2wlang 会把静态 Go 函数值生成为 `symbol`：

```json
{"symbol":"workflow.Reserve"}
```

receiver 方法值会生成为 `method`：

```json
{"method":[{"var":"worker"},"Compensate"]}
```

Go 的 `var reserve ReserveResult` 会生成为 typed zero：

```json
{"zero":"examples.ReserveResult"}
```

Go 的 `&reserve` 会生成为输出参数：

```json
{"out":"reserve"}
```

Temporal 风格链式调用：

```go
workflow.ExecuteActivity(ctx, workflow.Reserve, input.OrderID).Get(ctx, &reserve)
```

JSON:

```json
{
  "Get": [
    {
      "ExecuteActivity": [
        {"pkg":"workflow"},
        {"var":"ctx"},
        {"symbol":"workflow.Reserve"},
        {"var":"input.OrderID"}
      ]
    },
    {"var":"ctx"},
    {"out":"reserve"}
  ]
}
```

## 6. 下一步阅读

- [LANGUAGE.md](LANGUAGE.md)：完整语言语义。
- [go2wlang/README.md](go2wlang/README.md)：Go 翻译器 API 和 CLI。
- [go2wlang/SUPPORT.md](go2wlang/SUPPORT.md)：当前支持的 Go 子集。
- [go2wlang/examples/feature_showcase.go](go2wlang/examples/feature_showcase.go)：综合示例。
- [go2wlang/examples/order_workflow.go](go2wlang/examples/order_workflow.go)：Temporal 风格补偿 workflow 示例。
