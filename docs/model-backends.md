# 日常模型 API 与依据说明

依据说明复用服务器 `LLM_URL / LLM_API_KEY / LLM_MODEL` 和已有进程内并发额度。`NARRATION_BACKEND=api` 为默认；配置模型后启用生成，`disabled` 可显式关闭。配置不完整或仍携带旧推理 RPC 配置会启动失败，不自动更换服务商。

用户先运行只读常用任务，再核对脱敏的实际模型输入并确认，才创建说明子任务。说明依附原任务，历史说明折叠显示。查询任务本身不调用聊天模型；已启用的 Embedding/重排序是独立检索调用。

API 适配器采用有界 SSE 文本解析，核对完成标志和 usage，拒绝工具调用、截断及中途断流。SQL 保存草稿和状态，浏览器 SSE 只读订阅；刷新不重复生成。取消传播到上游 HTTP，迟到完成不能改写终态。进程中断后可读取原草稿，但不会自动重放；结果未知时重新审阅并明确确认可能产生重复调用，才创建新尝试。新的尝试重新发起生成，已有草稿保留在任务记录中。

可复现的无模型验收：

```sh
python3 scripts/verify-agent-explanation.py --output /tmp/ct-explanation
# 可选真实 Chromium 浏览器检查：
python3 scripts/verify-agent-explanation.py --output /tmp/ct-explanation-browser --playwright-module /absolute/path/node_modules/playwright
python3 -m unittest discover -s scripts -p 'test_*.py'
```

验收自建临时 MySQL/Redis 与本机 HTTP 测试服务，退出时清理自有进程和容器，不操作已有账号。覆盖预览零调用、认证/owner/CSRF、并发请求去重、流式草稿、断线重订阅、取消、SIGKILL 后检查点读取、未知结果显式重试，以及申请留档准备到完整资料阅读。租约过期在隔离数据库中注入，不是等待 60 秒的测量。

真实服务验收入口为 `scripts/verify-agent-explanation-live.py --help`，需要明确同意向已配置模型发送所选任务材料；不在普通回归中运行。默认回归使用本地 HTTP 模型桩验证流式协议与任务恢复；真实服务按显式授权的材料单独验收。

RPC/vLLM 与未发布流式 SDK 已移至[独立实验归档](../experiments/README.md)，不再作为当前部署选项。岗位证据分析使用新的 KanaRPC 普通调用，见[分析链路迁移](kanarpc-migration.md)；它与归档的模型推理实验分别维护。

## 申请准备的模型调用

新[申请准备 Agent](application-preparation-agent.md)使用现有 Chat Completions 工具调用适配器，可选择当前浏览器配置或服务器模型。启动前单独核对冻结材料、检索范围、模型和预算；这与上文固定任务后的流式依据说明是不同入口。学习检索使用本地 BM25，不隐式增加其他付费服务。

每次派发前保存未知调用保护与预算预留，返回 usage 后结算。进程中断或网络错误不会自动重新发送模型请求；用户明确授权新尝试后继续，并保留已有读取与历史未知标记。密钥只在活动请求/执行器内存中使用，不保存进检查点。恢复和界面使用本地 HTTP 受控响应验收；[真实模型记录](verification/preparation-agent-live-20261101/README.md)另以授权的网页 DeepSeek Flash 配置运行三个合成调试任务及一次调整复测，记录失败、usage 和单次观察，不包含个人求职材料。

后续[真实材料评测](verification/preparation-agent-real-materials-20261101/README.md)沿用明确授权的网页配置，发送已脱敏的现有简历、归档 JD 和项目材料，累计 120 次请求。完整输入和返回保存在仓库外的私有目录，密钥未写入报告或命令参数。已返回参数的格式修正有独立预算和检查点；结果未知仍沿用核对确认流程。

再下一轮[一致输入回归](verification/preparation-agent-followup-20261101/README.md)新增 101 次真实请求，补齐完整动态与重复验证，模型及地址保持原配置。引用失败反馈只提供已读材料的精确匹配候选，模型需明确重新选择；原文校验、来源范围与用户确认规则保持不变。


## 申请准备的 LangGraph 编排

申请档案的“准备这场面试”由同仓库 Python / LangGraph 服务组织模型与工具循环、澄清中断和检查点恢复；Go 继续负责账号、材料、检索、模型预算及确认保存。普通问答、固定 Skills 和其他模型业务保留原职责。该场景的检索仍为已授权本人材料的 BM25，不隐式启用 Embedding、重排序或外部 MCP。[部署、恢复与证据范围](preparation-langgraph.md)。


## 本机共用API配置

`.env.model.local`为用户授权的私有模型配置文件，包含LLM_URL、LLM_MODEL、LLM_API_KEY，权限0600且被Git和镜像构建忽略。统一CLI、应用Compose及本机启动读取它，命令行评测可导出同一环境，Python编排进程不接收模型Key。配置不进入检查点或报告。[使用与验证](preparation-quality.md#本机后端模型配置)。
