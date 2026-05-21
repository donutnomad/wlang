# wflang

## routine 与 await 用法

`routine` 用于启动一个宿主调用并立即返回 `routineHandle`。`await` 用于等待一个或多个 `routineHandle` 完成并读取结果。

### 1. fire-and-forget

适合后台提交任务，主流程立即继续执行。

```json
[
  {
    "routine": {
      "Publish": [
        { "pkg": "events" },
        { "var": "event" }
      ]
    }
  },
  {
    "return": {
      "literal": { "type": "string", "value": "submitted" }
    }
  }
]
```

后台调用失败时，错误进入宿主侧 `RoutineErrorHandler`。

### 2. 单个 handle + await

```json
[
  {
    "let": {
      "h": {
        "routine": {
          "Fetch": [
            { "pkg": "books" },
            { "var": "id" }
          ]
        }
      }
    }
  },
  {
    "return": {
      "await": { "var": "h" }
    }
  }
]
```

`await` 返回 `Fetch` 的业务返回值。

### 3. 等待多个 routine

```json
[
  {
    "let": {
      "a": {
        "routine": {
          "Fetch": [
            { "pkg": "svc" },
            { "literal": { "type": "string", "value": "a" } }
          ]
        }
      }
    }
  },
  {
    "let": {
      "b": {
        "routine": {
          "Fetch": [
            { "pkg": "svc" },
            { "literal": { "type": "string", "value": "b" } }
          ]
        }
      }
    }
  },
  {
    "return": {
      "await": [
        { "var": "a" },
        { "var": "b" }
      ]
    }
  }
]
```

多个 handle 返回 `array<any>`，结果顺序按 `await` 输入顺序排列。

### 4. 重复 await 同一个 handle

```json
[
  {
    "let": {
      "h": {
        "routine": {
          "Pair": [
            { "pkg": "svc" },
            { "literal": { "type": "string", "value": "abc" } }
          ]
        }
      }
    }
  },
  { "let": { "first": { "await": { "var": "h" } } } },
  { "let": { "second": { "await": { "var": "h" } } } },
  { "return": { "==": [ { "var": "first" }, { "var": "second" } ] } }
]
```

handle 会缓存完成结果或错误，重复 `await` 复用同一份结果。

### 5. 多返回值

Go 宿主函数：

```go
func Pair(s string) (string, int64, error)
```

wflang：

```json
[
  {
    "let": {
      "h": {
        "routine": {
          "Pair": [
            { "pkg": "svc" },
            { "literal": { "type": "string", "value": "abc" } }
          ]
        }
      }
    }
  },
  {
    "return": {
      "await": { "var": "h" }
    }
  }
]
```

返回类型是 `tuple<string,int64,error>`。最后一位是 `error` 值，成功时其 Go 承载值为 `nil`。

### 6. 只有 error 返回值

Go 宿主函数：

```go
func Save(s string) error
```

wflang：

```json
[
  {
    "let": {
      "h": {
        "routine": {
          "Save": [
            { "pkg": "svc" },
            { "var": "payload" }
          ]
        }
      }
    }
  },
  {
    "return": {
      "await": { "var": "h" }
    }
  }
]
```

成功时返回 `error` typed value，Go 承载值为 `nil`。

### 7. 显式处理 routine tuple error

Go 宿主函数：

```go
func Save(s string) (string, error)
```

```json
[
  {
    "let": {
      "h": {
        "routine": {
          "Save": [
            { "pkg": "svc" },
            { "var": "payload" }
          ]
        }
      }
    }
  },
  {
    "let": [
      [
        "_",
        "err"
      ],
      [
        "string",
        "error"
      ],
      {
        "await": { "var": "h" }
      }
    ]
  },
  {
    "return": {
      "Error": [{ "var": "err" }]
    }
  }
]
```

Go `error` 是普通语言值。`func Save() error` 成功时得到 Go 承载值为 `nil` 的 `error` typed value，失败时得到承载原始 Go error 的 `error` typed value。`(T, error)` 会得到 `tuple<T,error>`，可以用 tuple 解构拿到 error slot；调用 `Error` 前应保证该 slot 承载非 nil error。

### 8. 参数捕获语义

```json
[
  {
    "let": {
      "token": {
        "literal": { "type": "string", "value": "before" }
      }
    }
  },
  {
    "let": {
      "h": {
        "routine": {
          "Echo": [
            { "pkg": "svc" },
            { "var": "token" }
          ]
        }
      }
    }
  },
  {
    "set": {
      "token": {
        "literal": { "type": "string", "value": "after" }
      }
    }
  },
  {
    "return": {
      "await": { "var": "h" }
    }
  }
]
```

`routine` 在启动点求值 receiver 和参数，所以传给 `Echo` 的是 `"before"`。

### 9. 语义规则

- `routine` 内容可以是单个宿主调用：`{ "GoName": [receiver, ...args] }`，也可以是 `{"do":[...]}` 语句块。
- `routine` 返回 `routineHandle`。
- `await` 单个 handle 时返回该 routine 的 typed value。
- `await` 多个 handle 时返回 `array<any>`。
- 多返回值使用 `tuple<T1,T2,...>`；最后一位是 Go `error` 时保留为 `error` slot。
- 只有 `error` 返回值时返回 `error` typed value，成功时 Go 承载值为 `nil`。
- routine 内最后返回形态遵循普通求值规则，含 error 的 host call 结果会保留 error 值或 tuple。
- `await` 接收非 `routineHandle` 时返回 `E_TYPE`。
- `routine` 需要 `routine:spawn` capability。
- `routine` 受 `MaxRoutines` 预算限制。

复杂并发流程可以放在 `routine.do` 中；涉及共享状态、事务或外部副作用的复杂流程仍建议封装在 Go 宿主函数中。
