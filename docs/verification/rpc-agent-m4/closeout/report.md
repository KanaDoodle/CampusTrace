# M4 遗留收尾：迁移、测试隔离与 SDK 接入

日期：2026-10-31。先前 M4 报告与原始失败保留在 [历史报告](../report.md)。本轮先复现迁移/隔离/恢复失败，再修复和回归，最后验证版本化 SDK。保留同时进行的其他工作区修改，没有提交、打 tag、推送或公开发布。

验证范围已固定：收尾后工作区又出现并行的 Kafka 代码和依赖变更。下面的完整测试结论对应 [M4 验证源码快照](verification-snapshot.tgz)及其[文件哈希](verification-snapshot.json)，不覆盖这些后续 Kafka 改动；它们已原样保留。最终候选的[实际构建记录](archived-sdk-build.json)也已保存。

## 完成情况

- 迁移测试使用符合历史写入规则的 ID，重复迁移、原文保留、旧手工数据隔离和原 posting/job 复用均通过。没有改动生产迁移 SQL 或放宽搜索文档校验。
- 每个测试作用域有独立数据库和限制在该库的账户；同一用例内多个 setup 共享数据。额外公开来源、额外用户、子进程和重复测试不再污染后续用例。
- 恢复测试可连接显式指定的独占容器和实际端口，真实运行 dump/import、跨库写入拒绝、外键/未来版本拒绝以及保留副本检查，无需固定 Compose 项目。
- SDK 已有候选打包、独立消费者、关闭 workspace 的构建入口和公开版本验证入口。最终产物是 **v0.2.0-rc.4 本地未发布候选**；公开版本当前查询为 unknown revision。
- **真实 vLLM / GPU / 模型质量仍未验证**。所有模型响应、usage 和缓存提示均来自明确标注的 fixture。

## 复现、根因与修改

| 问题 | 修复前实际复现 | 根因与修改 |
| --- | --- | --- |
| 旧迁移用例在 Ingest 返回 business validation | [迁移失败](logs/legacy-before.txt) | 用例把 `OFFICIAL` 等标签直接当作 job ID；后续搜索投影要求 32 位十六进制 ID。改为 d.ID，标签仍放在名称字段；保留旧主键、原文、私有隔离和重复迁移断言，并确认不产生重复公司。[修复后 race](logs/legacy-valid-ids-after.txt) |
| 额外公开来源留下跨用例数据 | [新增最小复现](logs/isolation-before.txt) | 旧清理只追踪主 source/owner；额外全局 source/job 没有 owner。增加内部 sqlfixture，随机创建数据库/限制账户，作用域结束整体清理；子测试不能读取父作用域数据库，父记录保留。同一测试多次 setup 仍共享，子进程继承本测试 DSN。[重复 race](logs/isolation-migration-after-race.txt) |
| 备份恢复仅能跑固定 Compose | [恢复失败](logs/recovery-before.txt) | 测试调用硬编码 Compose 项目，合成配置还固定 13306。测试适配器将 MySQL exec 定向到 `MYSQL_TEST_CONTAINER`，使用 DSN 实际端口；生产 launcher 命令不变。[完整恢复 race 两轮](logs/recovery-after.txt) |
| 预测拒绝用例偶发变成已耗尽预算 | [历史 RPC 并发 race 失败](../rpc-m4-full-race.jsonl.gz) | 100ms 同时承担本机分词时间和被测目标。预测拒绝改用 2s，6000/1000 > 6s 的预测仍严格不能达标，保留原 `slo_unachievable` 断言；另加 40ms tokenizer / 10ms 预算用例，严格检查 `slo_exceeded` 和健康连接未被熔断。两种拒绝各重复 24 次后正常调用成功，[race 重复十轮](logs/rpc-budget-after.txt)。没有修改生产预算或熔断算法 |
| 首次收尾 race 中 429 分支未到达 provider | [原始全量失败](logs/first-race-failed.jsonl.gz) | 100ms 整体执行预算在 SQL 准备阶段耗尽，model_calls=0，测试期望 429。只改测试预算为 2s；429 和 timeout 均必须实际进入 provider，timeout fixture 等待 ctx.Done，调用数仍必须恰好 1。[三轮 race](logs/scheduler-rejection-after.txt) |
| SDK 独立消费者首次 race 的普通评测变成 TIMEOUT | [原始消费者失败](logs/eval-scheduling-before.jsonl.gz) | 合成评测给所有案例统一 30ms/3ms 窗口，普通参数检查被调度停顿误分类。普通评测用充足预算，只有故意等待取消的模型/工具 fixture 保留短超时；不改在线 Runtime 默认策略。[50 轮 race](logs/eval-isolation-after.txt.gz) |

迁移归一化路径也做过负对照：只调整公司名归一化仍失败，见 [负对照](logs/legacy-normalization-negative-control.txt)。没有把这项假设写成已确认根因。

## 测试隔离的具体边界

`internal/testsupport/sqlfixture` 要求显式 `MYSQL_TEST_ADMIN_DSN`，与模板连接指向同一服务器。它只创建随机 `ct_test_*` 数据库和账户，授予该库权限，连接和子进程关闭后清理。本轮没有重置模板数据库。`MYSQL_TEST_DSN` 按测试作用域传递给子进程；因此这些测试暂不使用 t.Parallel。

独立入口：

```sh
python3 scripts/verify-integration-isolation.py --output /tmp/ct-closeout --count 2 --shuffle 60431
python3 scripts/verify-integration-isolation.py --output /tmp/ct-closeout-race --race --count 2 \
  --sdk-candidate docs/verification/rpc-agent-m4/closeout/sdk-candidate
```

入口创建本次独占 MySQL/Redis/etcd，包含一个不应修改的模板哨兵，实际执行迁移和恢复测试，不传 `-skip`。连续测试后核对测试库/账户为零，模板仍只有原哨兵表，再清理本次容器。现有 radar/journey/adversarial/Makefile 测试入口已补管理员配置。

[重复 race 清理结果](race-repeat-cleanup.json)、[最终 rc.4 清理结果](rc4-race-cleanup.json)均为：遗留测试库 **0**、遗留测试账户 **0**、模板哨兵保留。迁移测试现在每轮创建空库，支持 `-count`，不再依赖用户手工换库。

## SDK 正式接入方式

[接入说明](../../../stream-sdk.md)给出完整命令；[本轮候选](sdk-candidate/manifest.json)及 proxy 文件一起保留，可直接使用：

```sh
python3 scripts/build-stream-sdk.py \
  --candidate docs/verification/rpc-agent-m4/closeout/sdk-candidate \
  --output /tmp/ct-stream-api
```

结果是独立 `campustrace-stream.mod/.sum` 和 API 二进制，根 go.mod/go.sum 保持不变。新 SDK 通过版本化模块下载，`GOWORK=off`、无 replace，实际源目录位于本次私有 module cache。构建 tag 只控制可选流式功能，已不要求相邻源码。

独立消费者不是 git clean clone：本轮从当前源码生成不带 `.git`、`go.work` 和相邻 RPC 仓库的快照。[上下文检查](sdk-context.json)确认三者均不存在；[SDK 验收](sdk-result.json)记录构建、76 项消费者 race、完整进程和真实 Chromium 流程通过。API、worker、gateway 均从同一个模块候选构建。

最终模块内容校验：`h1:9wOcoXf47wmXASAkt5iCQ9P0ivH2kFL80vmwOUBzku8=`。候选文件集、ZIP/.mod/.info 的 SHA-256 见 manifest；[历史公开 v0.1.0 内容校验对照](sdk-published-format-equivalence.json)证明打包方式没有因为只挑 Go 文件而改变标准模块内容。

公开发布仍未完成：

- 当前候选 `source_dirty=true`，不是干净发布提交。`--require-clean` 在本工作区实际拒绝，见 [日志](logs/dirty-release-refused.log)。
- 公开版本入口实际得到 [rc.4 unknown revision](logs/public-rc4-check.log)，没有回退到 latest/旧 SDK，也没有生成 API。
- 公开发布及成功的 proxy/checksum 验证尚未完成；GitHub 手动 SDK CI 未执行。维护者需要先审核并提交 RPC 完整源码，从干净提交重新打包，再发布并核对同版本 h1。
- 默认构建继续使用已发布 v0.1.0；[默认依赖边界检查](logs/boundary-final.log)通过。未把不存在的版本写进根 go.mod。

## 回归结果

下列 pass 数包含 Go 子测试；完整统计、SKIP 清单和失败记录见 [go-results.json](go-results.json)。中间失败保留，不会从总结果里删除。

| 验证 | 实际结果 |
| --- | --- |
| CampusTrace 普通，count=2、shuffle=60431 | **4262 pass / 76 SKIP / 0 fail**，每轮迁移与恢复实际执行 |
| 完整独占入口 race，count=2、shuffle=60433 | **4262 pass / 76 SKIP / 0 fail**；中间 SDK 候选；库/账户清理和模板哨兵检查通过 |
| 最终 rc.4 SDK，完整入口 race、shuffle=60434 | **2131 pass / 38 SKIP / 0 fail**；库/账户/哨兵检查通过 |
| 额外启用多 API 进程门控 | **5 pass / 0 fail**，覆盖共享容量、崩溃和预算竞争 |
| 默认发布依赖的 Go 全量 | **1915 pass / 236 SKIP / 0 fail**；集成依赖未启用的检查明确 SKIP |
| RPC 普通全量 | **203 pass / 3 真实 vLLM SKIP** |
| RPC 并发包 race 全量，count=3 | **609 pass / 9 真实 vLLM SKIP / 0 fail**，不再需要用 -p=1 回避预测拒绝的短时窗 |
| Node | **444 pass / 0 fail** |
| SDK 打包 / 消费保护测试 | **5 + 5 pass**，覆盖不可覆盖版本、脏发布拒绝、符号链接、校验损坏、越界版本、伪造发布标签、workspace 排除 |
| 真实服务入口 guard | **5 pass**，仍使用无模型测试服务 |
| Go vet | 两项目普通及流式 profile 通过 |
| M1/M2/M3 独立进程回归 | 全部通过；M3 仍是无模型缓存事件模拟 |
| 最终 SDK 完整链路与浏览器 | [PASS](process/result.json)：7 次 fixture 请求，3 完成/4 取消、2 个用户缓存作用域；审阅、重连、取消、强杀恢复、防重复、owner/CSRF、HTML 文本显示通过 |

仍有 38 项可选环境检查跳过，包括外部招聘来源、OpenSearch、沙箱和部分未启用的对抗场景；另外单独运行了多 API 模型治理进程用例。本轮没有将它们统称为已通过。真实 vLLM 的 3 项 RPC 测试保持跳过。

## 剩余风险与发布前事项

- 独立建库/迁移增加测试开销；未把测试时间或 fixture 延时当作推理性能收益，也未为速度牺牲隔离。
- 测试管理员只用于显式启用的独占测试服务。测试暂不支持 t.Parallel；若需要并行，应改用显式子进程 DSN 参数，避免测试内环境变量共享。
- SDK 尚无公开流式版本。候选的清单和 h1证明内容一致性，不是公开发布、签名审计或供应链安全证明。
- 外部推理的未知副作用/可能重复计费、最后检查点失败、快照轮询开销、RPC/admin 身份与 TLS、跨进程统一预算等生产设计风险沿用原报告，本轮未扩展实现。
- **真实 vLLM、GPU、真实缓存命中、引擎取消后的计算释放和模型质量继续待验。** 可运行入口已保留，模拟结果不能替代它们。

![最终候选的真实 Chromium 本地验收](process/browser-completed.png)
