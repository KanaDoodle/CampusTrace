# CampusTrace 可交付版本验收（2026-11-01）

本次固化现有 KanaRPC 接入、申请准备 Agent、Python / LangGraph 产品主链路及界面。没有新增独立演示项目或通用 Agent 子系统。

交付提交：`5114a0502105f2bfdadf7dd9616436aa5d8a52b4`。检索评测计数修复：`a611fcc39064f08063fbbb991a597bb24aca38a1`。后续发布记录提交只补充本文档和验收材料；固定检出标记为 `delivery-20261101`。

## 下载与运行

```sh
git clone --branch delivery-20261101 https://github.com/KanaDoodle/CampusTrace.git
cd CampusTrace
make run
./bin/campustrace status
./bin/campustrace logs preparation
make stop
```

需要 Go 1.27.3+、Git、Docker 和 Compose。Compose 构建 Go 与 Python 镜像，Python 使用 3.14.7 和带哈希的全量依赖锁。打开网页注册后，从申请档案进入“生成面试准备计划”，在模型设置中配置模型，核对外发材料后生成。模型 Key 不放入仓库或图检查点。

本机开发与评测安装见[LangGraph 运行说明](../../preparation-langgraph.md)。不要把本机 `.env`、`.venv` 或相邻 KanaRPC 源码复制到检出目录作为隐藏依赖。

## 干净检出实测

在 detached worktree 中从提交检出代码，确认 `git status --porcelain` 为空；创建全新 Python venv，使用全新的 Go module cache，`GOWORK=off`，从公共代理下载固定 KanaRPC 伪版本，无本地 replace 或相邻 SDK 依赖。

| 检查 | 结果 |
| --- | --- |
| 全新 venv 与锁定哈希安装 | PASS |
| 公共模块及独立 Search 依赖边界 | PASS |
| Go 全包、相关 race | PASS；带依赖的环境条件测试不能由普通单测替代 |
| 前端 / Python | 460/460、3/3 |
| 原申请入口 Chromium 演示 | PASS：生成、反馈60分钟、确认保存、刷新读取 |
| 新主链路 API / SQL / Python 重启 | 19项检查通过，12次受控模型HTTP请求，MySQL状态与回执实查 |
| 完整 Compose | API、Worker、双 Analysis、Python、MySQL、Redis、Kafka、etcd 共9项服务健康；topic初始化正常退出 |
| 容器跨服务配置 | Go 能读取 LangGraph 子系统健康，容器中 Python 单测通过 |
| 实际 RPC 后台链路 | 新导入岗位经 Kafka → Worker → KanaRPC → Analysis，观察记录达到 COMPLETE |

[机器检查记录](checks.json)、[Compose状态](compose.json)、[产品验收](product-acceptance.json)、[浏览器检查](browser.json)、[截图](clean-product.png)、[公共依赖](public-dependency-boundary.txt)及各测试文本位于本目录。

产品浏览器与 SQL 验收运行于 `5114a05`；完整 Compose、最终 race 和修正后的受控评测运行于 `a611fcc`。两者之间只修改评测统计及其回归测试，没有更改模型提示、业务工具或保存流程。所有测试数据、容器、网络和卷均为本轮独立创建，结束后清理，原有两个开发数据容器保持运行。首次额外 Compose 脚本使用了超过注册20字节上限的测试密码，正确收到400；改用有效的合成凭据后通过，未放宽产品校验。

## LangGraph 新链路真实模型小轮

使用用户授权的网页配置，请求模型名为 `deepseek-flash`，Chat Completions 地址为 `https://api.deepseek.com/chat/completions`。框架 LangGraph 1.2.19、SQLite Checkpointer 3.1.3。模型配置一次复用后后台完成四项任务；凭据仅在进程内使用，临时桥接及模型进程已关闭。

材料沿用仓库外已脱敏简历、真实归档 JD、项目 README 和学习资料；本轮没有真实面试复盘。申请目标与时间是构造任务，不代表实际面试预约或历史投递证明。反馈场景基于本轮正常任务实际返回的计划，明确标记其中第一项已掌握，再缩减为60分钟。

| 场景 | 程序校验 | 安排 / 预算 | 模型请求 | 检索 | 本次耗时 |
| --- | --- | --- | --- | --- | --- |
| 正常准备 | 通过 | 180 / 180分钟 | 2 | 2 | 5.74秒 |
| 上版反馈调整 | 通过 | 60 / 60分钟 | 1 | 0 | 2.34秒 |
| 15分钟准备 | 通过 | 15 / 15分钟 | 2 | 2 | 4.46秒 |
| 缺少简历 | 通过 | 180 / 180分钟 | 2 | 2 | 5.55秒 |

合计7次真实请求，报告输入55,693、输出3,053 Token；没有 usage 缺失或 UNKNOWN。沿用旧账本剩余额度，累计从101变为108，上限仍为120，没有重置或扩大额度。未提供货币单价，费用不估算。CLI质量任务只读，没有业务写入；真正确认保存由独立产品 SQL 验收验证。

这四项是定向小样本，不估计总体成功率，也没有与基线重新做质量对照，不把框架迁移描述为效果提升。真实模型执行的是 `5114a05` 的 LangGraph 主链路，区别于更早 Go 循环的真实成绩。

## 逐项语义审阅与保留问题

逐条阅读了 JD、来源引文、输出动作和时间安排，审阅者为当前代码助手，没有独立人工标注，也没有调用另一个模型自动打分。六维逐项评分与判断见 [semantic-review.json](semantic-review.json)，量化记录见 [live-summary.json](live-summary.json)。

- 正常方案能组织网络、并发和框架稳定性准备，但算法/设计模式覆盖偏浅，45分钟用于版本口径核对，优先级仍有改进空间。
- 正常及反馈计划有一处引用只是项目口号，逐字匹配成立但不能支撑对应行动。其他相关引文不能让这条弱引用自动变得有效。
- 反馈任务正确去除已掌握项并保留剩余ID/标题；模拟题使用“上述四题”指代，题目不够明确。
- 15分钟方案完整执行并生成三项各5分钟的速查/口述/草稿任务。它适用于已有项目知识的复习，不证明用户实际能在15分钟掌握全部主题。两次检索未在计划中引用学习来源，未观察到明确增益。
- 缺简历场景明确说明个人经历未核对，没有虚构简历或历史失分；但35分钟安排三道可运行算法题及模式说明偏紧，算法技巧和设计模式混用。

因此“4项程序校验通过”不等于“4项语义完全合格”。引用支撑力和有限时间内的工作量，是后续最小 Prompt 改进目标；本轮保留问题，不追加新模型角色或扩建评测系统。

## 发现并修复的计数问题

真实输出已引用知识来源，但报告 `retrieval_new_sources / retrieval_cited_sources` 均为0。复现后发现统计块被误放在来源过期/跨账号的提前返回分支中，正常任务根本未采集。

修复将统计移到执行与引用收集之后。新增测试同时覆盖动态、固定检索、单次提示三组：修复前动态与固定组失败，修复后全部通过。实际 LangGraph 24任务三组受控回归也通过，修正没有影响任务终态或计划校验。

[修复前](retrieval-counter-before.txt)、[修复后](retrieval-counter-after.txt)、[新受控汇总](corrected-controlled-summary.json)。历史报告及本轮真实原始输出保持原样，其中旧计数值明确标为无效，不用后验估计替代实际读取数。私人材料、完整模型输出和检查点数据库继续保存在仓库外；本目录只含统计、审阅意见与合成演示证据。
