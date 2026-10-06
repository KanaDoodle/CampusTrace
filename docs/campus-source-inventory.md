# 公司招聘来源清单

更新日期：2026-10-06。网页目录共 75 个招聘来源，其中 41 个有自动导入适配器，34 个保留官网入口及接入限制。公司、事业群和招聘单位分别计作来源；不是 75 家都能自动抓取。美团已有适配器但本机正式直连受限；汇川、招银网络科技核验时对应校招分类返回 0 岗。数量与当前可用性以实时预览为准。

进入“关注源”，按公司名称搜索，或按互联网、游戏、金融科技、国央企、制造业与消费电子、外企筛选。自动来源先选择并预览，再创建关注；只有官网入口的来源不能自动导入。电信、移动、平安按招聘单位接入，网易互联网、互娱、雷火也是独立项目。同花顺校招含实习转正岗位，不能统一视作全职。米哈游限定2027届应届生项目；平安、移动按官网应届/校招分类读取，具体届别见岗位原文。新增恒生、阅文按官网校招分类读取；吉比特与雷霆限定2027秋招，保留提前实习要求。

本轮补上腾讯的 2027 常规中国校招；此前接入的 TCL 流程与数字化转型中心、鸿鹄实验室，以及中国电子旗下麒麟软件/中电云也继续保留。TCL 固定 2027 项目；中国电子限定两个招聘单位，具体届别以岗位原文为准。

机器可读清单：[campus-source-inventory.csv](campus-source-inventory.csv)。抓取协议与验证范围见 [campus-sources.md](campus-sources.md)。

## 已实现的 41 个自动导入预设

| 来源 | 类型 | 官网入口 | 状态与范围 |
| --- | --- | --- | --- |
| 小红书 | 互联网 | [官网入口](https://job.xiaohongshu.com/campus/position) | 已接入：常规应届校招；支持官网方向筛选。此前正式抓取验证见 campus-sources.md。 |
| 腾讯 | 互联网 | [官网入口](https://join.qq.com/post.html) | 已接入：限定 2027 常规应届校招、官网中国工作地点分类；不含实习、青云专项和海外 Workday 岗位。2026-10-06 正式抓取器完整读取 105 岗，首尾独立详情核验通过，保留毕业范围、职责、要求和加分项。 |
| 百度 | 互联网 | [官网入口](https://talent.baidu.com/jobs/list?recruitType=GRADUATE) | 已接入：GRADUATE 应届生；此前正式抓取器验证 159 岗。 |
| 美团 | 互联网 | [官网入口](https://zhaopin.meituan.com/web/campus?hiringType=1_1) | 已接入但本机直连受限：适配器与固定数据测试已有；代理网络可读，但正式抓取器禁用代理，本机直连超时。 |
| 京东 | 互联网 | [官网入口](https://campus.jd.com/#/jobs?type=present) | 已接入：present 应届生项目；此前正式抓取器验证 124 岗。 |
| 网易互联网 | 互联网 | [官网入口](https://campus.163.com/app/job/position?id=103) | 已接入：2027届互联网项目103；此前验证81岗，不包含互娱、雷火。 |
| 阿里巴巴 | 互联网 | [官网入口](https://campus-talent.alibaba.com/campus/position?batchId=100000760001) | 已接入：2027届应届生固定批次；此前验证468岗。 |
| 哔哩哔哩 | 互联网 | [官网入口](https://jobs.bilibili.com/campus/positions?type=3) | 已接入：公开应届生范围；此前正式抓取器验证89岗。 |
| 西门子 | 外企与工业 | [官网入口](https://jobs.siemens.com.cn/siemens/position/index?recruitmentType=CAMPUSRECRUITMENT) | 已接入：中国官网校招分类；此前正式抓取器完整42岗与首尾详情。 |
| 海尔集团 | 制造业 | [官网入口](https://maker.haier.net/client/campusmobile/activity/id/68/fid.html) | 已接入：集团2027校招项目68；此前正式抓取器完整102岗与首尾详情。 |
| OPPO | 消费电子与制造 | [官网入口](https://careers.oppo.com/university/oppo/campus/post?recruitType=Graduate) | 已接入：项目30；此前正式抓取器完整118岗与首尾详情，包含后端岗位。 |
| 快手 | 互联网 | [官网入口](https://campus.kuaishou.cn/recruit/campus/e/#/campus/jobs?recruitSubProjectCodes=20271779425607) | 已接入：2027应届生；此前正式抓取器验证279岗。 |
| 360集团 | 互联网 | [官网入口](https://360campus.zhiye.com/campus/jobs) | 本轮接入并完整验证：官网校招分类；包含不同届别，具体年份见岗位原文；正式抓取器完整30岗，首尾详情核验成功。 |
| 三一集团 | 制造业 | [官网入口](https://sanycampus.zhiye.com/campus/jobs) | 本轮接入并完整验证：官网校招分类；不含社招、实习；正式抓取器完整95岗，首尾详情核验成功。 |
| 汇川技术 | 制造业 | [官网入口](https://inovance.zhiye.com/campus/jobs) | 已接入，当前0岗：官网校招分类本次返回0岗，可关注后等待更新；不代表其他渠道没有招聘；正式抓取器完整0岗。 |
| vivo | 消费电子与制造 | [官网入口](https://hr-campus.vivo.com/campus/jobs) | 本轮接入并完整验证：官网校招分类；不含社招、实习；正式抓取器完整165岗，首尾详情核验成功。 |
| 上汽通用/泛亚 | 汽车与制造 | [官网入口](https://sgm.zhiye.com/campusjobs) | 已接入：官网校招分类核验为 1 条秋招项目，包含多类储备生；具体岗位方向以原文为准。 |
| 荣耀 | 消费电子与制造 | [官网入口](https://career.honor.com/SU60eea919bef57c1023f6fe78/pb/school.html) | 本轮接入并完整验证：项目101801：2027届应届本硕；不含博士专项、实习；正式抓取器完整101岗，首尾详情核验成功。 |
| 联想 | 消费电子与制造 | [官网入口](https://talent.lenovo.com.cn/#/campus) | 本轮接入并完整验证：官网应届生分类projectType=1；不含人才专项、实习；正式抓取器完整83岗，首尾详情核验成功。 |
| 美的集团 | 制造业 | [官网入口](https://careers.midea.com/schoolOut/post?projectType=1) | 本轮接入并完整验证：2027届美的星校园招聘；不含博士专项、实习；正式抓取器完整148岗，首尾详情核验成功。 |
| 比亚迪 | 汽车与制造 | [官网入口](https://job.byd.com/portal/mobile/schoolPositionList?tab=%E5%BA%94%E5%B1%8A%E7%94%9F) | 本轮接入并完整验证：2027常规应届生；不含实习、外派专项；采用500容量内一次完整读取避开分页重复；正式抓取器完整372岗，首尾详情核验成功。 |
| 海康威视 | 智能物联与制造 | [官网入口](https://campushr.hikvision.com/school?schoolType=nozxf) | 本轮接入并完整验证：2027常规校招；不含智先锋、实习；合并岗位保留各子岗位职责、要求、部门和方向；正式抓取器完整167岗，首尾详情核验成功。 |
| 同花顺 | 金融科技 | [官网入口](https://campus.10jqka.com.cn/job/list?sid=61) | 已接入：2027 校招项目含实习转正岗位，官网未提供可核对的用工类型；保留原文，不统一标成全职。 2026-10-06 正式抓取器完整读取 52 岗，首尾独立原文核验成功。 |
| 招银网络科技 | 金融科技 | [官网入口](https://cmbntjob.cmbchina.com/pages/schoolRecruit/index.html) | 核验时暂无岗位：应届毕业生分类核验返回 0 岗，可关注后等待更新；不代表社招、实习或其他渠道没有岗位。 2026-10-06 正式抓取器完整读取 0 岗；当前没有应届岗位详情可做实网验证，详情协议由官网公开客户端及固定数据测试验证。 |
| 网易游戏互娱 | 游戏 | [官网入口](https://campus.game.163.com/app/job/position?id=102) | 已接入：预览时实时核验官网范围与岗位数量。 2026-10-06 正式抓取器完整读取 42 岗，首尾独立原文核验成功。 |
| 网易游戏雷火 | 游戏 | [官网入口](https://leihuo.163.com/campus/#/full) | 已接入：预览时实时核验官网范围与岗位数量。 2026-10-06 正式抓取器完整读取 63 岗，首尾独立原文核验成功。 |
| 天翼云科技有限公司 | 国央企 | [官网入口](https://job.chinatelecom.com.cn/wt/TELE/web/index?brandCode=1#/postinquiry?data=eyJrZXkiOiI1ODE4NTQiLCJyZWNydWl0UHJvamVjdCI6IjEwMTEwMSIsInJlY3J1aXRQcm9qZWN0TmFtZSI6IjIwMjflubTluqbnp4vlraPmoKHlm63mi5vogZgiLCJ0eXBlIjoiMSJ9) | 已接入：按招聘单位限定 2027 秋招，避免混入集团其他单位；官网链接打开该岗位的标题搜索页。 2026-10-06 正式抓取器完整读取 43 岗，首尾独立原文核验成功。 |
| 中国电信云计算研究院 | 国央企 | [官网入口](https://job.chinatelecom.com.cn/wt/TELE/web/index?brandCode=1#/postinquiry?data=eyJrZXkiOiI1ODE4NTEiLCJyZWNydWl0UHJvamVjdCI6IjEwMTEwMSIsInJlY3J1aXRQcm9qZWN0TmFtZSI6IjIwMjflubTluqbnp4vlraPmoKHlm63mi5vogZgiLCJ0eXBlIjoiMSJ9) | 已接入：按招聘单位限定 2027 秋招，避免混入集团其他单位；官网链接打开该岗位的标题搜索页。 2026-10-06 正式抓取器完整读取 12 岗，首尾独立原文核验成功。 |
| 米哈游 | 游戏 | [官网入口](https://jobs.mihoyo.com/campus/position) | 已接入：2027 届秋招应届生项目（不含实习）。2026-10-06正式抓取器完整124岗，首尾独立详情核验通过。 |
| 平安科技 | 金融科技 | [官网入口](https://campus.pingan.com/freshGraduates?id=PA011&type=company) | 已接入：官网应届生分类 · 平安科技（具体届别见原文）。2026-10-06正式抓取器完整177岗，首尾独立详情核验通过。 |
| 金融壹账通 | 金融科技 | [官网入口](https://campus.pingan.com/freshGraduates?id=PA038&type=company) | 已接入：官网应届生分类 · 金融壹账通（具体届别见原文）。2026-10-06正式抓取器完整53岗，首尾独立详情核验通过。 |
| 平安壹钱包 | 金融科技 | [官网入口](https://campus.pingan.com/freshGraduates?id=PA027&type=company) | 已接入：官网应届生分类 · 平安壹钱包（具体届别见原文）。2026-10-06正式抓取器完整2岗，首尾独立详情核验通过。 |
| 中国移动云公司 | 国央企 | [官网入口](https://job.10086.cn/personal/campus/campus_job_list.html?cId=79) | 已接入：官网校园招聘 · 云公司（具体届别见原文）。2026-10-06正式抓取器完整6岗，首尾公开列表原文核验通过。 |
| 中国移动物联网公司 | 国央企 | [官网入口](https://job.10086.cn/personal/campus/campus_job_list.html?cId=77) | 已接入：官网校园招聘 · 物联网公司（含博士岗位，届别见原文）。2026-10-06正式抓取器完整17岗，首尾公开列表原文核验通过。 |
| 中国移动智慧家庭运营中心 | 国央企 | [官网入口](https://job.10086.cn/personal/campus/campus_job_list.html?cId=81) | 已接入：官网校园招聘 · 智慧家庭运营中心（含博士岗位，届别见原文）。2026-10-06正式抓取器完整25岗，首尾公开列表原文核验通过。 |
| 恒生电子 | 金融科技 | [官网入口](https://campus.hundsun.com/campus/jobs) | 已接入：按官网应届生校招分类读取；不混入实习、社招。地点字段为空时保留未知，具体毕业年份见岗位原文。 2026-10-06 正式抓取器完整读取 21 岗，首尾独立详情核验通过。 |
| 阅文集团 | 互联网 | [官网入口](https://yuewen.zhiye.com/campus/jobs) | 已接入：按官网应届生校招分类读取；不混入实习、社招。地点字段为空时保留未知，具体毕业年份见岗位原文。 2026-10-06 正式抓取器完整读取 34 岗，首尾独立详情核验通过。 |
| 吉比特&雷霆游戏 | 游戏 | [官网入口](https://hr.g-bits.com/web/index.html#/post-web/post-list/8a82ac07a057a3ea01a0617904b4100c) | 已接入：限定 2027 秋招正式岗位，保留职责、要求、加分项与提前实习说明；用工类型保留未知。官网链接打开标题搜索页，同名岗位通过编号分别读取原文。 2026-10-06 正式抓取器完整读取 36 岗，首尾独立详情核验通过。 |
| TCL · 流程与数字化转型中心 | 消费电子与制造 | [官网入口](https://campus.tcl.com/campus/recruiting.html?id=43) | 已接入：限定2027全球校招与流程与数字化转型中心；正式抓取器完整12岗，首尾独立职责、要求与毕业范围核验通过。 |
| TCL · 鸿鹄实验室 | 消费电子与制造 | [官网入口](https://campus.tcl.com/campus/recruiting.html?id=46) | 已接入：限定2027全球校招与鸿鹄实验室；正式抓取器完整6岗，首尾独立职责、要求与毕业范围核验通过。 |
| 中国电子 · 麒麟软件/中电云 | 国央企 | [官网入口](https://campus.cec.com.cn/position?positionType=0) | 已接入：仅麒麟软件、中电云两单位的官网校招分类；正式抓取器完整72岗，首尾独立详情核验通过。官网入口打开集团校招页，可按单位筛选查看；具体届别、截止日期见原文。 |

## 34 个官网入口与接入限制

| 来源 | 类型 | 官网入口 | 状态与范围 |
| --- | --- | --- | --- |
| 字节跳动 | 互联网 | [官网入口](https://jobs.bytedance.com/campus/position) | 公开查询受限：官网与客户端可读；按公开客户端发送匿名岗位查询返回 405，尚未完成校招范围、完整分页与详情核验。 2026-10-06 复核。 |
| 携程/Trip.com | 互联网 | [官网入口](https://careers.ctrip.com/campus/jobList) | 官网验证阻断：公开接口曾返回 56 个应届生岗位；正式安全抓取器收到官网验证跳转，当前不可自动导入。 |
| 滴滴 | 互联网 | [官网入口](https://talent.didiglobal.com/campus/) | 访问受限：官网链接的 Moka 校招入口在本机出现重定向循环。 |
| 华为 | 通信与制造 | [官网入口](https://career.huawei.com/cn/campus-recruitment) | 访问受限：官网校招页可读，公开应届岗位查询本次返回 412，尚未完成正式抓取验证。 2026-10-06 复核。 |
| 小米 | 消费电子与制造 | [官网入口](https://hr.xiaomi.com/website/campus.html) | 范围待拆分：官网校招分类返回 1061 岗，北京分类也有 529 岗，超过当前单来源 500 岗上限；需完善范围划分与独立详情。 2026-10-06 复核。 |
| 博世 | 外企与工业 | [官网入口](https://www.bosch.com.cn/careers/) | 访问受限：中国官网可读，官网链接的 Moka 校招入口在本机出现重定向循环。 |
| ABB | 外企与工业 | [官网入口](https://careers.abb/china/zh/) | 待适配：中国职业页可读；中国毕业生岗位范围与公开查询尚未验证。 |
| 施耐德电气 | 外企与工业 | [官网入口](https://www.se.com/cn/zh/about-us/careers/students-and-young-professionals.jsp) | 访问受限：本机中国页面连接超时，全球职位入口返回 403，尚未完成岗位读取验证。 |
| SAP | 外企与软件 | [官网入口](https://jobs.sap.com/) | 待适配：官网此次可读；中国地点、毕业生分类及完整分页仍待验证。 |
| Intel | 外企与芯片 | [官网入口](https://jobs.intel.com/en/locations-china) | 访问受限：中国职位入口在本机返回 403，尚未完成毕业生范围与分页验证。 |
| IBM | 外企与软件 | [官网入口](https://www.ibm.com/careers/search) | 待适配：官网搜索页可读；中国毕业生岗位范围尚未验证，不能混入全球社招。 |
| 爱立信 | 外企与通信 | [官网入口](https://jobs.ericsson.com/careers) | 待适配：官网搜索页可读；中国地点、毕业生范围及完整详情尚未验证。 |
| 宁德时代 | 新能源与制造 | [官网入口](https://talent.catl.com/) | 访问受限：官网 Moka 招聘入口在本机出现重定向循环；2027 校招项目尚未核验。 |
| 吉利汽车/控股 | 汽车与制造 | [官网入口](https://www.geelyautogroup.com/recruitment) | 访问受限：集团招聘页可读；官网链接的校招站点连接失败，需分别核对子公司范围。 |
| 上汽集团 | 汽车与制造 | [官网入口](https://www.saicmotor.com/chinese/rlzy/rcxq/xyzp/index_xz2.shtml) | 需按子公司接入：官网汇总多个子公司渠道；尚未核验各渠道校招范围，不能视为集团统一列表。 |
| 格力电器 | 制造业 | [官网入口](https://www.gree.com/) | 招聘入口待确认：公司官网可读；当前正式校招列表尚未确认，此处保留公司官网入口。 |
| 巨人网络 | 游戏 | [官网入口](https://hr.ztgame.com/campus/) | 访问受限：官方校招列表可读；官网链接的 Moka 岗位入口在本机出现重定向循环，尚未接通独立岗位详情。 |
| 完美世界 | 游戏 | [官网入口](https://jobs.games.wanmei.com/) | 项目待核验：官方招聘入口可读；当届正式校招项目、完整岗位范围与第三方详情尚未核验。 |
| 中国银联 | 金融科技 | [官网入口](https://join.unionpay.com/wt/unionpayhr/web/index) | 待适配：官网招聘入口可读；当前校园项目、完整岗位范围与独立详情尚未核验。 |
| 京东方 | 消费电子与制造 | [官网入口](https://campus.boe.com/) | 渠道待适配：官网可读，标准校招查询返回空列表；官网定制招聘渠道尚未完成岗位范围与分页核验，不能据此判断没有招聘。 |
| TCL | 消费电子与制造 | [官网入口](https://campus.tcl.com/campus/recruiting.html?jobmx=308501) | 需按部门接入：已支持流程与数字化转型中心、鸿鹄实验室；集团全部岗位每页10条，其他部门需分别核验后接入。 |
| 鹰角网络 | 游戏 | [官网入口](https://career.hypergryph.com/) | 访问受限：官网已发布 2027 校招，链接的岗位站本次返回 403；暂不能自动读取完整岗位与详情。 |
| 叠纸游戏 | 游戏 | [官网入口](https://career.papegames.com/campus) | 岗位入口待确认：官网校招页可读，公开客户端链接的岗位列表返回 404；待确认新岗位入口。 |
| 莉莉丝游戏 | 游戏 | [官网入口](https://jobs.lilith.com/) | 岗位入口失效：招聘官网可读，但官网发布的校招链接当前返回 404；暂不能自动导入岗位。 |
| 建信金科 | 金融科技 | [官网入口](https://job2.ccb.com/cn/job/announcement.html?annoId=20260903100124717047&planType=XY) | 公告入口：已确认 2027 校招公告；具体岗位列表、招聘单位与详情待适配。 |
| 中国平安 | 金融科技 | [官网入口](https://campus.pingan.com/) | 需按单位接入：已支持平安科技、金融壹账通和平安壹钱包；集团应届生总列表超过单来源容量，其他单位需独立核验。 |
| 工银科技 | 金融科技 | [官网入口](https://www.tech.icbc.com.cn/) | 招聘入口待核验：保留公司官网及校园招聘栏目；当前届别、独立岗位列表与详情尚未核验。 |
| 中国移动 | 国央企 | [官网入口](https://job.10086.cn/) | 需按单位接入：已支持云公司、物联网公司和智慧家庭运营中心；按单位读取官网校招分类，其他单位需独立核验。 |
| 中国电信（集团入口） | 国央企 | [官网入口](https://job.chinatelecom.com.cn/) | 需按单位接入：已分别支持天翼云和云计算研究院；集团其他单位需独立核验，集团总列表超过单来源容量。 |
| 中国联通 | 国央企 | [官网入口](https://www.chinaunicom.com.cn/46/menu01/528/column06) | 项目待核验：保留集团官方校招入口；需核对当届公告及各招聘单位的具体岗位渠道。 |
| 国网信通产业集团 | 国央企 | [官网入口](https://campus.51job.com/m/SGIT2027/) | 公告入口：2027 校招页可读，当前公开的是单位介绍和岗位类别；缺少可独立核对的岗位编号及职责详情。 |
| 南瑞集团 | 国央企 | [官网入口](https://job.sgepri.sgcc.com.cn/) | 访问受限：岗位入口本次访问受限；当前校招项目、招聘单位和完整详情尚未核验。 |
| 中国电科 | 国央企 | [官网入口](https://www.cetc.com.cn/zgdk/1593022/1592495/index.html) | 公告入口：已确认集团 2027 校招公告；各成员单位岗位及投递入口需分别核验。 |
| 中国电子 | 国央企 | [官网入口](https://campus.cec.com.cn/) | 需按单位接入：已支持麒麟软件、中电云两单位；集团校招列表超过单来源容量，其他单位需分别核验后接入。 |

## 接入标准

正式自动导入预设必须具备公开可读的招聘范围、完整分页或明确终止协议、稳定原始编号、独立原文核对、500岗容量与任务时限内的正式抓取器直连验证、固定数据异常测试、私有来源登记及限速。不同批次和子公司只能按官网明确编号拆分，不依据标题猜测应届身份、毕业年份或签约主体。

后续优先完善字节、携程及明确中国毕业生范围的外企查询；Moka入口重定向、连接/访问限制、集团多渠道和容量拆分保留诊断。不会把全球社招列表冒充中国校招，也不会使用候选人账号、绕过验证或降低TLS校验来强行接入。
