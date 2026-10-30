# 可靠任务调度

后续已完成一轮[反例驱动验收](adversarial-acceptance.md)：包括真实子进程 SIGKILL、多 Worker 竞争、etcd/KanaRPC，以及七类已复现问题的必要修复。下方上一轮的测量与验证范围保留作历史记录，以后续验收报告为补充。

本轮深化的是两条实际执行路径：招聘抓取／结构化分析走持久化 Outbox 与 Redis；用户授权的岗位深度匹配走 API 内 executor。两者保留各自的恢复语义，没有把付费模型密钥写进任务队列，也没有新增服务或消息中间件。

## 后台：类型隔离与分布式轮转

`TASK_QUEUE_MODE=typed` 为默认值。`ANALYZE`、`ASSESS`、`WATCH_CHECK`、`WATCH_FETCH`、`SOURCE_IMPORT` 各有 user 与 `SYSTEM_` 通道，共十条独立 Stream、消费组及执行池。默认每 worker 进程 user ANALYZE、WATCH_FETCH 各 2 个执行槽，其余各 1 个，总计 12；每通道可配置 1–16，每进程总计不可超过 64。多个 worker 的执行并发相加，Redis resident 限额与轮转按同一 namespace 共享。池不会跨类型借用槽位，慢抓取或分析不能占用 ASSESS 的槽。

归属从 SQL 业务记录取得：用户私有岗位的分析／评估属于岗位 owner；关注任务属于 watch user；来源导入属于 batch user。共享目录岗位与无用户归属任务进入独立系统通道。来源限速、分析 RPC semaphore、模型限速和 SQL 连接池仍是下游共享资源，因此池隔离不代表网络、CPU、数据库或提供商的物理隔离。

SQL `outbox_tenants` 索引任务归属，`queue_dispatch_clock` 的行锁把多个 dispatcher 的转次串行化。每次最多访问 32 个 owner，按 `(owner, type)` 每轮取一个最早任务；一种满队列不会堵住该 owner 其他任务类型。Redis Lua 再按 owner 轮转，每轮把一个任务从 owner ready list 送到该类型 Stream。多个 worker 实例共享该轮转顺序；公平单位是一个任务，不是 token、CPU 时间或成本。已经执行的任务不被抢占，多个空闲槽可能处理同一 owner。不能承诺各用户相同的完成时间。公平性针对已接纳、可路由的任务；没有给新账号预留入队空位或提供跨实例的用户执行配额。

队列中 outstanding ID 去重；ACK、失败转 retry/DLQ、poison 归档都先保留目标再释放容量。各 Stream 的失败标记分别命名，避免不同 Stream 的相同 message ID 互相覆盖。有效但类型不符的消息不会被该池 ACK，需运维检查错误路由。SQL 回执、generation 和输入 CAS 仍决定重复处理是否可写回；Redis 去重仅覆盖 outstanding 阶段。

## 排队与容量

| 边界 | 默认值与行为 |
| --- | --- |
| 每类型／归属通道 resident | 256，含 ready 与 inflight；`QUEUE_CAPACITY` 1–4096 |
| 每 owner 在该通道 resident | 32；`QUEUE_OWNER_CAPACITY` 不得超过通道容量 |
| 每任务类型延迟重试 ZSET | 同 `QUEUE_CAPACITY`，用户与系统共用原 retry key；满时源仍在 PEL，占用原 resident 槽 |
| DLQ | 新 typed 转入最多 10000，满时保留 PEL；既有超量数据保留 |
| SQL unsent Outbox | 全局 100000、每 owner 20000；同事务串行准入，超出回滚并返回 `QUEUE_BUSY` / 503、Retry-After: 2 |
| 新后台任务排队期限 | Outbox 创建起 10 分钟，包含 durable 等待及重试；执行前过期进入终态，不调用下游 |
| 后台执行期限 | `TASK_TIMEOUT_SECONDS`，默认 15 秒；claim idle 必须更长，默认 45 秒 |
| API 深度匹配准入 | 每进程最多 16 个任务；满时 `MATCH_QUEUE_FULL` / 503、Retry-After: 2 |
| API 批次排队期限 | `MATCH_QUEUE_WAIT_SECONDS`，默认 30 秒 |
| API 批次执行期限 | `MATCH_EXECUTION_SECONDS`，默认 175 秒 |

Redis 队列满时 Outbox 保持 unsent；retry 满时不 ACK 源；DLQ 满时也不丢弃源。调度器按有界窗口轮转扫描到期 retry，避免满 owner 的最早重试一直挡住后面的 owner。旧数据超出新限制时保留原数据，暂停新增转入，逐步排空。这里的容量不包括 SQL 历史、旧 Stream、poison、分析缓存或 Redis 分配器开销，不能据此声称整个实例内存有硬上限。对故障历史仍需保留／归档策略。

SQL 准入当前用单独锁与计数查询保证一致性；这是适合本项目的简单方案，大批量导入会增加扫描与写入争用。100000 是拒绝上限，尚未证明在该积压下的延迟。空闲 typed worker 的轮询可能增加约一秒唤醒延迟，当前未引入额外唤醒协议。

## 岗位深度匹配：每批次公平许可

一个普通批次仍最多 3 个岗位／24 KB JD，显式单项重试为 1 个；同公司整体比较保持一个完整量子，最多 16 个候选。每量子归还许可后重新排队，不再由一轮最多 100 岗位的任务长期占槽。每账号最多持有一个本地许可；队列取消会立即释放等待对象，重复 release 无副作用。

等待超时不调用模型，未处理项目保持 QUEUED，任务进入 `WAITING_AUTH`，execution_state 为 `MATCH_QUEUE_TIMEOUT`。暂停仍在排队的任务会立即保存 PAUSED 并取消等待；拿到许可后还会复查状态，不能多发一批。执行超时／429／连接错误保留失败状态，不自动重试已授权的深度分析。页面显示等待截止、执行截止及人工恢复建议。`GET /api/matching/tasks/scheduler` 是登录后的本实例限额快照；单任务响应包含 `execution_state`、`queue_until`、`execution_until`。

公平许可只在单 API 实例内生效。SQL 账号行锁、执行 token／60 秒 lease 与 15 秒续租继续跨实例防止同账号多轮有效执行；既有 Redis 账号调用租约覆盖旧同步接口。但多个 API 实例的总执行并发会相加，跨实例没有统一公平排队，旧同步 `/api/matching/analyze` 不进入新公平许可。需这一级保证时应统一部署入口或再把授权执行调度持久化；本轮不宣称已经实现。

## 调用结果未知与恢复

后台分析在外部调用前写 SQL `analysis_invocations`，按 observation／处理版本唯一。若调用发出后超时、断开或进程崩溃，新的 worker 发现凭据但无已保存结果，会返回 `ANALYSIS_OUTCOME_UNKNOWN`，不再次调用。明确 HTTP 429／401／403 拒绝才释放凭据；RPC response 携带经过分类的拒绝状态，不依靠错误文字猜测可否重放。429 保持退避，已保存分析／缓存优先复用。

这是一道保守防重放边界，不是提供商 exactly-once。记录成功但尚未发出请求就崩溃也会要求核对；外部成功但本地结果丢失可能无法自动恢复。后台未知调用的普通 DLQ redrive 仍会被凭据挡住，不应靠改任务 ID 或清表重试；需先核对提供商执行结果并人工恢复可信结果。本轮没有新增绕过该凭据的付费重试入口。付费岗位深度匹配继续使用既有核对后恢复流程，明确重试仍可能产生费用。

## 升级及回退

1. 保留数据库／Redis 的可恢复备份，停止旧 API、worker 和其他任务生产者。旧 worker 的严格 envelope decoder 不保证识别新增字段，本轮不支持旧／新 worker 混跑的滚动升级。
2. 在同一数据库和 Redis prefix 部署新二进制，运行迁移（API 与 worker 启动均调用 `Store.Migrate`）。迁移 015 仅加元数据与调用凭据表；MatchRun 新状态字段放在原 JSON 中，旧记录无需改写。
3. 设置所有服务 `TASK_QUEUE_MODE=typed`，使用原 Redis prefix 启动。不要新建 namespace 来假装旧任务已经迁移。
4. dispatcher 每次补齐最多 100 条旧 unsent Outbox 的 owner 索引；旧 envelope 和排队期限不被臆造。后台桥接旧 `tasks/workers` 的未读消息及 XAUTOCLAIM pending，把新目标和旧 ACK 放在同一 Lua；容量满时旧消息仍 pending，桥接内存缓冲最多 32 条。原 retry key 原子转入 typed ready，原 attempt、generation、期限保留；损坏的旧 retry envelope 先保留安全摘要和原文哈希到 poison:retry 再移除，不阻塞有效任务；旧 DLQ 可经既有工具显式 redrive。
5. 查看 Worker 内网 `/queues` 与 `/metrics/prometheus`，以及旧 Stream PEL、unread 和原 retry key 的减少情况。typed ACK 后立即 XDEL 传输消息；SQL 回执与事件不删。旧 Stream 继续按既有安全 history trim 清理。

回退到 legacy 前必须停止全部 typed 生产者并排空 typed ready、Stream 和 PEL。legacy worker 初始化会检查 typed resident／Stream，有残留即拒绝启动；不得通过清空键绕过。新版本仍保留 legacy 模式供排空后使用；此操作不等于旧二进制可无条件回退。新模式的重试与 DLQ 使用原 key，因此无需复制或丢弃故障任务。

## 复现与实测

```sh
# 不读取 .env；使用临时 MySQL 8.4、Redis 7.4，合成账号和 mock 模型；退出清理自己的容器
GOCACHE=/tmp/campustrace-test-cache sh scripts/journey-local.sh scheduler-test
# 原产品完整 HTTP 闭环（需空闲本地端口）
JOURNEY_HTTP_PORT=23381 GOCACHE=/tmp/campustrace-test-cache sh scripts/journey-local.sh smoke
# 无网络、无密钥、无模型账单；只比较实际 fair Pool 与原 run-wide FIFO 许可
go run -race ./cmd/scheduler-bench -batch-users 8 -small-users 8 -quanta 20 -concurrency 2 -delay-ms 20 -repeats 3
```

2026-10-30，本机 darwin/arm64，Go 1.27.1，race 开启。8 个批量账号各 20 个量子，8 个小请求各一个量子；小请求在批量提交 10ms 后到达，量子固定 20ms。每设计重复 3 次，下表是各次统计的中位数，非把所有样本混合后的分位数。[原始测量 JSON](../examples/scheduling/benchmark-20261030.json) 随仓库保留。计时 Pool 的等待上限为 64；本场景只有 16 个总任务，不触发产品的 16 个总准入限制，过载行为由单独测试验证。

| 测量项 | 原整轮占槽 FIFO | 每量子公平许可 |
| --- | ---: | ---: |
| 小请求排队 P95 | 1654.6 ms | 130.4 ms |
| 小请求完成 P95 | 1674.7 ms | 150.6 ms |
| 全部量子排队 P99 | 1654.6 ms | 140.9 ms |
| 吞吐 | 99.70 量子/s | 99.55 量子/s |
| 测到的最大执行／等待 | 2 / 14 | 2 / 14 |
| 测到的 goroutine 峰值 | 18 | 18 |
| 采样 Go heap 最大值（三次取最大） | 260936 B | 310232 B |

公平性还由真实 MatchRun 测试验证：大账号 7 个岗位被分成 3、3、1，小账号在第一批模型受阻期间加入；模型实际执行顺序为大 3、小 1、大 3、大 1。慢后台 ANALYZE 未结束时，独立 ASSESS 已写完成回执。

计时 benchmark 不含 HTTP、SQL、Redis、真实模型、网络及多实例开销；heap 是采样值，包含运行时且会随版本变化；16 个账号／168 个量子的分位数只证明这一小规模机制。不能据此给出生产 QPS、费用或推荐质量结论。后台 Redis/SQL 的迁移、一致性与容量测试另以真实容器执行，没有做同规模端到端性能压测。

本轮验收日志在本机 `bin/verification/scheduler-20261030/`，原工作区恢复基线在 `bin/backups/scheduler-before-20261030/`（HEAD、status、tracked.patch、untracked.tar.gz）。备份包含此前已授权产品改造与用户未提交内容，不包含 .env；未 reset、提交或推送。


最终验收：`go test -race ./...`、`go vet ./...`、`go build ./...`、前端 432 项和完整产品 HTTP 烟测通过；隔离 SQL/Redis 定向 race 共 71 个顶层测试通过，无失败或跳过。生产规模、跨 API 实例统一公平、真实 kill -9／failover 以及全量 etcd/RPC 回归未验证；早期误选的 etcd 注册测试因临时栈没有 etcd 而失败，最终脚本没有将其当作通过。具体 passed / failed / not run 记录在本机 `bin/verification/scheduler-20261030/acceptance.md`。
