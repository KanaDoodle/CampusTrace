/* Chinese presentation only. Never translate API keys or persisted values. */
(function (root) {
  'use strict';
  const enums = Object.freeze({
    job: {OPEN:'可投递', CLOSED:'已关闭', NEEDS_VERIFICATION:'待核验', UNKNOWN:'暂无法确认'},
    eligibility: {ELIGIBLE:'符合投递条件', INELIGIBLE:'不符合投递条件', CONDITIONAL:'需确认附加条件', UNKNOWN:'资格暂无法判断'},
    fit: {EXPLICIT_GO:'明确使用 Go', LANGUAGE_FLEXIBLE:'开发语言不限，可选 Go', NO_GO_SIGNAL:'未明确使用 Go', CONFLICTING:'技术要求存在冲突', UNKNOWN:'技术方向暂无法判断'},
    application: {PLANNED:'计划投递', APPLIED:'已投递', OA:'笔试／测评', INTERVIEW:'面试中', HR:'人事沟通', OFFER:'已获录用意向', REJECTED:'未通过', WITHDRAWN:'已撤回'},
    rule: {PASS:'满足', FAIL:'不满足', UNKNOWN:'依据不足', CONDITIONAL:'需进一步确认', NOT_APPLICABLE:'未识别为硬性要求'},
    interview: {PASS:'通过', FAIL:'未通过', PENDING:'待反馈', UNKNOWN:'结果暂未确认'},
    fetch: {SUCCESS:'获取成功', BLOCKED:'访问受限', TIMEOUT:'获取超时', HTTP_ERROR:'页面访问异常', PARSE_ERROR:'页面解析失败'},
    extraction: {PENDING:'待分析', COMPLETE:'分析完成', FAILED:'分析失败'},
    trust: {OFFICIAL:'官方来源', THIRD_PARTY:'第三方来源', MANUAL:'手动录入'},
    method: {RULE:'规则提取', LLM:'模型提取', MANUAL:'人工录入'},
    job_type: {FULL_TIME:'校招全职', INTERNSHIP:'实习', UNKNOWN:'岗位类型未明确'},
    degree: {ASSOCIATE:'大专', BACHELOR:'本科', MASTER:'硕士', PHD:'博士'},
    signal: {PRESENT:'发现申请入口', ABSENT:'未发现申请入口', UNKNOWN:'入口情况待确认'},
    fact: {IMPLEMENTED:'已实现', LIMITATION:'已知局限', PLANNED:'计划实现'},
    terminal: {COMPLETED:'查询完成', ERROR:'查询失败', TIMEOUT:'查询超时', CANCELLED:'查询已取消', UNGROUNDED:'暂无充分依据', TOOL_LIMIT:'已达本次查询上限', STEP_LIMIT:'已达本次分析上限', OUTPUT_LIMIT:'查询内容超过本次输出上限，请缩小范围'},
    change: {JD_CONTENT_CHANGED:'岗位描述有更新', GRADUATION_CHANGED:'毕业届别要求有更新', LOCATION_CHANGED:'工作地点有更新', APPLY_SIGNAL_CHANGED:'申请入口有变化', DEADLINE_CHANGED:'截止日期有更新', TECH_REQUIREMENT_CHANGED:'技术要求有更新'},
    evidence: {GRADUATION_REQUIREMENT:'毕业届别要求', EDUCATION_REQUIREMENT:'学历要求', JOB_TYPE:'岗位类型', LOCATION:'工作地点', EXPERIENCE_REQUIREMENT:'经验要求', TECH_STACK:'技术要求', LANGUAGE_REQUIREMENT:'语言要求', MAJOR_REQUIREMENT:'专业要求', APPLY_ACTION:'申请入口', DEADLINE:'投递截止日期', OPEN_SIGNAL:'开放招聘信号', CLOSED_SIGNAL:'结束招聘信号'},
    tool: {search_jobs:'检索校招岗位', get_job:'查询岗位详情', get_job_evidence:'核对岗位证据', get_job_eligibility:'核对投递条件', list_applications:'查询投递记录', get_application_history:'查询投递进展', get_interview_history:'查询面试与复盘', get_weak_topics:'查询待加强知识点', search_knowledge:'检索复习资料', get_project_facts:'核对项目事实', get_preparation_context:'整理面试准备内容', create_application:'加入投递计划', transition_application:'更新投递进展', record_interview_review:'保存面试复盘'},
    ranking: {status:'岗位可投递情况', eligibility:'投递条件匹配', city:'意向城市匹配', type:'岗位类型偏好', go_fit:'Go 技术方向匹配', role:'意向职能匹配', freshness:'岗位信息时效'}
  });
  const fields = Object.freeze({
    id:'记录编号', job_id:'岗位编号', company_id:'公司编号', user_id:'账号编号', title:'名称', company:'公司', name:'名称', job_type:'岗位类型', locations:'工作地点', current_status:'岗位状态', created_at:'创建时间', updated_at:'更新时间', fingerprint:'岗位识别标记',
    source_id:'来源编号', source_posting_id:'来源发布编号', external_id:'来源岗位编号', url:'来源网址', first_seen_at:'首次发现时间', last_seen_at:'最近发现时间', merge_reason:'岗位归并依据',
    observed_at:'观察时间', fetch_status:'获取情况', http_status:'页面响应码', normalized_content_hash:'内容指纹', text:'原文内容', apply_signal:'申请入口情况', deadline_signal:'截止日期线索', parser_version:'解析版本', extraction_status:'分析进度', error_category:'异常类型', trust:'来源类型',
    observation_id:'观察记录编号', excerpt:'证据摘录（保留原文）', value:'提取内容', type:'类别', extraction_method:'提取方式', confidence:'提取置信度', evidence:'证据', observations:'观察记录', assessments:'状态评估历史',
    status:'判断结果', rule_version:'评估规则版本', assessed_at:'评估时间', evidence_ids:'依据编号', reason:'判断依据', from_observation:'更新前观察编号', to_observation:'更新后观察编号',
    graduation_year:'毕业届别', graduation_from:'毕业年份起点', graduation_to:'毕业年份终点', degree:'最高学历', majors:'所学专业', preferred_job_types:'意向岗位类型', preferred_cities:'首选城市', acceptable_cities:'可接受城市', target_roles:'意向职能', technical_skills:'已掌握技能', target_languages:'掌握的语言', experience_months:'相关经验（月）',
    rule:'核对项目', result:'核对结果', requirement:'岗位要求', candidate_value:'我的情况', explanation:'判断说明', results:'逐项核对', eligibility:'投递条件核对', ranking:'个人偏好排序', score:'匹配得分', breakdown:'得分构成', breakdown_sources:'评分依据来源', go_fit:'Go 技术方向匹配',
    application_id:'投递记录编号', current_state:'投递进展', version:'记录版本', applied_at:'投递时间', resume_version:'简历版本', from_state:'原进展', to_state:'新进展', occurred_at:'记录时间', note:'备注', actor:'操作人',
    interview_id:'面试编号', round:'面试轮次', scheduled_at:'面试时间', finished_at:'完成时间', notes:'备注', actual_questions:'实际被问到的问题', self_evaluation:'自我复盘', missed_points:'未答好的要点', follow_up_notes:'后续复习计划', weak_topics:'待加强知识点',
    topic:'知识点', weight:'加强程度', severity:'薄弱程度', evidence_sources:'复盘依据', first_seen:'首次发现时间', last_seen:'最近出现时间', occurrence_count:'累计出现次数',
    project_id:'项目编号', kind:'事实类型', claim:'事实内容', verified:'是否已核验', reference:'参考依据', verified_facts:'已核验的项目事实', unverified_not_facts:'尚未核验，不能作为已确认事实', project_facts:'项目事实',
    document_id:'资料编号', index:'分块序号', embedding_version:'索引版本', cosine:'向量相似度', keyword:'关键词匹配度', knowledge:'复习资料', requirements:'岗位要求', current_requirements:'当前岗位要求', current_observations:'当前观察', input_identity:'评估输入版本', historical_requirements:'历史岗位要求', recommended_topics:'建议优先准备', priority:'复习优先级', weak_evidence:'薄弱点依据', policy:'使用说明',
    action_id:'待确认操作编号', action_type:'拟执行操作', args:'操作预览', expires_at:'确认截止时间', state:'目标进展', job:'岗位', interviews:'面试安排', reviews:'面试复盘', chunks:'资料片段', saved:'保存结果', success:'执行结果', synthetic:'演示数据', notice:'说明', error:'提示',
    run_id:'本次查询编号', terminal_reason:'查询结果', model_steps:'分析轮次', executed_tool_count:'资料查询次数', grounded_observations:'本次查询依据'
  });
  const messages = Object.freeze({
    'No recent usable observation':'目前没有近期可用的观察记录。',
    'Recent official sources conflict':'近期官方来源的信息存在冲突，需要进一步核验。',
    'Official explicit closure or elapsed deadline':'官方来源已明确结束招聘，或投递截止日期已过。',
    'Latest official fetch failed or is stale; absence is not closure':'最近一次官方页面访问失败，或信息已过时；这并不等于岗位关闭。',
    'Recent official observation has an application action and no closure evidence':'近期官方页面仍有申请入口，且未发现结束招聘的证据。',
    'Official page lacks decisive application evidence':'官方页面缺少足以确认是否可投递的信息。',
    'Only non-official evidence is available':'目前只有非官方来源的信息，投递前建议核验官网。',
    'No sufficiently confident evidence':'缺少可信度足够的依据。',
    'Conflicting requirements require verification':'不同来源的要求不一致，需要核验。',
    'Identified requirement satisfied':'目前记录的个人情况满足这一要求。',
    'Explicit requirement is not satisfied':'目前记录的个人情况不满足这一明确要求。',
    'Requirement or candidate value needs clarification':'岗位要求或个人情况尚不明确，需要补充确认。',
    'Job type differs from preference':'岗位类型与当前求职意向不同，请确认是否考虑。',
    'Location is acceptable but not preferred':'工作地点在可接受城市中，但不是首选城市。',
    'Relocation preference requires confirmation':'请确认是否接受该城市的工作安排。',
    'Technology signal is not an explicit hard constraint':'岗位提到了这项技术，但未明确要求必须掌握。',
    'Create a candidate profile to evaluate':'请先完善求职资料，再核对投递条件。',
    'Only verified IMPLEMENTED facts establish implementation. LIMITATION and PLANNED never establish implementation.':'仅已核验且标记为“已实现”的记录可用于介绍已完成的功能；局限和计划不能说成已实现。',
    'Preparation suggestions; never invent personal experience or interview answers.':'准备建议仅供复习参考，不会编造个人经历或面试答案。',
    'Result too large; narrow the query':'查询结果较多，请缩小检索范围。',
    'Invalid run request':'请填写问题后重新查询。', 'Model call failed':'本次分析未能完成，请稍后重试。',
    'No authoritative data was retrieved; factual answer withheld.':'没有查到足够的可信依据，暂时无法确认。',
    'Too many proposed tools':'本次需要查询的内容较多，请拆分问题后重试。', 'Tool budget reached':'已达到本次查询上限，请缩小问题范围。',
    'Model step limit reached; last proposed tools were not executed.':'已达到本次分析上限，最后提出的操作未执行。'
  });
  // Display aliases only for known fixtures/canonical vocabulary. Source excerpts are never rewritten.
  const aliases = Object.freeze({
    Shanghai:'上海', Hangzhou:'杭州', Beijing:'北京', Shenzhen:'深圳', Guangzhou:'广州', Chengdu:'成都', Nanjing:'南京', Wuhan:'武汉', Suzhou:'苏州', English:'英语', Chinese:'中文', 'Computer Science':'计算机科学', backend:'后端开发',
    'Synthetic Cedar':'雪松科技（虚构演示）', 'Synthetic Harbor':'港湾科技（虚构演示）', 'Synthetic Maple':'枫叶科技（虚构演示）', 'Synthetic Pine':'青松科技（虚构演示）', 'Synthetic Loadgen':'负载测试公司（虚构演示）',
    'Synthetic Import Company':'导入示例公司（虚构演示）', 'Synthetic CSV Company':'表格导入公司（虚构演示）',
    'Go backend engineer':'Go 后端开发工程师', 'Go backend':'Go 后端开发', 'Go backend graduate':'Go 后端开发校招生', 'Go backend intern':'Go 后端开发实习生', 'Go backend load fixture':'Go 后端岗位（负载测试样例）',
    'Go platform engineer (closed)':'Go 平台开发工程师（已关闭样例）', 'Go backend engineer (blocked)':'Go 后端开发工程师（访问受限样例）', 'Go backend engineer (unverified)':'Go 后端开发工程师（待核验样例）', 'Java backend graduate':'Java 后端开发校招生', 'Backend unknown eligibility':'后端开发（投递条件待确认样例）',
    'Synthetic demo timeline':'虚构演示投递记录', 'Synthetic interview':'虚构演示面试', 'Synthetic review completed':'虚构演示复盘已完成',
    'How do Redis Streams recover pending messages?':'Redis 消息流如何恢复待处理消息？', 'Need to revisit PEL recovery':'需要进一步复习待处理消息的恢复机制。', 'PEL recovery':'待处理消息恢复', 'Read worker recovery integration tests':'阅读任务恢复的集成测试，梳理处理流程。', 'Redis Streams':'Redis 消息流', 'redis streams':'Redis 消息流',
    'Synthetic Learning Backend':'后端学习项目（虚构演示）', 'A synthetic exercise implements bounded workers and database idempotency':'虚构练习项目实现了有界任务处理与数据库幂等。', 'Synthetic example fact, not a user\'s real project':'这是一条虚构示例事实，不代表用户的真实项目。', 'No production deployment or real traffic measurements':'尚未上线运行，也没有真实流量测量结果。', 'Add semantic embedding provider':'计划接入语义向量服务。',
    'rules-v2':'第 2 版评估规则', 'generic-v2-lines':'第 2 版保留分行解析', 'claims-v2-semantics':'第 2 版证据语义校验', 'EVIDENCE_ASSESSMENT':'证据评估', 'EVIDENCE_AND_USER_PROFILE':'证据与个人资料', 'JOB_METADATA_AND_USER_PREFERENCE':'岗位元数据与个人偏好', 'OBSERVATION_METADATA':'观察时间记录', 'rules-v1':'第 1 版评估规则', 'generic-v1':'第 1 版通用解析', 'public-http-v1':'第 1 版公开页面解析', 'claims-v1':'第 1 版证据提取', 'lexical-hash-128-v1':'第 1 版词法索引', 'Synthetic Go backend handbook':'Go 后端复习手册（演示资料）', 'MyRPC integration notes':'MyRPC 接入笔记', 'Synthetic interview review':'面试复盘（虚构演示）'
  });
  function label(group, value) {return Object.hasOwn(enums[group]||{},value)?enums[group][value]:'暂未说明';}
  function field(key) {return fields[key] || '补充信息';}
  function text(value) {
    if (value == null || value === '') return '暂未填写';
    const s=String(value);
    if (Object.hasOwn(messages,s)) return messages[s];
    if (Object.hasOwn(aliases,s)) return aliases[s];
    if (/^Synthetic Integration [a-f0-9]+$/.test(s)) return '集成测试公司（虚构演示）';
    return s;
  }
  function reason(value) {return (Object.hasOwn(messages,value)?messages[value]:null) || (/[^\x00-\x7f]/.test(value || '') ? value : '暂无对应的中文说明，请结合本页证据核验。');}
  function date(value, onlyDate=false) {
    if (!value || String(value).startsWith('0001-')) return '暂未记录';
    const date = new Date(/^\d{4}-\d{2}-\d{2}$/.test(value) ? value+'T00:00:00+08:00' : value);
    if (Number.isNaN(date.getTime())) return '时间待确认';
    const options={timeZone:'Asia/Shanghai',year:'numeric',month:'long',day:'numeric'};
    if (!onlyDate) Object.assign(options,{hour:'2-digit',minute:'2-digit',hourCycle:'h23'});
    return new Intl.DateTimeFormat('zh-CN',options).format(date)+(onlyDate?'':'（北京时间）');
  }
  function scalar(key,value,context={}) {
    if (value == null || value === '') return '暂未填写';
    if (typeof value === 'boolean') return key==='verified'?(value?'已核验':'待核验'):(value?'是':'否');
    if (/(?:_at|_seen)$/.test(key)) return date(value);
    if (key==='deadline_signal') return value==='UNKNOWN'?'截止时间待确认':date(value,true);
    if (key==='reason'||key==='explanation'||key==='policy'||key==='eligibility_notice') return reason(value);
    const groups={current_status:'job', current_state:'application',from_state:'application',to_state:'application',state:'application',job_type:'job_type',degree:'degree',fetch_status:'fetch',extraction_status:'extraction',trust:'trust',extraction_method:'method',kind:'fact',go_fit:'fit',terminal_reason:'terminal',rule:'evidence',action_type:'tool',apply_signal:'signal'};
    if (groups[key]) return label(groups[key],value);
    if (key==='status') return label(['ELIGIBLE','INELIGIBLE','CONDITIONAL'].includes(value)||context.results?'eligibility':'job',value);
    if (key==='result') return label(context.application_id?'interview':'rule',value);
    if (key==='type') return enums.evidence[value]||enums.change[value]||enums.trust[value]||'其他记录';
    if (key==='requirement'||key==='candidate_value'||key==='value') return requirement(value,context.rule||context.type,key);
    if (key==='confidence') return `${Math.round(Number(value)*100)}%`;
    if (key==='round') return `第 ${value} 轮`;
    if (key==='error_category') return enums.fetch[value] || ({ANALYSIS_FAILED:'岗位分析失败',SCHEMA:'资料格式不符合要求',TRANSIENT:'暂时性异常',PERMANENT:'需人工处理的异常'}[value]) || '其他异常';
    if (key==='preferred_job_types') return label('job_type',value);
    if (key==='evidence_sources'||key==='weak_evidence') {const m=String(value).match(/^([a-f0-9]{32}):(.*)$/s);if(m)return `复盘 ${m[1]}：${text(m[2])}`;}
    return text(value);
  }
  function requirement(value,type,key='requirement') {
    if (value == null || value==='') return '暂无明确要求';
    const s=String(value);
    if (type==='DEADLINE') return date(s,true);
    if (type==='GRADUATION_REQUIREMENT' && /^20\d{2}(-20\d{2})?$/.test(s)) return s.replace('-','—')+' 届';
    if (type==='EXPERIENCE_REQUIREMENT' && /^\d+$/.test(s)) return Number(s)===0?(key==='candidate_value'?'0 个月相关经验':'不限相关经验'):s+' 个月';
    if (type==='APPLY_ACTION') return label('signal',s);
    if (type==='OPEN_SIGNAL'||type==='CLOSED_SIGNAL') return label('job',s);
    const tokenLabel=x=>enums.degree[x]||enums.job_type[x]||text(x);
    // Candidate values are a list of recorded attributes, not alternative requirements.
    if (key==='candidate_value') return s.split('|').map(tokenLabel).join('、');
    const required=s.startsWith('REQUIRED:');
    const groups=s.replace(/^REQUIRED:/,'').split('+');
    return (required?'必须掌握：':'')+groups.map(v=>{const choices=v.split('|').map(tokenLabel).join(' 或 ');return groups.length>1&&v.includes('|')?`（${choices}）`:choices;}).join(' 和 ');
  }
  function error(status,path='') {
    if (status===401) return path==='/auth/login'?'邮箱或密码不正确，请重新输入。':'登录已失效，请退出后重新登录。';
    if (status===403) return '暂无权限查看或修改这条记录。';
    if (status===404) return '未找到这条记录，可能尚未创建或已失效。';
    if (status===409) return path.includes('transition')||path.includes('/confirm')?'记录已有更新，或当前操作与已有记录冲突，请刷新后核对。':'已存在相同记录，请勿重复创建。';
    if (status===429) return '操作较频繁，请稍等片刻再试。';
    if (status>=500) return '服务暂时不可用，请稍后重试。';
    if (path.includes('transition')) return '当前进展不能直接调整到所选阶段，请核对后重试。';
    if (path.includes('/confirm')) return '该操作暂时无法确认，请重新查询并核对操作预览。';
    return '提交未成功，请检查必填项和填写格式后重试。';
  }
  function inputList(value) {return (Array.isArray(value)?value:[]).map(text).join('、');}
  function parseList(value) {
    const reverse=Object.fromEntries(Object.entries(aliases).map(([en,zh])=>[zh,en]));
    return String(value||'').split(/[、，,\n|]/).map(x=>x.trim()).filter(Boolean).map(x=>reverse[x]||x);
  }
  function shanghaiISO(value) {
    if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value||'')) throw new Error('请填写完整的面试日期和时间。');
    const d=new Date(value+':00+08:00');
    if (Number.isNaN(d.getTime())) throw new Error('面试时间格式不正确，请重新填写。');
    return d.toISOString();
  }
  const display=Object.freeze({enums,fields,label,field,text,reason,date,scalar,requirement,error,inputList,parseList,shanghaiISO});
  root.CampusDisplay=display;
  if (typeof module!=='undefined') module.exports=display;
})(globalThis);
