# 校招来源扩展与验证

本轮增加百度和美团的应届生来源适配，以及关注源页面的三个公司预设。入口为“选择公司 → 预览范围和样例 → 设置关键词与周期 → 开始关注并导入”。选择及预览只读，周期导入不调用大模型。

## 读取范围

- 小红书：沿用公开枚举中唯一 regular 项目、完整分页和独立岗位详情。
- 百度：官网 `/httservice/getPostListNew` 公开表单查询 `recruitType=GRADUATE`，固定 10 岗一页；独立 `/httservice/getPostDetail` 核验原岗位编号和校招项目范围。
- 美团：官网 `/api/official/job/getJobList` 公开 JSON 查询 `jobType=[{code:"1",subCode:[]}]`，固定 10 岗一页；独立 `/api/official/job/getJobDetail` 核验编号和 jobType=1，拒绝实习/社招行。

任职要求与工作职责保存为来源原文，不因仍在列表中就推定可投递，不推定未公开的投递截止时间。岗位类型、公司、地点来自已校验的公开字段。编号、范围、每页数量、总页数、总数漂移和重复编号任一校验失败，整次发现失败。单来源保留现有 500 岗容量；关键词在完整列表后过滤，不能用关键词绕过完整扫描容量。

## 账号与抓取边界

用户创建的来源仍为 PRIVATE/MANUAL。服务端根据已识别网址推导 adapter 和 tenant；存储按账号、adapter、tenant 去重，创建关注和来源在同一事务完成。百度和美团不支持方向筛选，接口明确拒绝非空 direction。五个公司最低检查周期为 30 分钟，调度和页面使用相同下限；同一公司所有账号的预览、发现和详情共用每分钟 30 次限速。

新增适配复用公共 DNS 验证、固定公网地址连接、限速退让、失败冷却、一次网络/5xx 重试、响应大小上限、GET 条件缓存及原有 watch receipt。没有登录、上传简历或发送候选人信息，未改动代理限制与访问控制。

## 2026-10-05 验证记录

百度使用项目正式 PublicPlatform/PublicClient 完成只读外网验证：完整 159 岗分页，独立详情成功。此计数是验证快照，不是永久数量。

美团官方公开接口经当前机器的系统网络代理可以读取岗位列表与独立详情；分页、身份与范围校验通过固定数据测试。但项目正式抓取器明确禁用代理，本机 DNS 返回的公网地址连接超时，完整直连外网测试未通过。不得将代理下的访问结果或固定数据测试宣传为后台完整导入成功。预览返回网络错误时不创建关注；当前用户应优先使用百度和已可访问的小红书。

普通 CI 使用固定数据，不依赖第三方网站可用性。手动外网验证：

```sh
CAMPUS_LIVE_SOURCES=1 GOWORK=off go test -count=1 -run '^TestGraduateLiveReadOnly$' -v ./internal/source
```

来源官网：[百度](https://talent.baidu.com/jobs/campus)、[美团](https://zhaopin.meituan.com/web/campus?hiringType=1_1)、[小红书](https://job.xiaohongshu.com/campus/position)。接口结构依据上述官网发布的客户端代码与只读响应核验，后续结构变化需要更新适配器。

## 第二轮：京东与网易互联网

- 京东预设 `https://campus.jd.com/#/jobs?type=present`。GET `/api/wx/position/getProjectList` 只接受唯一已公开的 present 项目组，再对 POST `/api/wx/position/page?type=present` 每行 planId 核验；分页从 pageIndex=0 开始，totalNumber 必须存在，页数根据总数推导（官网 pageCount 当前返回 0，不能依赖它）。独立 POST `/api/wx/position/detail/{publishId}` 核验编号和 recruitType=应届生。
- 网易互联网预设 `https://campus.163.com/app/job/position?id=103`。先从公开导航核对“应届生 → 网易互联网2027届校园招聘”及对应链接，再完整读取 GET `/api/campuspc/position/getJobList` 的 projectId=103 范围。每行核验 projectId，原文通过官网发布的 positionIdList 参数按编号重读公开列表。仅使用列表中已公开的 positionDescription 与 positionRequirement；登录限制的 getJobDetails 接口不进入抓取流程，不使用凭据。网易互娱、雷火和实习项目不包含在此预设中。

2026-10-05 的正式抓取器外网验证：京东完整分页列出 124 岗，网易互联网列出 81 岗，两家首尾岗位原文核验通过。全部岗位元数据符合导入校验；这不是向用户数据库导入全部岗位的记录，实际创建关注仍由用户发起。京东全国岗位最多观察到 270 个去重地点，因此将导入地点上限从 30 调整至 300，并以 SQL 测试验证超过 30 的地点完整保存和超限拒绝。原文中的省市标记、职责与要求保留；元数据仅从明确的“省-市”格式提取城市用于偏好匹配，未知地点不猜测，单来源 500 岗、账号隔离、最低 30 分钟检查周期与站点限速沿用。

手动验证本轮两个来源：

```sh
CAMPUS_LIVE_SOURCES=1 GOWORK=off go test -count=1 -run '^TestPortalCampusLiveReadOnly$' -v ./internal/source
```

来源官网：[京东](https://campus.jd.com/)、[网易互联网](https://campus.163.com/app/job/position?id=103)。固定数据测试覆盖项目变更、实习混入、总数缺失/漂移、漏页、重复编号、详情身份错误、访问限制与严格 URL 范围。

本轮暂缓：小米官网校招查询返回 1059 岗，超过当前单来源容量；华为公开岗位请求在本机返回 403；不把可打开首页等同于可稳定导入。以上是本机验证时的快照，不代表企业没有校招岗位。
