# 技术要求：原文、Analysis、缓存与资格判断

日期：2026-10-31。本轮修复默认规则提取及其下游判断，没有调用外部模型，没有批量修改现有账号或历史证据。

## 已复现的问题

1. 明确的 C/C++ 未被默认规则提取；“Go 或 Rust 任一种”却会被替换成固定的 `go|java|c++`。
2. 资格判断直接按 `+` 拆条件，`REQUIRED:c++` 会被拆成 C 和空操作数，导致填写 C++ 技能的用户也失败。
3. `REQUIRED:go|` 曾经可以通过，`go++java` 被当成明确不符；Go 与 Golang 也无法对应。
4. `go+c|c++` 中 C/C++ 可选，不表示 Go 可选；旧 GoFit 只看有没有 `|`，会误标为语言任选。

原始失败日志随 evidence.tar.gz 保存。没有仅凭代码阅读推断这些问题。

## 实现与约束

- 默认提取支持常见编程语言的词边界及 Go/Golang、C++/cpp 等别名；语言列表取自原文，保留职责、优先、开放举例与学习安排的区别。
- 明确必须、或有连续原文支持的任职条件使用 `REQUIRED:`；跨行时保留实际标题及中间原文，不拼接摘录。超出证据或值预算时返回 schema 错误，不截断后当作满足。
- 混合且/或、未解析的语言选项、“任意两条”等跨条件选择使用 `UNCERTAIN:`。资格为 `UNKNOWN`，不凭关键词直接判定不符。
- 条件沿用组间 AND、组内 OR 的协议：`go+c|c++` 是 Go 且 C/C++ 任一。专门保留 C++ 的字面量，空操作数或不支持的表达式为 `UNKNOWN`。不同证据原有的冲突保护保留。
- `claims-v4-tech-requirements` 与 `rules-v7-tech-expressions` 隔离旧缓存、回执和评估结果。旧 `claims-v1` 配置仍映射到有效新命名空间；历史证据保留，已知旧代重放不能重新激活。
- RPC 请求与响应携带实际 claims 语义版本。新服务在调用 Extractor 前拒绝显式不匹配；新客户端拒绝无版本或旧版本响应，防止混合部署时把旧提取结果写进新缓存。旧调用方省略请求字段仍可接收新服务的响应。

没有修改权限、事务、输入 CAS 或处理代际校验。升级需要协调 Analysis 与 Worker，版本不一致时按既有失败/未知结果处理，不自动重放潜在付费调用。未重新处理的旧 Observation 不会自动变成新提取结果；本轮没有发起批量重做。

## 同输入前后回放

同一公开 API 测试 harness、同一最终 fixture、Go 1.27.1 与发布依赖（`GOWORK=off`）：

| 实现 | 定向检查通过数 | 外部模型调用 |
|---|---:|---:|
| 冻结基线：claims-v3 / rules-v6 | 21 / 85 | 0 |
| 候选：claims-v4 / rules-v7 | 85 / 85 | 0 |

85 项为 14 个用例 × 6 份合成技能资料的技术条件断言，加 1 项 GoFit 断言。用例包括 4 段本地历史公开 JD 摘录和 10 个合成控制；原始 snapshot 哈希及来源链接记录在 [fixture.json](fixture.json)。摘录不含招聘联系人信息，不表示当前岗位仍开放。

标签由助手检查，**不是独立人工金标准，也不是模型准确率或完整岗位匹配准确率**。检查只针对技术条件，不代表学历、届别、经验等全部资格，更不代表录用概率。基线执行副本只增加同一个测试 harness 与 fixture，业务源码未修改。

### 标签复核记录

初版把 s09 只按 C/C++ 语言作参考判断，遗漏同句的 Visual Studio 环境要求，候选出现 79/85。重新阅读原文后，按本轮“不完整表达式整体待核对”的保守策略，将该片段参考状态调整为 `UNKNOWN`。片段文字没有修改；初版 fixture、失败结果和 `reference-review.json` 均保留。之后基线与候选使用同一份修订 fixture 重跑。没有将不支持的环境要求强行解析为已满足。

这套规则不覆盖任意自然语言条件、任意技术名词或版本熟练程度。复杂招聘要求仍需核对完整材料；整体模型分析继续沿用完整 JD 路径。

## 其他验证

- `GOWORK=off go test -race -count=1 ./...`、`go vet ./...`、`go build ./...` 通过。
- 隔离 MySQL/Redis/etcd 的脚本共 32 项顶层检查通过，包含 2 项新增集成测试、真实双实例 KanaRPC、Worker Outbox RPC、输入 CAS 及迟到旧代保护。
- 集成测试预置旧 receipt、旧 Redis 错误 claims；旧缓存未压制新提取，重复处理未再次调用分析器，迟到旧结果只保留历史，Rust 资料消费新证据后通过本例资格检查。
- RPC 检查确认版本不匹配时 Extractor 调用数为 0；旧响应 JSON 缺少版本时被新客户端拒绝。真实 RPC 回归证明当前版本字段能通过发布依赖传输。
- 20 秒原文绑定 fuzz：102,634 次执行通过；20 秒表达式往返 fuzz：3,006,729 次执行通过。这些是有限测试，不是完整语义正确性的证明。

## 源码与复现

基线是上一轮工作区的冻结候选，父提交为 `f17b1ae8df67eaadbfe4ad93e2ad01c6f97b5460`，不能用该父提交直接代替本轮基线。

- 基线运行源码指纹：`786e24602941c1f8d2a77cffabac3a015609d58547a71df46aa2a43c7c5588d6`。
- 候选运行源码指纹：`b5a3a6b2f6690c7be7c7e4026a0695900fc5d3f003c1e87a59b4b90ea7791c43`。
- 13 个运行源码/测试/fixture 文件变化，基线另外 616 个文件不变。差分见 [upgrade.patch](upgrade.patch)。
- 基线源码复用[上一轮源码归档](../search-recovery-20261031/source-snapshots.tar.gz)的 `candidate/`，本轮保存差分与逐文件哈希，避免重复保存相同源码。

在仓库根目录执行：

```sh
GOWORK=off go test -race -count=1 ./internal/domain ./internal/analysis ./internal/rules ./internal/pipeline
GOWORK=off go test -count=1 -v ./internal/analysis -run '^TestTechnologyHistoricalAndSyntheticReplay$'
ADVERSARIAL_TESTS='^(TestTechnology.*|TestMyRPC.*|TestRunningWorkerOutboxRPC.*|TestReleaseAnalysisVersionDowngrade|TestReleaseActiveGenerationConsumersAndInputCAS|TestReleaseParserReceiptReconciliation)$' sh scripts/adversarial-local.sh
python3 docs/verification/technology-requirements-20261031/verify.py
```

`verify.py` 校验原始输出和 fixture 哈希，从上一轮归档提取基线并应用本轮差分，逐文件核对候选。可选 `--check-worktree /绝对路径/campustrace` 仅验证工作区与本次候选完全一致。历史归档本身不要求后续分支保持不变。
