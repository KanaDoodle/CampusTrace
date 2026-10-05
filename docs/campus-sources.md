# 校招来源扩展与验证

来源扩展已覆盖小红书、百度、美团、京东、网易互联网、阿里巴巴、哔哩哔哩与快手八个公司预设。入口为“选择公司 → 预览范围和样例 → 设置关键词与周期 → 开始关注并导入”。选择及预览只读，周期导入不调用大模型。

## 读取范围

- 小红书：沿用公开枚举中唯一 regular 项目、完整分页和独立岗位详情。
- 百度：官网 `/httservice/getPostListNew` 公开表单查询 `recruitType=GRADUATE`，固定 10 岗一页；独立 `/httservice/getPostDetail` 核验原岗位编号和校招项目范围。
- 美团：官网 `/api/official/job/getJobList` 公开 JSON 查询 `jobType=[{code:"1",subCode:[]}]`，固定 10 岗一页；独立 `/api/official/job/getJobDetail` 核验编号和 jobType=1，拒绝实习/社招行。

任职要求与工作职责保存为来源原文，不因仍在列表中就推定可投递，不推定未公开的投递截止时间。岗位类型、公司、地点来自已校验的公开字段。编号、范围、每页数量、总页数、总数漂移和重复编号任一校验失败，整次发现失败。单来源保留现有 500 岗容量；关键词在完整列表后过滤，不能用关键词绕过完整扫描容量。

## 账号与抓取边界

用户创建的来源仍为 PRIVATE/MANUAL。服务端根据已识别网址推导 adapter 和 tenant；存储按账号、adapter、tenant 去重，创建关注和来源在同一事务完成。百度和美团不支持方向筛选，接口明确拒绝非空 direction。八个公司最低检查周期为 30 分钟，调度和页面使用相同下限；同一公司所有账号的预览、发现和详情共用每分钟 30 次限速。

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

## 第三轮：阿里巴巴；Bilibili 初次核验受阻

阿里巴巴预设为 `https://campus-talent.alibaba.com/campus/position?batchId=100000760001`，限定“阿里巴巴2027届应届生”。普通访问者打开官网页面即可获发匿名 Cookie 和 CSRF 令牌，随后调用该官网发布的 POST `/searchCondition/listBatch`、`/position/search`、`/position/detail` 只读查询。不是登录，也不使用用户浏览器会话；每个预览、发现、详情操作独立创建内存 CookieJar，操作结束不保留，不写入数据库、响应缓存或日志。只允许同源 HTTPS 重定向，沿用公网 DNS 与代理禁用边界。启动页不缓存，岗位查询均为 POST；启动页和查询都计入同一公司每分钟 30 次限速。

从 graduate 分类核对固定批次的编号、名称与 type；列表和详情核对 batchId、batchName、categoryType=freshman、status=recruit 与原岗位编号。列表接口接受每页 50 条，1-based 分页；校验实际 pageSize/currentPage、必需 totalCount、每页行数、总数一致与重复编号，单源上限仍为 500。468 个岗位一次完整发现需要 12 个请求（启动页、批次、10 页列表），关键词在完整扫描后过滤。官网列出的一个或多个业务集团保留在来源原文，不猜测多个集团中的实际雇主；保留职责、要求与明确公开的毕业时间范围。仍在列表中不等于已核实投递时间。

2026-10-05 的正式 PublicPlatform/PublicClient 只读外网验证：完整列出 468 岗，首岗 `199907740040`、尾岗 `199907720007` 的独立详情原文均通过核验，全部元数据符合导入校验。计数仅为验证快照，没有创建用户关注或向用户数据库批量导入。普通 CI 使用固定数据，覆盖分页截断、总数变化、重复岗位、实习混入、详情身份/范围错误、空原文、会话访问限制、越域重定向与令牌不持久化。

```sh
CAMPUS_LIVE_SOURCES=1 GOWORK=off go test -count=1 -run '^TestAlibabaLiveReadOnly$' -v ./internal/source
```

初次探测 Bilibili 官网和匿名会话接口时出现 HTTP 412、连接超时及缺少 ajSessionId 的响应，当时未将其加入预设。第四轮已查明公开请求初始化差异并完成接入，当前状态以以下记录为准。

来源官网：[阿里巴巴](https://campus-talent.alibaba.com/campus/position?batchId=100000760001)、[Bilibili](https://jobs.bilibili.com/campus/positions)。

## 第四轮：哔哩哔哩已接入

官网页面无需登录即可显示应届生岗位。公开客户端先以 X-UserType=2、X-AppKey=ops.ehr-api.auth 调用 GET `/api/auth/v1/csrf/token`，将返回的匿名令牌作为后续 X-CSRF 请求头。以上初始化常量来自官网公开客户端，不是个人密钥。每个预览、发现或详情操作复制客户端并创建独立内存 CookieJar，不读取浏览器登录信息，令牌端点禁止持久化与条件缓存，令牌不进入结构化日志。限制为 jobs.bilibili.com 同源 HTTPS 请求与跳转；不改变代理禁用、公网 DNS 整体核验和响应大小边界。

预设 `https://jobs.bilibili.com/campus/positions?type=3`，接受该路径无查询参数的官网入口。Freshmen=3、Intern=0 是官网发布的枚举；POST `/api/campus/position/positionList` 同时限定 recruitType=1、workTypeList=["3"]、positionTypeList=["3"]，仅处理列表公开应届生范围，不扩展到单独的 B-STAR/其他专项页面。元数据核验正编号、校招项目、recruitType=1 与“全职”类型；官网主动隐藏的标记名拒绝导入。独立 GET `/api/campus/position/detail/{id}` 核验原编号、positionType=3 和 recruitType=1，保留岗位描述（职责与要求）、公开的网申开始/截止日期与毕业范围，不推定页面存在即仍可投递。

固定每页 10 条，完整扫描后按标题/地点关键词筛选。官网最后一页目前返回 total=89、size=9、pages=10，因为它根据本页行数计算页数；适配器根据总数和请求大小推导 9 页，核验每页确切行数、总数一致与重复编号，不能依赖不稳定的 pages/size。缺页、范围变化与容量超过 500 均使整次发现失败，不把部分扫描当成成功导入。

网络探测发现某些 DNS 结果包含 8 个不可达公网地址，顺序尝试每个 2 秒会耗尽请求/任务时限。正式传输层仍先核验全部 DNS 答案，再最多两个并行 TCP 尝试；每个尝试仍限制 2 秒，连接仍固定在已验证的 IP，选出成功连接后取消并关闭其他连接。不增加并发 HTTP 查询或重试次数。测试覆盖整个 DNS 答案包含私网时零连接、不可达首地址、并发上限、失败汇总、取消与落败连接关闭。

2026-10-05 正式 PublicPlatform/PublicClient 验证：默认网络策略、每个操作 15 秒任务时限内，完整列出 89 岗，首岗 `30712`、尾岗 `29370` 的独立详情均成功；全部元数据通过导入校验。本机与应用 Docker 网络中的独立只读探针均取得相同结果。快照计数会变化。没有向用户数据库创建关注或批量录入岗位，也没有模型调用。普通 CI 使用固定数据并覆盖匿名会话隔离、令牌不缓存、访问限制、实习混入、总数漂移、分页截断、重复编号、详情身份/范围错误与跨域拒绝。

```sh
CAMPUS_LIVE_SOURCES=1 GOWORK=off go test -count=1 -run '^TestBilibiliLiveReadOnly$' -v ./internal/source
```

来源官网：[哔哩哔哩应届生招聘](https://jobs.bilibili.com/campus/positions?type=3)。

## 第五轮：快手 2027 届应届生

预设 `https://campus.kuaishou.cn/recruit/campus/e/#/campus/jobs?recruitSubProjectCodes=20271779425607`，可从同路径首页归一。官网应届生与留用实习采用独立项目编号：应届生 20271779425607、留用实习 20271772783534。只处理已核验的应届生范围，首页、无条件岗位页均归一到该预设，其他筛选、内推参数和专项入口拒绝；不把标题含有“校招”当作范围依据。

先 GET `/recruit/campus/e/api/v1/open/sub-project/findByCode?code=20271779425607`，核对 code、name=2027应届生、year=2027、projectType=fulltime 和 active=true；项目停用或范围变化时明确失败。POST `/recruit/campus/e/api/v1/open/positions/simple` 只传项目数组、pageNum 和 pageSize，每行核验 schoolr、对应项目、fulltime、Release 和 ifShowRecruitWebsite=true。完整分页后才按标题/地点关键词筛选。独立 GET `/recruit/campus/e/api/v1/open/positions/find?id={id}` 核验原编号与同一范围，保存职责 description、要求 positionDemand、公开地点；再按公开 positionId 参数读取该岗位的毕业时间范围。缺少可读职责或要求时不当作成功详情。

官网客户端的 pageSize 可由查询参数设置，实测支持每页 50 条。279 岗只需 6 次列表请求，项目核验另计 1 次；相较默认每页 10 条的 28 次列表请求，减少限速与任务超时压力。核对每页页号、页大小、页数、精确行数与总数，重复编号、缺页、总数漂移、实习混入或超过单源 500 岗均使完整发现失败。所有请求沿用同一公开传输层的连接复用、公网 DNS 校验、响应大小限制与错误退让；不增加站点限速，不扩大任务时限。

这些公开查询不要求登录、Cookie 或令牌。按操作复制 HTTP 客户端并禁用 CookieJar，连接池仍复用；限制同源 HTTPS 请求与跳转，不读取用户会话、简历或模型设置。来源仍为账号私有 MANUAL，重复创建复用原关注，最低检查周期 30 分钟，同站点全部账号共用每分钟 30 次来源限速，周期检查不自动调用模型。没有公开投递截止时间时保留未知状态，不把 Release、项目开始时间或岗位发布日期作为仍可投递的依据。

2026-10-05 正式 PublicPlatform/PublicClient 验证：每个操作使用 15 秒任务时限，完整读取 279 岗，首岗 13991、尾岗 12778 的独立详情及公开毕业范围成功；所有岗位元数据通过导入校验。本机与应用 Docker 网络中的只读探针结果一致。数量为本次快照，没有向真实账号创建关注或批量录入岗位。固定数据测试覆盖项目错配、停用、实习与社招混入、展示标识缺失、截断、分页不一致、总数漂移、重复编号、容量、职责/要求缺失、限速时不返回部分结果、无凭据查询与严格 URL 范围；SQL 测试验证私有来源、去重与调度下限。

```sh
CAMPUS_LIVE_SOURCES=1 GOWORK=off go test -count=1 -run '^TestKuaishouLiveReadOnly$' -v ./internal/source
```

来源官网：[快手校园招聘](https://campus.kuaishou.cn/recruit/campus/e/)。
