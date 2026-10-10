# 监控与分析任务排障

原有 `/metrics` JSON 保持兼容。新增 API `http://127.0.0.1:8080/metrics/prometheus` 和 Worker `http://127.0.0.1:18081/metrics/prometheus`，提供 Prometheus text exposition；Worker 在 Compose 内监听配置地址，可通过容器内部端口抓取，不需要把该端口发布到公网。

## 指标

- `campustrace_http_requests_total`、`campustrace_http_errors_total`（5xx）、`campustrace_http_request_seconds` 直方图。
- `campustrace_matching_queue_seconds`，以及 `matching_prepare_seconds`、`matching_extract_seconds`、`matching_compare_seconds`、`matching_save_seconds` 直方图：排队、准备、提取、匹配及保存耗时。
- `campustrace_matching_result_commit_seconds` 记录逐个匹配结果准备与提交的耗时。
- `campustrace_matching_failures`、`campustrace_matching_model_calls_total`、`campustrace_matching_result_cache_hits_total`、`campustrace_matching_requirements_cache_hits_total`。
- `campustrace_db_connections_open/in_use/idle`、`campustrace_db_wait_total`、`campustrace_db_wait_seconds_total`、`campustrace_go_goroutines`、`campustrace_go_heap_bytes`、`campustrace_process_uptime_seconds`。
- 原有 Worker 管线的计数和累计耗时保留；`Metrics.Since` 另外生成 `*_duration_seconds` 直方图。
- `campustrace_queue_history_trimmed_total` 记录当前 Worker 回收的已确认队列消息数；业务记录不会随这项清理删除。
- `campustrace_assessments_scheduled_total` 记录当前 Worker 从到期计划中补发的状态更新任务数；抓取／解析变化触发的任务不计入此项。

桶边界从 5 ms 到 180 s，所有标签仅有固定耗时边界，不以账号、岗位、请求编号或 URL 作指标标签。计数、直方图与运行时指标为进程内状态，重启后重置，SQL 任务与事件不随重启丢失。

接入已有 Prometheus 的抓取配置示例（与 CampusTrace 同一 Compose 网络）：

```yaml
scrape_configs:
  - job_name: campustrace-api
    metrics_path: /metrics/prometheus
    static_configs:
      - targets: [api:8080]
  - job_name: campustrace-worker
    metrics_path: /metrics/prometheus
    static_configs:
      - targets: [worker:18081]
```

示例查询：

```promql
histogram_quantile(0.95, sum(rate(campustrace_http_request_seconds_bucket[5m])) by (le))
rate(campustrace_http_errors_total[5m]) / clamp_min(rate(campustrace_http_requests_total[5m]), 0.001)
rate(campustrace_matching_failures[5m])
rate(campustrace_db_wait_seconds_total[5m])
```

项目提供抓取接口与指标契约，没有默认安装 Prometheus/Grafana、主动告警服务或 OpenTelemetry collector。

## 关联记录

新分析结果的详情若显示「已按当前资料在本地更新资格与偏好」，表示复用了已验证的能力比较，展示日期仍是原模型分析时间；这次读取不产生模型调用。技能/项目事实变更或无法本地解释的资格依赖变更仍显示待更新。旧任务的授权使用完整资料身份，不能因能力可复用就忽略重新核对。

HTTP 响应带 `X-Request-ID`。日志记录请求编号、HTTP 方法、路由模板、状态和耗时，不记录查询字符串、请求正文或密钥。深度分析日志带同一轮的请求编号与任务编号，以及批次数量、耗时、模型调用数和安全错误分类。

岗位库任务区域的「查看处理记录」从 SQL 读取最近 200 个阶段事件，返回准备、提取、匹配、保存的阶段、耗时与关联编号。每轮最多保留 2,000 个事件；事件按用户任务归属鉴权，岗位标题仅在当前仍可见时批量回填。

排障：先在页面查看失败阶段和具体核对原因，再用任务/请求编号定位 `./campustrace logs api`。401/403 检查模型密钥，402 检查模型账户余额，429/服务错误稍后明确重试，超时不要自动重放。结构或摘录核对失败会保留可以复用的要求解析。取消或过期租约禁止旧执行写回匹配结果。服务中断后重新核对资料与模型即可继续；不同模型或输入变化需发起新一轮。

任务记录是应用内部的持久阶段追踪，配合现有 Worker/KanaRPC 关联字段；不是跨外部模型供应商的完整分布式 tracing。

## 运行与资源占用

`./campustrace resources` 只采样当前 CampusTrace Compose 项目中的运行组件，显示各自的 CPU 与内存；没有运行组件时不会退回查询全部 Docker 容器。CPU 是瞬时采样，容器内存也不等于 macOS 上 Docker 虚拟机的全部开销。排查时应同时查看系统活动监视器，避免把其他应用的占用算到本项目上。

默认 Compose 适合个人使用：MySQL 保留 128 MiB InnoDB 缓存与 Performance Schema，但缩小诊断历史、表缓存、日志缓冲和临时表内存预算，最多 64 个连接。Go 连接池仍最多 16 个连接，空闲连接超过一分钟回收。这里是组件预算调整，不是整个 MySQL 进程的硬内存上限；多用户、高并发部署需按负载重新评估。

Worker 有工作时以 200 ms 间隔发送 Outbox、调度到期重试；连续空闲时逐步延长到 3 秒，出错时也退避到 3 秒。因此空闲后的新任务最多多等待约 3 秒进入调度，不含排队与执行时间。消费端仍使用 Redis 阻塞读取，领取、幂等和故障恢复规则保持原样。招聘源导入的 `SOURCE_IMPORT` 也纳入延迟重试调度。

### 岗位状态按变化与到期更新

不再每小时为全部岗位生成 `ASSESS` 任务。新抓取、解析开始／完成／失败触发对应岗位的更新，同一岗位已有待处理任务时只标记新的输入，不重复排队。处理时读取最新提交的数据；输入未变且没有到时间节点，不追加相同的状态评估历史。

状态评估后，计算下一次会改变状态或判断依据的时间，记录到 MySQL 的 `job_assessment_schedule`。可信截止日期按来源时区处理，只有日期时仍在该地次日零点结束；官方成功观察超过七天时更新为待核验状态。已过期、失败或没有未来变化的岗位不会反复安排任务。排序中新鲜度的自然衰减、个人资料和偏好的变化，在读取时按当前资料计算，不能据此静默重新调用深度分析模型。

Worker 平时每分钟只查询到期索引，每批最多 100 个；批次满时一秒后继续补齐。到期扫描、空闲发送和队列执行都会带来延迟，通常约一分钟内进入调度；大量岗位同时到期或任务积压时会更晚，不承诺严格实时。新任务通过 SQL Outbox 与待处理编号同事务提交，多个调度器不会重复领取；待处理租约为十分钟，过期后可补发新编号，旧任务不能清除后来任务的编号。

升级首次启动会为旧岗位分批补齐更新计划；后续重启保留既有计划。规则版本变化时重新安排相关岗位，新增计划随岗位删除级联清理。这里仅更新本地状态与评估，不改变招聘源的定期抓取设置。

已处理消息默认保留最近 24 小时，可用 `.env` 的 `TASK_HISTORY_HOURS=24` 调整，允许 1–8760 小时。Worker 用 Redis 服务器时间判断年龄，在同一原子脚本中检查所有消费组的已读取位置和最早未确认消息，再按更保守的边界回收旧消息。首次启动与有积压时每秒最多检查 10,000 条，平时每分钟检查；按整节点近似裁剪，因此可能多保留部分已确认消息。没有消费组、存在旧的未读／未确认消息时，会保留相应历史。

这项机制只回收任务 Stream 中可安全裁剪的传输历史，不触及 SQL Outbox、幂等回执、岗位、个人资料、深度分析结果、投递记录、延迟重试或死信。旧的未确认消息可能阻止后面的历史裁剪，此时应先排查任务，不能直接清空队列。裁剪次数可通过上述 Worker 指标查看。

健康检查平时每 30 秒一次，启动阶段仍每 3 秒检查，避免常驻组件频繁拉起探针；故障发现可能比原来的 3 秒间隔晚一些。暂时不用时运行 `./campustrace stop --all`，下次 `./campustrace start` 即可继续，保留数据卷。

实现依据：[Redis XTRIM 的 MINID 与有界近似裁剪](https://redis.io/docs/latest/commands/xtrim/)、[MySQL 8.4 Performance Schema 配置](https://dev.mysql.com/doc/refman/8.4/en/performance-schema-system-variables.html)、[Compose 健康检查](https://docs.docker.com/reference/compose-file/services/#healthcheck)。Redis 7.4 没有新版本的 `ACKED` 裁剪选项，所以消费组边界由原子脚本显式保护。
