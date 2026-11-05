# 答辩教练的离线教学策略优化

围绕已保存的答辩执行轨迹提出教学策略假设，在相同任务、模型、材料和预算下对照，通过程序检查并输出供人工审阅的对照结果；无收益、失败和退化都保留。它是单场景、有界的策略优化，优化对象为解释顺序、追问方式与阅读安排。

## 产品接入

从“投递进展 → 申请档案 → 准备这场面试 → 练习讲清项目”开启的新练习，使用 Go 代码中的默认教学策略。预览和执行记录绑定策略快照，续练沿用原快照。早于快照功能的记录按最初的策略解释。

候选只能选择以下枚举，没有自由改写系统规则或执行代码的入口：

| 维度 | 选项 |
| --- | --- |
| 解释顺序 | 从代码动作解释／先核对原回答再联系代码 |
| 追问方式 | 当前缺口的具体问题／用对照问题区分机制与误解 |
| 阅读安排 | 有缺口才安排阅读／明确一个阅读片段和一个问题 |

共有 8 种允许的组合。离线评测可以显式传入候选；报告记录候选文件、实际提示哈希和工具版本。教学策略的产品变更随普通代码提交和部署发布。

## 离线流程

1. 输入一份与任务文件哈希匹配的调试集报告。基线策略读取自这份报告，并校验策略哈希。提案模型只获得调试案例的原问题、原回答、具体反馈、时间约束和有界轨迹；验证集不进入提案上下文。最多12个案例、32KiB，超过上限明确拒绝。
   原任务文件和来源报告按原始字节归档；历史轨迹用于形成假设，对照成绩来自当前代码重新运行的基线与候选。
2. `ProposeDefenseStrategy` 复用既有 `Model` 接口，单次调用选择一个结构化候选，绑定父策略、调试案例和待验证假设。枚举、父版本、案例出处与大小由Go校验。当前CLI只接收明确提供的受控响应文件，不创建外部模型客户端。
3. 复用 `cmd/preparation-eval` 与 `scripts/evaluate-preparation-langgraph.py`。基线和候选先跑调试集；候选存在程序失败或额外模型调用就停止，保留失败且不运行验证集。通过该阶段后再对照验证集。
4. 固定任务哈希、代码、提示/工具版本、框架、模型配置、调用与时间预算，记录实际提示哈希和策略标识。报告缺失、任务不全或配置不同都会拒绝比较；原报告不可改写。
5. 输出逐题对照页和沿用原评分结构的四份评分文件。空评分保持待审阅；验证集至少一处语义改善、各案例各维度无退化，且程序/调用检查通过，才记录审阅中的改善。总分上升不能覆盖局部退化。
6. 将审阅结果作为后续代码评审的依据，保留无收益与退化结果。

## 运行

先使用现有合成任务产生一份调试集报告：

```sh
python3 scripts/evaluate-preparation-langgraph.py \
  --dataset internal/agent/testdata/project-defense-tasks.json \
  --arms dynamic --splits development --output /tmp/ct-strategy-seed-new
```

候选响应文件格式如下，`parent_sha256` 取调试集报告的 `defense_teaching_strategy_sha256`，`case_ids`必须确实来自输入的调试报告：

```json
{
  "parent_sha256": "当前策略的SHA-256",
  "hypothesis": "结合具体失败轨迹说明要比较的教学选择，效果待验证",
  "case_ids": ["go-langgraph-2"],
  "profile": {
    "schema": "defense-teaching-v1",
    "explain": "code-first",
    "followup": "contrast",
    "reading": "gap-only"
  }
}
```

```sh
python3 scripts/defense-strategy.py run \
  --dataset internal/agent/testdata/project-defense-tasks.json \
  --development-report /tmp/ct-strategy-seed-new/development.json \
  --response /absolute/private/candidate-response.json \
  --output /absolute/private/strategy-experiment-new
```

实验必须放在仓库外，文件按0600保存，输出目录只能新建。逐题对照页复用已有人工审阅格式，包含共同任务、本轮原回答、双方具体反馈、代码原文和轨迹；在 `*-scores.json` 填分与证据，保留真实审阅者及类型。脚本验证归属与报告绑定，不能代替审阅判断。

```sh
python3 scripts/defense-strategy.py review \
  --experiment /absolute/private/strategy-experiment-new \
  --output /absolute/private/strategy-decision-new.json
```

语义维度包括回答反馈正确性、引用支撑、工作量、反馈遵守、虚构经历、记忆增益与纠正遵守。逐题记录0／1／2、N/A或未审阅；N/A必须两组可比。无收益、退化、待审阅和可采用是不同终态，既有程序通过分数不会自动转换为语义分数。

## 审阅结果与发布

`SYNTHETIC_GATE_PASSED` 只表示合成评分通过规则。`REVIEWED_IMPROVEMENT` 表示在真实报告中经署名审阅后观察到改善。可通过 `--evaluation-root` 导入已完成的授权评测报告，模型、材料、版本和预算仍须一致。

如需将新策略用于日常陪练，审阅相关案例后，通过代码修改默认策略并走常规发布流程。回退使用对应的代码版本。旧的采用与回滚实验保留在[历史验收](verification/defense-strategy-20261102/README.md)。

## 验证

```sh
GOWORK=off go test -race ./internal/agent ./internal/defensestrategy ./cmd/preparation-eval
python3 -m unittest discover -s scripts -p test_defense_strategy.py -v
python3 scripts/verify-defense-strategy.py --output /tmp/ct-strategy-verification-new
```

最后一个命令实际运行Go＋Python＋LangGraph、受控提案、调试和验证对照，保留无收益、语义退化、程序退化、额外调用和验证集失败。合成评分只测试审阅门槛。原准备、答辩和记忆回归继续使用既有验收脚本。[本轮实测](verification/defense-strategy-20261102/README.md)。
