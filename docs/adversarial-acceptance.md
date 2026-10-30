# 反例驱动验收：2026-10-30

本轮在上轮未提交交付上继续验证，没有 reset、提交、推送或公开部署。恢复基线为 `bin/backups/adversarial-before-20261030/`，含 HEAD、状态、196698 字节 tracked.patch 与 63597 字节 untracked.tar.gz；归档已列目录检查。HEAD 仍是 `93c6fbebd872f0db0ea11f08531d838c5782f075`，原三个 company_auto 文件 SHA-256 未改变。

曾在写测试脚本时断连。恢复后核对未决 apply_patch 已完成且两个文件均已落盘，没有重放补丁；没有在连接不明时启动或强杀测试进程。

## 实际复现及修复

| 反例 | 修复前的实跑证据 | 最小修复及回归 |
| --- | --- | --- |
| UTC/GMT 被默认成北京时间 | `mail-before.log`：14:00 UTC/GMT 提出 06:00Z；CST 也被默认为北京时间 | 支持明确 UTC/GMT 和数字偏移；未支持／含糊时区停止预填并提示。`mail-after.log` 和 `final.log` 通过 |
| 回复截止冒充面试时间 | 同上：“12:00 前回复，面试另行通知”预填 04:00Z | 保留时间候选，检测相邻回复截止语境后不预填活动时间；不声称能理解所有自然语言 |
| 旧索引未回填时绕过用户限额 | `process-before.log`：20000 条未索引旧任务存在时，4 个独立进程仍全部获准新增 | 准入将未知归属旧行保守计入每个 owner，回填后恢复精确计数；修复后 4 个进程全部被拒绝。迁移期可能暂时拒绝其他用户，换取不超发 |
| EXECUTING→调用前的暂停竞态 | `pause-before.log`：SQL 读取闸门让 PAUSE 恰好进入该窗口，模型调用 0，最终却是 WAITING_AUTH | 调用前复查发现 PAUSING 时完成 PAUSED 转移；同一闸门断言通过 |
| 已缓存的合法空结果无法恢复 | `process-before.log` 的 BEFORE_SQL_NULL：cache=null、SQL 未提交，强杀恢复后误报 OUTCOME_UNKNOWN | 用独立 cacheValid 标记区分合法空结果和缓存缺失；同一强杀场景恢复 COMPLETE，调用次数保持 1 |
| 一个悬空旧 ANALYZE 阻断健康任务 | `race-before.log`：Observation 不存在导致整个 Outbox dispatch 返回 ErrNoRows | 默认 typed dispatch 保留原 envelope，送入正常终态路径，健康任务继续转发；`orphan-after.log` 进一步确认原 task ID 最终进入 DLQ。该场景是受控异常数据，不是已证实的正常用户操作路径 |
| 满积压阻止派生评估，反过来让源分析无法排空 | `full-repro.log`：初始 ASSESS 已完成、ANALYZE 仍待处理，加入合法旧积压后，BindAnalysis 因派生 ASSESS 的准入拒绝而回滚 | `enqueueAssessment` 已先保存 due 计划；仅遇 ErrQueueBusy 时保留 due、继续源处理，名额释放后由既有 ScheduleAssessments 补发。`full-after.log` 验证排空后可补发且不超过 20000；不绕过容量或丢派生任务 |

主要实现定位：`internal/recruitmentmail/parse.go`、`internal/persistence/jobs.go`、`internal/persistence/assessment_schedule.go`、`internal/pipeline/worker.go`、`internal/transport/matching_tasks.go`。修复未改数据库 schema，继续使用迁移 015。容量不足时，派生评估可延至既有下一次到期扫描，默认约一分钟；due 记录不会消失。

容量组合夹具最初没有先完成初始评估，因此不能触发派生评估准入；已保留 `full-before.log`，补上真实初始 ASSESS 完成步骤后才在 `full-repro.log` 复现 ErrQueueBusy，未将最初夹具失败当产品故障。最初暂停闸门放错到持锁事务内，出现测试自身锁等待超时；已保留 `race-before.log`，改为事务提交后的独立读取闸门后，得到上述真实 WAITING_AUTH 反例。最初 AFTER_SQL 测试在新 Worker 尚未恢复 ACK 时就停止它，也已修正为等待 resident 释放；这些测试脚手架错误不计作产品漏洞。

## 多进程与真实强杀

`internal/integration/adversarial_process_test.go` 启动当前项目的 race 测试二进制子进程，子进程使用生产 Queue／Worker／Store 实现；只传入明确的合成环境，不继承模型密钥。父进程同步记录 PID，SIGKILL 只针对它刚创建的 `exec.Cmd.Process`，随后 Wait 回收。强杀场景还断言退出信号确实是 SIGKILL。

六个窗口均已运行：

1. SQL 调用凭据已写、mock 外部调用尚未开始：强杀后 calls=0，保守进入结果未知。
2. mock 调用中：calls=1，重启不再调用。
3. mock 已返回、Redis cache SET 尚未执行：calls=1，重启不再调用。
4. cache 已写、SQL PersistAnalysis 被父进程持有的合成 job 行锁阻塞：calls=1，重启复用缓存完成。
5. 上一项返回值换成合法 `(nil,nil)`：calls=1，修复后可完成。
6. SQL 已提交、ACK 尚未执行：calls=1，重启凭 SQL 回执恢复 ACK。

其他实测包括：6 个进程共 120 次入队竞争，共享 resident 容量 8，最终恰好接纳 8；4 个进程重放同一 MatchRun 请求，只创建一轮；不同进程新请求被账号有效 lease 拒绝；人为令合成 lease 过期后，旧 token 不能写，新 token 可创建替代执行。

两个真实 Worker 进程同时迁移旧未读 Stream、旧 PEL、due retry 与显式 DLQ redrive；输入含重复消息，resident=3、owner=2。8 个唯一任务各有 SQL 完成回执，旧 pending/retry 与 resident 最终排空。这里验证的是幂等结果与不丢任务，传输仍允许重复，不宣称处理函数 exactly-once。

## 产品决策与通知

合成博士硬门槛对本科学历给出 INELIGIBLE，学历缺失保持 UNKNOWN；公司方案不把缺失资料当成硬性不符合。两个不同选岗同时争同批次最后一个名额，只提交一个申请；另一事务冲突，未部分提交。已核对预览后 JD 改变，旧提交 key 被拒绝。

通知按引用证据排序仍保留歧义，不因导入就推进申请。测试覆盖引用冲突、跨账号确认拒绝、UTC 时间确认与原流程的改期／快照一致性。**人工确认仍允许用户选择本人申请并修改提取字段；没有证明能够自动阻止所有同用户误选，也不把合成测试当真实岗位推荐准确率。**

## 负载证据与边界

原始输出随仓库保存于 [adversarial-load-20261030.json](../examples/scheduling/adversarial-load-20261030.json)。2 个 Worker 进程，6 个岗位、2 个 owner；mock fast=10ms、slow=120ms、429 首次明确拒绝后 60ms 成功，另一路超时为 2000ms。该轮 5 成功、1 结果未知；429 路恰好调用 2 次，其余各 1 次，没有重放未知调用。

该次运行从 Worker 启动到观测全部终态约 5310ms，终态吞吐 1.13 个/s；观察创建至首次 mock 调用的等待 1069–1249ms，经验 P95=1249ms；至观测终态 1107–5346ms，经验 P95=5346ms。只有 6 个样本，P95 与最大值相同，不能作为稳定尾延迟估计。每 10ms 查询 SQL 状态，完成时刻含采样误差；创建时间取 Observation，等待包含入库与调度开销，并非只测 Redis ready 段。

采样 resident 峰值为 12（包含衍生 ASSESS）；进程日志中测到的最大单个 race 测试子进程 MaxRSS 约 86.9MB，来自退出后的 OS rusage，不是整个应用／多进程同时内存上界。空闲队列约一秒轮询延迟在此负载中可见。没有用这次结果宣称性能改善、生产 QPS、实际模型费用或用户规模。

API 内公平性仍只保证单实例；跨实例共享的是 SQL lease／token 和 Redis 队列机制，没有新增全局 API 公平调度。本轮并未启动两个完整 API HTTP 服务做多实例流量压测。

## 验收与复现

```sh
# 需本地 Go 依赖与 mysql:8.4、redis:7.4-alpine、quay.io/coreos/etcd:v3.6.7 镜像
GOCACHE=/tmp/campustrace-adversarial-cache sh scripts/adversarial-local.sh
# 原产品烟测，端口需空闲
JOURNEY_HTTP_PORT=23381 sh scripts/journey-local.sh smoke
```

脚本不读 .env，使用 tmpfs／临时容器与随机 loopback 端口，退出仅删除自己创建的三个容器。本机原始日志在 `bin/verification/adversarial-20261030/`，其中：

| 项目 | 本轮结果 |
| --- | --- |
| 反例／产品／RPC／队列定向 race | PASSED：`final.log` 顶层 71 passed，0 failed；1 个预期 helper 入口 skip |
| 强杀、进程竞争、迁移、变延迟／429／超时 | PASSED：同上与逐进程 `child-*.log`、`processes.jsonl` |
| 悬空任务 DLQ 去向 | PASSED：`orphan-after.log` |
| etcd lease 丢失后重新注册、双实例 KanaRPC、Worker→RPC | PASSED：`TestRepairAnalysisRegistrationReadiness`、`TestMyRPCTwoInstances`、`TestRunningWorkerOutboxRPC` |
| 全 Go race／vet／build | PASSED：`unit-race.log`、`vet.log`、`build.log`；普通命令的外部依赖测试跳过，上述定向集成另行实跑 |
| 前端与产品 HTTP 闭环 | PASSED：432 项，`web.log`；`http-smoke.log` |
| 进程及容器清理 | 本轮各次执行共 118 个 start 均有 reaped 记录（含复现与重跑，非并发数）；本轮容器列表为空；没有操作用户其他服务 |
| 全量所有历史集成、任意损坏 Outbox JSON、10万 SQL backlog 极限 | NOT RUN |
| Redis/MySQL 自身强杀、断电、持久盘损坏、failover／OOM、异机压测 | NOT RUN；本轮强杀的是应用测试进程，依赖容器按正常流程清理 |
| 真实邮箱／付费模型／个人材料外传／真实推荐质量 | NOT RUN；不在本轮范围 |

本轮没有剩余连接或审批阻塞。调用已经成功但在 cache 前丢失结果时，仍只能保守人工核对；不能因此绕过防重放凭据自动收费重试。
