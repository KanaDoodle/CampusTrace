# 工程设计与验证

CampusTrace 将招聘追踪、岗位分析、投递留档和面试复盘串联为完整流程。本文按业务问题组织关键设计、复现入口和验证记录。

构建工具链、运行参数和浏览器依赖的维护记录见[技术栈维护](stack-maintenance.md)。

## 关键处理链路

| 业务问题 | 实现机制 | 验证入口 |
| --- | --- | --- |
| 岗位变更需要分别驱动搜索与通知 | MySQL 事务 Outbox、Kafka 独立消费组、通知事务回执、索引外部版本；发布确认与索引可见性分别跟踪 | [岗位事件](job-events.md)，`make kafka-test` / `make kafka-stack-test`；覆盖确认丢失、独立停机恢复、重放与删除 |
| 后台任务中断、重复投递与延迟重试 | Kafka 分通道投递，SQL 任务日志、租约与修订号，结果持久化后提交位点 | [可靠任务调度](task-scheduling.md)，`make task-queue-test`；旧队列交接与恢复 |
| Search 依赖异常时维持查询响应并保护数据库 | 查询预算、前台与远程并发隔离、后台恢复探测、迟到状态隔离、SQL 有界等待；Core 复核权限和数据版本 | [搜索故障隔离](search-resilience.md)，`make resilience-test`；故障矩阵与同机 k6 对照 |
| 多 API 共享付费模型资源与调用预算 | SQL 公平许可、调用次数原子预留、执行账本、UNKNOWN 核对、心跳与迟到写入隔离 | [模型执行控制](model-control.md)，`sh scripts/model-control-local.sh`；多进程竞争、SIGKILL 与恢复 |
| 索引重建期间持续服务，切换后可回滚 | 旧索引读取、快照回填、增量追平、校验屏障、别名切换与 Core 确认交接 | [在线重建](search-service.md)、[重建验收](verification/online-rebuild-20261031/README.md) |
| 数据库恢复后隔离仍保留的事件与索引状态 | 数据集代际与版本 fencing，按恢复副本重新发布和追平 | [快照恢复演练](verification/recovery-epoch-20261031/README.md) |
| 准确保留 C++ 和技术要求的必需、优先、任选关系 | 技术词识别、上下文提取、条件表达式解析、Analysis RPC 语义版本、缓存与代际校验 | [技术要求解析回归](verification/technology-requirements-20261031/README.md) |

Search 独立维护派生索引；Notify 独立消费并复用 Core 数据域。服务关系见[架构](architecture.md)，公开依赖与上游来源见[依赖说明](dependency-audit.md)。

## 复现完整流程

```sh
sh scripts/journey-local.sh smoke
```

一次性环境覆盖岗位导入与搜索、公司内计划、材料冻结、通知确认与改期、复盘笔记保存，以及下一次面试准备。交互演示可执行 `sh scripts/journey-local.sh demo`，使用说明见[申请闭环](application-journey.md)。

故障复现使用独立的合成环境。报告保留运行条件、基准版本、源码哈希、失败记录与完整分位数；入口汇总见[版本验收](verification/release-20261031/README.md)。

## 验证口径

| 类型 | 输入与环境 | 记录内容 |
| --- | --- | --- |
| 行为与恢复回归 | 合成业务数据、实际依赖、受控提供商响应 | 权限、幂等、事务、版本、取消和进程恢复 |
| 性能对照 | 固定源码、同机资源配额和指定工作负载 | 查询模式、失败与丢弃、P95/P99、分配开销、恢复等待 |
| 模型与检索评测 | 按来源登记的标签和模型配置 | 工具选择、引用、排序质量、耗时和调用次数 |

详细指标和复现命令见[性能验收](performance.md)、[岗位匹配评测](matching-evaluation.md)、[Agent 评测](agent-evaluation.md)与[检索评测](knowledge-retrieval.md#检查效果)。

## 架构审阅要点

- 数据恢复：数据库、Kafka 与索引使用数据集代际识别同一份业务数据。
- 索引切换：回填、增量追平、屏障和确认交接共同处理并发写入及响应丢失。
- 异步提交：业务写入、事件发布、索引刷新和通知回执分别确认，重复事件通过版本与幂等回执收敛。
- 故障隔离：依赖调用和数据库回退各自执行准入预算，恢复探测与前台查询分离。
- 模型调用：已知拒绝、完成和未知结果分别记账，核对和重试保持独立。
