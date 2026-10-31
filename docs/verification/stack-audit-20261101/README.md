# 技术栈维护验收

本轮完成构建工具链、CI、Kafka 配置和浏览器读取依赖的调整，审查结论见[技术栈维护](../../stack-maintenance.md)。

| 验证 | 结果 |
| --- | --- |
| Go 1.27.3 全量隔离竞态回归 | 609 个顶层测试通过、35 项按条件跳过；其中业务集成 202 项通过 |
| Node.js 26.13.0 容器 | 453 项前端测试通过 |
| Chromium 本地 PDF/DOCX | 5 项检查通过，外部请求为零 |
| Kafka / Search / Notify / Analysis 容器 | 独立停机恢复、Kafka 暂停后任务续跑、重放、异常消息隔离、索引恢复与追踪通过 |
| 构建与依赖 | 最终应用镜像构建通过，二进制元数据确认为 Go 1.27.3；build、vet、公开 KanaRPC / Search 依赖边界通过 |
| CI 与性能脚本 | actionlint 1.7.12 检查通过；16 项 Python、6 项 JavaScript 性能验收契约通过 |

配置缺陷先由无效 Kafka 地址反例复现，再完成共用校验与回归。浏览器文件从 npm 官方发布包提取，SHA-512 校验与本地文件摘要记录在 `web/vendor/provenance.json`；PDF 读取器与 Worker 版本一致。真实浏览器检查分别验证解析、脱敏和边界，未调用外部模型。

结果及来源摘要见 [acceptance.json](acceptance.json)。原始本机日志在 `/tmp/ct-stack-audit-20261101/`，完整集成在 `/tmp/ct-stack-audit-integration-20261101/`，容器结果在 `/tmp/ct-stack-audit-observer-20261101/`。

集成中的条件跳过包括真实招聘网站、子进程辅助入口及额外依赖专项，未计为通过。本轮为单机合成验收，没有重新测量生产吞吐量，也未执行远程 GitHub Actions。旧性能报告和旧验收摘要保持原样。
