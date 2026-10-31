# 精简与申请准备验收

2026-10-31。本页记录功能整理完成后的本地验收。历史推理实验及其原报告位于[归档分支](https://github.com/KanaDoodle/CampusTrace/tree/archive/campustrace-product-experiments-20261031)，恢复方式见[实验说明](../../../experiments/README.md)。

## 当前行为

- 删除不在当前匹配主流程使用的旧 Extract/Compare 执行路径和相关死代码，保留整体 JD/完整求职资料比较。
- Go 练习执行器、页面、Docker socket 配置及未发布推理 SDK 接入移出主应用。旧练习 GET 保留本人只读历史，POST 返回 410，读取不改写旧状态。
- 新增申请准备任务：申请档案预填本人申请，主动开始后读取冻结 JD/材料与同一申请的实际复盘问题、遗漏点，再检索本账号笔记。结果可打开完整材料；当前简历/JD 更新不替换历史，缺快照不补造、不检索。
- 岗位准备使用当前 JD 与同岗位复盘，历史薄弱点按相关范围选择。知识库或本申请记录变化使旧检查点失效。
- 招聘变化提醒归入今日待办，站内提醒历史从同页进入；历史依据说明折叠，运行中或可继续的说明重新读取持久状态，不自动生成。

第二轮整理相对其开始时工作区增加 1,438 行、移除 2,647 行，净减少 1,209 行代码与文档；不把此前旧匹配代码精简重复计入。发布说明与验收摘要的整理未计入这个历史增量。

## 验证结果

| 检查 | 结果 |
| --- | --- |
| 全项目默认 build / vet / race | PASS；最后新增回答格式又运行 agent/inferencebridge race/vet |
| 已发布 RPC 与独立 Search 依赖边界 | PASS；GOWORK=off、KanaRPC-Go v0.1.0，无相邻源码依赖 |
| 前端 | 448/448 PASS |
| Python 验收入口防护 | 5/5 PASS |
| 隔离 MySQL/Redis/etcd 全量 race | [PASS](sql-result.json)；[559 项顶层测试通过、48 项按环境条件跳过](sql-tests.json)，另有 1,620 项嵌套子测试通过 |
| Chromium + 本机 HTTP 无模型服务 | [PASS](browser-result.json)，页面异常 0；[进程恢复验收](result.json)、[测试服务计数](fixture-final.json) |
| 完整 Compose 合并配置 | PASS；基础/app/Search/Kafka/观测文件配置检查，未启动业务栈 |
| 归档源码恢复 | [PASS](archive-build.json)；临时 detached worktree 默认编译与 practice/inferencebridge 测试 |

浏览器覆盖审阅关闭零调用、未知结果重试必须明确认可、流式草稿、刷新不重发、HTML 只显示文本、取消传播到上游；完整走通“申请档案 → 预填任务 → 显式开始 → 冻结申请 RAG → 完整笔记 → 今日待办 → 提醒历史”。准备与导航前后聊天模型测试调用计数相同。

数据库覆盖修改当前资料/JD 不替换留档、实际复盘进入检索、跨账号申请/笔记隔离、知识删除后旧任务 STALE、历史无快照不补造，以及旧练习禁止执行。自有测试库/账号清理为 0，模板 guard 保留；未操作已有个人数据。

## 首次失败与修复

浏览器验收先修正了合成 profile 的错误字段和从任务摘要读取完整依据的错误断言。整理历史展示还暴露出自动恢复入口依赖旧 DOM 的问题，已改为按运行中/可继续说明 ID 读取持久状态。SQL 首次只有 MCP 工具数旧断言失败，按退休练习工具更新为 9 个，并明确验证该工具不再暴露；重跑全量通过。

原始 stdout/JSONL、截图、首次失败及逐文件增量保留在本机 `bin/verification/cohesion-20261031/`，未把本地日志路径当作 GitHub 可下载文件。

## 复现与边界

```sh
GOWORK=off go build -mod=readonly ./...
go vet ./...
go test -race ./...
node --test web/*.test.cjs
python3 -m unittest discover -s scripts -p 'test_*.py'
python3 scripts/verify-integration-isolation.py --race --count 1
python3 scripts/verify-agent-explanation.py --output /tmp/ct-cohesion-browser --playwright-module /absolute/path/node_modules/playwright
```

本轮端到端检索为本地 BM25，模型上游是本机无模型服务；未调用真实聊天/Embedding/重排序服务，未新增真实回答质量、vLLM/GPU、用户节时或生产容量证据。SIGKILL 恢复中租约过期由隔离 SQL 注入。Kafka/OpenSearch 全栈性能未在本轮重测。
