# Agent 评测基线

岗位排序的效果评测另见 [匹配评测使用说明](matching-evaluation.md)。这里的运行时与工具路由检查，不能替代真实岗位的排序评测。

`make eval` 连续运行两套评测：原有 24 例预设调用检查运行时边界；新增的 43 例中文求职问题检查从问题到工具调用、参数、回答和拒答的完整路径。第二套问题与预期写在 [`internal/agent/testdata/journeys.json`](../internal/agent/testdata/journeys.json)，可直接审阅和扩充。

```sh
make eval
go run ./cmd/eval -mode journey -only comparison
go run ./cmd/eval -mode journey -only match-stale
```

输出为 JSON。每例列出预期与实际工具、参数是否合法且符合预期、终态、回答断言、拒答、未授权写提案、模型请求次数、工具次数及耗时；汇总给出通过数和 P50/P95 耗时。模型请求次数是调用次数，不是 token 或账单费用。离线模式耗时只反映本地合成工具，不能当作线上延迟。

本轮基线：离线路由 43/43 通过；其中 7 例需要在没有可信依据时拒答。问题覆盖岗位匹配、同公司对比、失败任务、岗位状态、投递、项目事实、雷达、技术检索、写操作预览、越界请求和诱导编造结论。评测发现并修正了“最近的薄弱点”误选最近变化工具、岗位搜索固定使用 Go、无关问题也默认搜索岗位三个路由问题。匹配结果、失败任务和岗位资料均为合成数据；评测不会读取真实账号、简历、模型密钥或数据库，也不会发起深度分析。

外部模型可以用同一套合成问题单独评测。只有明确提供公开 HTTPS Chat Completions 地址、模型名及保存密钥的环境变量时才会调用外部服务；`-only` 和 `-limit` 可先缩小请求量。密钥内容不会进入报告，URL 和模型名不会自动从网页读取。

```sh
go run ./cmd/eval -mode journey -only matching -limit 3 \
  -url '<public-chat-completions-url>' -model '<model-name>' \
  -key-env CAMPUS_EVAL_API_KEY -strict=false
```

这是一套确定性回归基线：工具和参数按预期精确核对，回答检查关键内容、禁止内容及是否只来自成功工具观察。本次新增完整核对资料、事件待办和 MCP 授权目录三例；执行恢复和预算另有真实数据库/受控 HTTP 回归。它不替代人工检查复杂语义，也不能把离线 43/43 理解成外部模型在真实岗位上的准确率。外部模型的分数应单独记录，并查看失败样例后再调整提示词或工具定义。
