# 发布收尾验收 · 2026-10-31

本轮固定已有实现、串联演示入口并补齐公开回归，不再增加产品功能。业务闭环为：导入岗位 → 搜索 → 公司内选择与计划 → 冻结投递材料 → 面试通知与改期 → 复盘 → 学习笔记 → 下一次准备。

## 当前源码与本地结果

运行时代码清单在 [source-files.json](source-files.json)，其 SHA-256 指纹为 `b5a3a6b2f6690c7be7c7e4026a0695900fc5d3f003c1e87a59b4b90ea7791c43`，与[技术要求修复的最终验收源码](../technology-requirements-20261031/README.md)一致。指纹覆盖生产源码、测试、前端、迁移及模块/镜像构建入口，不包含文档或验收脚本；本轮验收入口另列在 harness-files.json。

| 验收 | 结果与范围 |
|---|---|
| 完整 HTTP 业务流程 | 7 组检查通过；核对材料冻结、重复请求、过期确认、复盘笔记与下一次准备 |
| 隔离集成与 race | 616 个顶层测试通过，24 个跳过，0 失败；使用独占 MySQL/Redis/etcd/OpenSearch，包含进程故障验收 |
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
