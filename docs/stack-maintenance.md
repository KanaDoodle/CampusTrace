# 技术栈维护与职责审查

2026-11-01 对当前构建、依赖、任务配置、浏览器文件读取和双路径设计进行审查。调整围绕支持周期、实际执行职责与配置一致性展开，验证记录见[本轮验收](verification/stack-audit-20261101/README.md)。

## 已调整

| 发现 | 调整与作用 |
| --- | --- |
| Go 构建仍固定 1.25.9 | `go.mod`、应用与 Search 镜像、Compose、源码入口和本地工作区目标统一为 1.27.3；CI 从 `go.mod` 读取版本 |
| CI 测试及 Actions 仍使用旧 Node 运行时 | 前端测试切换 Node.js 26 LTS；checkout、setup-go、setup-node、setup-python 升级至 v7，动作自身使用 Node.js 24 |
| Kafka Worker 保留全局并发与 Stream idle 命名 | 删除不起作用的全局执行数量和冗余模式字段，使用通道并发与执行租约；旧全局环境变量给出迁移提示，旧租约环境变量继续兼容 |
| Kafka 两条链路接受无效端口 | 任务与岗位事件共用地址校验，启动时拒绝空主机、服务名端口及超出 1–65535 的数值 |
| 简历读取组件版本和传输历史注释落后于实现 | PDF.js 升级至 6.4.299，读取器与 Worker 同步；JSZip 升级至 3.10.2；发布包校验值、许可证及文件摘要留档，历史清理注释明确对应 SQL 任务日志 |

Go 的支持周期覆盖最近两个大版本；1.27.3 包含官方发布的安全修复。Node.js 26 属于 LTS，Node.js 20 已结束支持。[Go 版本政策与发布记录](https://go.dev/doc/devel/release)、[Node.js 发布状态](https://nodejs.org/en/about/previous-releases)。

## 保留的设计

- Redis 保存缓存、限流和短期状态；Kafka 投递任务与岗位事件，职责独立。Redis 7.4 仍处于官方支持周期。[Redis 版本维护政策](https://redis.io/docs/latest/operate/oss_and_stack/install/version-mgmt/)
- Search 故障时使用有预算的数据库回退；正常查询和故障恢复分别受限流与权限校验保护。
- 学习资料使用 BM25，可按用户授权启用语义检索；岗位全文搜索维护另一份派生索引，数据和用途不同。
- 后台任务统一使用 Kafka。可选 Search 栈也通过 Kafka 同步岗位索引并生成岗位变更通知。切换部署仍需协调 API、Worker 与消费者。
- Redis 旧队列读取器属于一次性升级工具，正常任务生产者和 Worker 不再使用 Streams。兼容旧数据的迁移代码保留。
- 分析链路已升级到独立仓库 KanaRPC 的公开提交，移除旧 KanaRPC-Go；etcd 客户端、Protobuf 与 gRPC 版本随模块图更新。依赖边界与验收见[迁移说明](kanarpc-migration.md)。历史栈审查报告保留其原始依赖快照。

## 复核方式

```sh
GOWORK=off go build ./...
GOWORK=off go vet ./...
python3 scripts/verify-integration-isolation.py --race --count 1 --adversarial
node --test web/*.test.cjs
make performance-test
```

浏览器读取组件的真实 PDF/DOCX 检查需要安装 Playwright 与 Chromium：

```sh
CAMPUS_PLAYWRIGHT_MODULE=/path/to/node_modules/playwright make browser-vendor-test
```

校验覆盖本地文件解析、脱敏、PDF 页数限制、DOCX 正文缺失和外部请求隔离。发布包完整性见 [浏览器依赖来源](../web/vendor/provenance.json)。历史验收和性能数据保留原工具链、源文件摘要与验证日期。
