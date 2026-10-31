# 日常模型 API 与可选 RPC 实验后端

## 实施步骤（2026-10-31）

1. **明确后端选择**：现有普通模型调用继续使用 `LLM_URL / LLM_API_KEY / LLM_MODEL`。依据说明任务默认 `NARRATION_BACKEND=api`；`rpc` 必须显式选择并提供原有 `INFERENCE_*`。未配置模型时保持离线；错误配置启动失败，失败调用不自动切换后端。先用默认构建复现当前只有 RPC 的缺口，再测试配置矩阵。
2. **流式 API 适配**：复用现有 Chat Completions 服务商参数与进程内并发额度，增加有界 SSE 解析、usage、完成判定和 context 取消；拒绝工具输出、截断及中途断流，忽略 reasoning。通过真实 loopback HTTP 测试流片段、取消、超时、坏帧、重定向、并发和错误，不请求真实云服务。
3. **任务与端到端验证**：继续使用 NarrationRunner、SQL checkpoint、只读 SSE 和原有确认/nonce/权限边界。验证 API 路径下预览零调用、流式展示、重订阅不重复调用、用户取消、SIGTERM/SIGKILL 恢复、未知结果显式重试；用真实 Chromium 检查展示、刷新及停止。恢复指读取已保存草稿和显式新尝试，不承诺接续服务商已中断的生成。
4. **回归与交付**：默认构建不依赖未发布流式 SDK；显式 RPC 路径保留，使用既有候选 SDK 回归。运行普通测试、race、SQL/Redis/etcd 隔离集成及前端测试，更新配置与使用说明，记录跳过和失败。无真实服务证据时，云 API 兼容性、模型质量与真实 vLLM/GPU 继续标为未验证。

变更范围仅涉及模型接入和验收；已有 Kafka、搜索、观测等并行变更不作为本次新增功能。

## 协议依据

- [Chat Completions 流事件](https://developers.openai.com/api/reference/resources/chat/subresources/completions/streaming-events)
- [Chat Completions 参数](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)
- [DeepSeek Chat Completions](https://api-docs.deepseek.com/api/create-chat-completion/)

本轮保留已有服务商参数规则，新增的是兼容该协议的流式文本依据说明，不新增模型或工具权限。

## 日常使用

在启动 API 的本机环境或不提交的 `.env` 中设置：

```sh
NARRATION_BACKEND=api
LLM_URL=https://your-provider.example/v1/chat/completions
LLM_MODEL=your-model-name
# LLM_API_KEY 通过本机环境或私有配置提供，不提交、不粘贴到报告。
```

`LLM_URL` 是完整 Chat Completions 地址，不是只填 `/v1` 的 base URL；支持 HTTPS，以及无密钥外发风险的 loopback HTTP 联调。URL 不接受凭据、query 或 fragment。密钥通过 Bearer header 提供，本地无认证兼容服务可以留空。未配置 URL、模型和密钥时，依据说明关闭，其他离线功能仍可用；只配置一部分时明确报错。

正常构建即可运行 `GOWORK=off go run ./cmd/api`；数据库、Redis、JWT 仍按项目原有配置。`go run` 需要变量已加载到进程环境；私有 `.env` 由现有 CLI/Compose 启动方式读取。已有 Compose 启动方式也会传入 `NARRATION_BACKEND` 与 `LLM_*`，无需启动推理 worker/gateway。这里没有修改运行中的部署或真实 `.env`。

进入“求职问答”，完成一个固定只读任务，点击“核对依据后生成说明”，确认预览显示的服务器模型和脱敏材料后才发起调用。**这一流式任务使用服务器模型，网页临时选择的模型和浏览器 API 密钥不会替换它。** 普通 Agent 问答、简历整理和岗位匹配仍走各自既有流程；本次不把它们全部改成流式。

- 默认 `api` 只调用配置的 API；`disabled` 关闭依据说明；`rpc` 只调用明确配置的推理网关。错误和超时不自动重试，也不跨后端回退。
- 说明任务与现有服务器模型共享进程内 `LLM_CONCURRENCY`；沿用说明任务的用户/全局限流、4 个本地任务和 SQL 每用户活跃任务限制。说明任务的全局限流只扣一次，不扩大为岗位匹配的跨进程容量许可。
- 单次说明最大 512 输出 token、原始/脱敏文字各 32KiB、任务 35 秒；已有 HTTP client 若有更短超时则继续生效。SSE 单事件 64KiB、整条协议流 1MiB，完成需看到文本、`finish_reason=stop` 及 `[DONE]`。`length`、工具调用、拒绝、异常帧和断流均不会标成完成。
- 不发送 vLLM `cache_salt`、工具定义、原始 owner ID 或网关控制字段。忽略模型 reasoning；usage 仅采用上游实际提供值，字段缺失不推断消耗或缓存命中。服务商返回规范化/带版本模型名不视为配置模型被替换。
- 不支持 SSE/usage 选项或使用其他协议的服务商，需要显式适配；不会先失败再自动发起第二次调用探测兼容性。已按官方协议做本地验证，真实云服务兼容性与回答质量尚未验证。

## 取消、恢复与切换后端

刷新或断开浏览器只停止订阅，不重发模型请求；重新进入读取 SQL 中的草稿。停止生成会先保存取消状态，再取消上游 HTTP；迟到结果不能覆盖取消。服务重启保留草稿，崩溃遗留租约到期后可显式处理。已发送但未完整结束的请求标为 UNKNOWN，需要核对预览并勾选未知结果重试确认；它是新的生成尝试，不能接续引擎内部状态。

更换 URL、模型或切换 API/RPC 会改变预览哈希，**旧任务的批准不会自动用于新后端**。旧结果和草稿仍可读取；回到原只读父任务，在新后端重新预览并确认，创建新的说明任务。同一后端重启则可以按原流程继续显式重试。API 密钥轮换不改变模型身份。

取消能证明 CampusTrace 结束 HTTP 请求并阻止迟到写入；服务商是否立即停止计算或收费，必须由服务商协议和真实账单验证。

## 可选 RPC / vLLM 实验

```sh
export NARRATION_BACKEND=rpc
export INFERENCE_RPC_ADDRESS=127.0.0.1:9094
export INFERENCE_MODEL=your-served-model
# 构建与运行方式见 stream-sdk.md；真实服务使用 configured-upstream 标记。
```

`INFERENCE_RPC_ADDRESS` 指 scheduled gateway 的 RPC 地址；vLLM HTTP 地址只由 RPC worker 配置。日常 API 模式若遗留 `INFERENCE_RPC_ADDRESS / INFERENCE_MODEL` 会明确拒绝启动，请清理这两个变量或显式切回 `rpc`。普通构建不支持尚未发布的 RPC 流式接口，会明确提示所需构建方式。

不需要常驻 GPU 服务。先验证单实例真实流式/取消/usage；多实例调度与 KV 选址效果至少需要两个真实实例及同轨迹对照。现有 M2/M3 的模拟结果继续仅作为模拟证据。

## 可复现验证

```sh
# 日常路径：独占 SQL/Redis + 实际 API 进程 + 无模型 HTTP 服务；不依赖相邻 RPC 仓库。
python3 scripts/verify-rpc-agent.py --backend api --output /tmp/ct-api-acceptance
# 可加 --playwright-module /absolute/path/to/playwright 检查真实 Chromium。

# 原有 RPC 路径：同一个任务和 UI 协议，经候选 SDK / gateway / worker 到无模型服务。
python3 scripts/verify-rpc-agent.py --backend rpc \
  --sdk-candidate docs/verification/rpc-agent-m4/closeout/sdk-candidate \
  --output /tmp/ct-rpc-acceptance

# 隔离 Go 集成回归。
python3 scripts/verify-integration-isolation.py --race --count 1 --output /tmp/ct-api-race
```

真实服务验收继续使用 `scripts/verify-rpc-agent-live.py`：先保存并检查预览，再显式提交 review_hash；脚本同样适用于日常 API 路径，不自动重试。`configured-upstream` 是部署者声明，不是已完成真实模型验证的证明。本轮结果和限制见[验收报告](verification/model-backends/report.md)。
