# 分析链路迁移到 KanaRPC

## 版本与调用链

CampusTrace 固定依赖 `github.com/KanaDoodle/KanaRPC v0.0.0-20261031172914-7b63b80f0544`，对应公开提交 `7b63b80f05449c2033b6b3a18648a510b43a6d2c`。版本通过 Go 模块获取；正常构建和 Docker 构建不需要本地 RPC 源码或 replace。

```text
Kafka 任务 → Worker → routing 连接池 → KNR2/TCP → Analysis → 提取器
                           ↑                         ↓
                    etcd 新鲜发现快照 ← instance ID + lease
分析结果 → 语义版本与证据校验 → 缓存 / MySQL 业务回执 → 后续评估
```

Analysis 注册 Context 优先的 `/campustrace.analysis.v1/AnalyzeObservation` 方法。请求保留任务编号、业务 deadline 和 traceparent；响应保留语义版本、证据、实例与安全错误分类。分析响应仍需通过当前语义版本和原文引用校验，才能进入缓存和业务持久化。

## 运行机制

| 环节 | 实现 |
| --- | --- |
| 取消与截止时间 | 原生 CANCEL 进入处理器 Context，继续传到提取器；业务 deadline 与 RPC deadline 共同约束执行，最长 30 秒 |
| 执行预算 | 每实例执行上限为 `ANALYSIS_CONCURRENCY`，等待队列容量与该值一致，排队上限 100 ms；不启用自适应调参 |
| 客户端预算 | 保留调用并发限制；连接池最多 8 个连接，使用短效压力反馈；连接与帧缓冲由 SDK 预算约束 |
| 发现与重连 | 按实例 ID 注册，使用新鲜快照轮转选址；快照过期后通过 SDK 公开 API 有界重建 watch；失败建连退避，连接失效后由后续调用重建；撤出节点的连接在使用结束后回收 |
| 就绪与恢复 | listener、KeepAlive 及注册归属共同决定就绪；lease 丢失后降低 readiness，重新注册验证后恢复 |
| 关闭 | 先降低 readiness、撤销注册，再在 5 秒内等待在途调用与协议回执；超时取消，日志保留 drain 的资源与交付结果 |
| 业务幂等 | 一次 RPC 不自动重放；SQL 调用凭据、处理版本及业务回执继续控制恢复 |

SQL 调用凭据在 RPC 之前写入。明确的提供商 401/403/429 拒绝、`routing.AdmissionError`、本地等待调用名额期间的取消/超时，以及已识别的本地发送前准入拒绝可清除本次调用凭据，交回原有任务失败策略处理。其他连接错误、超时及没有执行凭证的远端过载/关闭拒绝保持结果未知；不能仅凭状态码断言模型没有执行。排队截止与执行截止也不通过相同的 deadline 状态码区分。取消停止后续工作，不撤销模型提供商已经受理的请求或计费。

协议回执确认 RPC 交付，业务提交由 MySQL 回执确认。关闭报告的 `Quiescent` 表示框架连接与处理器收敛，累计交付故障单独记录。

发现快照保留 SDK 默认 3 秒新鲜度门槛。适配层每 250 ms 检查一次：快照过期时重新建立 watch，使用 SDK 读到的新快照验证 cluster ID 和 revision 后替换，再关闭旧 watch。一次最多保留两个 resolver，每次读取受 SDK 1 秒请求预算约束；etcd 不可用时继续拒绝派发。该恢复策略处理 watch 连接仍存活、进度通知未及时更新的情况，不延长旧地址的有效期。

## 部署升级

KNR2 与旧 KanaRPC-Go 协议不兼容，使用独立发现 namespace `/campustrace/kanarpc/v2`。`ANALYSIS_RPC_NAMESPACE` 可覆盖该值，Worker 与所有 Analysis 实例必须一致；多套部署可使用不同 namespace 和实例 ID。相同 namespace 下的实例 ID 必须唯一，已有注册归属受 lease 校验保护。

1. 先备份数据库，停止 Worker 派发并按原任务恢复流程处理在途工作；已存在的结果未知调用继续保留核对状态。
2. 停止旧 Analysis，更新并启动两个 Analysis 实例，确认 `/readyz` 返回 200。
3. 更新并启动 Worker，确认任务完成、语义版本匹配和 SQL 回执。Kafka 中待处理任务沿用现有 envelope，无需重建业务数据库。

使用项目统一启动入口时，先停止再重新构建、启动应用进程。旧 namespace 的租约自然失效，不会被新 Worker 选中。需要回退时协调回退 Worker 与 Analysis，并保留 SQL 调用凭据和任务状态。

服务端使用私有网络内的 JSON/TCP 普通调用。SDK 自带的 Protobuf、流式 SDK、TLS 与 QUIC 作为库能力保留，分析链路的部署配置以本页为准。对外部署应在网络边界提供访问控制与传输保护。

## 验证

```sh
GOWORK=off go test -race -count=5 ./internal/analysis -run TestKanaRPC
python3 scripts/verify-integration-isolation.py --race --count 1 --adversarial
sh scripts/verify-release-boundary.sh
CAMPUS_SMOKE_KAFKA=1 CAMPUS_OBSERVER_EVIDENCE=/tmp/ct-rpc-stack sh scripts/observability-local.sh
```

专项检查包括实际 TCP 上的远端取消、deadline、连接复用、有界过载、在途结果交付与 drain；实际 etcd 的双实例选址、节点撤出、lease 撤销恢复；Worker → RPC → SQL 的业务处理，以及派发前拒绝可重试、已派发超时不重放的调用凭据行为。

本轮结果与运行环境见[验收记录](verification/kanarpc-migration-20261101/README.md)。历史性能与验收记录保持原版本，未将它们作为本次迁移的性能结果。
