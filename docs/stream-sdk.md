# 流式 SDK 实验归档

未发布的流式 RPC SDK 接入和候选构建工具已移至[实验归档](../experiments/README.md)。归档包含 SDK 候选源码、校验和与接入说明，复现时使用归档内的源码与构建配置。

当前分析链路已迁到独立仓库 KanaRPC 的公开提交，见[迁移说明](kanarpc-migration.md)。`GOWORK=off go build -mod=readonly ./...` 不需要相邻 RPC 源码、候选 SDK 或特殊 build tag。Analysis 使用 Context 优先的普通调用；日常流式依据说明通过[服务器模型 API](model-backends.md)实现。

旧[收尾报告](https://github.com/KanaDoodle/CampusTrace/blob/6ff5a5c5e3338b824bc38acc47c72237a34cf0fd/docs/verification/rpc-agent-m4/closeout/report.md)保留在归档提交，记录对应日期、SDK 候选版本及本地测试环境。
