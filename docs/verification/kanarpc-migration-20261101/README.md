# KanaRPC 分析链路迁移验收

2026-11-01，CampusTrace 从 `ea0f048d6d020535a25e32e4c4f7b69970293c44` 工作区基线迁移分析链路。固定使用公开 KanaRPC 提交 `7b63b80f05449c2033b6b3a18648a510b43a6d2c`，Go 版本为 `v0.0.0-20261031172914-7b63b80f0544`；这是伪版本，不是发布 tag。

实现及部署顺序见[迁移说明](../../kanarpc-migration.md)。本轮替换旧模块、注册与调用 API、服务发现和连接所有权；保留分析语义版本、原文证据校验、SQL 调用凭据、Kafka 任务投递及 Trace 上下文。分析使用 KNR2/JSON/TCP 普通调用。

## 验证结果

| 检查 | 实际结果 |
| --- | --- |
| 全包真实依赖与进程回归，`--race --count 1 --adversarial` | 619 个顶层测试通过，35 个条件跳过，无失败 |
| 其中 `internal/integration` | 205 个顶层测试通过、17 个跳过；已包含在上一行总数中 |
| 独立 TCP 竞态专项 | 6 项普通调用专项连续运行 10 次通过；依赖 etcd 的专项在独立运行时按条件跳过，并在全包验收中实际执行 |
| Go 构建、vet、依赖边界 | 通过；Search 的依赖图不引入 Core 数据库客户端或 KanaRPC |
| 模块校验与整理 | `go mod verify` 通过，`go mod tidy -diff` 为空；无 replace、无旧 KanaRPC-Go 模块 |
| 前端与 Python 检查 | 453 项前端测试、5 项 Python 测试通过 |
| Core 与独立 Search 镜像 | 使用固定模块成功构建；镜像不复制相邻 SDK 仓库，不使用本地 go.work |
| 全栈追踪与观测 | HTTP → Kafka → Worker → KanaRPC → Analysis 连续追踪通过；Tempo 停止期间业务继续完成，恢复后采集恢复 |
| Search 与通知 | 独立消费者重启追平、搜索服务替换、索引故障与丢失重建、数据库网络隔离、SQL 回执重复消费去重通过 |
| Kafka 故障 | broker 暂停期间 Core CRUD 与持久任务交接可用，恢复后发送及独立消费完成 |
| 清理 | 隔离 SQL 账号/库剩余均为 0，模板保护标记保留；本轮自有容器清理，既有开发容器保留 |

环境为 macOS arm64、Go 1.27.3、Node.js 26.7.0；容器应用为 Linux arm64，使用 MySQL 8.4、Redis 7.4、Kafka 4.4.0、etcd 3.6.7 和可选 OpenSearch/观测组件。etcd Go 客户端随新 SDK 升至 3.7.3，真实服务调用与恢复测试覆盖该组合。

## 关键行为与发现

- 实际 TCP 验证远程取消到达已进入的提取器、释放执行名额，并保留同一连接供下一次调用使用。deadline 进入提取器上下文。
- 有界队列满时不增加提取器调用，也不重放业务请求。不能证明尚未执行的远端状态保持结果未知；本地发送前拒绝与已派发超时分别验证 SQL 调用凭据的清除/保留。
- 父进程收到退出信号后，注册先撤回，readiness 降低；在途调用继续交付结果，drain 报告确认资源收敛，业务提交仍以 SQL 回执为准。
- 初次验收发现注册成功与 KeepAlive 就绪之间的窗口，启动流程现等待 listener 与注册共同就绪。空闲发现测试也等待首个完整端点快照，再开始采样。
- 全栈首次出现两个实例仍在注册、Worker 发现快照过期的现象。主机与 Linux 对照中也观察到空闲后快照失效；单独把 etcd 进度通知周期调到 1 秒仍未稳定恢复，该配置尝试已撤回。适配层现每 250 ms 检查过期状态，通过公开 SDK 获取新快照，校验集群和 revision 后替换 watch，最多持有两个 resolver。旧快照不延长有效期；etcd 不可用时保持拒绝派发。
- 补充实际 etcd 上连续三轮 watch 重建、跨两个新鲜度窗口的空闲检查及关闭后的资源释放。修复后完整容器链路与全量竞态回归通过。初次失败摘要和固定字段诊断一并保留。

## 证据与复现

- [汇总](acceptance.json)、[模块版本与校验值](module.json)、[源码指纹](source-sha256.json)
- [RPC 专项测试结果](rpc-tests.json)、[条件跳过列表](skipped-tests.json)、[SQL 隔离与清理](integration.json)
- [观测](observability.json)、[Search](search.json)、[Kafka](kafka.json)
- [初次失败摘要](initial-failures.json)、[RPC 诊断摘录](rpc-diagnostics.json)
- [依赖边界输出](dependency-boundary.txt)、[循环竞态检查](loopback-race.txt)、[应用构建](build-core.txt)、[Search 构建](build-search.txt)

```sh
GOWORK=off go test -race -count=10 ./internal/analysis -run TestKanaRPC
python3 scripts/verify-integration-isolation.py --race --count 1 --adversarial --output /tmp/ct-rpc-regression-new
sh scripts/verify-release-boundary.sh
CAMPUS_SMOKE_KAFKA=1 CAMPUS_OBSERVER_EVIDENCE=/tmp/ct-rpc-stack-new sh scripts/observability-local.sh
node --test web/*.test.cjs
python3 -m unittest discover -s scripts -p 'test_*.py'
```

本次使用隔离合成输入与本地依赖，真实模型调用为 0。记录验证功能、资源约束和故障恢复；不作为生产部署、吞吐量或模型质量测量。历史性能与验收报告保持原版本，不重新标记为本次结果。
