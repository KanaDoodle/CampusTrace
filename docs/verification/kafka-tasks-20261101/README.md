# Kafka 后台任务验收

CampusTrace 的来源导入、岗位抓取、结构化分析和状态评估统一通过 Kafka 投递。五类任务按用户／系统归属划分十个通道，MySQL 保存容量、用户轮转、重试时间与执行租约；业务结果或后续处理状态持久化后提交消费位点。岗位变更继续使用独立的主题和 Search／通知消费组。

实现与升级步骤见[可靠任务调度](../../task-scheduling.md)。验证结果见 [acceptance.json](acceptance.json)，交付文件摘要见 [source-sha256.json](source-sha256.json)。原有历史性能报告与源文件摘要保留。

| 验证 | 结果 |
| --- | --- |
| 全量隔离竞态集成回归 | 605 个顶层测试通过，35 个条件用例跳过；其中业务集成 202 项通过 |
| 任务专项连续两轮 | 24 次顶层测试通过，包含并发容量、轮转、消费失败、SQL 状态提交失败、SIGKILL、重试、死信与旧队列迁移 |
| 恢复修复后专项 | 6 项通过，包含真实 mysqldump 导入、Kafka 保留事件隔离、Search／通知独立恢复、重复准备与任务租约保护 |
| 完整容器故障验收 | Kafka 暂停期间业务写入及 SQL 任务交接继续；恢复后分析、索引与通知追平；重放与异常消息隔离通过 |
| 前端与业务流程 | 453 项前端测试、7 项 HTTP 求职流程检查通过 |
| 构建与依赖 | Go build、vet、公开 KanaRPC v0.1.0 及 Search 独立依赖边界通过；Go 1.25.9 容器镜像构建通过 |
| 性能验收脚本契约 | 16 项 Python 与 6 项 JavaScript 检查通过，本轮没有重新测量性能指标 |

全量集成中的条件跳过包含真实招聘网站、子进程辅助入口和需额外 OpenSearch 的用例。Kafka／Search／恢复另由真实依赖专项与容器故障验收验证，跳过项不计为通过。隔离脚本核对临时数据库、账号均已清理，模板标记保留；原有业务容器与数据未切换。

## 复现

```sh
python3 scripts/verify-integration-isolation.py --race --count 1 --adversarial
make task-queue-test
CAMPUS_SMOKE_KAFKA=1 sh scripts/observability-local.sh
sh scripts/journey-local.sh smoke
```

任务重复检查可执行：

```sh
python3 scripts/verify-integration-isolation.py --race --count 2 \
  --run 'Test(Journal|KafkaTask|TaskMigration|RepairPoisonWorker|ModelControlUnknownReview)'
```

真实恢复与独立消费检查：

```sh
python3 scripts/verify-integration-isolation.py --search --race --count 1 \
  --run 'Test(KafkaNotificationAudienceAndCurrentStatus|KafkaSearchAndNotificationsIndependentRecovery|KafkaRecoveryEpochIgnoresRetainedFutureEvents|OpenSearchContract|JournalRecoveryCopy|Recovery)'
```

原始本机记录位于 `/tmp/ct-task-release-20261101/`、`/tmp/ct-task-repeat-20261101/`、`/tmp/ct-task-recovery-final-20261101/` 和 `/tmp/ct-task-stack-20261101-release/`。新 CI 工作流会保存对应诊断记录，本次未执行远程 GitHub Actions。

本次使用单机、单节点 Kafka 和合成业务／模型响应，外部模型调用为零。多副本可用性、跨机认证和生产吞吐量需在对应部署环境验收。
