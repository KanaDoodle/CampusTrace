# M4 实现与验证报告

日期：2026-10-30—31。范围：CampusTrace + KanaRPC-Go 当前工作区，保留先前 M1–M3 和搜索修改。实现步骤与运行入口见 [实施说明](../../rpc-agent-m4.md)。

后续状态见 [2026-10-31 收尾报告](closeout/report.md)：下列迁移与隔离例外已有复现和修复，SDK 已增加无需 workspace 的版本化接入。本文件保留首次 M4 验收的原始结果，不将历史失败改写成通过。

## 交付结果

已打通「现有固定只读任务 → 核对实际脱敏输入 → 独立流式说明 → SQL 快照展示 / 用户取消 / 任务恢复」。原 grounded 结果保留，生成文本没有执行工具或修改业务的权限。两个项目使用真实本地进程、TCP、HTTP、MySQL、Redis 和 Chromium；推理后端完全是 **fixture-no-model**。

这不是自由问答和所有匹配任务的全面流式改造，也没有引入多 Agent/A2A 协议。M4 优先复用已有任务、权限和确认边界。默认发布依赖仍是 KanaRPC-Go v0.1.0，新接口通过 `kanarpc_stream` + 本地 workspace 启用，未冒充已发布 SDK。

## 分阶段验证与对照

| 阶段 | 已实现 | 实际验证 / 对照 |
| --- | --- | --- |
| 1 持久任务 | 来源身份、创建/尝试 nonce、owner、租约、最新草稿与取消围栏 | 隔离 SQL/Redis；10 项 narration 集成测试 race 重复 3 次；取消后迟到完成、跨 API 取消、正常关闭、未知结果恢复、资料变化和遗忘；另有 2 项输出安全单元测试 |
| 2 RPC 接入 | 固定模型、35s 预算、文本/usage、HMAC 用户 salt、拒绝 tool_calls、不展示 reasoning | 真实 TCP + HTTP lab；相同依据不同 owner 得到不同 salt；非法模型事件与缺失汇总失败；默认构建配置关闭/拒绝测试 |
| 3 页面 | 预览弹窗、文本快照、只读重连、刷新恢复、停止生成、未知重试提示 | 444 项 Node 测试通过；真实 Chromium 核对/暂不生成调用数不变、完成前有文本、刷新无额外调用、HTML 不执行、取消传到上游 |
| 4 端到端 | API→gateway→worker→HTTP fixture；强杀/重启、权限与防重复 | 新建独占容器；完整链路和浏览器通过；强杀后草稿可读、不自动重发，明确认可未知结果后才新增一次调用 |

端到端固定轨迹合计 **7 次 fixture 请求，3 次完成、4 次取消、2 个用户缓存作用域**。普通完成、取消、强杀后的已确认重试、浏览器重载/取消均按预期改变计数；预览、取消预览、重复 nonce、GET/SSE 及重新附着均不额外调用。见 [进程结果](process/result.json)、[上游计数](process/fixture-final.json)、[浏览器结果](process/browser-result.json)。这些数值证明调用边界，不表示真实吞吐、缓存命中或 GPU 性能。

| 操作 | 对照结果 |
| --- | --- |
| 未认证读取 / 他人任务读取、流订阅、预览、创建、恢复、取消 | 401 / 404；provider 调用数不变 |
| Cookie 写入伪造 Origin / 注入 model_config / confirm=false | 403 / 400 / 400；provider 调用数不变 |
| 同 nonce 并发确认 4 次，及单元集成中的 8 次重放 | 同一 SQL 子任务，仅一次推理 |
| 重放任意已记录的尝试 nonce | 读取当前状态，不重放推理 |
| 关闭观察连接、刷新、服务重启后重新订阅 | 从 SQL 读取，继续观察原任务；不生成 |
| 结果未知的恢复未勾选 / 显式认可后用新 nonce | 拒绝 / 新增一次调用；不是无损续写 |
| 上游忽略 ctx 并迟到返回 | SQL 取消/遗忘/租约围栏拒绝迟到完成 |
| 登出撤销会话，已有 SSE 仍打开 | 每轮快照前复核身份，停止观察；后台任务不重放、不因观察者退出而取消 |

强杀恢复用例在**独占测试数据库**中人为提前租约到期；这验证租约到期分支，不冒充等待真实 60 秒。跨 API 取消及迟到写入由集成测试覆盖；完整进程验收使用一个 API 实例重启和静态 gateway 地址，etcd 发现由 RPC 回归测试覆盖。

## 测试确认并修复的问题

以下都是本轮新增实现中先复现、后修复的问题，并非宣称发现了历史生产故障。

| 问题与最小复现 | 根因 | 修复与复测 |
| --- | --- | --- |
| 发送前 1ns 超时被永久标为 STALE | 初次 Preview 的任意错误都被当成资料变化 | 区分来源失效与临时存储/取消错误；后者 PAUSED + NOT_DISPATCHED，可显式恢复；race 三次通过 |
| 限流原因被覆盖为 INTERRUPTED | defer 无条件覆盖已设置的失败原因 | 保留具体失败原因；未调用模型，保持 NOT_DISPATCHED；race 三次通过 |
| CRLF 跨网络片段时不能组成 SSE 帧 | 仅在单个片段内规范化换行 | 拼接缓冲后规范化 CRLF；分片/UTF-8/重连测试通过 |
| 取消/失效的历史卡片重新展示旧 draft；流结束后摘要仍可能是 RUNNING | 卡片没有按终态隐藏草稿，终态未刷新列表 | CANCELLED/STALE 隐藏草稿；流终态刷新记录，输入和预览弹窗位于稳定容器；Node 与浏览器复测通过 |
| 撤销 Cookie 会话后旧 SSE 继续读取 | 只在 HTTP 建连时鉴权 | 每次快照循环重新验证会话/JWT 与 owner，认证错误直接结束观察；750ms 复现测试先失败，修复后约 100ms 结束，race 三次通过 |
| 脱敏替换后 3KB 原文扩展到 51KB，越过快照上限 | 只限制原始生成文本，没有限制脱敏后的文本 | 每次草稿和最终答案都检查脱敏后 32KiB 上限，超限明确失败，不发布截断结论；集成及单元 race 三次通过 |
| 在途编辑记忆后任务停留 RUNNING，仍占用租约 | 新记忆版本拒绝部分检查点，清理继续使用旧版本而被拒绝 | 清理时核对最新记忆版本，按本次 token 将子任务置 STALE、清空答案/草稿并释放租约；取消/遗忘的 token 围栏继续有效；race 三次通过 |

原始失败：[超时与限流](ct-m4-predispatch-before.txt)、[CRLF](ct-m4-sse-parser-before.txt)、[旧草稿](ct-m4-cancel-history-before.txt)、[会话撤销](ct-m4-revocation-before.txt)。复测见 `ct-m4-stage1-final-race.txt`、`ct-m4-enabled-config.txt`、`ct-m4-disabled-config.txt`、`node-final.txt.gz` 和进程结果。

输出与记忆版本边界的失败复现见 [脱敏扩展](ct-m4-redaction-before.txt)、[在途编辑记忆](ct-m4-memory-edit-before.txt)。

## 回归结果与保留的例外

完整运行统计在 [go-results.json](go-results.json)；最终版本使用 `ct-m4-ordinary-delivery`、`ct-m4-race-delivery`、`ct-m4-default-delivery` 三组记录。对应原始 `go test -json` 日志压缩保留，可独立统计。[M4 源码哈希](source-manifest.json)限定本轮文件，不覆盖同时存在的其他工作区进展。pass 数包含 Go 子测试，不是生产用户/请求数量。

| 检查 | 结果 |
| --- | --- |
| CampusTrace `GOWORK=off go test ./...` | 1915 pass / 235 SKIP；默认发布依赖构建与测试通过，集成、live 等按环境 SKIP |
| CampusTrace 启用 tag，隔离依赖，普通与 race | 两轮均 2126 pass / 40 SKIP / 0 fail；每轮使用全新 schema，显式排除固定 Compose 恢复测试 |
| KanaRPC-Go 普通 | 202 pass / 3 真实 vLLM 测试 SKIP |
| KanaRPC-Go `go test -race -p=1 ./...` | 202 pass / 3 真实 vLLM 测试 SKIP |
| 两个项目 `go vet` | 通过 |
| Node | 444 pass / 0 fail |
| 真实服务入口的本地 guard 测试 | 5 pass；覆盖未确认不生成、hash 改变拒绝、fixture 拒绝、重用 nonce、认证请求不跟随重定向 |
| M1 / M2 / M3 独立进程脚本回归 | 全部通过；M3 的缓存事件和延时仍为模拟 |
| 真实 vLLM / GPU / 模型质量 | **未执行**；缺少服务。已提供先预览、再按 hash 确认的可运行入口 |

没有隐藏首次失败或修改不相关业务代码来让总数变绿：

1. 未过滤的第一次普通全量运行有两项失败：`TestRecoveryRoundTripAndImportIsolation` 硬编码使用项目名 `campustrace` 的 Compose MySQL，本轮只有独占临时容器；未触碰既有业务容器。后续仅显式 `-skip '^TestRecoveryRoundTripAndImportIsolation$'`。`TestRepairLegacyMigration` 在调用 Ingest 时返回 business validation；从 M4 前备份展开到另一个目录、全新迁移库，单独运行同样失败，见 [基线证据](ct-m4-baseline-migration.txt)。这项未在 M4 中修复，后续不提供 `MYSQL_MIGRATION_TEST_DSN`，由原测试明确 SKIP。
2. 复用已经跑过整个 suite 的测试库时，有三个既有岗位目录测试失败（预览数量、摘要新岗位数量、忽略后的窗口）。使用每轮全新 schema 后，普通/race 全部通过。说明现有 suite 的数据库清理与重复运行隔离仍需改进；本轮没有清空或操作个人业务库。
3. 并发包 race 下 `TestGatewayRepeatedBudgetRejectionDoesNotOpenBreaker` 一次返回 `slo_exceeded` 而非预期 `slo_unachievable`，日志表明分词已耗尽 100ms 首文本预算。串行包 `-p=1` 的完整 race 通过。宿主负载影响是推断，未宣称修复了该测试的时序敏感性，也未放宽原断言。

最终检查没有改动 RPC wire format 或调度算法，也未做本轮无关重构；因此未额外开展调度 benchmark/fuzz，已有协议和调度测试继续跑。没有用 CPU fixture 延时制造推理收益。

复测最小用例（仅指向自己新建的临时 SQL/Redis，不使用个人业务库；workspace 建立方式见实施说明）：

```sh
GOWORK="$TASK_WORK/go.work" CAMPUS_INTEGRATION=1 \
  MYSQL_TEST_DSN='USER:PASSWORD@tcp(127.0.0.1:MYSQL_PORT)/EMPTY_DISPOSABLE_DB?parseTime=true&loc=UTC' \
  REDIS_ADDR='127.0.0.1:REDIS_PORT' \
  go test -tags kanarpc_stream -race -count=3 -run '^TestNarration' -v ./internal/integration ./internal/agent
GOWORK="$TASK_WORK/go.work" go test -tags kanarpc_stream -race -count=3 ./internal/inferencebridge
node --test web/agent_narration.test.cjs
python3 -m unittest discover -s scripts -p 'test_rpc_agent_live.py' -v
```

当前修复版本的预期是通过；各条失败日志保留修复前的实际运行结果。完整隔离进程验收直接运行 `scripts/verify-rpc-agent.py`，无需手工配置业务库。

## 尚未解决的设计风险

- 外部模型没有持久调用回执/幂等保障。DISPATCHED 后断开包括“尚未真正发送”的保守未知情况；重新生成可能重复计费，用户确认不能使外部副作用 exactly-once。usage 仅记录当前成功尝试，并非完整账单。
- 恢复的是最新 SQL 快照，不是每 token 日志或引擎 KV；中断后重新生成会清除该次草稿。最后一次数据库写失败时只能靠租约恢复，不保证一定保存终态。
- 35s 预算、512 token 和 256 rune 脱敏尾缓冲是保守初值。短回答可能只在结束时显示；脱敏为 best effort，不是通用 DLP。来源开始前和发布完成前核对，资料修改与在途草稿之间仍有时间窗口。
- SSE 每 100ms 查 SQL，Cookie 会话同时查 Redis；没有单用户订阅数上限，断连风暴仍可增加存储负载。每次观察最多 15s 后只读重连。
- RPC/admin 管理面仍依赖可信私网，缺少 TLS/服务身份；HMAC salt 是用户键空间隔离，不提供传输授权或模型隐私证明。gateway 配额仍有单进程边界，固定模型和 worker 池沿用 M2/M3 限制。
- 付费匹配已有独立 SQL 资源治理，本轮说明任务没有与它合并统一计费账本。普通自由问答、现有 MCP 读能力和写动作确认没有扩展权限。
- 新 SDK 未发布，tag/workspace 是开发接入边界；需要发布经过审核的 RPC 版本后才能将此适配默认启用。

下一步最值得做的三个改进：

1. 接入真实 vLLM，用实际 CampusTrace 脱敏样本测试质量、首文本、取消后的引擎释放、真实 usage 和 KV 提示；先建立强基线再评估收益。
2. 补齐跨服务 attempt ID、持久回执/对账与统一预算，降低未知结果和重复计费的运维成本，保留明确人工重试边界。
3. 补服务身份/TLS、管理面授权和订阅配额，并在实测需要时将 SQL 高频快照轮询改为有界事件通知与持久快照结合；验证多 API/gateway 扩展。

![实际 Chromium 中的本地无模型说明](process/browser-completed.png)
