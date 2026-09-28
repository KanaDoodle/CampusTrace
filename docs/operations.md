# 监控与分析任务排障

原有 `/metrics` JSON 保持兼容。新增 API `http://127.0.0.1:8080/metrics/prometheus` 和 Worker `http://127.0.0.1:18081/metrics/prometheus`，提供 Prometheus text exposition；Worker 在 Compose 内监听配置地址，可通过容器内部端口抓取，不需要把该端口发布到公网。

## 指标

- `campustrace_http_requests_total`、`campustrace_http_errors_total`（5xx）、`campustrace_http_request_seconds` 直方图。
- `campustrace_matching_queue_seconds`，以及 `matching_prepare_seconds`、`matching_extract_seconds`、`matching_compare_seconds`、`matching_save_seconds` 直方图：排队、准备、提取、匹配及保存耗时。
- `campustrace_matching_result_commit_seconds` 记录逐个匹配结果准备与提交的耗时。
- `campustrace_matching_failures`、`campustrace_matching_model_calls_total`、`campustrace_matching_result_cache_hits_total`、`campustrace_matching_requirements_cache_hits_total`。
- `campustrace_db_connections_open/in_use/idle`、`campustrace_db_wait_total`、`campustrace_db_wait_seconds_total`、`campustrace_go_goroutines`、`campustrace_go_heap_bytes`、`campustrace_process_uptime_seconds`。
- 原有 Worker 管线的计数和累计耗时保留；`Metrics.Since` 另外生成 `*_duration_seconds` 直方图。

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
