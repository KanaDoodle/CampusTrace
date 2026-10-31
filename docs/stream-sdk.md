# 流式 SDK 的版本化接入

2026-10-31。当前公开依赖仍为 KanaRPC-Go v0.1.0；工作区新增流式代码尚未公开发布。这里交付的是标准模块候选包、独立消费验证和已发布版本的构建入口。**真实 vLLM / GPU 未验证。**

日常流式依据说明已可直接使用 `LLM_URL / LLM_MODEL / LLM_API_KEY`，默认 `NARRATION_BACKEND=api`，无需本页的未发布 SDK 或推理网关。见[模型后端选择](model-backends.md)。本页 SDK 仅用于显式 `NARRATION_BACKEND=rpc` 的推理实验；没有自动回退或自动切换服务商。

## 三种状态

| 状态 | 入口 | 模块/发布边界 |
| --- | --- | --- |
| 常规发布构建 | `GOWORK=off go build ./...` | 根 go.mod 固定已发布 v0.1.0；说明功能默认关闭 |
| 版本化候选验收 | `build-stream-sdk.py --candidate` | 本地未发布候选；独立 module cache、显式版本和内容校验，无 workspace/replace |
| 已公开版本构建 | `build-stream-sdk.py --version` | 从公开 Go proxy + checksum database 实际下载成功才构建；未发布/不支持版本失败，不回退 |

`kanarpc_stream` 是可选功能的构建开关，不再要求使用相邻源码。两种流式构建都会生成独立的 `campustrace-stream.mod/.sum`，保留根 go.mod/go.sum 和现有默认 CI。当前候选为 **v0.2.0-rc.4，未发布**；候选版本号不是远端可安装性的证明。

## 本地候选与独立接入

先在 RPC 项目生成新的、不可覆盖的候选目录：

```sh
python3 ../KanaRPC-Go/scripts/package-sdk.py --version v0.2.0-rc.4 --output /tmp/kanarpc-rc4
python3 scripts/build-stream-sdk.py --candidate /tmp/kanarpc-rc4 --output /tmp/campustrace-stream-build
```

第一条只打包，不提交、不打 tag、不推送。正常发布准备应从干净的已提交源码加 `--require-clean`；本轮脏工作区产物明确记录 `source_dirty=true`。包包含标准 Go 模块源文件集及许可，排除本地 workspace、凭据、嵌套模块、vendor，拒绝源代码符号链接。每个文件和交付物都有 SHA-256；消费者还由 Go 计算模块 `h1` 内容校验。

构建产物 `/tmp/campustrace-stream-build/api` 可按现有 API 配置运行，并显式设 `NARRATION_BACKEND=rpc`、`INFERENCE_RPC_ADDRESS` / `INFERENCE_MODEL`。无模型联调必须设 `INFERENCE_BACKEND_KIND=fixture-no-model`。候选 SDK 只下载到本次私有缓存，不进入常规公开模块缓存。它的本地 `GONOSUMDB` 例外不应复制到已发布版本或生产构建。

独立消费验收会复制一个不带 `.git` / `go.work` / 相邻 RPC 项目的 CampusTrace 源码快照，直接从候选模块编译 API、worker 和 gateway，并实际运行完整链路：

```sh
python3 scripts/verify-stream-sdk.py --candidate /tmp/kanarpc-rc4 --output /tmp/ct-sdk-acceptance
# 已在临时目录安装 Playwright/Chromium 时，可增加：
# --browser /absolute/path/node_modules/playwright
```

结果记录 `workspace=off`、`replace=false`、实际 module cache 路径、版本和 h1。即使没有相邻 RPC 仓库也可以消费这个候选。端到端使用无模型 fixture，不证明真实推理质量或性能。

## 已发布 SDK 的正式构建入口

**以下命令只适用于将来已经公开发布的版本，本轮 rc.4 在公开 proxy 查询为 unknown revision。**

```sh
python3 scripts/build-stream-sdk.py --version ALREADY_PUBLISHED_V0_2_VERSION --output /tmp/ct-published-sdk-build
```

构建入口强制关闭 workspace，从公开 proxy 和 checksum database 验证版本，使用新的 module cache，不接受 sibling replace。版本不存在时直接失败，不调用其他模型、不自动选择 latest，不改根依赖。独立的手动 CI `Published streaming SDK acceptance` 使用相同入口；它没有在远端执行，也不自动部署。

如之后要让默认构建也使用流式 SDK，需在公开发布验证完成后单独提交根 go.mod/go.sum、更新 `verify-release-boundary.sh` 的固定发布版本和默认 CI，再跑常规/流式两个构建。当前没有把不存在的版本写进根 go.mod。

## 测试隔离入口

```sh
python3 scripts/verify-integration-isolation.py --output /tmp/ct-isolation --count 2 --shuffle 60431
python3 scripts/verify-integration-isolation.py --output /tmp/ct-isolation-race --race --count 2 \
  --sdk-candidate /tmp/kanarpc-rc4
```

脚本创建独占 SQL/Redis/etcd 容器，使用随机端口，不读 `.env`。每个测试作用域使用随机数据库和仅有该库权限的账户；同一用例内多个 setup 共享数据，独立子测试和 `-count` 轮次不共享。启动子进程时沿用本用例 DSN。迁移测试同样每轮新建空库；恢复测试通过显式 `MYSQL_TEST_CONTAINER` 执行真实 dump/import，使用实际发布端口。

脚本不需要 `-skip`：迁移和恢复都实际执行。结束前核对没有遗留测试库/账户，且模板库哨兵未改；然后仅删除本次服务。手动使用现有**独占测试服务**时，需显式提供 `MYSQL_TEST_ADMIN_DSN`；管理员只创建随机测试资源，不清空模板数据库。`t.Setenv` 为子进程传递 DSN，这些测试暂不使用 `t.Parallel`。

实现和运行结果见 [M4 收尾报告](verification/rpc-agent-m4/closeout/report.md)。Go 模块格式与版本语义依据 [Go Modules Reference](https://go.dev/ref/mod#module-proxy)。
