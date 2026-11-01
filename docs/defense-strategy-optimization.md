# 答辩教练的离线教学策略优化

围绕已保存的答辩执行轨迹提出教学策略假设，在相同任务、模型、材料和预算下对照，经过程序检查与人工审阅后采用；无收益、失败和退化都保留。它是单场景、有界的策略优化，优化对象为解释顺序、追问方式与阅读安排。

## 产品接入

产品继续从“投递进展 → 申请档案 → 练习讲清项目”进入。Go从随应用构建的 `internal/defensestrategy/registry.json` 读取有效版本，校验 `versions/<SHA-256>.json`，把策略快照放进本轮预览与执行记录，实际系统提示追加对应的教学指引。既有LangGraph图负责模型和工具循环；账号、证据、时间、预算、取消、确认保存与记忆规则不受候选改变。

候选只能选择以下枚举，没有自由改写系统规则或执行代码的入口：

| 维度 | 选项 |
| --- | --- |
| 解释顺序 | 从代码动作解释／先核对原回答再联系代码 |
| 追问方式 | 当前缺口的具体问题／用对照问题区分机制与误解 |
| 阅读安排 | 有缺口才安排阅读／明确一个阅读片段和一个问题 |

有效组合共8种，默认策略保持既有提示行为。新练习使用当前构建的有效版本；同一次练习的后续回答保留已经核对的策略快照。旧记录没有快照时沿用原默认行为。执行轨迹的按需诊断中可查看策略，保存的跨练习经历也记录其策略标识。候选文件、实际提示哈希和工具版本在评测报告中分别记录。

注册表损坏、缺少版本或哈希不符时明确报错，不回退掩盖错误。`verification_only` 注册表不能用于产品。采用或回滚后执行 `./campustrace restart --build`，让Go镜像包含相应版本；该步骤沿用现有部署流程，没有新依赖、表、端口或服务。

## 离线流程

1. 输入一份与任务文件哈希匹配的调试集报告。提案模型只获得调试案例的原问题、原回答、具体反馈、时间约束和有界轨迹；验证集不进入提案上下文。最多12个案例、32KiB，超过上限明确拒绝。
   原任务文件和来源报告按原始字节归档；历史轨迹用于形成假设，对照成绩来自当前代码重新运行的基线与候选。
2. `ProposeDefenseStrategy` 复用既有 `Model` 接口，单次调用选择一个结构化候选，绑定父策略、调试案例和待验证假设。枚举、父版本、案例出处与大小由Go校验。当前CLI只接收明确提供的受控响应文件，不创建外部模型客户端。
3. 复用 `cmd/preparation-eval` 与 `scripts/evaluate-preparation-langgraph.py`。基线和候选先跑调试集；候选存在程序失败或额外模型调用就停止，保留失败且不运行验证集。通过该阶段后再对照验证集。
4. 固定任务哈希、代码、提示/工具版本、框架、模型配置、调用与时间预算，记录实际提示哈希和策略标识。报告缺失、任务不全或配置不同都会拒绝比较；原报告不可改写。
5. 输出逐题对照页和沿用原评分结构的四份评分文件。空评分保持待审阅；验证集至少一处语义改善、各案例各维度无退化，且程序/调用检查通过，才产生可采用决策。总分上升不能覆盖局部退化。
6. 用户核对决策后显式采用。采用时重新校验实验、报告、审阅和注册表哈希，保留不可变版本及历史；回滚只能指向已记录版本，并填写原因。

每个候选是一轮独立实验；后续实验可以把已采用的版本作为父策略。没有自动发布、无限搜索、模型自评采用或修改优化器自身的机制。

## 运行

先使用现有合成任务产生一份调试集报告：

```sh
python3 scripts/evaluate-preparation-langgraph.py \
  --dataset internal/agent/testdata/project-defense-tasks.json \
  --arms dynamic --splits development --output /tmp/ct-strategy-seed-new
python3 scripts/defense-strategy.py status
```

候选响应文件格式如下，`parent_sha256`取当前策略标识，`case_ids`必须确实来自输入的调试报告：

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

## 采用和回滚

真实评测只沿用已有明确授权的CLI配置、材料和额度。离线工具的 `--evaluation-root` 可以导入已完成的四份真实报告，不发出新请求；模型、材料、版本和预算仍须一致。当前本轮没有新增真实调用，既有授权预算120次已经用满。

受控模型响应不能批准正式采用。合成评分必须标注 `SYNTHETIC_CONTROL`，只允许在仓库外、明确标记的验证注册表中验收；Go产品会拒绝这种注册表。正式采用需要真实报告及署名、带依据的目标用户或独立人工审阅。

```sh
# 仅当决策满足正式准入；当前本轮受控结果不能执行正式采用。
python3 scripts/defense-strategy.py adopt \
  --experiment /absolute/private/strategy-experiment-new \
  --decision /absolute/private/strategy-decision-new.json \
  --expected-registry-sha256 "核对过的注册表文件哈希"

python3 scripts/defense-strategy.py rollback \
  --target "已记录的旧策略标识" --reason "具体回滚原因" \
  --expected-registry-sha256 "核对过的当前注册表文件哈希"
./campustrace restart --build
```

采用只发布教学枚举及决策/实验哈希，不复制私人回答和模型凭据。注册表写入有互斥锁、当前版本检查和原子指针替换，旧版本保留。采用和回滚都形成Git可审阅的文件改动，提交发布另行执行。业务记录不随策略回滚撤销。

## 验证

```sh
GOWORK=off go test -race ./internal/agent ./internal/defensestrategy ./cmd/preparation-eval
python3 -m unittest discover -s scripts -p test_defense_strategy.py -v
python3 scripts/verify-defense-strategy.py --output /tmp/ct-strategy-verification-new
```

最后一个命令实际运行Go＋Python＋LangGraph、受控提案、调试和验证对照，保留无收益、语义退化、程序退化、额外调用和验证集失败；采用与回滚在隔离目录中验收。合成评分只测试门槛，正式评分保持未审阅。原准备、答辩和记忆回归继续使用既有验收脚本。[本轮实测](verification/defense-strategy-20261102/README.md)。
