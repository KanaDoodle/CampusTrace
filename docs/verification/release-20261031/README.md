# 发布收尾验收 · 2026-10-31

本轮固定已有实现、串联演示入口并补齐公开回归；同时修复 CI 验收发现的候选索引并发发布问题，不再增加产品功能。业务闭环为：导入岗位 → 搜索 → 公司内选择与计划 → 冻结投递材料 → 面试通知与改期 → 复盘 → 学习笔记 → 下一次准备。

## 当前源码与本地结果

运行时代码清单在 [source-files.json](source-files.json)，其 SHA-256 指纹为 `bc18a80cddd8103ae06e93f7b6d4105fa4211ccdb5a6f32698a53e28a7b999b3`。[技术要求修复的原验收源码](../technology-requirements-20261031/README.md)保留不变；本次另外修复了候选索引发布的 CAS 竞态，运行时增量仅为 `internal/searchservice/rebuild.go` 与对应回归测试。指纹覆盖生产源码、测试、前端、迁移及模块/镜像构建入口，不包含文档或验收脚本；本轮验收入口另列在 harness-files.json。

| 验收 | 结果与范围 |
|---|---|
| 完整 HTTP 业务流程 | 7 组检查通过；核对材料冻结、重复请求、过期确认、复盘笔记与下一次准备 |
| 隔离集成与 race | 617 个顶层测试通过，24 个跳过，0 失败；使用独占 MySQL/Redis/etcd/OpenSearch，包含进程故障验收 |
| Go 发布检查 | 全量普通 race 回归 25 个包通过，vet/build 通过 |
| 前端行为回归 | 453 项通过，0 失败、0 跳过 |
| Chromium + 本地模型桩 | 17 组浏览器检查通过，无页面错误；含刷新重连、取消、文本渲染、复盘联动及保存响应丢失后的去重 |
| 性能验收脚本契约 | Python 16 项、Node 6 项通过；检查报告拒绝不完整样本/不等条件及查询响应权限约束 |
| 发布依赖边界 | `GOWORK=off`，固定公开 KanaRPC-Go v0.1.0；独立 Search 不依赖 Core 存储驱动 |
| 原始证据完整性 | [verify.py](verify.py)核对归档哈希、通过/跳过数及运行时源码指纹 |

24 个跳过项包括 19 个外站实时读取、3 个 Kafka 专项，以及 2 个只由父测试启动的子进程入口。此本地集成结果不包含真实招聘网站或 Kafka 验收；Kafka 的独立消费、恢复及完整栈由专门 CI 作业执行。临时数据库与账号在本地验收后均为 0，既有模板库保护检查通过。

[summary.json](summary.json)提供机器可读结果；[evidence.tar.gz](evidence.tar.gz)保留原始测试输出、浏览器结果及流程记录，[raw-file-sha256.json](raw-file-sha256.json)列出逐文件校验值。所有账号、岗位、模型响应均为合成数据，流程与浏览器验收未调用外部模型。

## 复现

```sh
export GOWORK=off
node --test web/*.test.cjs
make performance-test
sh scripts/verify-release-boundary.sh
sh scripts/journey-local.sh smoke
python3 scripts/verify-integration-isolation.py --race --search --adversarial --count 1 --output bin/release-integration
python3 docs/verification/release-20261031/verify.py --check-worktree .
```

隔离脚本需要 Docker，下载镜像和启动依赖需要时间；已占用演示端口时设置 `JOURNEY_HTTP_PORT`。浏览器测试通过 `scripts/verify-agent-explanation.py` 显式传入本机 Playwright 模块与 Chromium 路径，见该脚本的 `--help`。证据验证只读取归档与源码，不会调用模型、外站或业务数据库。

## 公开回归入口

以 [GitHub Actions](https://github.com/KanaDoodle/CampusTrace/actions) 对应提交的实际状态为准，归档的本地 PASS 不代替远程结果。

- [基础 CI](../../../.github/workflows/ci.yml)：公开依赖构建、vet、Go/race、前端、性能契约、全新 Compose 启停与完整 HTTP 流程。
- [性能回归](../../../.github/workflows/performance.yml)：同 runner 的参考版/候选版 k6 对照，保留失败和原始数据。
- [Search 故障](../../../.github/workflows/search-resilience.yml)、[在线重建](../../../.github/workflows/search-online-rebuild.yml)、[Kafka 事件](../../../.github/workflows/job-events.yml)：各自独立启动合成依赖、验证恢复并上传诊断。

基础 CI 现在无论成功或失败都保留流程结果与诊断；带 `tee` 的故障验收显式启用 `pipefail`，避免日志管道掩盖失败。

## 面向审阅的三个入口

1. [事件一致性](../../job-events.md)：事务 Outbox、独立消费组、幂等与确认丢失、代际恢复。
2. [搜索故障隔离](../../search-resilience.md)与[性能诊断](../search-diagnosis-20261031/README.md)：权限复核、有界回退、Trace/EXPLAIN/pprof；[恢复探测报告](../search-recovery-20261031/README.md)保留全部分位数及负面结果。
3. [模型执行控制](../../model-control.md)：公平许可、预算、执行账本、UNKNOWN、迟到结果隔离；[申请闭环](../../application-journey.md)展示这些能力如何服务实际流程。

历史压测数字只对应各报告归档的源码与合成负载；本次没有重新进行 1,000/5,000/10,000 岗位压测。测试通过数不代表真实模型质量、生产容量、线上 SLA、用户节省时间或多节点高可用。浏览器模型桩测试没有验证云模型、vLLM 或 GPU 推理；故障测试中的租约过期由隔离库注入，不是自然等待时间测量。

## 首次远程失败及修复

[首次性能 CI](https://github.com/KanaDoodle/CampusTrace/actions/runs/41210993795)在重建启动阶段失败，完整归档保留于 evidence.tar.gz 内的 `ci-first-failure.zip`。候选版尚未完成四阶段对照，这次失败不能当作延迟回归或性能通过。重建请求和 Worker 的状态读取会同时准备同一个候选索引，后完成者的 CAS 写入返回 409，导致 CLI 报错；对外查询仍然保留原索引。

新增确定性回归先复现了 4 个阶段均误报冲突，再验证修复：CAS 失败后只读取持久结果，要求操作 ID、来源代际、目标物理 UUID、候选索引名和模式一致，并只接受 BUILDING/CUTTING/COMMITTED/DONE。操作改变、来源或 UUID 改变、目标未发布、取消仍返回冲突；不覆盖已经写入的 dataset 或切换状态。归档保留 `rebuild-cas-before.txt` 与 `rebuild-cas-after.txt`，原性能阈值和同条件要求未修改。

Search 修复后重新执行完整 Go race、vet、build 和隔离集成。前端、业务 API、演示脚本和浏览器脚本没有变化，沿用同一源码哈希的 453 项前端、7 组 HTTP 流程及 17 组浏览器结果；首次 616 项集成记录另留在 `initial-integration/`。
