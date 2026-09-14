# CampusTrace Job Radar Productization Report

## Executive Summary

CampusTrace Job Radar 增量已实现，等待独立工程审查。用户可关注已登记的招聘来源，由 Worker 内调度循环持续发现及重新核验岗位，进入原有 Observation → Evidence → deterministic Assessment 链，再读取每日摘要、变化与截止雷达，使用通知和投递快捷操作。

本轮完成 A–J：WatchTarget、Scheduler、Source discovery/fetch、Recent Changes、Deadline Radar、Daily Digest、Notification Inbox、UserJobPreference/Quick Actions、Agent Radar Tools、中文 Job Radar 首页。未新增业务服务、消息系统或依赖，未修改 KanaRPC-Go。未 commit、push、tag 或部署替换现有服务。

## Baseline

| 项目 | 审计基线 |
| --- | --- |
| 工作目录 | `/Users/kana/Projects/campustrace` |
| branch / HEAD | `main` / `b3e847c45148e2d32241b6dbf6180b503ef6d105` |
| 开始时工作区 | clean |
| 版本标识 | HEAD 无 tag；原 README/UI 标记 1.0，本报告按任务约定称原核心为 v0.1；Job Radar 为内部 v0.2 标识 |
| module | `github.com/KanaDoodle/CampusTrace`，Go 1.25.9 |
| KanaRPC | `github.com/KanaDoodle/KanaRPC-Go v0.1.0`，仅公开 `/rpc` facade |
| dependency 验证 | 本地 ignored go.work 有 sibling replace；本轮所有验收使用 `GOWORK=off`，验证已发布依赖 |
| migrations | 原 `001_init.sql`、`002_visibility.sql`、`003_release_repair.sql` 及可重入 repair migration |
| Source | 既有 `Adapter.Fetch(ctx, URL) (Result,error)` / `Version()`，公共 HTTP 安全抓取与手动导入 |
| task / delivery | Version 1 envelope；ANALYZE / ASSESS；MySQL Outbox、Redis Stream/PEL、retry ZSET、DLQ、SQL receipts |
| JobChange | 原 changes 表与追加 Assessment 历史；不新建第二份 Job truth |
| Application | 原 PLANNED → APPLIED 等版本化 FSM，终态不可重开 |
| Agent | strict validation、grounded facts、输出预算、trace、PendingAction、显式确认与 MySQL receipt |
| 原首页 / tests | 中文岗位列表；Go unit/integration/repair/race、SSE/MCP/Agent/RAG、Node display tests |

## Architecture Delta

Before：用户导入 URL/文本 → Ingest → Observation → Analysis/Evidence/Assessment；freshness 仅重算已有观察。

After：用户关注来源 → MySQL 权威周期调度 → 既有 Stream 上的发现与抓取任务 → 同一 Ingest → 同一分析链 → Radar/Notification。

- WatchTarget 的归属、启停、间隔、下一次时间、调度版本保存在 MySQL。
- Scheduler 在现有 Worker 进程内运行；通知扫描也是同一进程的有界循环。没有 Scheduler/Crawler/Notification Service。
- PostingDiscoverer / PostingFetcher 为新增能力接口；旧 fetch-only Adapter 保留。
- DailyDigest、ClosingJobs、RecentChanges 为 SQL/规则 read models，不是新的事实表。
- UserJobPreference 独立于 Application；NONE / SAVED / IGNORED 不进入 FSM。
- Notification 持久化在 MySQL，以唯一键承担最终去重。
- Analysis 仍是唯一主要 RPC 业务边界。发现、抓取、调度、通知与摘要均不经过 RPC。

保持不变：MySQL business authority；Redis async/retry/rate limit/cache/ephemeral 边界；HTTP 200 不等于 OPEN；Adapter 不写 Assessment/Eligibility/Ranking；LLM 不成为事实权威；Agent 写操作必须经过独立确认。

## Database Delta

新增 migration：`migrations/004_job_radar.sql`。原 migration 文件不修改。

| 新表 | 作用与关键约束 |
| --- | --- |
| watch_targets | owner/source FK；enabled + next_check_at + id 调度索引；user_id + id 查询索引；JSON body 保存 interval/keyword/schedule_version/检查进度 |
| watch_results | `(watch_id,schedule_version,posting_key,outcome)` PK；引用唯一一次失败或成功 Observation，允许同代失败后成功；重复投递返回已有 Observation |
| watch_postings | `(watch_id,posting_key)` PK；仅保存复查所需 posting reference/元数据，不保存正文、证据、业务状态或排名 |
| watch_runs | `(watch_id,schedule_version)` PK；记录单代 fan-out 的 expected/completed/failed 数量 |
| user_job_preferences | `(user_id,job_id)` PK；user/job FK；disposition CHECK |
| notifications | `(user_id,dedup_key)` UNIQUE；owner/created_at/id Inbox 索引；JSON 通知内容及 read_at |

复用 `completed_tasks` 存储发现/抓取终态 receipt，复用 `outbox` 和 `action_receipts`。Source JSON 新增可选 `adapter`、`tenant`、`rate_limit`；旧记录无需改写。任务 JSON 新增可选 watch_id、schedule_version、posting。

迁移使用数据库级命名锁、CREATE TABLE IF NOT EXISTS 和末尾 schema_migrations receipt；中断后可重新执行。实测 fresh install、初始 legacy schema 升级及完整 v0.1 schema → v0.2 升级，重复迁移保留原 IDs、正文与 posting identity。删除 Watch 级联删除其进度/receipt/reference，已经提交的 Job/Observation 历史保留。

## Pipeline Delta

```text
Due WatchTarget
  → transaction: SELECT FOR UPDATE SKIP LOCKED, LIMIT 100
  → schedule_version++, next_check_at, INSERT Outbox(WATCH_CHECK)
  → existing publisher → Redis Stream → Worker
  → PostingDiscoverer
  → transaction: recheck generation, discovery receipt, Outbox(WATCH_FETCH[])
  → same Stream / worker pool / per-posting timeout and retry
  → PostingFetcher
  → transaction: lock Watch, recheck generation, existing ingestTx + watch result receipt
  → SourcePosting → Observation → Outbox(ANALYZE)
  → existing KanaRPC Analysis → validated Evidence → ASSESS
  → current status / Eligibility / Ranking → Radar and notifications
```

WATCH_FETCH 是本轮增加的第二个 task type，用于避免一个列表的多次串行网络请求耗尽单任务 deadline；它使用既有队列、retry、PEL 与 SQL receipt，没有平行抓取数据库或服务。

- WATCH_CHECK identity 是 watch_id + schedule_version 的稳定 hash；WATCH_FETCH 再加入现有 SourceKey。重试保留 identity 与 correlation_id。
- 首次 schedule commit 后即使 publisher 崩溃，未发送 Outbox 仍可恢复；发布可能重复，SQL 负责收敛。
- 启停/更新配置增加 schedule_version；旧 generation 到达、网络请求完成后才过期、删除后的迟到任务均不能更新新状态或写新观察。
- 每个 posting 每代最多持久化一条失败结果和一条成功结果；重试成功可以替代本轮失败成为新的实际观察。同一成功投递重复执行不会追加 Observation。
- 已知 posting 与当前发现结果合并，列表中消失的岗位仍重新 Fetch；消失、404、403、timeout 不被转换为 CLOSED。
- 429 与来源 Lua 限流至少等待一分钟；5xx/网络/timeout 进入原 backoff/retry；401/403、unsupported、schema/capacity 错误在本次任务中不无限重试。
- 保留 v0.1 canonicalization。新增平台观察用 `public-platforms-v1` source parser identity 进入原 ProcessingVersion；手动/旧 HTTP 入口保持原 parser identity。

新增进程指标：watch_checks_scheduled/succeeded/failed、source_fetch_latency、new_jobs_discovered、job_changes_detected、notifications_created/deduplicated。失败计数记录失败尝试，成功计数记录完成的成功 Watch run；计数非业务账本，重启会归零。成功 posting 日志包含 watch_id/source_id/job_id/task_id/correlation_id，不记录 JD 全文或 token。

## Source Adapter Status

最终实网验证日期：2026-09-14。下列 LIVE VERIFIED 均实际完成 Discover → Fetch → Ingest → 从 MySQL 读回 Observation；不是只验证 HTTP 200。普通 CI 不调用这些外部接口。

| Adapter / tenant | IMPLEMENTED | FIXTURE TESTED | LIVE VERIFIED | 最终样本 |
| --- | --- | --- | --- | --- |
| Lever / weride | 是 | 是 | 是 | 发现 17 条；China New Grads 2026 岗位；初次验证时为 18 条，来源数量确有变化 |
| Greenhouse / pingcap | 是 | 是 | 是 | 发现 10 条；Tokyo Account Manager 样本，未宣称该样本覆盖中国应届岗位 |
| SmartRecruiters / Ubisoft2 | 是 | 是 | 是 | 发现 293 条；Shanghai Marketing Manager 样本，未宣称其为应届岗位 |

最终 Observation IDs：Lever `9e95e35d150aebb6b440700396f0cd2d`；Greenhouse `958ef95f222af29b98f63e11c780ddd9`；SmartRecruiters `9fb8925969390e97e74a3acef9d2282f`。这些记录在本轮独立测试库中完成持久化验证，测试库不作为运行部署。

接口依据及来源链接：[Lever](https://github.com/lever/postings-api)、[Greenhouse](https://docs.greenhouse.io/job-board.html)、[SmartRecruiters](https://developers.smartrecruiters.com/docs/endpoints)。企业标识与公开接口未来可能变化；不宣称支持这些平台的所有 tenant。

UNSUPPORTED：未实现的 adapter、非法 tenant、登录/验证码/反爬场景、超过响应或 posting 容量的来源。401/403 明确按 BLOCKED 处理。没有绕过访问限制。

## API Delta

所有新业务路由均复用 JWT ownership。完整字段见 `docs/api.md`。

- Source catalog：GET `/api/sources`。
- Watch：GET/POST `/api/watches`，GET/PUT/DELETE `/api/watches/{id}`；PUT 的 enabled 字段完成启用/暂停，interval/keyword 可更新。
- Radar：GET `/api/radar/digest`、`/api/radar/changes?days=1|7`、`/api/radar/closing?days=3|7|14`。
- Preference：GET `/api/preferences`，PUT `/api/jobs/{id}/preference`。
- Inbox：GET `/api/notifications`，POST `/api/notifications/refresh`、`/api/notifications/{id}/read`。
- 投递快捷操作复用既有 create/transition API 和 expected version；不新增 Application 状态。

摘要 GET 和 Agent read tools 不物化通知、不写 evaluation history。显式 notification refresh 和 Worker 周期循环负责通知写入。超出 Radar 聚合容量返回 HTTP 409 / RADAR_CAPACITY。主摘要提供完整 counts，精简列表以 truncated 明示。

## Agent Delta

新增 reads：get_daily_digest、get_recent_changes、get_closing_jobs、get_watched_sources。

新增 proposal writes：watch_source、unwatch_source。校验字段、范围、owner，创建 Redis PendingAction；确认端点重新检查并在 MySQL 事务内执行，与 action receipt 同时提交。并发确认只得到一个业务结果；另一个用户不能重放该 receipt。模型没有确认工具。

DemoModel 增加“今天有什么值得处理”“最近哪些岗位关闭”“未来三天截止”等工具路由。每日摘要回答由工具返回的 counts 确定性渲染；自由模型文本不覆盖事实。Runtime 核心、输出预算、trace、SSE、会话行为不修改。原五工具 stdio MCP 对外接口不扩大。

## UI Delta

中文首页改为“我的校招雷达”：今日新增、优先投递、7 天内截止、状态变化、本周面试；下方为优先岗位、最近变化、截止岗位、本周安排和新发现岗位。

新增关注源管理、通知收件箱、截止/变化窗口切换、稍后看/忽略及恢复默认。卡片提供稍后看、忽略、准备投递、已投递。保留岗位详情、Evidence Timeline、Eligibility/Ranking、投递、面试、WeakTopic、项目事实与 Agent 页面。首页不是聊天界面。

已在真实本地浏览器中验证：登录后进入首页、查看统计/卡片、收藏状态变化、创建暂停的 Watch、修改检查间隔、通知标为已读、每日雷达问答完成；页面控制台未发现错误。视觉验收使用隔离测试数据，包含明确标记的 synthetic fixtures。

## Tests

以下 Go 验收均设置 `GOWORK=off`，使用已发布 KanaRPC-Go v0.1.0。

| 实际命令 | Exit code | 范围 |
| --- | --- | --- |
| go test -count=1 ./... | 0 | 全部普通回归；真实服务/外网测试按 gate 跳过 |
| go test -race -count=1 ./... | 0 | 普通回归 race |
| go vet ./... | 0 | 全 module |
| go build ./... | 0 | 全 module |
| node --test web/display.test.cjs web/radar.test.cjs | 0 | 12 项前端测试 |
| ./scripts/verify-radar.sh | 0 | 新建隔离库；CAMPUS_INTEGRATION=1；完整 Go suite、真实 MySQL/Redis/etcd/KanaRPC、fresh/legacy/v0.1 upgrade |
| RADAR_RACE=1 ./scripts/verify-radar.sh | 0 | 相同真实服务与 migration 的完整 race 验收 |
| CAMPUS_INTEGRATION=1 CAMPUS_LIVE_SOURCES=1 go test -count=1 -run TestRadarLiveSources -v ./internal/integration | 0 | 三个 adapter 完整实网链路；MYSQL_TEST_DSN 指向隔离库 |
| ./scripts/verify-release-boundary.sh | 0 | published dependency / public facade / build |
| git diff --check | 0 | whitespace 检查 |

验收证据路径在本报告最后列出。常规真实集成中唯一主动跳过的外部来源测试另以 LIVE gate 实际运行；两类迁移 gate 在验证脚本中均启用。

新测试覆盖：并发 Scheduler 单 generation、commit 后发布恢复/重复发布、重复 discovery fan-out、并发抓取重复投递、成功前失败 receipt、暂停/删除/config 修改后的旧任务、429 retry ZSET 调度、timeout/503、永久 unsupported、相同 deadline 多轮去重、deadline 改动及改回的新版本、一次 OPEN→CLOSED 通知、Watch/Notification/Preference/Proposal/receipt 隔离、ignored/终态 application 排除、时区 offset 的 change/interview 查询、Agent grounding/确认、静态资源可访问、完整 Watch → 真实 KanaRPC → Assessment → Notification。

初次对已有 campustrace_test 跑原基线，TestRunningWorkerOutboxRPC 因历史任务积压未在既定时限处理到本轮任务。未扩大 timeout 或 skip；增加独立临时库验证脚本，并清理新 Radar fixture 的 Watch/Outbox，避免后续旧 Worker 测试误消费测试来源任务。独立库上的旧核心与新功能测试全部通过。

## Compatibility

- 原 v0.1 API、业务表与字段、Observation/Evidence/Assessment 链、canonicalization、Eligibility/GoFit、Ranking CAS、Application FSM 保持。
- Ingest 仅提取同事务内部 helper，手动与自动入口共享原实现；evaluation 计算提取共享 helper，Radar 不复制一套评分规则。
- 旧 Source.Adapter 方法签名不变；新增 discovery/fetch capabilities 为附加接口。泛用/manual 来源仍可以只有 Fetch。
- Source JSON 可选字段、task JSON 可选字段、migration 004 是增量兼容；任务 payload_version 仍为 1。
- Worker 协议有部署兼容限制：旧 Worker 会拒绝 WATCH_CHECK/WATCH_FETCH，必须停止旧消费者、迁移并整体升级后再启用 Watch；不支持混跑。
- KanaRPC module、sibling 仓库及 RPC facade 均未修改。未增加 Go/npm 依赖。

## Existing Core Regression Result

全部通过：Observation/Evidence/Assessment、Processing Generation、Eligibility、GoFit、Ranking Input CAS、Application FSM、Outbox、Stream/PEL、Retry/DLQ、Lua failure recovery、poison isolation、Agent grounding/output budget/write confirmation、SSE、MCP、RAG owner isolation、KanaRPC integration。旧测试保留，新测试使用独立 fixture 清理，不通过弱化断言来获得通过结果。

## Known Limitations

1. 这是三个公开平台适配器的有限覆盖，未完成中国所有企业秋招覆盖。平台全职岗位不是自动认定的校招岗位；当前保守 parser 未识别的毕业届别、学历、岗位类型、申请入口或截止信息仍保持 UNKNOWN/待核验。发现接口的 apply URL 和 HTTP 200 不会被人为补成 OPEN。
2. 仅维护者登记 adapter/tenant/trust；没有面向用户的任意 URL 官方来源创建能力或来源真实性自动认证。Watch 只修改间隔、关键词、enabled，更换来源需另建 Watch。
3. 每用户 100 Watch；每 Watch 500 个 posting reference；响应 1 MiB、正文 60,000 bytes。Radar 聚合上限为 500 个可见 Job、500 个未完成 Interview、10,000 个窗口内 changes。超限明确拒绝，尚无全量分页聚合。
4. 摘要计数完整但岗位各仅前 5 条，变化/面试前 10 条；feed/Inbox 最近 100 条。没有 Inbox 历史翻页、批量已读或通知偏好设置。
5. 时间窗口为滚动 24h/7d、3/7/14 天；“本周”实际为未来 7 天。推荐与截止默认排除已进入 APPLIED 或后续阶段的投递。新优先岗位提醒门槛为当前排序分 ≥70。
6. Scheduler 每分钟最多分配 100 个 due Watch；通知每分钟轮转 10 个用户。吞吐及延迟没有生产规模 SLA。通知补扫窗口有限，停机超过 7 天不会追补全部旧状态通知；事实历史仍在原表。
7. Deadline 提醒发当前最紧 D7/D3/D1 窗口，不补发已经错过的宽窗口。同值连续观察不重复，修改后及修改再改回可发新版本通知。
8. 已完成/终态旧 generation 的 DLQ redrive 会受 SQL receipt/版本检查抑制；下一次调度或更新设置后的新 generation 才重新检查。外部访问限制不绕过，失败后旧观察按原 freshness 规则变 stale。
9. 新旧历史/receipt/Outbox/notification 没有自动归档，deadline 语义版本依赖保留的观察历史；没有 schema downgrade 或混合版本消费者支持。
10. 实网验证到 Observation，不是对真实岗位资格或开放结论的背书；真实 LLM provider 质量、长期定时运行、外部来源未来可用性和生产规模未验证。原 RAG/MCP/SSE/RPC 运维限制仍在 README 中保留。

## NOT IMPLEMENTED

Kafka、RabbitMQ、Vector DB、Elasticsearch、Kubernetes、Multi-Agent、Planner、新业务微服务、Crawler/Notification/Scheduler Service、自动海投、浏览器自动投递、招聘网站自动登录、验证码破解/反爬绕过、Email/Gmail 同步、Email/Telegram/Push 通知、Cover Letter 系统、复杂 workflow engine、semantic retrieval rewrite 均未实现。

## Changed Files

共 42 个新增/修改文件（均未提交）。

- **cmd**：`cmd/ingest/main.go`, `cmd/seed/main.go`。
- **docs**：`docs/api.md`。
- **documentation**：`PRODUCTIZATION_REPORT.md`, `README.md`, `README_EN.md`。
- **internal/agent**：`internal/agent/grounding.go`, `internal/agent/model.go`, `internal/agent/radar_test.go`, `internal/agent/tools.go`。
- **internal/bootstrap**：`internal/bootstrap/bootstrap.go`。
- **internal/domain**：`internal/domain/model.go`, `internal/domain/radar.go`, `internal/domain/radar_test.go`。
- **internal/integration**：`internal/integration/radar_adversarial_test.go`, `internal/integration/radar_http_test.go`, `internal/integration/radar_live_test.go`, `internal/integration/radar_test.go`。
- **internal/persistence**：`internal/persistence/evaluation.go`, `internal/persistence/jobs.go`, `internal/persistence/radar.go`, `internal/persistence/radar_migration_test.go`, `internal/persistence/store.go`, `internal/persistence/watch.go`, `internal/persistence/workflow.go`。
- **internal/pipeline**：`internal/pipeline/redis.go`, `internal/pipeline/watch.go`, `internal/pipeline/worker.go`。
- **internal/source**：`internal/source/discovery.go`, `internal/source/discovery_test.go`。
- **internal/transport**：`internal/transport/http.go`, `internal/transport/radar.go`。
- **migrations**：`migrations/004_job_radar.sql`, `migrations/embed.go`。
- **scripts**：`scripts/verify-radar.sh`。
- **web**：`web/app.js`, `web/display.js`, `web/embed.go`, `web/index.html`, `web/radar.js`, `web/radar.test.cjs`, `web/style.css`。

## Verification Evidence

- 普通 test/race/vet/build/frontend exit codes：`/tmp/campustrace-reviewed-checks.json`，对应日志见其中路径。
- 最新完整真实服务集成（含 migration gates）：`/tmp/campustrace-verify-20260914044444_8666/integration.log`，exit 0。
- 最新完整真实服务 race（含 migration gates）：`/tmp/campustrace-verify-20260914043505_8426/integration.log`，exit 0。
- 实网适配器：`/tmp/campustrace-final-live.log`。
- KanaRPC dependency boundary：`/tmp/campustrace-final-boundary.log`。
- 初始既有测试库失败证据：`/tmp/campustrace-baseline-integration.log`。
- 全部测试库为本地验证用途；验证脚本成功后只删除本次创建的临时 schema，不清空既有业务或测试库。

验收用临时 API（18090）及浏览器标签已关闭；既有部署未替换。实现停止于未提交的工作区，等待独立审查。
