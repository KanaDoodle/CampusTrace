# 流式 SDK 实验归档

未发布的流式 RPC SDK 接入和候选构建工具已移至[实验归档](../experiments/README.md)。归档包含 SDK 候选源码、校验和与接入说明，复现时使用归档内的源码与构建配置。

当前根模块继续固定已发布的 KanaRPC-Go v0.1.0，`GOWORK=off go build -mod=readonly ./...` 不需要相邻 RPC 源码、候选 SDK 或特殊 build tag。原有 Analysis RPC 不变；日常流式依据说明通过[服务器模型 API](model-backends.md)实现。

旧[收尾报告](https://github.com/KanaDoodle/CampusTrace/blob/6ff5a5c5e3338b824bc38acc47c72237a34cf0fd/docs/verification/rpc-agent-m4/closeout/report.md)保留在归档提交，记录对应日期、SDK 候选版本及本地测试环境。
