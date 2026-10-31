# 已归档的独立实验

CampusTrace 主应用围绕“发现岗位 → 判断与投递 → 申请留档 → 面试与复盘”维护。Go 执行练习和 RPC/vLLM 推理接入于 2026-10-31 归档为独立实验；原有 Analysis RPC、Search、Kafka、Redis 调度与观测保持原职责。

完整实验源代码保存在[归档分支](https://github.com/KanaDoodle/CampusTrace/tree/archive/campustrace-product-experiments-20261031)，源码快照提交为 `8556e2a8aee3a7668108ed743cc12671196c430b`。该快照包含清理前工作区源码和本地 SDK 候选，排除密钥、数据库和卷。后续归档提交 `6ff5a5c5e3338b824bc38acc47c72237a34cf0fd` 补存历史验收材料；历史报告保留原日期、源码版本与验证范围。

在独立目录查看或编译，不切换当前工作区：

```sh
git fetch origin archive/campustrace-product-experiments-20261031
git worktree add --detach /tmp/campustrace-experiments 8556e2a8aee3a7668108ed743cc12671196c430b
cd /tmp/campustrace-experiments
GOWORK=off go build -mod=readonly ./...
# 阅读 internal/practice、internal/inferencebridge/rpc_enabled.go 及原实验文档。
```

归档快照已在临时 worktree 通过默认编译与 practice/inferencebridge 单元测试。复现流式 RPC 时需使用归档说明中的 SDK 候选源码与构建配置；模型与 GPU 相关结果以对应历史报告为准。

旧练习数据不迁移或删除：认证后的 `GET /api/practice/runs` 继续返回本账号历史，读取不改写旧状态。能力接口标记 retired，`POST /api/practice/runs` 返回 `410 PRACTICE_RETIRED`。不再提供执行器、网页运行入口或 Docker socket 挂载；旧 CLI practice 命令会说明已归档。正常停止旧部署时，启动器可停止本项目命名的遗留 sandbox 容器。

日常依据说明继续走经过用户审阅的模型 API，详见[模型接入](../docs/model-backends.md)。面试资料继续由当前申请或岗位的准备任务检索，详见[申请流程](../docs/application-journey.md)。
