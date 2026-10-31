# 日常模型 API 与依据说明

依据说明复用服务器 `LLM_URL / LLM_API_KEY / LLM_MODEL` 和已有进程内并发额度。`NARRATION_BACKEND=api` 为默认；没有配置模型时不可生成，`disabled` 可显式关闭。配置不完整或仍携带旧推理 RPC 配置会启动失败，不自动更换服务商。

用户先运行只读常用任务，再核对脱敏的实际模型输入并确认，才创建说明子任务。说明依附原任务，历史说明折叠显示。查询任务本身不调用聊天模型；已启用的 Embedding/重排序是独立检索调用。

API 适配器采用有界 SSE 文本解析，核对完成标志和 usage，拒绝工具调用、截断及中途断流。SQL 保存草稿和状态，浏览器 SSE 只读订阅；刷新不重复生成。取消传播到上游 HTTP，迟到完成不能改写终态。进程中断后可读取原草稿，但不会自动重放；结果未知时重新审阅并明确确认可能产生重复调用，才创建新尝试。不承诺接续服务商中断的生成。

可复现的无模型验收：

```sh
python3 scripts/verify-agent-explanation.py --output /tmp/ct-explanation
# 可选真实 Chromium 浏览器检查：
python3 scripts/verify-agent-explanation.py --output /tmp/ct-explanation-browser --playwright-module /absolute/path/node_modules/playwright
python3 -m unittest discover -s scripts -p 'test_*.py'
```

验收自建临时 MySQL/Redis 与本机 HTTP 测试服务，退出时清理自有进程和容器，不操作已有账号。覆盖预览零调用、认证/owner/CSRF、并发请求去重、流式草稿、断线重订阅、取消、SIGKILL 后检查点读取、未知结果显式重试，以及申请留档准备到完整资料阅读。租约过期在隔离数据库中注入，不是等待 60 秒的测量。

真实服务验收入口为 `scripts/verify-agent-explanation-live.py --help`，需要明确同意向已配置模型发送所选任务材料；不在普通回归中运行。本轮仅验证本地无模型服务，未新增云端兼容性、回答质量、真实 vLLM/GPU 或性能收益证据。

RPC/vLLM 与未发布流式 SDK 已移至[独立实验归档](../experiments/README.md)，不再作为当前部署选项。现有 Analysis 服务使用的已发布 KanaRPC-Go v0.1.0 不受影响。
