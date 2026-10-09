# 同公司排序效果评测

这套工具回答两个不同问题：结果能否通过原文与资料核对，以及它的排序是否符合人工认可的取舍。引用正确不代表推理正确，也不把排序一致性解释成录用概率。它不读取真实账号或自动使用浏览器中的密钥。

## 从真实岗位收集案例

1. 在“岗位雷达 → 公司投递决策”选择公司和候选范围，阅读已有比较，点击页面上方的“导出评测 JSON”。岗位雷达的“同公司岗位对比”窗口也有这个按钮，进入导出页面时会保留本次公司和候选岗位，不扩大到全公司。
2. 自行选择首选，写明理由，并确认已经阅读岗位与资料。不会默认勾选模型的第一名。
3. 点击“预览导出 JSON”，检查实际导出的脱敏材料、完整 JD 和引用索引，再点击“确认下载 JSON”。文件名为 `CampusTrace-匹配评测-案例编号.json`，下载不发送到外部服务。

页面会说明当前范围是否已有有效比较。已有比较时，JSON 一并包含排序和关键依据；没有报告时也能先标注自己的参考结论，但文件不含模型分析结果。想评估本次模型结果，应先完成同一范围的公司比较，再导出。

案例绑定当前岗位集合与资料输入标识。导出前资料或 JD 已变化会拒绝，需刷新后重新核对。参考结论属于个人选择，不会修改岗位报告、技能或项目事实。下载后标注视为已保存；再次修改时会提醒未保存。

文件格式为 `campustrace-matching-eval-v1`。`cases` 每项包含 `candidate`、完整 `jobs`、`reference` 和来源；`recorded_results` 仅携带当前范围已存在的有效公司比较，没有报告时为空。个人材料保留本地引用索引；实际模型请求仍通过应用里的 `CompareHolistically` 路径，仅发送拼好的完整正文一次。

建议逐步收集 20～30 个真实场景：Go 与 Agent 方向取舍、加分项、任选语言、两个学历、资料缺失、方向相近但核心职责不同。部分案例用于调整，部分保留验证。允许多个可接受首选；不确定时不要为了算分强行标注。

## 离线回放已有结果

```sh
go run ./cmd/match-eval -dataset '/path/to/case.json'
go run ./cmd/match-eval -dataset '/path/to/case-a.json' -dataset '/path/to/case-b.json'
```

默认只评分文件中的已记录结果，没有网络请求。没有报告的案例标为 `NO_RECORDED_RESULT`；有人工标注的失败输出仍计入分母，不通过遗漏失败项提高一致性。没有人工确认的案例不产生质量分数。文件中可添加 `reference.pairs`，格式为 `{"preferred":"岗位ID","other":"岗位ID","relation":"ABOVE"}`；允许 `TIED`。越界、重复与矛盾标注会拒绝。

其他版本的结果可用 `-responses '/path/to/results.json'` 回放，格式为 `{"trials":[{"case_id":"案例ID","attempt":1,"model":"模型或版本标签","report":{...},"usage":{"known":false}}]}`。`report` 使用 `HolisticCompanyReport` 格式，须包含 version、company、summary、choices、questions。一次最多合并 64 个案例、192 个结果；单文件最多 10 MiB，完整公司输入仍受 16 个岗位/128,000 字节限制。

## 显式评测外部模型

```sh
go run ./cmd/match-eval -dataset '/path/to/case.json' \
  -live -url '<public-chat-completions-url>' -model '<model-name>' \
  -key-env CAMPUS_MATCH_EVAL_KEY -limit 3 -attempts 1 -max-calls 3
```

只有明确指定 `-live` 和完整配置才调用模型，使用已有公开 HTTPS 与 DNS 检查。密钥从指定环境变量读取，不写报告；没有自动重试。每例一次调用，`-attempts` 可设 1～3。默认总请求上限 30 次，超限在任何请求前拒绝。报告包含通过核对的最终结果、固定错误码、端到端耗时和服务商实际返回的 Token 用量；未返回 usage 时明确记录未知，不估算账单。

汇总的 `trial_count` 是尝试次数；`contract_passed` 是结构与引用核对通过数；`quality_scored` 是有人工标注的尝试数；`top_agreement` 是认可首选的尝试数。并列第一中所有岗位都必须属于可接受首选，避免把全部岗位排第一骗过评分。`pair_correct/pair_total` 衡量人工标注的成对顺序。`groups` 按资料来源与模型分开，真实案例与合成案例不混成一个模型准确率；P50/P95 只计算实际测得的请求耗时。

`-strict` 会在结果无效、首选不一致或成对顺序不一致时返回非零，适合固定基线检查；这些失败可能来自合理的人工分歧，仍需阅读具体结果。

## 合成检查的边界

`make matching-eval` 回放 24 个带明确说明的合成场景，验证评分格式和边界。它们的 `recorded_results` 是手工构造的评分器样例，不是模型输出，更不是外部模型 24/24 准确。本轮没有调用付费模型。真实效果需要收集人工标注案例并回放真实结果，或显式运行模型评测。
