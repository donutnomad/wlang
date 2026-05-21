# Temporal 编排、兼容性与 Saga 源码分析

分析对象：`/Users/ubuntu/projects/go/temporal`，即 Temporal server 仓库。用户 Workflow 代码由 SDK Worker 执行，server 负责事件历史、mutable state、任务路由、持久化和一致性边界。

## 核心结论

Temporal 的编排模型是事件溯源驱动的 durable orchestration：

1. Workflow Execution 的事实源是 append-only event history。
2. History Service 持有每个 workflow 的 mutable state 摘要，并把 state transition、history events、internal tasks 一起推进。
3. Matching Service 只负责把 Workflow Task / Activity Task 投递给 worker。
4. SDK Worker 通过 replay event history 恢复 Workflow 代码执行位置，然后产生命令。
5. server 接收 `RespondWorkflowTaskCompleted` 里的 commands，把 commands 转成新的 history events 和后续 tasks。
6. v1/v2 代码兼容依赖 SDK replay determinism、`GetVersion`/patch marker、worker versioning、replay tests 这几层共同约束。
7. Saga 在 Temporal 中通常是一个 Workflow 编排多个 Activity，并把补偿动作作为 Workflow 状态的一部分持久化到 history。
8. Child Workflow 是子编排边界；父 Saga 通过子流程的公开正向入口和补偿入口协调全局一致性。
9. MQ、三方回调这类异步结果可以用 Async Activity Completion 写回 Activity 结果。

## 源码地图

关键模块：

- `docs/architecture/README.md`: 架构概览。说明 Workflow 代码在用户 worker 进程中执行，server 做 durable execution、event sourcing 和 task 管理。
- `docs/architecture/history-service.md`: History Service 细节。说明 history events、mutable state、transfer/timer queues、transactional outbox。
- `docs/architecture/workflow-lifecycle.md`: 从 start workflow 到 activity complete 的完整时序。
- `service/history/api/respondworkflowtaskcompleted/`: worker 完成 Workflow Task 后，server 如何处理 commands。
- `service/history/api/recordworkflowtaskstarted/`: Matching 投递 Workflow Task 时，History 如何写 `WorkflowTaskStarted` 并返回 history。
- `service/history/workflow/mutable_state_impl.go`: workflow mutable state 的核心数据结构。
- `service/history/workflow/task_generator.go`: 根据 history event / mutable state 生成 transfer、timer、replication 等 internal tasks。
- `service/history/transfer_queue_active_task_executor.go`: transfer task 如何调用 Matching，创建 Workflow Task / Activity Task。
- `service/matching/`: task queue、poller、partition、version routing。
- `common/worker_versioning/`: worker build/deployment 版本路由、pinned/auto-upgrade 策略、滚动升级 fallback。
- `service/worker/*`: Temporal server 自身的 internal workflows，能看到 `GetVersion`、`SideEffect`、`MutableSideEffect`、replay tests 的实际用法。

## 编排链路

Temporal 的一次典型编排如下：

1. 客户端调用 `StartWorkflowExecution`。
2. History Service 初始化 history：`WorkflowExecutionStarted`、`WorkflowTaskScheduled`。
3. History 持久化 mutable state，并写入 transfer task。
4. transfer queue processor 调用 Matching 的 `AddWorkflowTask`。
5. Worker poll 到 Workflow Task。
6. Matching 调用 History 的 `RecordWorkflowTaskStarted`，History 追加 `WorkflowTaskStarted`，并把 full history 或 sticky partial history 返回给 worker。
7. SDK Worker replay history，运行 Workflow 代码到阻塞点，比如 activity、timer、child workflow、signal。
8. Worker 调用 `RespondWorkflowTaskCompleted`，提交 command 列表。
9. History 追加 `WorkflowTaskCompleted`，按 command 类型追加业务事件，比如 `ActivityTaskScheduled`、`TimerStarted`、`MarkerRecorded`。
10. History 更新 mutable state，并生成下一批 transfer/timer tasks。
11. transfer/timer processors 持续推进 workflow。

源码对应：

- 架构文档把 server 分成 Frontend、History、Matching、Internal Workers，并说明 Workflow Task 完成后 worker 会把 commands 发回 server：`docs/architecture/README.md:44-73`。
- History Service 文档把核心动作概括为追加 history、更新 mutable state、写 transfer/timer task：`docs/architecture/history-service.md:9-22`。
- state transition 的持久化模型是先在内存中构造 events/state/tasks，然后把 state 和 tasks 放进原子事务：`docs/architecture/history-service.md:278-322`。

## `RespondWorkflowTaskCompleted` 如何把命令变成事件

入口文件：`service/history/api/respondworkflowtaskcompleted/api.go`。

关键步骤：

1. 反序列化 task token，拿到 namespace/workflow/run/scheduled event。
2. 通过 consistency checker 拿 workflow lease 和 mutable state。
3. 校验当前 Workflow Task 的 scheduled/started/attempt/version。
4. 写入 `WorkflowTaskCompleted`。
5. 创建 `workflowTaskCompletedHandler`。
6. 调用 `handleCommands` 遍历 SDK 返回的 commands。
7. 根据结果决定是否创建新的 Workflow Task。
8. 调用 `UpdateWorkflowExecutionAsActive` 或 `UpdateWorkflowExecutionWithNewAsActive` 持久化。
9. 持久化成功后 apply effects，并返回可能的 eager new Workflow Task。

核心代码点：

- `handleCommands` 先校验 command 顺序，再逐个调用 `handleCommand`：`service/history/api/respondworkflowtaskcompleted/workflow_task_completed_handler.go:168-223`。
- `handleCommand` 对 command type 做 dispatch：schedule activity、complete/fail/cancel workflow、start/cancel timer、record marker、child workflow、external signal 等：`service/history/api/respondworkflowtaskcompleted/workflow_task_completed_handler.go:274-382`。
- 对 CHASM command，代码先尝试 CHASM handler，再落到 HSM handler：`service/history/api/respondworkflowtaskcompleted/workflow_task_completed_handler.go:335-362`。
- 持久化分支在 `api.go:613-641`，普通 completion 会调用 `UpdateWorkflowExecutionAsActive`，continue-as-new 等新 run 场景走 `UpdateWorkflowExecutionWithNewAsActive`。

这说明 server 的 orchestration 是 command-to-event-to-task 的状态机推进。Workflow 代码解释和 replay 位于 SDK Worker，server 只处理 commands 和 history。

## 异步 Activity Completion 与 MQ 回调

Temporal 支持 Activity 异步完成。适用场景是：Workflow 语义上在等待一个 Activity 的结果，但这个结果来自 MQ、三方 webhook、批处理回调或另一个系统的异步通知。

典型链路：

1. Workflow 调用 `workflow.ExecuteActivity(ctx, SubmitAndWaitMQActivity, input)`。
2. History 写入 `ActivityTaskScheduled`，Activity worker poll 到任务。
3. Activity worker 在 Activity 内发布 MQ 请求，并把 `TaskToken` 或 `workflowID/runID/activityID` 保存到业务库或放进 MQ message。
4. Activity 返回 SDK 的 pending sentinel，例如 Go SDK 的 `activity.ErrResultPending`。
5. Temporal 中这个 Activity 保持 pending 状态，等待外部 completion API。
6. MQ consumer 收到结果后，调用 Temporal client `CompleteActivity` 或 `CompleteActivityByID`。
7. History Service 追加 `ActivityTaskCompleted`，并创建新的 Workflow Task。
8. Workflow worker replay 后，`future.Get()` 读取到这个 Activity result，流程继续推进。

Go SDK 侧示意：

```go
func SubmitAndWaitMQActivity(ctx context.Context, input SubmitInput) (SubmitResult, error) {
    info := activity.GetInfo(ctx)

    err := publishMQ(MQRequest{
        OrderID:   input.OrderID,
        TaskToken: info.TaskToken,
    })
    if err != nil {
        return SubmitResult{}, err
    }

    return SubmitResult{}, activity.ErrResultPending
}
```

MQ consumer 收到外部结果后完成 Activity：

```go
func OnMQResult(ctx context.Context, c client.Client, msg MQResultMessage) error {
    return c.CompleteActivity(
        ctx,
        msg.TaskToken,
        SubmitResult{
            ExternalID: msg.ExternalID,
            Status:     msg.Status,
        },
        nil,
    )
}
```

使用 Activity ID 完成时，MQ message 保存稳定业务标识：

```go
func OnMQResultByID(ctx context.Context, c client.Client, msg MQResultMessage) error {
    return c.CompleteActivityByID(
        ctx,
        msg.Namespace,
        msg.WorkflowID,
        msg.RunID,
        msg.ActivityID,
        SubmitResult{
            ExternalID: msg.ExternalID,
            Status:     msg.Status,
        },
        nil,
    )
}
```

History 侧入口是 `RespondActivityTaskCompleted`：

- `service/history/api/respondactivitytaskcompleted/api.go:27-40` 反序列化 task token，并补齐 namespace、workflow、run。
- `service/history/api/respondactivitytaskcompleted/api.go:64-72` 支持 `CompleteActivityById` 路径：token 中 scheduled event id 为空时，用 activity ID 查 pending Activity。
- `service/history/api/respondactivitytaskcompleted/api.go:73-86` 从 mutable state 找到 pending Activity，并校验 token 仍指向当前有效 attempt。
- `service/history/api/respondactivitytaskcompleted/api.go:88-107` 支持 force complete：Activity 尚未 started 时可补一个 started event。
- `service/history/api/respondactivitytaskcompleted/api.go:109-124` 追加 `ActivityTaskCompleted`，并设置 `CreateWorkflowTask: true`，让 Workflow 继续执行。

失败结果也走同一类 completion 入口：外部 consumer 可以用 `CompleteActivity(..., nil, err)` 写入失败。随后 Activity retry 由 `RespondActivityTaskFailed` 和 `RetryActivity` 继续处理：

- `service/history/api/respondactivitytaskfailed/api.go:87-95` 保存最后 heartbeat details，作为下一次 attempt 的进度输入。
- `service/history/api/respondactivitytaskfailed/api.go:97-121` 调用 `mutableState.RetryActivity`，返回 `RETRY_STATE_IN_PROGRESS` 时继续 pending/retry，达到终止 retry 状态时追加 `ActivityTaskFailed` 并创建 Workflow Task。

超时和重试边界：

- Async Activity 仍受 Activity timeout 约束，尤其是 `StartToCloseTimeout`、`ScheduleToCloseTimeout`、`HeartbeatTimeout`。
- MQ 等待时间要落在 Activity timeout 预算内。
- 外部 worker 可以定期 heartbeat，把三方任务 ID、进度游标、最后处理位置写入 heartbeat details。
- Activity retry 会产生新的 attempt；旧 MQ 回调携带旧 task token 时，server 通过 token 和 mutable state 校验过滤旧 attempt。
- 使用 `CompleteActivityByID` 时，Activity ID 要稳定且具备业务幂等含义，例如 `mq-submit:{orderID}`。
- MQ consumer 对重复消息执行幂等完成；Temporal 已完成的 Activity 再次完成会返回关闭或未找到类错误，consumer 可按幂等成功处理。

选型建议：

- 结果语义属于某个外部任务的完成值，用 Async Activity Completion，比如支付网关授权结果、批处理 job result、三方工单回执。
- 结果语义属于业务事件推进，用 Workflow Signal 或 Update，比如用户确认、审批通过、订单状态变更事件。
- 等待周期较短且和一次 Activity 调用强绑定，用 Async Activity Completion。
- 等待周期很长、事件类型很多、需要多次交互，用 Signal/Update 驱动 Workflow 状态机。

## 自研 Temporal 功能地图

自研实现可以按四层目标拆分：

```text
P0: durable execution 最小内核
P1: 生产可用编排引擎
P2: 平台化和升级治理
P3: Temporal 高级能力对齐
```

### P0: durable execution 最小内核

P0 的目标是跑通“Workflow 调 Activity、等待结果、失败重试、replay 恢复”的闭环。

必做模块：

| 模块 | 能力 | 输出物 |
| --- | --- | --- |
| Client API | start workflow、describe workflow、get history、signal、cancel、terminate | HTTP/gRPC API 和 request id 幂等 |
| History Store | append-only event history、event batch、history 查询 | `workflow_events` 表 |
| Mutable State | 当前 run 摘要、pending activity、pending timer、pending child、last workflow task | `workflow_executions` 表 |
| Current Pointer | 同一 Workflow ID 当前 run 指针 | `current_executions` 表 |
| Command Handler | `ScheduleActivity`、`StartTimer`、`CompleteWorkflow`、`FailWorkflow` | command -> event -> task |
| Workflow Task | schedule/start/complete/fail/timeout 生命周期 | Workflow worker poll 协议 |
| Activity Task | schedule/start/complete/fail/timeout 生命周期 | Activity worker poll 协议 |
| Matching | task queue、long poll、sync match、backlog | worker 拉任务 |
| Timer Queue | workflow timer、activity timeout、workflow task timeout | durable timer task |
| Retry | activity retry、workflow task retry、service/internal task retry | retry policy 和 backoff |
| SDK Runtime | deterministic replay、future、selector、sleep、activity stub | 用户写 Workflow 的最小 SDK |
| Data Converter | payload encode/decode、错误序列化 | event payload 协议 |
| Idempotency | request id、workflow id reuse policy、activity id | 客户端和 worker 重试安全 |

P0 事件类型最小集合：

- `WorkflowExecutionStarted`
- `WorkflowTaskScheduled`
- `WorkflowTaskStarted`
- `WorkflowTaskCompleted`
- `WorkflowTaskFailed`
- `WorkflowTaskTimedOut`
- `ActivityTaskScheduled`
- `ActivityTaskStarted`
- `ActivityTaskCompleted`
- `ActivityTaskFailed`
- `ActivityTaskTimedOut`
- `TimerStarted`
- `TimerFired`
- `TimerCanceled`
- `WorkflowExecutionCompleted`
- `WorkflowExecutionFailed`
- `WorkflowExecutionCanceled`
- `WorkflowExecutionTerminated`
- `MarkerRecorded`
- `WorkflowExecutionSignaled`

P0 command 类型最小集合：

- `ScheduleActivityTask`
- `RequestCancelActivityTask`
- `StartTimer`
- `CancelTimer`
- `RecordMarker`
- `CompleteWorkflowExecution`
- `FailWorkflowExecution`
- `CancelWorkflowExecution`
- `ContinueAsNewWorkflowExecution`

P0 数据库建议：

```text
namespaces
  id, name, retention, state

current_executions
  namespace_id, workflow_id, run_id, state, create_time, update_time

workflow_executions
  namespace_id, workflow_id, run_id, next_event_id, state, status,
  attempt, workflow_type, task_queue, memo, search_attributes,
  mutable_state_json, version, create_time, update_time

workflow_events
  namespace_id, workflow_id, run_id, event_id, event_type,
  event_time, attributes_json, batch_id

tasks
  shard_id, task_id, category, visibility_time, payload_json, acked

task_queues
  namespace_id, task_queue, task_type, range_id, metadata_json

task_queue_tasks
  namespace_id, task_queue, task_type, task_id, payload_json, create_time
```

P0 的核心事务边界：

1. History handler 拿 workflow lease。
2. 读取 mutable state。
3. 校验请求 token、attempt、event id、run id。
4. 追加 history events。
5. 更新 mutable state。
6. 写 transfer/timer/internal tasks。
7. 提交同一个数据库事务。
8. 后台 queue processor 投递到 Matching 或触发超时。

### P1: 生产可用编排引擎

P1 的目标是支持真实业务流程、补偿、长时间等待和基本运维。

必做模块：

| 模块 | 能力 | 设计重点 |
| --- | --- | --- |
| Signal | 外部事件写入 Workflow history | signal id 幂等、buffer、触发 Workflow Task |
| Query | 读取 replay 后内存状态 | consistent query、sticky worker 优先 |
| Update | 带返回值的同步/异步推进请求 | accepted/completed 两阶段状态 |
| Continue-As-New | 同 Workflow ID 新 run | 新 run input、旧 run close event、current pointer 切换 |
| Child Workflow | 父子编排 | child initiated/started/completed 事件、parent close policy |
| Cancellation | workflow/activity/child/timer 取消传播 | cancel requested、cancel accepted、cleanup |
| Async Activity Completion | task token / activity ID 完成 Activity | MQ/webhook 回调、旧 attempt 过滤 |
| Heartbeat | Activity 进度上报和取消感知 | heartbeat details、heartbeat timeout |
| Saga Helper | 正向步骤和补偿步骤封装 | deterministic compensation stack |
| Retry Policy | activity/workflow/child retry | non-retryable type、next retry delay |
| Timeout | execution/run/task/activity/timer timeout | timer task 驱动 |
| Visibility | list/search workflow | 独立 visibility store |
| Admin CLI | history dump、reset、terminate、补偿观察 | 运维入口 |
| Web UI | 最小 History Viewer：workflow 列表、详情头、history 时间线、event JSON | 只读排障入口 |
| Replay Test Tool | 从历史文件 replay 当前 Workflow 代码 | 升级回归 |

P1 事件补充：

- `WorkflowExecutionContinuedAsNew`
- `WorkflowExecutionCancelRequested`
- `ActivityTaskCancelRequested`
- `ActivityTaskCanceled`
- `RequestCancelActivityTaskFailed`
- `StartChildWorkflowExecutionInitiated`
- `ChildWorkflowExecutionStarted`
- `ChildWorkflowExecutionCompleted`
- `ChildWorkflowExecutionFailed`
- `ChildWorkflowExecutionCanceled`
- `ChildWorkflowExecutionTimedOut`
- `ChildWorkflowExecutionTerminated`
- `StartChildWorkflowExecutionFailed`
- `WorkflowExecutionUpdateAccepted`
- `WorkflowExecutionUpdateCompleted`

P1 对业务开发者最重要的 SDK 能力：

- `ExecuteActivity`
- `ExecuteChildWorkflow`
- `Sleep`
- `Await`
- `Selector`
- `SignalChannel`
- `SetQueryHandler`
- `SetUpdateHandler`
- `GetVersion`
- `SideEffect`
- `MutableSideEffect`
- `ContinueAsNew`
- `NewDisconnectedContext`
- `Activity heartbeat`
- `Async activity completion`

### P2: 平台化和升级治理

P2 的目标是让系统长期运行、灰度升级、按团队隔离。

强建议模块：

| 模块 | 能力 | 价值 |
| --- | --- | --- |
| Worker Versioning | task 按 build/deployment 路由 | 灰度发布和旧 run 兼容 |
| Sticky Workflow Task | 同一 workflow 优先回到同一 worker | 降低 replay 成本 |
| Sharding | workflow 按 namespace/workflow id 分片 | 横向扩展 History |
| Shard Ownership | 节点持有 shard lease | 多 history 节点运行 |
| Internal Queue Ack | transfer/timer/visibility ack level | 后台任务恢复 |
| Dynamic Config | namespace/task queue/cluster 维度配置 | 在线调参 |
| Rate Limit | task queue、namespace、worker 维度限流 | 保护依赖系统 |
| Priority/Fairness | 任务优先级和公平调度 | 多租户治理 |
| Search Attributes | 结构化检索字段 | 运维和业务查询 |
| Retention/Delete | 关闭 workflow 清理 | 存储成本控制 |
| Batch Operations | 批量 terminate/cancel/reset/signal | 运维批处理 |
| Metrics/Tracing/Logs | SDK/server 全链路观测 | 生产排障 |
| Schema Migration | expand/backfill/switch/contract | 数据库长期演进 |

P2 升级机制建议：

- Workflow 代码兼容由 `GetVersion`/marker 承担。
- worker 发布治理由 Worker Versioning 承担。
- Activity payload schema 使用显式 `SchemaVersion`。
- 数据库 schema 使用 expand -> backfill -> switch -> contract。
- replay fixture 作为 Workflow 兼容性测试门禁。

### P3: Temporal 高级能力对齐

P3 的目标是对齐 Temporal 平台级能力。

可选模块：

| 模块 | 能力 | 适用场景 |
| --- | --- | --- |
| Multi-cluster Replication | namespace 跨集群复制、failover | 异地容灾 |
| Conflict Resolution | 多集群 history 分叉收敛 | active-active 或 failover |
| Archival | history/visibility 归档到对象存储 | 长保留周期 |
| Schedules | 独立 scheduler、backfill、overlap policy | 定时触发 workflow |
| Cron Workflow | run 关闭后按 cron 新 run | 简单周期任务 |
| Nexus | 跨服务 operation 编排 | 服务间异步操作标准化 |
| Speculative Workflow Task | Update/query 低写放大优化 | 高频 update/query |
| Eager Workflow/Activity Task | 响应里直接带下一任务 | 降低延迟 |
| Worker Deployment API | deployment/ramping/version rules | 大规模 worker 发布治理 |
| Namespace Lifecycle | register/update/deprecate/delete namespace | 多团队租户 |
| Auth/RBAC | token、mTLS、权限、审计 | 企业内控 |
| Encryption Codec | payload 加密和 codec server | 敏感数据 |
| Web UI Advanced | pending 视图、reset 点、activity 操作、event diff、worker versioning 面板 | 高级运维 |
| Activity Operability | pause/unpause/reset activity | 生产故障干预 |

### 模块依赖关系

```text
SDK Runtime
  -> Frontend API
  -> History Engine
  -> Event Store + Mutable State
  -> Transfer/Timer Tasks
  -> Matching
  -> Workers

Signal/Update/Query
  -> History Engine
  -> Workflow Task
  -> SDK handler

Activity Retry
  -> Retry Policy
  -> Mutable State ActivityInfo
  -> Timer Queue
  -> Matching Activity Task

Continue-As-New / Workflow Retry / Cron
  -> close current run
  -> create new run
  -> update current pointer

Child Workflow
  -> parent history events
  -> child workflow execution
  -> parent close policy worker

Visibility
  -> history transition emits visibility task
  -> visibility store index
```

### 推荐实现顺序

1. 单机 P0：Postgres/MySQL + in-process matching + Go SDK prototype。
2. Activity/Timer/Retry：跑通长时间等待、失败重试、worker 崩溃恢复。
3. Determinism：实现 replay checker、command/event matching、`GetVersion` marker。
4. Signal/Query/Update：支持外部业务事件推进。
5. Continue-As-New 和 Child Workflow：支持长流程和模块化编排。
6. Async Activity Completion：接 MQ/webhook 回调型任务。
7. Saga Helper：把补偿栈做成 SDK 层开发体验。
8. Visibility/Web UI：先做 SQL visibility 和最小 History Viewer。
9. Sticky/Worker Versioning：降低 replay 成本并支持灰度。
10. Sharding/多节点：进入平台化部署。

### 需要你拍板的特性

这些能力会显著影响架构复杂度，建议逐项决定：

| 决策项 | 选项 A | 选项 B | 默认建议 | 当前决策 |
| --- | --- | --- | --- | --- |
| SDK 语言 | 先做 Go SDK | 直接设计跨语言协议 | 先做 Go SDK | Go SDK |
| Workflow 写法 | Go 代码 deterministic runtime | 自定义 DSL/状态机配置 | Go 代码 runtime | Go SDK deterministic runtime |
| 数据库 | Postgres/MySQL 单库 | Cassandra/分布式 KV | Postgres/MySQL 单库 | MySQL/InnoDB |
| 服务形态 | 单体服务内分模块 | Frontend/History/Matching 多进程 | 单体模块化起步 | 单体服务内分模块 |
| Matching | DB backlog + long poll | 内存队列 + DB 兜底 | DB backlog + long poll | 参考 `/Users/ubuntu/cuti/taskq` 的分区 Matching |
| Membership | DB heartbeat + hash ring + DB lease | 外部服务发现 | DB heartbeat + hash ring + DB lease | 参考 `/Users/ubuntu/cuti/taskq/membership` |
| Visibility | SQL 基础查询 | OpenSearch/ES 高级检索 | SQL 基础查询 | SQL 基础查询 |
| Worker Versioning | P2 再做 | P0/P1 内置 | P2 再做 | 首版进入设计 |
| Multi-cluster | P3 再做 | 初始支持 | P3 再做 | 待定 |
| Schedule/Cron | P1 做 Cron，P3 做 Schedule | 直接完整 Schedule | P1 Cron | 待定 |
| Async Activity | P1 做通用 completion API | 内置 MQ connector | 通用 completion API | 通用 completion API |
| Signal/Query/Update | 首版全部实现 | 先 Signal + Query，后续补 Update | 首版 Signal + Query | 首版全部实现 |
| Saga Helper | SDK 层工具 | Server 原生 Saga 事件 | SDK 层工具 | SDK 层工具 |
| Activity 操作 | pause/reset/unpause | 保留 retry/timeout | retry/timeout 起步 | pause/reset/unpause 全部保留 |
| Web UI | 最小 history viewer | 完整运维控制台 | 最小 history viewer | 最小 history viewer |
| Auth/RBAC | 内网信任起步 | 首版内置权限模型 | 内网信任起步 | 待定 |

第一批关键决策：

1. Workflow SDK 使用 Go SDK deterministic runtime。已确认。
2. 首版数据库使用 MySQL/InnoDB。已确认。
3. 首版采用单体服务内分模块。已确认。
4. Matching/Membership 参考 `/Users/ubuntu/cuti/taskq` 的分区模型。已确认。
5. Signal、Query、Update 首版全部实现。已确认。
6. Worker Versioning 首版进入设计。已确认。
7. Visibility 首版使用 SQL 基础查询。已确认。
8. Async Activity 首版使用通用 completion API。已确认。
9. Saga Helper 放在 SDK 层。已确认。
10. Activity pause/reset/unpause 首版全部保留。已确认。
11. Web UI 首版做最小 history viewer。已确认。

Go SDK runtime 的设计含义：

- Workflow 用 Go 函数表达业务流程。
- SDK runtime 负责 replay event history，重建 future、selector、signal channel、activity result、timer result。
- Workflow API 暴露 `ExecuteActivity`、`ExecuteChildWorkflow`、`Sleep`、`Await`、`Selector`、`GetVersion`、`ContinueAsNew`。
- Workflow 代码遵守 deterministic 约束；随机数、当前时间、外部 IO 通过 `SideEffect`、Activity 或 runtime API 进入 history。
- Server 只处理 command/event/task，业务代码运行在 worker 进程。
- 后续可在 Go SDK runtime 之上增加 Saga Helper，形式是 SDK 库函数。

MySQL/InnoDB 的设计含义：

- workflow execution、event history、task queue backlog、timer task 都落在 MySQL 表。
- 核心一致性依赖 InnoDB 事务：同一次状态转换同时写 events、mutable state、internal tasks。
- `workflow_executions.version` 做 optimistic lock，防止并发更新同一个 run。
- `current_executions` 保存同一 Workflow ID 的当前 run 指针，Continue-As-New 和 Workflow retry 在同一事务里切换。
- 后台 transfer/timer/visibility queue 使用表扫描 + ack level 起步，后续再按 shard 分区优化。

单体模块化服务的设计含义：

- 一个进程内包含 Frontend API、History Engine、Matching、Timer Processor、Internal Queue Processor、Visibility Writer。
- 模块之间先用 Go interface 调用，接口形状对齐未来 RPC 边界。
- Frontend 模块只做鉴权、参数校验、幂等 request id、路由到 History。
- History 模块独占 workflow 状态转换，负责 event append、mutable state、internal tasks。
- Matching 模块负责 task queue、long poll、worker poller 管理和 backlog。
- Timer/Internal Queue 模块后台扫描 MySQL tasks 表并回调 History/Matching。
- Visibility 模块先写 MySQL 查询表，后续可替换成 OpenSearch writer。
- 模块包边界按未来进程拆分设计，避免业务逻辑跨模块直接读写表。

taskq 对 Matching/Membership 的参考价值：

- `/Users/ubuntu/cuti/taskq/membership` 已实现 DB heartbeat、成员轮询、ServiceResolver、HashRing、ChangedEvent。
- `/Users/ubuntu/cuti/taskq/matching` 已实现 partition manager、分区所有权判断、long poll、gRPC handler、membership change reconcile。
- `/Users/ubuntu/cuti/taskq/queue` 已实现单分区 queue 的 delivery state、waiter registry、visibility timeout、DB lease、RangeID CAS。
- `docs/superpowers/specs/2026-04-21-single-partition-actor-design.md` 的单分区 actor 设计适合迁移到 Temporal task queue partition。

对自研 Temporal 的落地方式：

- `TaskQueuePartition` 作为单分区 actor，串行处理 add task、poll、start task、timeout、cancel waiter、ownership lost。
- Membership 哈希环提供建议所有权：`Lookup(taskQueueType/taskQueueName/partitionID)`。
- DB lease 提供实际所有权：拿到 lease 的节点创建 `TaskQueuePartition` actor。
- RangeID/lease epoch 进入 task token，worker 上报 started/completed 时用于 fencing。
- Matching 模块保留 DB backlog + long poll：任务先写 MySQL backlog，再尝试 sync match 唤醒等待中的 poller。
- ChangedEvent 触发 partition manager reconcile：加载新分区，关闭迁出的分区。
- 节点崩溃恢复依赖 heartbeat cutoff + poll interval + lease TTL。

Temporal 和普通 taskq 的语义映射：

| taskq 概念 | Temporal 对应概念 | 迁移方式 |
| --- | --- | --- |
| `AddTask` | `AddWorkflowTask` / `AddActivityTask` | History transfer task 投递到 Matching |
| `PollTask` | Worker poll Workflow/Activity task | long poll + sync match |
| `AckTask` | `RecordWorkflowTaskStarted` / `RecordActivityTaskStarted` | worker 拿到任务时由 History 写 started event |
| `NackTask` | Workflow/Activity task fail/timeout | completion API 或 timeout task 驱动 |
| visibility timeout | Activity start-to-close / heartbeat timeout | History timer queue 负责最终事件 |
| queue partition | task queue partition | task queue type + task queue name + partition id |
| token | task token | namespace/workflow/run/scheduled/started/attempt/lease epoch |

实现时保留 taskq 的这些设计：

- DB heartbeat membership。
- HashRing + virtual nodes。
- ServiceResolver 原子替换 ring。
- ChangedEvent listener。
- partition manager reconcile。
- DB lease + RangeID CAS 双层 fencing。
- 单分区 actor。
- waiter registry + long poll。
- sync match。

Temporal 场景里的调整：

- Workflow/Activity task 的最终状态写在 History，Matching 只负责投递和 poller 管理。
- started/completed/failed 由 History API 处理，Matching 返回 task token 给 worker。
- Activity retry 由 History timer queue 重新投递到 Matching。
- Workflow Task sticky queue 和 worker versioning 在 P2 接入。

Signal/Query/Update 首版实现边界：

- Signal 是异步写入事件：Frontend 接收 signal request，History 追加 `WorkflowExecutionSignaled`，创建 Workflow Task，SDK replay 后从 signal channel 读取。
- Signal 需要 `signal_id` 或 request id 幂等，重复 signal 返回同一处理结果或静默去重。
- Query 是只读请求：优先发给持有 sticky cache 的 worker，worker replay 到最新事件后执行 query handler 返回内存状态。
- Query 需要支持 strong query：如果当前 run 有未处理事件，先调度 Workflow Task，等 worker 推进到最新 event 后再 query。
- Update 是带返回值的业务推进请求：History 先记录 accepted 状态，Workflow worker 执行 update handler，完成后记录 completed 状态并返回结果。
- Update 首版采用 accepted/completed 两阶段模型，支持 caller 等待 accepted 或 completed。
- Update handler 内可以调 Activity、timer、child workflow；这要求 Update 进入 Workflow Task command 流程。
- Update request 需要 update id 幂等，重复请求能拿到已有 accepted/completed 状态。

Signal/Query/Update 对 event 和 mutable state 的要求：

```text
Signal:
  WorkflowExecutionSignaled
  mutable_state.signal_requested_ids

Query:
  mutable_state.query_buffer / sticky worker hint
  query 本身不写 history

Update:
  WorkflowExecutionUpdateAccepted
  WorkflowExecutionUpdateCompleted
  mutable_state.update_registry
```

实现优先级：

1. Signal：先落库事件并触发 Workflow Task。
2. Query：先支持非 sticky strong query，再优化 sticky query。
3. Update：实现 update registry、accepted/completed 状态、幂等 update id。

Worker Versioning 首版实现边界：

- worker 启动时注册 `task_queue`、`worker_type`、`build_id`、`deployment_name`、`versioning_behavior`。
- Workflow run 在 first Workflow Task started/completed 时记录当前 `build_id` 或 deployment version。
- History 生成 Workflow Task / Activity Task 时写入 `version_directive`。
- Matching poller 带自己的 `build_id` 和 supported task queues。
- Matching 只把带 version directive 的 task 投递给兼容 build id 的 poller。
- Workflow run 支持两种行为：`PINNED` 固定到首次运行 build，`AUTO_UPGRADE` 按 task queue 当前规则升级。
- 首版灰度用 task queue 级别规则：default build、compatible build set、ramping percentage。
- `RespondWorkflowTaskCompleted` 校验 worker 上报的 build id 与 mutable state/directive 匹配。
- Continue-As-New、Workflow retry、Child Workflow 启动时继承或重算 versioning behavior。

最小数据结构：

```text
worker_builds
  task_queue, worker_type, build_id, deployment_name, version, state,
  first_seen_at, last_seen_at

task_queue_versioning_rules
  task_queue, worker_type, default_build_id, compatible_sets_json,
  ramping_build_id, ramping_percentage, updated_at

workflow_executions.mutable_state_json
  versioning_behavior
  assigned_build_id
  inherited_build_id
  last_worker_version_stamp

task_queue_tasks.payload_json
  version_directive
```

首版路由规则：

```text
PINNED:
  task.version_directive = assigned_build_id
  Matching 只交给同 build_id worker

AUTO_UPGRADE:
  task.version_directive = task queue 当前 default/ramping build
  新 Workflow Task 可根据规则切到新 build

UNVERSIONED:
  task.version_directive 为空
  任意同 task queue worker 可接
```

与 `GetVersion` 的关系：

- `GetVersion` 负责 Workflow 代码内部兼容分支。
- Worker Versioning 负责把 task 投递给合适 worker。
- 首版实现两者都保留，因为 Worker Versioning 解决部署路由，`GetVersion` 解决 history replay 分支。

SQL Visibility 首版实现边界：

- 使用 MySQL 表保存 Workflow 可查询摘要。
- 每次 Workflow 状态转换后，同事务写 visibility task，后台 Visibility Writer 更新查询表。
- 首版支持按 namespace、workflow id、workflow type、status、task queue、start time、close time 查询。
- Search Attributes 首版作为 JSON 存储，先支持精确匹配和少量固定字段索引。
- Web UI 和 CLI 都走 SQL visibility 查询。
- 后续接 OpenSearch 时，Visibility Writer 增加第二个 sink，History 主链路保持稳定。

最小数据结构：

```text
workflow_visibility
  namespace_id
  workflow_id
  run_id
  workflow_type
  task_queue
  status
  start_time
  close_time
  execution_time
  memo_json
  search_attributes_json
  history_length
  assigned_build_id
  update_time

workflow_visibility_search_attrs
  namespace_id
  attr_name
  attr_value
  workflow_id
  run_id
```

首版索引建议：

```text
idx_visibility_namespace_status_start(namespace_id, status, start_time)
idx_visibility_namespace_type_start(namespace_id, workflow_type, start_time)
idx_visibility_namespace_workflow(namespace_id, workflow_id)
idx_visibility_task_queue_start(namespace_id, task_queue, start_time)
idx_visibility_build_start(namespace_id, assigned_build_id, start_time)
idx_visibility_attr(namespace_id, attr_name, attr_value)
```

Async Activity Completion API 首版实现边界：

- Activity worker 可以返回 pending sentinel，表示 Activity result 由外部进程稍后完成。
- completion API 支持按 task token 完成：`CompleteActivity(task_token, result, failure)`。
- completion API 支持按业务定位完成：`CompleteActivityByID(namespace, workflow_id, run_id, activity_id, result, failure)`。
- failure completion 进入 Activity failure/retry 流程，success completion 写 `ActivityTaskCompleted`。
- server 校验 pending Activity、attempt、scheduled event id、started event id、run id、lease epoch。
- completion 成功后创建 Workflow Task，让 `future.Get()` 拿到 result 或 error。
- 重复 completion 返回稳定错误码，外部 MQ consumer 可按幂等成功处理。
- Activity timeout 继续由 History timer queue 负责。

最小 API：

```text
CompleteActivity(request)
  namespace
  task_token
  result_payload
  failure
  identity
  request_id

CompleteActivityByID(request)
  namespace
  workflow_id
  run_id
  activity_id
  result_payload
  failure
  identity
  request_id
```

最小事件和状态：

```text
Activity 返回 pending:
  ActivityInfo.async_pending = true
  ActivityInfo.task_token = token
  ActivityInfo.last_heartbeat_details 可选

CompleteActivity success:
  ActivityTaskCompleted
  delete pending ActivityInfo
  create WorkflowTask

CompleteActivity failure:
  RetryActivity(...)
  或 ActivityTaskFailed + create WorkflowTask
```

和 MQ 的关系：

- 框架只提供 completion API。
- MQ publish、MQ consume、去重表、外部 result id 由业务 Activity 或业务 adapter 实现。
- Activity input 里使用业务幂等键，MQ message 携带 task token 或 activity ID。

SDK Saga Helper 首版实现边界：

- Saga Helper 是 Go SDK 库函数，Server 感知到的仍然是普通 Activity、Child Workflow、Timer、Marker command。
- Saga Helper 维护 deterministic compensation stack。
- 每个 step 成功后，把补偿所需 input 固化到 Workflow 内存状态；replay 时通过 Activity/Child result 重建同一补偿栈。
- 补偿执行顺序是成功 step 的反向顺序。
- 补偿本身使用 Activity 或 Child Workflow，实现幂等。
- Saga Helper 支持 `GetVersion`，step 协议变化时由调用方显式包版本。
- Saga Helper 可以被父 Workflow 和子 Workflow 各自使用；父 Workflow 保存子域级补偿入口，子 Workflow 管理内部补偿栈。

推荐 API 形态：

```go
type Step[In any, Out any, UndoIn any] struct {
    Name      string
    Input     In
    Do        func(workflow.Context, In) (Out, error)
    BuildUndo func(In, Out) UndoIn
    Undo      func(workflow.Context, UndoIn) error
}

type Saga struct {
    compensations []func(workflow.Context) error
}

func (s *Saga) Do(ctx workflow.Context, step Step[In, Out, UndoIn]) (Out, error)
func (s *Saga) Compensate(ctx workflow.Context) error
```

使用形态：

```go
payment, err := saga.Do(ctx, Step[PaymentInput, PaymentResult, RefundInput]{
    Name:  "payment",
    Input: paymentInput,
    Do:    Pay,
    BuildUndo: func(in PaymentInput, out PaymentResult) RefundInput {
        return RefundInput{
            OrderID:   in.OrderID,
            PaymentID: out.PaymentID,
            Amount:    in.Amount,
        }
    },
    Undo: Refund,
})
if err != nil {
    _ = saga.Compensate(ctx)
    return err
}
```

设计约束：

- `BuildUndo` 只使用 step input、step output 和稳定 Workflow input。
- `Undo` 内部通过 Activity/Child Workflow 产生外部副作用。
- compensation payload 使用结构化类型，避免依赖闭包捕获隐式变量。
- 补偿失败的处理策略首版采用 best-effort + 返回聚合错误；需要强保证的补偿用 Child Workflow 包装。

Activity Operability 首版实现边界：

- 支持 pause activity：暂停等待 retry 或等待调度的 Activity。
- 支持 unpause activity：恢复 paused Activity，并按策略立即调度或按原 backoff 调度。
- 支持 reset activity：重置 attempt、失败信息、heartbeat details，并生成新的 retry task。
- 支持 force complete activity：人工写入 `ActivityTaskCompleted`，触发 Workflow Task。
- 支持 force fail activity：人工写入 failure，进入 Activity retry 或最终 `ActivityTaskFailed`。
- 支持按 `activity_id` 和 `scheduled_event_id` 定位 Activity。
- 所有人工操作写审计记录，并进入 visibility/admin log。

最小 API：

```text
PauseActivity(namespace, workflow_id, run_id, activity_id, reason, identity)
UnpauseActivity(namespace, workflow_id, run_id, activity_id, reset_heartbeats, identity)
ResetActivity(namespace, workflow_id, run_id, activity_id, reset_heartbeats, reset_attempts, identity)
CompleteActivityByID(namespace, workflow_id, run_id, activity_id, result, identity)
FailActivityByID(namespace, workflow_id, run_id, activity_id, failure, identity)
```

最小状态：

```text
ActivityInfo.paused
ActivityInfo.pause_info
ActivityInfo.stamp
ActivityInfo.attempt
ActivityInfo.retry_last_failure
ActivityInfo.last_heartbeat_details
ActivityInfo.next_attempt_schedule_time
```

设计要点：

- `stamp` 或 lease epoch 用来让旧 retry timer、旧 task token、旧 poll 结果失效。
- pause 作用于 pending Activity；running Activity 收到 pause 后通过 heartbeat 感知取消或暂停请求。
- reset 会递增 `stamp`，防止旧 attempt 回写覆盖新状态。
- force complete/fail 走同一套 Activity completion/failure handler，避免绕开 History 状态机。

Web UI 首版：最小 History Viewer

首版目标是给开发和运维提供一个只读排障入口，核心能力聚焦在“找到一次 Workflow Run，并看清楚它的 Event History”。

首版实现边界：

- Workflow 列表页来自 SQL visibility，支持按 namespace、workflow id、run id、status、workflow type、task queue、时间范围过滤。
- Workflow 详情头展示 namespace、workflow id、run id、workflow type、status、task queue、start time、close time、attempt、assigned build id、parent workflow。
- Event History 时间线展示 event id、event type、event time、核心关联字段，比如 `activity_id`、`timer_id`、`child_workflow_id`、`scheduled_event_id`、`started_event_id`。
- Event JSON 抽屉展示完整 `attributes_json`，payload 首版按原始编码展示，后期接 codec server 解码。
- History 使用 event id 游标分页，默认每页 100 条，支持跳转到指定 event id。
- 支持按 event type 过滤，支持在当前页内搜索 activity id、timer id、child workflow id、failure message。
- 支持复制 namespace、workflow id、run id、event id、activity id、scheduled event id、task token 摘要字段。
- 支持失败事件高亮：`WorkflowTaskFailed`、`ActivityTaskFailed`、`ActivityTaskTimedOut`、`ChildWorkflowExecutionFailed`、`WorkflowExecutionFailed`。

首版页面：

```text
/workflows
/workflows/:namespace/:workflow_id/:run_id/history
```

首版后端 API：

```text
ListWorkflows(filter, page)
DescribeWorkflow(namespace, workflow_id, run_id)
GetWorkflowHistory(namespace, workflow_id, run_id, start_event_id, page_size, event_type_filter)
```

首版查询来源：

```text
workflow_executions
  详情头、status、type、task_queue、start/close time、attempt、assigned build id

workflow_events
  event_id、event_type、event_time、attributes_json、batch_id

current_executions
  当前 run 指针，用于 workflow id 入口跳转
```

首版必要索引：

```text
workflow_executions(namespace_id, status, create_time)
workflow_executions(namespace_id, workflow_id, run_id)
workflow_executions(namespace_id, workflow_type, create_time)
workflow_executions(namespace_id, task_queue, create_time)
workflow_events(namespace_id, workflow_id, run_id, event_id)
workflow_events(namespace_id, workflow_id, run_id, event_type, event_id)
```

前端组件建议：

| 组件 | 职责 |
| --- | --- |
| WorkflowList | 查询条件、分页、run 列表 |
| WorkflowHeader | 详情头和复制字段 |
| HistoryTimeline | 按 event id 顺序展示事件 |
| EventInspector | 展示核心字段和完整 JSON |
| FailureBadge | 标识失败、超时、取消事件 |
| CopyField | 统一复制 namespace/workflow id/run id/event id |

后期可推进功能用于确定下一阶段方向：

| 阶段 | 功能 | 解决的问题 | 前置依赖 | 推进信号 |
| --- | --- | --- | --- | --- |
| H1 | Pending Activities 只读页 | 看当前卡住的 Activity、attempt、last failure、next retry time、heartbeat details | Mutable State 查询 API | Activity 故障排查频繁依赖 SQL |
| H1 | Pending Workflow Task 只读页 | 看 Workflow Task 调度、启动、失败、sticky/build directive | Mutable State 查询 API | Workflow 卡住但 history 只有调度事件 |
| H1 | Signal/Update/Child 只读页 | 看外部事件、Update 状态、子 Workflow 状态 | Signal/Update/Child state 查询 API | 长流程开始大量使用 Signal/Child |
| H1 | Failure summary | 把失败事件、failure message、retry state 聚合到详情头 | failure parser | history 事件很多，人工定位失败耗时 |
| H1 | Payload codec UI | 解码 payload、failure details、heartbeat details | codec server、权限模型 | payload 原始编码影响排障效率 |
| H1 | Workflow graph | 用 history 生成 Activity/Timer/Child 关系图 | history parser | 单条 history 超过数百事件 |
| H2 | Activity pause/unpause/reset 操作 | 生产故障干预 Activity retry 和卡住状态 | Activity operability API、审计日志 | 运维开始需要从 UI 处理单个 Activity |
| H2 | Force complete/fail Activity | 人工修复异步 Activity 或外部回调丢失 | completion API、审计日志 | MQ/webhook 回调型 Activity 增多 |
| H2 | Reset Workflow UI | 从指定 event 生成新 run | reset API、history branch | 升级兼容和人工修复需要回放到中间点 |
| H2 | Workflow diff | 对比两个 run、reset 前后、版本升级前后 history | history parser | 多版本升级后需要分析行为差异 |
| H2 | Replay diagnostics | 上传 history 或选择 run，用当前 worker replay | replay test runner、worker registry | 升级发布前需要验证 determinism |
| H2 | Worker Versioning 面板 | 查看 build、ramping、pinned run、task queue 规则 | versioning rules、worker heartbeat | 首版 Worker Versioning 开始灰度 |
| H2 | Task Queue backlog | 查看 backlog、poller、分区 owner、RangeID | Matching metrics/API | 任务堆积和 worker 扩容排查成为常态 |
| H2 | Search attributes builder | 可视化组合查询条件 | visibility schema、SQL query builder | 查询条件逐渐复杂 |
| H3 | Batch operations | 批量 cancel、terminate、signal、reset | batch worker、审计日志 | 大规模错误数据修复需求出现 |
| H3 | Schedule/Cron 管理 | 创建、暂停、触发、回填周期任务 | Schedule/Cron 模块 | 定时工作流成为平台能力 |
| H3 | Namespace 管理 | retention、limits、状态、owner | namespace lifecycle、Auth/RBAC | 多团队接入后需要租户治理 |
| H3 | Audit log | 查看人工操作记录 | audit table | UI 出现写操作后必须上线 |
| H3 | Metrics dashboard | 延迟、失败率、retry、backlog、poller | metrics/tracing | 平台稳定性需要趋势视图 |
| H3 | RBAC | 控制 namespace、workflow、operation 权限 | authn/authz、审计日志 | 多团队、多环境、生产权限隔离 |
| H3 | Archival viewer | 查看对象存储中的历史归档 | archival store | retention 之外仍需查历史 |

## 重试机制全景

Temporal 的 retry 分成业务 retry、worker/task retry、server internal retry、RPC/persistence retry 四层。自研实现要把这些层分开建模，避免一个 retry policy 同时承担业务重试和基础设施重试。

### 1. RPC 和服务调用重试

服务间 gRPC 失败会在 handler 和 client 两侧按错误类型重试：

- 通用 retry 原语在 `common/backoff`，架构文档是 `docs/architecture/retry.md`。
- `backoff.IsRetryable` 判断错误类型，`backoff.RetryPolicy` 决定 backoff。
- 各服务 handler 使用 `interceptor.RetryableInterceptor`，如 Frontend/History/Matching 的 `RetryableInterceptorProvider`。
- 服务 client 使用 retryable client，比如 Frontend 调 History、History 调 Matching。
- `ResourceExhausted` 使用专门策略，避免过载时雪崩。

自研建议：

- RPC retry 只处理网络抖动、leader/shard lease 切换、瞬时资源错误。
- RPC retry 和业务 Activity retry 分开配置。
- 所有写请求带 request id 或 task token，保证 retry 后幂等。

### 2. Worker 上报结果的重试

Worker 执行完 Workflow Task 或 Activity Task 后，会调用 server completion API：

```text
Workflow Task:
  RespondWorkflowTaskCompleted
  RespondWorkflowTaskFailed

Activity Task:
  RespondActivityTaskCompleted
  RespondActivityTaskFailed
  RespondActivityTaskCanceled
  RecordActivityTaskHeartbeat
```

这些上报请求的可靠性来自三层：

1. SDK client / gRPC client 对 retryable 错误进行 RPC retry。
2. server 用 task token 校验 namespace、workflow id、run id、scheduled event id、started event id、attempt、clock。
3. History commit 成功后，重复 completion 会被 mutable state 校验过滤，worker 再 poll 或拉 history 即可回到最新状态。

`RespondWorkflowTaskCompleted` 的关键校验：

- 反序列化 task token，拿到 namespace/workflow/run/scheduled event。
- 校验 Workflow Task scheduled/started/attempt/version。
- 校验 worker build id 和 version directive。
- command 转 event 后在同一事务里提交 history、mutable state、tasks。

`RespondActivityTaskCompleted` 的关键校验：

- task token 指向 pending Activity。
- `CompleteActivityByID` 路径用 activity ID 查 scheduled event id。
- `IsActivityTaskNotFoundForToken` 校验 token 和当前 attempt。
- 成功后追加 `ActivityTaskCompleted`，创建新的 Workflow Task。

自研建议：

- completion API 设计成 at-least-once safe。
- token 中包含 scheduled event id、started event id、attempt、run id。
- server commit 成功后的重复 completion 返回稳定错误码，worker/consumer 将其归类为幂等完成。

### 3. Workflow Task retry

Workflow Task 是驱动 Workflow 代码 replay 和产生命令的任务。失败来源包括 worker crash、task timeout、determinism mismatch、`RespondWorkflowTaskCompleted` 处理失败、worker 主动 `RespondWorkflowTaskFailed`。

Temporal 的 Workflow Task retry 形态：

- normal Workflow Task：history 中有 `WorkflowTaskScheduled`、`WorkflowTaskStarted`。
- transient Workflow Task：前一次 Workflow Task 失败后，下一次 attempt 的 scheduled/started 事件随 poll response 发给 worker，减少 history 噪声。
- speculative Workflow Task：主要用于 Workflow Update，先用内存任务尝试，成功路径减少数据库写入。

行为要点：

- Workflow Task failure 会追加 `WorkflowTaskFailed` 或 timeout event。
- server 增加 attempt，并调度下一次 Workflow Task。
- 后续 attempt 成功时，server 再把必要的 scheduled/started/completed 事件写回 history。
- determinism mismatch 属于 Workflow Task failure，流程本身保持 running，直到兼容代码部署或人工 reset/terminate。

源码参考：

- `docs/architecture/speculative-workflow-task.md:14-27` 描述 normal -> transient retry。
- `docs/architecture/speculative-workflow-task.md:157-184` 描述 speculative Workflow Task failure/timeout 后转 normal retry。

自研建议：

- P0 可先实现 normal Workflow Task retry。
- P1 加 transient Workflow Task，降低 replay bug 或 worker crash 带来的 history 增长。
- P3 再做 speculative Workflow Task。

### 4. Activity Task retry

Activity retry 是业务重试主路径。Activity worker 返回失败或 timeout 后，History 根据 Activity `RetryPolicy` 决定下一次 attempt。

关键流程：

```text
Activity worker 执行失败
  -> RespondActivityTaskFailed
  -> mutableState.RetryActivity
  -> 计算 next backoff
  -> 写 ActivityRetryTimerTask
  -> timer 到期后 AddActivityTask 到 Matching
  -> worker poll 到下一次 attempt
```

Activity retry 的实现特点：

- `RespondActivityTaskFailed` 调用 `mutableState.RetryActivity`。
- retry 仍在进行时，失败 attempt 的详情主要保存在 pending activity mutable state。
- retry 结束时，History 追加最终 `ActivityTaskFailed`，并创建 Workflow Task，让 Workflow 代码收到错误。
- `ActivityRetryTimerTask` 到期后，timer executor 直接调用 Matching `AddActivityTask`。
- `ActivityInfo.Attempt`、`ScheduledTime`、`LastAttemptCompleteTime`、`RetryLastFailure`、`HeartbeatDetails` 组成 retry 状态。

源码参考：

- `service/history/api/respondactivitytaskfailed/api.go:97-121`
- `service/history/workflow/retry.go:32-113`
- `service/history/workflow/task_generator.go:569-579`
- `service/history/timer_queue_active_task_executor.go:522-638`

RetryPolicy 关键字段：

- `InitialInterval`
- `BackoffCoefficient`
- `MaximumInterval`
- `MaximumAttempts`
- `ExpirationTime`
- `NonRetryableErrorTypes`
- application failure 的 `NextRetryDelay`

失败类型判断：

- canceled/terminated 进入终止态。
- start-to-close timeout 和 heartbeat timeout 可按 retry policy 重试。
- schedule-to-start timeout 和 schedule-to-close timeout 通常进入 timeout 终止态。
- application failure 的 `NonRetryable` 或 type 命中 `NonRetryableErrorTypes` 时进入终止态。

自研建议：

- Activity 实现 at-least-once 执行语义。
- Activity handler 使用业务幂等键。
- retry 中间态保存在 mutable state，最终失败再进入 Workflow 可见 history。
- 支持 heartbeat details，重试后 worker 能恢复进度。

### 5. Async Activity completion retry

Async Activity completion 依赖外部进程上报结果，retry 边界来自 MQ consumer 和 completion API。

关键点：

- Activity 返回 pending sentinel 后，server 保持 pending Activity。
- MQ consumer 调 `CompleteActivity` 或 `CompleteActivityByID`。
- completion RPC 可 retry。
- task token 指向当前 attempt，旧 attempt token 会被 server 过滤。
- `CompleteActivity(..., nil, err)` 会进入 Activity failure/retry 流程。

自研建议：

- MQ message 保存 task token 或 activity ID。
- MQ consumer 保存去重表，按业务 result id 幂等处理。
- Activity timeout 覆盖外部系统最长等待时间。
- heartbeat 可保存外部 job id、进度游标、最近状态。

### 6. Workflow Execution retry

Workflow retry 是整个 run 失败后的新 run。它和 Activity retry 的粒度不同：Activity retry 只重跑一个 Activity attempt，Workflow retry 会创建新的 Workflow run。

关键流程：

```text
WorkflowExecutionFailed
  -> 根据 Workflow RetryPolicy 计算 backoff
  -> 创建 new run
  -> ContinueAsNewInitiator = RETRY
  -> current_executions 指向 new run
  -> new run attempt = old attempt + 1
```

源码参考：

- `service/history/api/respondworkflowtaskcompleted/workflow_task_completed_handler.go:859-887`
- `service/history/workflow/retry.go:156-360`

自研建议：

- Workflow retry 复用 Continue-As-New 的 run 链模型。
- new run 继承 workflow id、input、retry policy、memo/search attributes、parent/root 信息。
- retry backoff 通过 first workflow task backoff 实现。
- child workflow retry 直接落到 child workflow 自己的 Workflow RetryPolicy。

### 7. Timer 和 timeout retry

Timer queue 负责所有时间驱动动作：

- user timer / sleep 到期。
- Workflow Task timeout。
- Activity schedule-to-start / start-to-close / schedule-to-close / heartbeat timeout。
- Activity retry backoff 到期。
- Workflow run/execution timeout。
- Workflow backoff timer。

timer task 处理失败后，internal task scheduler 根据 task retry policy 重试。standby/replication 场景下，`ErrTaskRetry` 表达“条件尚未满足，稍后重试”。

自研建议：

- timer task 使用 `visibility_time` 排序。
- task handler 具备幂等校验，例如 event id、attempt、stamp。
- 过期 task 通过 mutable state 判断有效性。
- timeout event 写入 history 后再创建 Workflow Task。

### 8. Internal task retry

History 的 transfer/timer/visibility/replication/archival queues 都是 transactional outbox。业务状态事务提交后，后台队列负责最终投递。

关键行为：

- state transition 同事务写 history、mutable state、internal tasks。
- queue processor 拉取 tasks。
- task executor 调用 Matching、Visibility、Archival、Replication 等目标。
- task 失败后按 retry policy 重试。
- task 成功后推进 ack level。

源码参考：

- `docs/architecture/history-service.md:176-206`
- `common/tasks/execution_queue_scheduler.go:262-265`
- `common/tasks/fifo_scheduler.go:201-204`
- `common/tasks/sequential_scheduler.go:332-334`

自研建议：

- transfer queue 投递 Workflow/Activity Task 到 Matching。
- timer queue 处理时间事件。
- visibility queue 更新查询索引。
- 每个 task handler 都通过 mutable state 做二次校验。
- ack level 独立持久化，进程重启后继续扫描。

### 9. Matching 和 poll retry

Matching 负责把 History 生成的 task 交给 worker。

重试点：

- History transfer task 调 `AddWorkflowTask` / `AddActivityTask` 失败后，由 transfer task retry。
- worker poll long-poll 超时后继续 poll。
- sticky task queue 超时后回落到 normal task queue。
- task queue backlog 读写失败由 matching 内部 retry policy 处理。
- task 被 worker 拿到后，History 的 `RecordWorkflowTaskStarted` / `RecordActivityTaskStarted` 写 started event。

自研建议：

- Matching 的 task delivery 使用 at-least-once。
- worker start task 时写 started event，完成时用 token 校验。
- task queue backlog 可先用 SQL 表实现，再优化为分区队列。
- sticky queue 作为 P2 优化。

### 10. Manual retry / reset

生产运维还需要人工干预能力：

- reset Workflow 到某个 Workflow Task completed event 之后重新跑。
- retry failed Workflow，新建 run 或 reset run。
- force complete/fail Activity。
- pause/unpause/reset Activity。
- batch reset/cancel/terminate/signal。

自研建议：

- P1 实现 Workflow reset 和 force complete Activity。
- Activity pause/reset/unpause 放到 P3 或运维增强阶段。
- 所有人工操作写审计记录。

## Event History 和 Determinism

Workflow 的历史兼容以 event history 为准：

- history 中已完成的 activity、timer、child workflow、signal、marker 是 replay 的事实输入。
- SDK replay 时会按 history 重放旧结果，让 Workflow 代码回到同一个执行点。
- Workflow 代码在 replay 期间产生的 commands 需要和历史中对应事件匹配。
- command/event 序列发生分叉时，SDK 会报告 determinism mismatch，Workflow Task 失败，server 记录失败并调度后续 Workflow Task。

server 侧核心持久化点：

- `RecordMarker` command 会转成 `EVENT_TYPE_MARKER_RECORDED`：`service/history/api/respondworkflowtaskcompleted/workflow_task_completed_handler.go:990-1012`。
- Marker event 保存 `MarkerName`、`Details`、`Header`、`Failure`、`WorkflowTaskCompletedEventId`：`service/history/historybuilder/event_factory.go:795-810`。
- mutable state rebuild 遇到 `EVENT_TYPE_MARKER_RECORDED` 时无需 server mutable state 动作：`service/history/workflow/mutable_state_rebuilder.go:508-509`。

Marker 的价值主要在 SDK replay。`SideEffect`、`MutableSideEffect`、`GetVersion`/patch 都会通过 marker 固定一次历史选择或一次副作用结果。

## v1 升级 v2 时如何对齐旧 history

典型问题：

```go
// v1
workflow.ExecuteActivity(ctx, A)

// v2
workflow.ExecuteActivity(ctx, B)
workflow.ExecuteActivity(ctx, A)
```

已有运行的 history 里记录了 A 的 schedule/start/complete。v2 直接在前面插入 B，会让 replay 到旧 history 时生成 B command，历史当前位置却是 A event，序列分叉。

Temporal 的处理方式分三层。

### 1. SDK patch / `GetVersion` 固定分支

变更点包一层版本判断：

```go
v := workflow.GetVersion(ctx, "add-B-before-A", workflow.DefaultVersion, 1)
if v == workflow.DefaultVersion {
    workflow.ExecuteActivity(ctx, A)
} else {
    workflow.ExecuteActivity(ctx, B)
    workflow.ExecuteActivity(ctx, A)
}
```

行为：

- 旧 run replay 到这个点时，history 中已有旧 command 序列，`GetVersion` 返回 `DefaultVersion`，继续走 v1 分支。
- 新 run 首次走到这个点时，SDK 记录 version marker，返回 `1`，走 v2 分支。
- 后续 replay 读取 marker，稳定返回同一个版本。

server 看到的只是 `RecordMarker` command 和 `MarkerRecorded` event；真正的分支选择在 SDK replay 内完成。

本仓库中的实际用法：

- `service/worker/migration/force_replication_workflow.go` 用 `workflow.GetVersion(ctx, taskQueueUserDataReplicationVersionMarker, workflow.DefaultVersion, 1)` 控制新增 task queue user data replication 行为。
- `service/worker/migration/handover_workflow.go` 用 `workflow.GetVersion(ctx, "detach-handover-ctx-20250829", workflow.DefaultVersion, 1)` 控制 defer 中的修复逻辑。
- `service/worker/workerdeployment/workflow.go` 结合 `GetVersion` 和 `MutableSideEffect` 固定 workflow 实现版本。

### 2. Worker Versioning 把任务路由到兼容 worker

`GetVersion` 解决代码分支兼容，worker versioning 解决 worker 部署兼容。

关键点：

- History 生成 Workflow Task transfer task 时，会计算 `TaskVersionDirective`：`common/worker_versioning/worker_versioning.go:250-276`。
- transfer executor 调用 Matching `AddWorkflowTask` 时带上 `VersionDirective`：`service/history/transfer_queue_task_executor_base.go:147-173`。
- Matching 根据 directive 把 task 投递给对应 build/deployment 的 worker。
- `RespondWorkflowTaskCompleted` 会校验完成任务的 build ID 和 mutable state 中记录的 build ID：`service/history/api/respondworkflowtaskcompleted/api.go:220-240`。
- 若 workflow 正在 deployment transition，server 会让任务经过 Matching 路由到目标 worker：`service/history/api/respondworkflowtaskcompleted/api.go:552-574`。

这让正在跑的旧 run 能继续由兼容旧 history 的 worker 执行，新 run 或 auto-upgrade run 切到新 worker。

### 3. Replay tests 锁住历史兼容

Temporal server 自己的 internal workflows 会把旧版本 history 存成测试数据，然后用当前代码 replay：

- `service/worker/scheduler/replay_test.go:17-38` 使用 `worker.NewWorkflowReplayer()`，加载 `testdata/replay_*.json.gz`，调用 `ReplayWorkflowHistory`。
- 注释明确要求 Workflow 逻辑变化时采集新 history 并提交测试样本。

这是最直接的兼容性回归测试：当前 Workflow 代码必须能 replay 旧 history。

## Continue-As-New 的 run 关系与存储

Continue-As-New 是当前 run 正常关闭，并在同一个 Workflow ID 下创建后继 run。它常用于历史截断、replay 成本控制、状态压缩、版本收口。

组织关系是链式关系：

```text
Workflow ID: order-123

Run 1: run-a
  ...
  WorkflowExecutionContinuedAsNew(new_execution_run_id = run-b)

Run 2: run-b
  ...
  WorkflowExecutionContinuedAsNew(new_execution_run_id = run-c)

Run 3: run-c
  ...
```

执行角度：

- 每个 run 有自己的 event history。
- 每个 run 有自己的 mutable state。
- replay 当前 run 时只需要当前 run 的 history。
- 新 run 通过 Continue-As-New command 的 input 接收压缩后的业务状态。

业务身份角度：

- Workflow ID 相同。
- Run ID 不同。
- 旧 run 的 close event 记录后继 run ID。
- `current_executions` 指向当前 open run。
- 旧 run 保留审计历史，新 run 继续承载业务实例。

### 数据库结构

SQL schema 里有两张核心表：

```sql
executions(
  shard_id,
  namespace_id,
  workflow_id,
  run_id,
  data,
  state,
  ...
  PRIMARY KEY (shard_id, namespace_id, workflow_id, run_id)
)

current_executions(
  shard_id,
  namespace_id,
  workflow_id,
  run_id,
  ...
  PRIMARY KEY (shard_id, namespace_id, workflow_id)
)
```

源码位置：

- MySQL/Postgres schema：`schema/mysql/v8/temporal/schema.sql:30-62`、`schema/postgresql/v12/temporal/schema.sql:30-62`。
- Cassandra 把 execution、current execution、tasks 放在同一张宽表 `executions`，通过 `type` 和 `current_run_id` 区分记录：`schema/cassandra/temporal/schema.cql:7-54`。

Continue-As-New 后的逻辑形态：

```text
executions:
  (workflow_id=order-123, run_id=run-1, status=CONTINUED_AS_NEW, new_execution_run_id=run-2)
  (workflow_id=order-123, run_id=run-2, status=RUNNING)

current_executions:
  (workflow_id=order-123, run_id=run-2)
```

### Close event 指向后继 run

“旧 run 指向新 run”具体体现在旧 run 的最后一个 history event：

```text
WorkflowExecutionContinuedAsNew {
  new_execution_run_id: "run-2",
  input: <new run input>,
  workflow_type: ...,
  task_queue: ...
}
```

构造位置：

- `service/history/historybuilder/event_factory.go:468-510` 创建 `WorkflowExecutionContinuedAsNewEventAttributes`，其中 `NewExecutionRunId` 保存新 run ID，`Input` 保存新 run 的启动输入。
- `service/history/historybuilder/history_builder.go:504-514` 把这个 close event 加到旧 run 的 history builder。

mutable state 同步保存这层关系：

- `service/history/workflow/mutable_state_impl.go:5691-5796` 生成 `newRunID`，创建旧 run 的 `ContinuedAsNew` event，并用 `NewMutableStateInChain` 构造新 run 的 mutable state。
- `service/history/workflow/mutable_state_impl.go:5829-5843` 将旧 run 状态更新为 `WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW`，并设置 `executionInfo.NewExecutionRunId = newRunID`。

### 同事务更新旧 run 与新 run

`RespondWorkflowTaskCompleted` 处理 `ContinueAsNew` command 时，`handleCommandContinueAsNewWorkflow` 会把新 run 的 mutable state 放到 `handler.newMutableState`：

- `service/history/api/respondworkflowtaskcompleted/workflow_task_completed_handler.go:1015-1123`。

随后 `api.go` 持久化时走 `UpdateWorkflowExecutionWithNewAsActive`：

- `service/history/api/respondworkflowtaskcompleted/api.go:613-641`。
- `service/history/workflow/context.go:487-641` 关闭旧 run 的 mutation，同时关闭新 run 的 snapshot，然后调用 `NewTransaction(shardContext).UpdateWorkflowExecution(...)`。

SQL 持久化层的动作：

- 先 append 旧 run 和新 run 的 history nodes：`common/persistence/sql/execution.go:334-348`。
- 在 shard 锁保护的 DB transaction 里更新 mutable state：`common/persistence/sql/execution.go:350-480`。
- `UpdateWorkflowModeUpdateCurrent` 分支把 `current_executions.run_id` 更新成新 run ID：`common/persistence/sql/execution.go:390-432`。
- 同一事务里对旧 run 应用 mutation，对新 run 写 snapshot：`common/persistence/sql/execution.go:443-449`。

所以 Continue-As-New 的持久化语义是：

1. 旧 run 追加 `WorkflowExecutionContinuedAsNew` close event。
2. 旧 run mutable state 标记为 `CONTINUED_AS_NEW`，记录 `new_execution_run_id`。
3. 新 run 创建自己的 `WorkflowExecutionStarted` event 和初始 mutable state。
4. `current_executions` 从旧 run ID 切到新 run ID。
5. 查询当前 Workflow ID 时得到新 run；按旧 run ID 查询时仍能读到旧 history。

## Server 滚动升级兼容

server 自身滚动升级的兼容由这些机制覆盖：

1. Protobuf API 演进：新增字段走默认值语义，旧节点解析时保持向前兼容。
2. Dynamic config / feature gates：新行为逐步开启。
3. RPC fallback：`common/worker_versioning/worker_versioning.go:320-350` 在 Matching 旧版本尚未支持 `CheckTaskQueueVersionMembership` 时，fallback 到 `GetTaskQueueUserData`。
4. HSM/CHASM 迁移：`RespondWorkflowTaskCompleted` 处理未知 command 时先尝试 CHASM，再使用 HSM registry，便于状态机迁移。
5. Task version / stamp 校验：transfer task 执行前校验 stamp、event version，过期 task 会被丢弃或重试：`service/history/transfer_queue_active_task_executor.go:280-318`。

这类兼容面向 server 集群滚动升级；Workflow 业务代码升级仍然依赖 replay determinism、version marker 和 worker versioning。

## 子编排：Child Workflow

Temporal 支持子编排，对应概念是 Child Workflow。一个父 Workflow 可以启动一个或多个子 Workflow，子 Workflow 拥有独立的 event history、mutable state、run chain、timeout、retry、task queue 和 worker versioning 策略。

典型结构：

```text
OrderWorkflow
  ├── PaymentWorkflow
  ├── FulfillmentWorkflow
  └── NotificationWorkflow
```

Go SDK 里父 Workflow 可以这样启动子 Workflow：

```go
childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
    WorkflowID: "payment-" + orderID,
    TaskQueue: "payment-task-queue",
})

var result PaymentResult
err := workflow.ExecuteChildWorkflow(childCtx, PaymentWorkflow, input).Get(ctx, &result)
if err != nil {
    return err
}
```

Child Workflow 和 Activity 的边界不同：

- Activity 是一段外部副作用代码，history 记录 schedule/start/complete/fail 等事件。
- Child Workflow 是另一个完整 Workflow Execution，有自己的 history 和 replay。
- 父 Workflow history 只记录 child 的 initiated、started、completed/failed/canceled/timed out/terminated 等摘要事件。
- 子 Workflow 可以继续 `Continue-As-New`，父 Workflow 仍把它视为一个 child execution chain。
- 子 Workflow 可以绑定不同 task queue，由不同 worker 组或团队维护。

server 侧事件链路：

```text
StartChildWorkflowExecution command
  -> StartChildWorkflowExecutionInitiated event
  -> StartChildExecutionTask transfer task
  -> 子 workflow StartWorkflowExecution
  -> ChildWorkflowExecutionStarted event
  -> ChildWorkflowExecutionCompleted / Failed / Canceled / TimedOut / Terminated event
```

源码入口：

- command 分发：`service/history/api/respondworkflowtaskcompleted/workflow_task_completed_handler.go:323-324`。
- command handler：`service/history/api/respondworkflowtaskcompleted/workflow_task_completed_handler.go` 中的 `handleCommandStartChildWorkflow`。
- mutable state 接口：`service/history/interfaces/mutable_state.go` 中的 `AddStartChildWorkflowExecutionInitiatedEvent`、`AddChildWorkflowExecutionStartedEvent`、`AddChildWorkflowExecutionCompletedEvent` 等。
- task 生成：`service/history/workflow/task_generator.go` 的 `GenerateChildWorkflowTasks`。
- transfer task 执行：`service/history/transfer_queue_active_task_executor.go` 的 `processStartChildExecution`。
- child 完成回写父流程：`service/history/api/recordchildworkflowcompleted/api.go`。

父 Workflow 关闭时，子 Workflow 的生命周期由 `ParentClosePolicy` 控制：

- `TERMINATE`: 父流程关闭时终止子流程。
- `REQUEST_CANCEL`: 父流程关闭时请求取消子流程。
- `ABANDON`: 父流程关闭后子流程继续独立运行。

在版本升级里，Child Workflow 是重要的复杂度隔离工具：

1. 父流程保持稳定，把易变模块拆到子 Workflow。
2. 子 Workflow 独立使用 `GetVersion`、replay tests、worker versioning。
3. 子 Workflow 独立 `Continue-As-New`，降低父流程 history 长度。
4. 不同子域可以使用不同 task queue 和 worker 发布节奏。
5. Saga 补偿可以按子 Workflow 边界拆分，父流程只关心子流程结果和补偿入口。

### Child Workflow 设计指南

把一个模块拆成 Child Workflow 的判断标准：

- 子流程有独立业务生命周期，比如支付、履约、发票、清结算。
- 子流程会等待较长时间，比如人工审批、三方回调、异步对账。
- 子流程有自己的补偿逻辑和错误恢复策略。
- 子流程由独立团队维护，或者需要独立 task queue、worker deployment、发布节奏。
- 子流程 history 可能很长，需要独立 `Continue-As-New`。
- 父流程只需要子流程的最终结果、进度摘要或补偿入口。

接口设计建议：

```go
type PaymentWorkflowInput struct {
    SchemaVersion int
    OrderID       string
    AmountCents   int64
    Currency      string
    IdempotencyKey string
}

type PaymentWorkflowResult struct {
    PaymentID string
    Status    string
}
```

设计原则：

- input 使用稳定领域字段，带 `SchemaVersion`。
- result 返回父流程需要推进下一步的最小信息。
- 子流程内部细节留在子流程 history 中。
- 外部系统调用放在 Activity 中，Activity 使用业务幂等键。
- 子流程公开 query/update/signal 时，把协议当作长期 API 管理。

Workflow ID 策略：

```go
paymentWorkflowID := "payment:" + orderID
```

推荐使用业务幂等 ID 派生 child `WorkflowID`。父 Workflow replay 或重试时，确定性地得到同一个 child ID。对于同一父流程内可重复启动的子流程，把业务轮次放进 ID：

```go
paymentWorkflowID := fmt.Sprintf("payment:%s:attempt:%d", orderID, attempt)
```

父流程调用子流程时，建议显式设置 options：

```go
childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
    WorkflowID:        paymentWorkflowID,
    TaskQueue:         "payment-task-queue",
    WorkflowRunTimeout: time.Hour * 24,
    WorkflowTaskTimeout: time.Second * 10,
    ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
})

var paymentResult PaymentWorkflowResult
err := workflow.ExecuteChildWorkflow(
    childCtx,
    PaymentWorkflow,
    PaymentWorkflowInput{
        SchemaVersion:  2,
        OrderID:        orderID,
        AmountCents:    amountCents,
        Currency:       currency,
        IdempotencyKey: "payment:" + orderID,
    },
).Get(ctx, &paymentResult)
```

`ParentClosePolicy` 选择：

- `REQUEST_CANCEL`: 父流程取消时，子流程进入自己的取消和补偿逻辑。适合订单、支付、履约这类需要收口的子流程。
- `TERMINATE`: 父流程关闭时直接终止子流程。适合可丢弃的后台辅助任务。
- `ABANDON`: 子流程脱离父流程继续运行。适合独立交付、异步通知、长期监管任务。

错误处理建议：

- 子 Workflow 返回业务错误时，父 Workflow 按子域失败处理。
- 子 Workflow 返回可重试技术错误时，让子流程内部 Activity retry 先处理。
- 子 Workflow 进入最终失败状态时，父 Workflow 注册或触发跨子域补偿。
- 子 Workflow 取消时，子流程内部负责清理已完成步骤，父流程记录取消结果。

版本升级建议：

- 父 Workflow 和子 Workflow 各自维护 `GetVersion` marker。
- 子 Workflow 的 payload schema 独立演进。
- 子 Workflow 可以独立 worker versioning，父 Workflow 只依赖子流程接口。
- 子 Workflow 使用 `Continue-As-New` 收口历史和 schema。
- 父 Workflow 调用子 Workflow 的接口变化时，在父流程侧加 `GetVersion`。

接口和数据库 schema 演进建议：

- Workflow input/result 使用稳定业务 ID、金额、枚举、版本号这类领域字段。
- Activity 层负责适配当前数据库 schema，并用业务 ID 做幂等读写。
- 数据库 schema 迁移采用 expand -> backfill -> switch -> contract：先加新列和兼容读写，再回填，再切新逻辑，最后清理旧列。
- Workflow 侧用 `GetVersion` 固定调用协议，例如旧 history 继续使用 `PaymentWorkflowInput{SchemaVersion: 1}`，新 run 使用 `SchemaVersion: 2`。
- 子 Workflow 升级时，父流程稳定依赖 `PaymentWorkflowInput`、`PaymentWorkflowResult`、`CompensatePaymentInput` 这几个公开协议。
- 补偿 input 包含执行补偿所需的稳定 ID；大对象通过 Activity 按业务 ID 从业务库加载。

测试清单：

- 父流程 replay 测试：验证 child initiated/started/completed 事件序列稳定。
- 子流程 replay 测试：验证子流程内部升级兼容旧 history。
- 取消测试：覆盖父取消、子取消、父关闭策略。
- 幂等测试：父流程重试启动 child 时使用相同 child `WorkflowID`。
- 失败补偿测试：子流程失败后父流程触发正确补偿。
- Continue-As-New 测试：子流程换 run 后父流程仍能拿到最终结果。

### Child Workflow 与 Saga 的关系

Child Workflow 解决模块边界和生命周期拆分；Saga 解决跨步骤一致性和补偿。两者可以组合成分层 Saga。

常见组合：

```text
OrderSagaWorkflow
  ├── PaymentSagaWorkflow
  │     ├── AuthorizePayment Activity
  │     └── VoidAuthorization Activity
  ├── FulfillmentSagaWorkflow
  │     ├── ReserveInventory Activity
  │     └── ReleaseInventory Activity
  └── NotificationWorkflow
```

父流程承担全局 Saga：

- 决定业务总顺序。
- 启动各子域 Child Workflow。
- 收集子流程结果。
- 保存跨子域补偿顺序。
- 在全局失败时触发子域补偿入口。

子流程承担局部 Saga：

- 管理子域内部步骤。
- 维护子域补偿栈。
- 封装子域 Activity retry、timeout、幂等。
- 对父流程暴露稳定结果、失败类型、补偿入口。

关键点是两个失败时间点不同：

```text
时间点 1：子 Workflow 正在执行中失败
  -> 子 Workflow 自己在 if err != nil 中执行局部补偿
  -> 子 Workflow 返回失败给父 Workflow

时间点 2：子 Workflow 已经成功返回给父 Workflow
  -> 后续其他子域失败
  -> 父 Workflow 调用这个子域暴露的补偿入口
```

所以子 Workflow 仍然可以、也通常应该自己使用 Saga。父 Workflow 存的是子 Workflow 成功之后的模块级补偿入口。

两层补偿栈：

```text
父级补偿栈：
  compensateFulfillment(reservationID)
  compensatePayment(paymentID)

Payment 子级补偿栈：
  refundCapture(paymentID)
  voidAuthorization(authID)

Fulfillment 子级补偿栈：
  cancelShipment(shipmentID)
  releaseInventory(reservationID)
```

父 Workflow 只知道“Payment 成功了”“Fulfillment 成功了”，并决定跨子域失败时按什么顺序回滚。子 Workflow 知道自己内部做过哪些步骤，以及这些步骤怎么补偿。

父 Workflow 调用子 Workflow 的 Saga，落到两个公开入口：

```text
正向入口：
  PaymentSagaWorkflow(PaymentWorkflowInput) -> PaymentWorkflowResult

补偿入口：
  CompensatePaymentSagaWorkflow(CompensatePaymentInput) -> error
```

父流程保存的是补偿入口的可重放描述，子流程内部补偿栈由子流程 history 管理。这样 Payment 子流程既能被 Order 父流程调用，也能被其他父流程或独立启动方复用。

生产代码里建议把父级补偿项保存成数据结构，再用统一执行器调用补偿 Child Workflow：

```go
type CompensationKind string

const (
    CompensationPayment     CompensationKind = "payment"
    CompensationFulfillment CompensationKind = "fulfillment"
)

type GlobalCompensation struct {
    Kind       CompensationKind
    WorkflowID string
    TaskQueue   string
    Input       any
}

func executeCompensation(ctx workflow.Context, c GlobalCompensation) error {
    switch c.Kind {
    case CompensationPayment:
        input := c.Input.(CompensatePaymentInput)
        childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
            WorkflowID: c.WorkflowID,
            TaskQueue:  c.TaskQueue,
        })
        return workflow.ExecuteChildWorkflow(ctx, childCtx, CompensatePaymentSagaWorkflow, input).Get(ctx, nil)
    case CompensationFulfillment:
        input := c.Input.(CompensateFulfillmentInput)
        childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
            WorkflowID: c.WorkflowID,
            TaskQueue:  c.TaskQueue,
        })
        return workflow.ExecuteChildWorkflow(ctx, childCtx, CompensateFulfillmentSagaWorkflow, input).Get(ctx, nil)
    default:
        return temporal.NewNonRetryableApplicationError("unknown compensation kind", "UnknownCompensationKind", nil)
    }
}
```

父流程的 helper 负责生成确定性的 child workflow ID，并把子流程结果转换成父级补偿项：

```go
func runPaymentSaga(ctx workflow.Context, input OrderInput) (PaymentWorkflowResult, error) {
    childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
        WorkflowID:        "payment:" + input.OrderID,
        TaskQueue:         "payment-task-queue",
        ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
    })

    var result PaymentWorkflowResult
    err := workflow.ExecuteChildWorkflow(
        childCtx,
        PaymentSagaWorkflow,
        PaymentWorkflowInput{
            SchemaVersion:  2,
            OrderID:        input.OrderID,
            AmountCents:    input.AmountCents,
            Currency:       input.Currency,
            IdempotencyKey: "payment:" + input.OrderID,
        },
    ).Get(ctx, &result)
    return result, err
}

func paymentCompensationEntry(input OrderInput, payment PaymentWorkflowResult) GlobalCompensation {
    return GlobalCompensation{
        Kind:       CompensationPayment,
        WorkflowID: "compensate-payment:" + input.OrderID + ":" + payment.PaymentID,
        TaskQueue:   "payment-task-queue",
        Input: CompensatePaymentInput{
            SchemaVersion: 2,
            OrderID:       input.OrderID,
            PaymentID:     payment.PaymentID,
        },
    }
}
```

父流程执行顺序：

```go
func OrderSagaWorkflow(ctx workflow.Context, input OrderInput) error {
    var compensations []GlobalCompensation

    payment, err := runPaymentSaga(ctx, input)
    if err != nil {
        return err
    }
    compensations = append(compensations, paymentCompensationEntry(input, payment))

    fulfillment, err := runFulfillmentSaga(ctx, input)
    if err != nil {
        for i := len(compensations) - 1; i >= 0; i-- {
            _ = executeCompensation(ctx, compensations[i])
        }
        return err
    }
    compensations = append(compensations, fulfillmentCompensationEntry(input, fulfillment))

    return nil
}
```

这里的状态来源很明确：

- 子流程内部补偿状态来自子流程自己的 history replay。
- 父流程全局补偿状态来自父流程 history 中的 `ChildWorkflowExecutionCompleted` result payload。
- 补偿 Workflow 的执行参数来自父流程保存的 `GlobalCompensation`。
- 补偿 Activity 通过业务 ID 读取当前业务库，并用幂等键保证重复执行安全。

父流程示例：

```go
func OrderSagaWorkflow(ctx workflow.Context, input OrderInput) error {
    compensations := []func(workflow.Context) error{}

    paymentID, err := runPaymentSaga(ctx, input)
    if err != nil {
        return err
    }
    compensations = append(compensations, func(ctx workflow.Context) error {
        return runPaymentCompensation(ctx, input.OrderID, paymentID)
    })

    fulfillmentID, err := runFulfillmentSaga(ctx, input)
    if err != nil {
        for i := len(compensations) - 1; i >= 0; i-- {
            _ = compensations[i](ctx)
        }
        return err
    }
    compensations = append(compensations, func(ctx workflow.Context) error {
        return runFulfillmentCompensation(ctx, input.OrderID, fulfillmentID)
    })

    return nil
}
```

子流程示例：

```go
func PaymentSagaWorkflow(ctx workflow.Context, input PaymentWorkflowInput) (PaymentWorkflowResult, error) {
    ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
        StartToCloseTimeout: time.Minute,
        RetryPolicy: &temporal.RetryPolicy{
            MaximumAttempts: 5,
        },
    })

    var authorization AuthorizationResult
    err := workflow.ExecuteActivity(ctx, AuthorizePayment, input).Get(ctx, &authorization)
    if err != nil {
        return PaymentWorkflowResult{}, err
    }

    var capture CaptureResult
    err = workflow.ExecuteActivity(ctx, CapturePayment, authorization.PaymentID).Get(ctx, &capture)
    if err != nil {
        _ = workflow.ExecuteActivity(ctx, VoidAuthorization, authorization.PaymentID).Get(ctx, nil)
        return PaymentWorkflowResult{}, err
    }

    return PaymentWorkflowResult{
        PaymentID: capture.PaymentID,
        Status:    "AUTHORIZED",
    }, nil
}
```

支付子流程成功返回后，父流程保存的是公开补偿入口：

```go
payment, err := runPaymentSaga(ctx, input)
if err != nil {
    return err
}

globalCompensations = append(globalCompensations, func(ctx workflow.Context) error {
    return workflow.ExecuteChildWorkflow(
        workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
            WorkflowID: "compensate-payment:" + input.OrderID,
            TaskQueue:  "payment-task-queue",
        }),
        CompensatePaymentSagaWorkflow,
        CompensatePaymentInput{
            OrderID:   input.OrderID,
            PaymentID: payment.PaymentID,
        },
    ).Get(ctx, nil)
})
```

补偿入口通常也是一个 Child Workflow：

```go
func CompensatePaymentSagaWorkflow(ctx workflow.Context, input CompensatePaymentInput) error {
    return workflow.ExecuteActivity(ctx, RefundPayment, input.PaymentID).Get(ctx, nil)
}
```

联动方式有三种：

1. 父流程直接等待子 Saga 完成。适合强顺序订单流程。
2. 父流程并发启动多个子 Saga，用 selector 收集结果。适合支付、库存、风控并行。
3. 父流程启动子 Saga 后使用 signal/update 协调。适合人工审批、三方回调、长时间异步流程。

补偿设计有两种常用形态：

- 子流程自补偿：子流程内部失败时清理自己的已完成步骤，父流程只接收最终失败。
- 父流程全局补偿：父流程记录已成功子流程，跨子域失败后按反向顺序调用补偿 child workflow 或补偿 signal。

推荐边界：

- 子域内部一致性放在子 Workflow 中。
- 跨子域一致性放在父 Saga 中。
- 外部副作用放在 Activity 中。
- 长周期和复杂升级放在 Child Workflow 中。
- 补偿入口设计成幂等 Activity 或幂等 Child Workflow。
- 父 Workflow 保存子域级补偿入口，避免直接调用子域内部 Activity。
- 子 Workflow 内部失败时先自补偿，再把失败返回给父 Workflow。

## Temporal 与 Saga

Temporal 的 Saga 是 durable workflow orchestration：

- 正向步骤通常是 Activity。
- 每个成功步骤对应一个补偿 Activity。
- Workflow 把已完成步骤和补偿闭包保存在 deterministic 状态中。
- 失败、取消、超时发生时，Workflow 根据 history replay 出同一组补偿动作，并按业务顺序执行。
- Activity 通过 retry policy、timeout、heartbeat、idempotency key 承担外部系统调用的可靠性。

一个典型 Go SDK 结构：

```go
func OrderSaga(ctx workflow.Context, input OrderInput) error {
    ao := workflow.ActivityOptions{
        StartToCloseTimeout: time.Minute,
        RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 5},
    }
    ctx = workflow.WithActivityOptions(ctx, ao)

    compensations := []func(workflow.Context) error{}

    err := workflow.ExecuteActivity(ctx, ReserveInventory, input.OrderID).Get(ctx, nil)
    if err != nil {
        return err
    }
    compensations = append(compensations, func(ctx workflow.Context) error {
        return workflow.ExecuteActivity(ctx, ReleaseInventory, input.OrderID).Get(ctx, nil)
    })

    err = workflow.ExecuteActivity(ctx, ChargePayment, input.OrderID).Get(ctx, nil)
    if err != nil {
        for i := len(compensations) - 1; i >= 0; i-- {
            _ = compensations[i](ctx)
        }
        return err
    }

    return workflow.ExecuteActivity(ctx, ConfirmOrder, input.OrderID).Get(ctx, nil)
}
```

Temporal 相比经典 Saga 协调器，多了这些运行时能力：

- Workflow history 持久保存每一步状态和补偿决策。
- Worker 崩溃后，SDK replay history 恢复补偿列表。
- Activity 重试、timeout、heartbeat 和 cancellation 由 server/SDK 协同维护。
- transfer/timer queues 相当于持久 outbox，任务投递具备 eventually dispatch 语义。
- 查询、signal、update 能让外部系统安全观察或推进 Saga。

## 实践建议

1. Workflow 代码变更涉及 command 顺序、activity/timer/child workflow 数量或 ID 时，使用 `GetVersion`/patch。
2. 旧分支保留到所有旧 run 都越过变更点或完成；之后按 SDK 推荐流程清理 patch。
3. Activity input/output payload 加显式 schema version，decoder 兼容旧字段。
4. Activity 使用业务幂等键，比如 order ID、payment intent ID、reservation ID。
5. 大型 Saga 使用 child workflows 拆分边界，用 parent close policy 表达生命周期。
6. 长历史 workflow 定期 continue-as-new，把新 schema 带入新 run。
7. 重要 Workflow 维护 replay fixture，像 `service/worker/scheduler/replay_test.go` 一样用历史样本跑 `ReplayWorkflowHistory`。
8. 部署层使用 worker versioning：旧 run 固定在兼容 build，新 run 进入新 build，灰度时用 auto-upgrade/ramping 策略。
9. MQ 回调型 Activity 使用 Async Activity Completion，并把 task token 或稳定 Activity ID 和业务幂等键一起持久化。

## 阅读索引

- 架构总览：`/Users/ubuntu/projects/go/temporal/docs/architecture/README.md`
- History Service：`/Users/ubuntu/projects/go/temporal/docs/architecture/history-service.md`
- 生命周期时序：`/Users/ubuntu/projects/go/temporal/docs/architecture/workflow-lifecycle.md`
- Workflow Task completion：`/Users/ubuntu/projects/go/temporal/service/history/api/respondworkflowtaskcompleted/api.go`
- Activity async completion：`/Users/ubuntu/projects/go/temporal/service/history/api/respondactivitytaskcompleted/api.go`
- Activity async failure / retry：`/Users/ubuntu/projects/go/temporal/service/history/api/respondactivitytaskfailed/api.go`
- Retry 架构：`/Users/ubuntu/projects/go/temporal/docs/architecture/retry.md`
- Speculative / transient Workflow Task：`/Users/ubuntu/projects/go/temporal/docs/architecture/speculative-workflow-task.md`
- Activity retry timer executor：`/Users/ubuntu/projects/go/temporal/service/history/timer_queue_active_task_executor.go`
- Workflow retry helper：`/Users/ubuntu/projects/go/temporal/service/history/workflow/retry.go`
- Command handler：`/Users/ubuntu/projects/go/temporal/service/history/api/respondworkflowtaskcompleted/workflow_task_completed_handler.go`
- Marker event factory：`/Users/ubuntu/projects/go/temporal/service/history/historybuilder/event_factory.go`
- Mutable state：`/Users/ubuntu/projects/go/temporal/service/history/workflow/mutable_state_impl.go`
- Continue-As-New event factory：`/Users/ubuntu/projects/go/temporal/service/history/historybuilder/event_factory.go`
- Workflow context 持久化：`/Users/ubuntu/projects/go/temporal/service/history/workflow/context.go`
- SQL execution schema：`/Users/ubuntu/projects/go/temporal/schema/mysql/v8/temporal/schema.sql`
- Task generation：`/Users/ubuntu/projects/go/temporal/service/history/workflow/task_generator.go`
- Transfer task executor：`/Users/ubuntu/projects/go/temporal/service/history/transfer_queue_active_task_executor.go`
- Child workflow completion API：`/Users/ubuntu/projects/go/temporal/service/history/api/recordchildworkflowcompleted/api.go`
- Worker versioning：`/Users/ubuntu/projects/go/temporal/common/worker_versioning/worker_versioning.go`
- Replay test 示例：`/Users/ubuntu/projects/go/temporal/service/worker/scheduler/replay_test.go`
