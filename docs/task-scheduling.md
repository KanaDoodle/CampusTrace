# 可靠任务调度

岗位抓取、来源导入、结构化分析和状态评估通过 MySQL 事务 Outbox 与 Kafka 执行。用户授权的岗位深度匹配继续使用 API 内执行器，共享模型许可、调用账本和版本校验；模型密钥不进入消息队列。

## 后台：Kafka 投递与持久任务状态

`TASK_QUEUE_MODE=kafka` 是唯一运行模式。五类任务 `ANALYZE`、`ASSESS`、`WATCH_CHECK`、`WATCH_FETCH`、`SOURCE_IMPORT` 分为用户与 `SYSTEM_` 通道，共十个 Kafka 主题和独立消费组。任务主题前缀默认为 `campustrace.tasks`，例如 `campustrace.tasks.analyze.v1`；岗位事件继续使用 `campustrace.jobs.v1`，两条链路互不提交对方的消费位点。

每个 Worker 进程默认给用户分析、岗位抓取各两个执行槽，其余通道各一个，共十二个；每通道可配置 1–16 个，总计不超过 64。默认每主题三个分区，同一消费组内的有效消费者数量受分区数限制。多个实例共享 SQL 容量和调度游标。执行槽统一使用 `WORKER_<LANE>_CONCURRENCY`；旧的 `WORKER_CONCURRENCY` 不参与通道配置，启动时提示改用通道参数。

1. 业务事务写入 `outbox`，`FairOutbox` 按用户与任务类型轮转转交。
2. `kafka_tasks` 持久保存任务内容、投递修订号、状态、到期时间及执行租约。SQL 交接成功后标记源 Outbox 已发送；Kafka 暂停期间任务仍可持久接纳。
3. 发布器按通道、用户轮转，将到期任务发到 Kafka；Broker 确认后更新为 `SENT`。网络调用在 SQL 事务之外执行，发送中断由有限发布租约恢复。
4. 消费者核对任务身份、内容哈希、投递版本和数据库恢复代号，领取 SQL 执行租约，执行原业务处理。
5. 业务结果提交后，持久保存 `DONE`、`RETRY` 或 `DEAD`，再提交 Kafka 消费位点。处理失败、状态提交失败或进程退出时保留未提交位点。

重复消息通过任务修订号与租约隔离，业务写入另由 SQL 回执、generation 和输入版本校验保护。模型调用状态未知时进入人工核对，避免自动重复付费调用。租约到期后可以恢复任务；已发送但长期未消费的任务会重新发布，使恢复不依赖 Kafka 无限保留历史。

公平轮转作用于任务接纳与发布；Kafka 分区及已开始执行的任务会影响实际完成顺序。慢任务不会占用其他类型的执行槽。

## 重试、死信与容量

临时错误按指数退避加随机抖动重试，遵守来源的 `Retry-After`，最长等待一小时。来源本地限速将原任务延后一分钟，不消耗网络重试次数。延迟时间保存在 MySQL，到期后重新投递 Kafka，不占用等待中的执行槽。

永久错误、超过重试次数或超过排队期限的任务进入 SQL 死信。Kafka 死信主题只包含任务编号、版本、错误分类及内容摘要；畸形消息只保留安全诊断和哈希，不复制原始内容。运维工具从 SQL 读取任务并显式重放，重放递增投递修订号，旧消息不能激活新一轮任务。

| 边界 | 默认值与行为 |
| --- | --- |
| 每通道未完成任务 | 256；`QUEUE_CAPACITY` 范围 1–4096，含排队、投递、执行及延迟重试 |
| 每用户在该通道未完成任务 | 32；`QUEUE_OWNER_CAPACITY` 不超过通道容量 |
| SQL 未发送业务 Outbox | 全局 100000、每用户 20000，超出同事务拒绝并返回 `QUEUE_BUSY` |
| 新后台任务排队期限 | Outbox 创建起十分钟，包含等待和重试；过期后不调用下游 |
| 后台执行期限与租约 | `TASK_TIMEOUT_SECONDS=15`、`TASK_LEASE_SECONDS=45`；租约必须长于执行期限 |
| 已完成任务传输记录 | `TASK_HISTORY_HOURS=24`；每次最多清理 1000 条 SQL `DONE` 记录 |
| Kafka 传输历史 | 默认七天；清理独立于 SQL 任务与业务回执 |

容量已满时源 Outbox 保持未发送；延迟重试继续占用原任务容量。死信、异常诊断和业务回执保留供排障，需按运维保留策略管理，不计入活跃任务配额。`/queues` 与 Prometheus 展示固定通道的未完成、执行中及排队数量，用户标识不作为指标标签。

## 岗位深度匹配：每批次公平许可

一个普通批次仍最多 3 个岗位／24 KB JD，显式单项重试为 1 个；同公司整体比较保持一个完整量子，最多 16 个候选。每量子归还许可后重新排队，不再由一轮最多 100 岗位的任务长期占槽。每账号最多持有一个本地许可；队列取消会立即释放等待对象，重复 release 无副作用。

等待超时不调用模型，未处理项目保持 QUEUED，任务进入 `WAITING_AUTH`，execution_state 为 `MATCH_QUEUE_TIMEOUT`。暂停仍在排队的任务会立即保存 PAUSED 并取消等待；拿到许可后还会复查状态，不能多发一批。执行超时／429／连接错误保留失败状态，不自动重试已授权的深度分析。页面显示等待截止、执行截止及人工恢复建议。`GET /api/matching/tasks/scheduler` 是登录后的本实例限额快照；单任务响应包含 `execution_state`、`queue_until`、`execution_until`。

任务执行许可在单 API 内按账号轮转，SQL 账号行锁、执行 token／60 秒 lease 与 15 秒续租协调跨实例的同账号执行。多个 API 的任务执行槽分别计数；实际模型请求由[跨 API 模型执行控制](model-control.md)共享全局许可、公平队列与调用预算。旧同步 `/api/matching/analyze` 保留账号调用租约，并在实际调用模型时进入同一全局控制。


## 启动与从 Redis 升级

默认 Compose 包含 Kafka 与主题初始化任务。主机开发使用 `KAFKA_BROKERS=127.0.0.1:19092`，容器内使用 `kafka:9092`。主题前缀通过 `KAFKA_TASK_PREFIX` 配置；同一部署的 API、Worker 与运维工具必须一致。`KAFKA_TASK_NAMESPACE` 默认沿用 Redis prefix，只作为任务隔离标识；恢复数据库时保留原任务 namespace，同时可更换缓存与会话的 Redis prefix。

新环境初始化：

```sh
GOWORK=off go run ./cmd/tasks
```

已有 Redis 任务的升级步骤：

1. 备份 MySQL 与 Redis，停止旧 API、Worker 和其他任务生产者。升级阶段不混跑两套队列的生产者。
2. 保留原数据库、Redis prefix 与任务 namespace，将 `TASK_QUEUE_MODE` 设置为 `kafka`，应用迁移后执行：

```sh
GOWORK=off go run ./cmd/tasks -migrate-redis -writers-stopped
GOWORK=off go run ./cmd/tasks
```

容器内可用 `docker compose -f docker-compose.yml -f compose.app.yml run --rm --no-deps worker /app/tasks -migrate-redis -writers-stopped`。先确保依赖已启动且使用本次构建的镜像。

3. 命令转交旧死信、延迟任务、按类型排队任务，以及旧 Stream 的 pending 和未读消息；保留任务编号、尝试次数、generation 和延迟时间。已确认的 Stream 历史不重新执行，畸形消息保存安全摘要。重复运行使用 SQL 交接标记返回，不重新激活已完成任务。
4. 启动新 Worker，观察 `/queues` 和业务结果。旧 Redis 键保留用于备份核查，运行链路不再读取。检测到未迁移的旧任务键时，新 Worker 拒绝启动并给出迁移命令。

迁移后的回退需要针对 MySQL 任务状态和 Kafka 位点制定计划，不能直接启动旧 Redis Worker。数据库恢复会旋转任务代号并将未完成任务重新置为待发送，保留 SQL 业务回执和付费调用凭据；对同一恢复副本重复准备时保留已经建立的任务代号与执行租约。

## 验证

本轮结果见 [Kafka 后台任务验收](verification/kafka-tasks-20261101/README.md)。

`internal/taskqueue/kafka_test.go` 覆盖实际 MySQL/Kafka 下的并发容量、用户轮转、修订号、租约、延迟重试、死信重放、畸形消息、发送失败、消费失败重投和 SIGKILL 恢复。`internal/taskmigration` 验证旧任务交接与重复运行。业务回归继续覆盖分析去重、来源限速、任务取消、版本校验和权限。

```sh
python3 scripts/verify-integration-isolation.py --race --count 1
```

该脚本创建并清理独占的 MySQL、Redis、etcd 和 Kafka，不读取个人 `.env` 或已有数据卷。2026-10-30 的 Redis 队列反例与测量保留在[历史验收](adversarial-acceptance.md)，其数据不代表 Kafka 版本的性能。
