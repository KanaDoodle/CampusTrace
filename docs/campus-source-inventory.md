# 公司招聘来源清单

核验日期：2026-10-05。共收集 37 家公司，包含 11 个已实现的公司预设与 26 家候选。美团已有适配器，但本机正式直连仍受限；不能将 11 个预设解读为 11 家在所有网络环境下都可用。

状态以官网页面、官网发布的客户端和公开只读请求为依据。候选公司的入口收集与完整导入验证是两个阶段；搜索缓存中的岗位数量、旧年份简章和全集团品牌名均不能代替当前招聘项目边界。本轮没有创建用户关注、自动导入所有公司岗位或调用大模型。

可编辑的结构化清单：[CSV](campus-source-inventory.csv)。正式接入协议、验证命令和使用方式：[来源验证记录](campus-sources.md)、[README](../README.md#job-radar-v02)。

## 已实现的公司预设

| 公司 | 类型 | 入口 | 核验结果 |
| --- | --- | --- | --- |
| 小红书 | 互联网 | [官网入口](https://job.xiaohongshu.com/campus/position) | 已接入：常规应届校招；支持官网方向筛选。此前正式抓取验证见 campus-sources.md。 |
| 百度 | 互联网 | [官网入口](https://talent.baidu.com/jobs/list?recruitType=GRADUATE) | 已接入：GRADUATE 应届生；此前正式抓取器验证 159 岗。 |
| 美团 | 互联网 | [官网入口](https://zhaopin.meituan.com/web/campus?hiringType=1_1) | 已接入但本机直连受限：适配器与固定数据测试已有；代理网络可读，但正式抓取器禁用代理，本机直连超时。 |
| 京东 | 互联网 | [官网入口](https://campus.jd.com/#/jobs?type=present) | 已接入：present 应届生项目；此前正式抓取器验证 124 岗。 |
| 网易互联网 | 互联网 | [官网入口](https://campus.163.com/app/job/position?id=103) | 已接入：2027届互联网项目103；此前验证81岗，不包含互娱、雷火。 |
| 阿里巴巴 | 互联网 | [官网入口](https://campus-talent.alibaba.com/campus/position?batchId=100000760001) | 已接入：2027届应届生固定批次；此前验证468岗。 |
| 哔哩哔哩 | 互联网 | [官网入口](https://jobs.bilibili.com/campus/positions?type=3) | 已接入：公开应届生范围；此前正式抓取器验证89岗。 |
| 快手 | 互联网 | [官网入口](https://campus.kuaishou.cn/recruit/campus/e/#/campus/jobs?recruitSubProjectCodes=20271779425607) | 已接入：2027应届生；此前正式抓取器验证279岗。 |
| OPPO | 消费电子与制造 | [官网入口](https://careers.oppo.com/university/oppo/campus/post?recruitType=Graduate) | 本轮接入并完整验证：项目30；正式抓取器完整118岗与首尾详情，包含后端岗位。 |
| 西门子 | 外企与工业 | [官网入口](https://jobs.siemens.com.cn/siemens/position/index?recruitmentType=CAMPUSRECRUITMENT) | 本轮接入并完整验证：中国官网校招分类；正式抓取器完整42岗与首尾详情。 |
| 海尔集团 | 制造业 | [官网入口](https://maker.haier.net/client/campusmobile/activity/id/68/fid.html) | 本轮接入并完整验证：集团2027校招项目68；正式抓取器完整102岗与首尾详情。 |

## 互联网、通信与消费电子候选

这些公司尚不能通过 CampusTrace 页面自动导入。

| 公司 | 入口或线索 | 状态与下一步 |
| --- | --- | --- |
| 腾讯 | [入口](https://join.qq.com/) | 待适配：官网HTML可访问，首批公开客户端资源请求403；尚未核验完整校招分页/详情，不以单一业务部门门户代替腾讯全量。 |
| 字节跳动 | [入口](https://jobs.bytedance.com/campus/position) | 待适配：公开HTML和客户端资源可读；尚未完成招聘项目、全分页和详情的正式抓取验证。 |
| 携程/Trip.com | [入口](https://careers.ctrip.com/) | 待适配：官网与客户端校招路由可读；已找到公开getJobAd查询线索，完整参数与校招范围尚未核验。 |
| 360集团 | [入口](https://360campus.zhiye.com/campus/jobs) | 待适配：北森页面可读；需验证应届/实习及年份范围，不能用带筛选/内推参数的少量岗位声称全量。 |
| 滴滴 | [入口](https://talent.didiglobal.com/campus/) | 访问受限，待适配：官网明确链接campus.didiglobal.com的Moka校招入口；本机公开入口出现重定向循环。 |
| vivo | [入口](https://hr.vivo.com/) | 待适配：官网与客户端资源可读；另有hr-campus.vivo.com北森校招站点，尚未完整核验岗位项目与分页。 |
| 联想 | [入口](https://talent.lenovo.com.cn/) | 待适配：官方应届生、人才项目、实习分类明确；HTML与客户端入口可读，公开岗位接口尚未完整核验。 |
| 华为 | [入口](https://career.huawei.com/cn/campus-recruitment) | 访问受限，待适配：官方2027毕业范围可查；此前岗位请求在本机返回403，不把首页可读等同于列表可导入。 |
| 小米 | [入口](https://hr.xiaomi.com/) | 容量限制，待适配：此前公开校招查询1059岗，超过当前单来源500岗上限；需按官网明确项目拆分，不能靠关键词绕过容量。 |

## 外企候选

这些公司尚不能通过 CampusTrace 页面自动导入。

| 公司 | 入口或线索 | 状态与下一步 |
| --- | --- | --- |
| 博世 | [入口](https://www.bosch.com.cn/careers/) | 访问受限，待适配：官方链接Moka应届生入口app.mokahr.com/campus-recruitment/bosch/73873；本机出现重定向循环。全球jobs.bosch.com不等于中国校招范围。 |
| ABB | [入口](https://careers.abb/china/zh/) | 待适配：官方中国职业页可读；需核验毕业生项目与岗位公开接口，不以投递时需要账号推断读岗位也需要登录。 |
| 施耐德电气 | [入口](https://www.se.com/cn/zh/about-us/careers/students-and-young-professionals.jsp) | 访问受限，待适配：中国毕业生页面与全球careers.se.com/jobs在本机请求403，未完成职位读取验证。 |
| SAP | [入口](https://jobs.sap.com/) | 访问受限，待适配：官方岗位入口在本机请求403；需验证中国地点、毕业生/实习项目及完整岗位读取。 |
| Intel | [入口](https://jobs.intel.com/en/locations-china) | 访问受限，待适配：官方中国职位入口在本机请求403；未完成毕业生范围与分页验证。 |
| IBM | [入口](https://www.ibm.com/careers/search) | 待适配：官方搜索页面可读，公开职位入口careers.ibm.com；需核验中国地点和毕业生项目，不能混入全球社招。 |
| 爱立信 | [入口](https://jobs.ericsson.com/careers) | 待适配：官方职位搜索HTML可读；需核验中国地点、毕业生范围与完整分页/详情。 |

## 制造业候选

这些公司尚不能通过 CampusTrace 页面自动导入。

| 公司 | 入口或线索 | 状态与下一步 |
| --- | --- | --- |
| 比亚迪 | [入口](https://job.byd.com/portal/mobile/school-home) | 待适配：官方2027应届、博士、实习、外派分类明确；PC公开客户端可读，列表接口和各项目边界尚未完整核验。 |
| 美的集团 | [入口](https://careers.midea.com/schoolOut) | 待适配：生产站点schoolOut与公开客户端可读，发现school/position/common/project/list线索；尚未完整核验，不使用SIT测试站点。 |
| 宁德时代 | [入口](https://talent.catl.com/) | 访问受限，待适配：官网Moka招聘入口talent.catl.com/social-recruitment/catlhr/96144公开页面有2027校招栏目；本机入口重定向循环，具体校招项目尚未核验。 |
| 三一集团 | [入口](https://sanycampus.zhiye.com/) | 待适配：官方北森门户可查，sany.zhiye.com页面也可读；校招与实习项目、全分页/详情待验证。 |
| 汇川技术 | [入口](https://inovance.zhiye.com/campus) | 待适配：北森HTML入口可读；公开旧项目说明仅供渠道识别，当前2027项目与岗位API尚未核验。 |
| 海康威视 | [入口](https://campushr.hikvision.com/school?schoolType=nozxf) | 待适配：官方2027校招页面与FAQ可查；此前探测campus.hikvision.com并非已确认入口，需改用campushr核验接口。搜索缓存岗位数不作实时总数。 |
| 荣耀 | [入口](https://www.honor.com/cn/career/) | 待适配：官方2027校招公告与校招链接可查；career.honor.com根路径当前默认社招，必须使用明确school页面，尚未完整核验公开API。 |
| 吉利汽车/控股 | [入口](https://www.geelyautogroup.com/recruitment) | 入口收集，项目待核验：官网列校园招聘渠道；各集团和子公司的校招入口需分别确认，不能把其他集团或社会招聘门户当作全量校招。 |
| 上汽集团 | [入口](https://www.saicmotor.com/chinese/rlzy/rcxq/xyzp/index_xz2.shtml) | 待适配：官方校园招聘页汇总子公司入口，另有saic-recruit.saicmotor.com含校招/实习的岗位列表；需核验岗位类型与子公司范围。 |
| 格力电器 | [入口](https://www.gree.com/) | 入口线索待确认：公司官网与旧北森招聘渠道为线索；本机job.gree.com候选域名TLS连接失败，尚未确认当前正式校招列表，不标记为已核验招聘入口。 |

## 后续接入顺序

优先验证联想、美的、海康威视等公开客户端与校招项目边界明确的来源；随后完善字节、360、携程及北森/Moka公共查询适配。外企优先选择明确中国地点和毕业生范围的岗位，制造业优先关注软件研发、数字化、工业平台、云服务等与后端求职相关的分类。官网访问限制和超容量来源保留诊断，待条件满足后重验。

正式进入预设须同时满足：公开可读的项目边界、完整分页或明确终止协议、稳定原始编号、独立岗位原文核对、500岗容量与任务时限、正式抓取器直连验证、固定数据异常测试、私有来源登记和限速。不同批次和子公司可在官网提供明确编号时拆分；不依据岗位名称猜测应届身份、毕业年份或签约主体。
