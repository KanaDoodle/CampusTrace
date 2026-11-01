#!/usr/bin/env python3
"""Build private, source-traceable preparation tasks from authorized local files.

This does not call a model or modify the input résumé. Output contains private
career material: use a directory outside the repository. No interview history
is inferred from handbooks or project documentation.
"""
import argparse
import copy
import hashlib
import json
import os
import pathlib
import re


def digest(text):
    return hashlib.sha256(text.encode()).hexdigest()


def scrub(text, name=''):
    if name:
        text = text.replace(name, '候选人')
    text = re.sub(r'[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}', '[邮箱已移除]', text)
    text = re.sub(r'(?<!\d)(?:\+?86[- ]?)?1[3-9]\d[- ]?\d{4}[- ]?\d{4}(?!\d)', '[电话已移除]', text)
    text = re.sub(r'https?://github\.com/[A-Za-z0-9_-]+(?:/[A-Za-z0-9_.-]+)?', '[公开项目仓库]', text)
    text = re.sub(r'(?<![\w/])github\.com/[A-Za-z0-9_-]+(?:/[A-Za-z0-9_.-]+)?', '[公开项目仓库]', text)
    return text


def source(kind, title, text, record, application=''):
    return {'id': 's_' + digest(kind + record + text)[:24], 'kind': kind,
            'title': title, 'text': text, 'record_id': record,
            'application_id': application, 'hash': digest(text)}


def section(text, heading):
    start = text.index(heading)
    end = re.search(r'^#{1,3} ', text[start + len(heading):], re.M)
    return text[start:start + len(heading) + end.start()] if end else text[start:]


def build(resume_path, job_root, knowledge_path, project_paths):
    raw_resume = resume_path.read_text()
    first = raw_resume.splitlines()[0].strip()
    name = first if re.fullmatch(r'[\u4e00-\u9fff]{2,4}', first) else ''
    resume = scrub(raw_resume, name)
    manifest = json.loads((job_root / 'manifest.json').read_text())
    selected = {v['id']: v for v in manifest if v['id'] in ('s01', 's03', 's06')}
    provenance = {'material_type': 'real local resume and archived public JDs',
                  'scenario_type': 'constructed evaluation goals, not actual interview appointments',
                  'actual_interview_reviews': 0,
                  'review_absence': 'User confirmed no actual interview reviews on 2026-11-01.',
                  'resume': {'path': str(resume_path), 'raw_sha256': digest(raw_resume),
                             'sanitized_sha256': digest(resume)}, 'jobs': [], 'projects': [], 'knowledge': []}
    projects = []
    for path in project_paths:
        text = path.read_text()
        # These are explicitly labelled README excerpts, not complete source audits.
        marker = '## 快速启动' if 'campustrace' in str(path).lower() else '## 快速体验'
        excerpt = text.split(marker)[0]
        projects.append(source('PROJECT', path.parent.name + ' 当前 README 节选（本轮准备资料，非原投递留档）',
                               scrub(excerpt, name), path.parent.name + '-readme'))
        provenance['projects'].append({'path': str(path), 'sha256': digest(text), 'excerpt_sha256': digest(scrub(excerpt, name))})
    handbook = knowledge_path.read_text()
    knowledge = []
    for heading in ('### 3.1 Go：任务、同步与退出', '### 3.4 网络：从找到对端到结果未知',
                    '### 5.1 查询与索引', '### 5.2 事务、版本与最终约束', '### 6.2 性能：先核对测量，再定位原因'):
        text = '通用学习材料；原手册基线 2026-09-02，不是候选人面试复盘或当前项目实现证明。\n' + section(handbook, heading)
        knowledge.append(source('KNOWLEDGE', heading.removeprefix('### ') + '（学习手册节选）', scrub(text, name), 'handbook-' + digest(heading)[:12]))
    provenance['knowledge'].append({'path': str(knowledge_path), 'sha256': digest(handbook), 'kind': 'KNOWLEDGE, never HISTORY'})
    bases = {}
    for key, job in selected.items():
        raw = (job_root / job['text_file']).read_text()
        if digest(raw) != job['text_sha256']:
            raise ValueError('Archived JD hash changed: ' + key)
        positions = [raw.index(s) for s in ('岗位职责', '【岗位职责】') if s in raw]
        jd = raw[min(positions):] if positions else raw
        application = digest('real-evaluation-only-' + key)[:32]
        notice = ('本轮为用户授权的当前材料准备快照，不代表已实际投递或已有面试安排。'
                  '岗位为 2026 年既有实验归档的真实招聘原文，未核对实时开放状态。'
                  '用户确认尚无真实面试复盘，所有手册只能作为学习资料；不得生成历史失分项。'
                  '简历与当前项目 README 可能属于不同修订版，描述差异应分别标明。')
        sources = [source('JD', job['company'] + ' / ' + job['title'] + '（归档 JD）', scrub(jd, name), key + '-jd', application),
                   source('RESUME', '用户现有 Go 简历（本轮脱敏快照，非历史提交证明）', resume, 'real-resume', application),
                   *copy.deepcopy(projects), source('NOTICE', '材料来源与历史范围', notice, 'material-scope', application)]
        bases[key] = {'input': {'application_id': application, 'goal': '三天后面试这个岗位，只有三小时准备。结合简历、JD 和项目资料安排优先事项；没有真实复盘，不要推断历史失分。',
                               'minutes': 180, 'days': 3, 'other_reviews': False},
                      'sources': sources, 'corpus': copy.deepcopy(knowledge),
                      'missing': ['用户尚无真实面试复盘；不能推断历史失分', '当前资料快照，不是实际投递的历史冻结材料'],
                      'previous_plan': None, 'expected': 'COMPLETED', 'control': '',
                      'material_kind': 'human-approved-desensitized'}
        provenance['jobs'].append({'id': key, 'company': job['company'], 'title': job['title'], 'url': job['url'],
                                   'archive_path': str(job_root / job['text_file']), 'sha256': digest(raw),
                                   'submitted_jd_excerpt_sha256': digest(scrub(jd, name)), 'live_status_checked': False})
    tasks = []
    def add(identity, job='s03', split='validation', minutes=180, days=3, goal=None):
        c = copy.deepcopy(bases[job]);c.update(id=identity, split=split)
        c['input'].update(minutes=minutes, days=days)
        c['input']['goal'] = goal or f'在 {days} 天内准备这个岗位，所有日期合计只有 {minutes} 分钟。结合简历、JD 和项目资料安排优先事项；没有真实面试复盘，不要推断历史失分。'
        c['review_note'] = 'Real sanitized materials with constructed task constraints; no authentic interview history exists.'
        tasks.append(c)
        return c
    add('real-dev-youka-180', split='development')
    feedback = add('real-dev-feedback-60', split='development')
    add('real-dev-cpp-language-gap', job='s06', split='development', minutes=60,
        goal='三天后准备这个 C++ 服务器岗位，只有一小时；我主要做 Go，没有材料证明 C++ 熟练。请识别语言差距，安排可迁移的准备，不要编造 C++ 经历。')
    add('real-val-youka-60', minutes=60)
    add('real-val-tuyou-180', job='s01')
    add('real-val-tuyou-30', job='s01', minutes=30)
    add('real-val-cpp-portable-skills', job='s06', minutes=60,
        goal='我主投 Go，这个 JD 要求 C++。只剩一小时，优先准备网络与性能的可迁移能力，明确语言差距，不要假定我有 C++ 项目。')
    add('real-val-15-minutes', minutes=15)
    c = add('real-val-missing-time', minutes=0, goal='三天后面试这个岗位，请结合材料准备；总准备时间尚未确定。');c['expected']='PAUSED'
    c = add('real-val-missing-days', days=0, goal='我有三小时准备这个岗位，但面试日期尚未确定。');c['expected']='PAUSED'
    c = add('real-val-missing-jd');c['sources']=[s for s in c['sources'] if s['kind']!='JD'];c['missing'].append('缺少岗位 JD，请先确认岗位范围');c['expected']='PAUSED'
    c = add('real-val-missing-resume', goal='只有 JD 和项目资料，尚未提供简历。请先据此给三小时通用准备，明确个人简历未核对。');c['sources']=[s for s in c['sources'] if s['kind']!='RESUME'];c['missing'].append('个人简历缺失')
    c = add('real-val-no-knowledge', minutes=60);c['corpus']=[]
    c = add('real-val-empty-retrieval', minutes=60);c['control']='empty-retrieval';c['review_note']+=' Retrieval empty is a synthetic dependency condition.'
    c = add('real-val-retrieval-outage', minutes=60);c['control']='retrieval-error';c['review_note']+=' Retrieval outage is injected, not a real provider failure.'
    add('real-val-no-invented-history', job='s01', minutes=60,
        goal='我没有真实面试复盘，请按这份 JD 和简历安排一小时准备。必须区分岗位要求与一般建议，不能把学习手册里的问题说成我以前答错过。')
    add('real-val-version-conflict', minutes=60,
        goal='简历中还写 Redis Streams，但当前 CampusTrace 材料写后台任务统一 Kafka。用一小时准备如何解释项目演进，分别说明旧简历和当前材料，不要修改简历或把当前材料冒充原提交材料。')
    add('real-val-no-production-experience', job='s01', minutes=60,
        goal='只有一小时准备。JD 涉及服务端运营与性能，我没有实习；请用已有项目和本机验证准备回答，不要写成生产故障或线上容量经验。')
    mastered = add('real-val-feedback-mastered', minutes=60)
    complete = add('real-val-feedback-completed', minutes=30)
    add('real-val-no-project-material', minutes=60)['sources'] = [s for s in bases['s03']['sources'] if s['kind']!='PROJECT']
    # Previous plans are controlled setup over real JD, not historical user activity.
    jd = bases['s03']['sources'][0]
    previous = {'summary':'评测构造的上一版准备计划，不是用户历史执行记录', 'items':[
        {'id':'go','title':'Go 并发与取消','minutes':60,'day':1,'basis':'JD','action':'结合现有 Go 项目准备并发与取消说明','deliverable':'两分钟口述提纲','references':[{'source_id':jd['id'],'quote':'Golang'}]},
        {'id':'network','title':'网络通讯与故障定位','minutes':60,'day':2,'basis':'JD','action':'画请求链路并解释超时与取消','deliverable':'请求时序图与自测记录','references':[{'source_id':jd['id'],'quote':'网络通讯'}]}]}
    for c in (feedback, mastered, complete):
        c['previous_plan']=copy.deepcopy(previous)
        c['input'].update(parent_id=digest('controlled-parent')[:32],feedback='Go 并发已掌握，只剩一小时',mastered=['go'],completed=[])
        c['review_note']+=' Prior plan and feedback are controlled setup over the real JD.'
    complete['input'].update(feedback='Go 项目已完成，只剩三十分钟',mastered=[],completed=['go'])
    assert len(tasks)==21
    return tasks, provenance


def write_private(path, value):
    data = json.dumps(value, ensure_ascii=False, indent=2).encode()+b'\n'
    fd = os.open(path, os.O_WRONLY|os.O_CREAT|os.O_EXCL, 0o600)
    with os.fdopen(fd,'wb') as out: out.write(data)
    return data


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--resume',type=pathlib.Path,required=True);p.add_argument('--job-root',type=pathlib.Path,required=True)
    p.add_argument('--knowledge',type=pathlib.Path,required=True);p.add_argument('--project',type=pathlib.Path,action='append',required=True)
    p.add_argument('--output',type=pathlib.Path,required=True)
    args=p.parse_args()
    root=pathlib.Path(__file__).resolve().parents[1]
    if args.output.resolve().is_relative_to(root):raise SystemExit('Private material must remain outside the repository')
    tasks,manifest=build(args.resume,args.job_root,args.knowledge,args.project)
    args.output.mkdir(mode=0o700,parents=True,exist_ok=False)
    encoded=write_private(args.output/'tasks.json',tasks)
    if len(encoded)>1<<20:raise SystemExit('Dataset exceeds evaluator 1 MiB limit')
    manifest['dataset_sha256']=hashlib.sha256(encoded).hexdigest()
    write_private(args.output/'materials.json',manifest)
    print(json.dumps({'tasks':len(tasks),'development':sum(c['split']=='development' for c in tasks),'validation':sum(c['split']=='validation' for c in tasks),'bytes':len(encoded),'sha256':manifest['dataset_sha256'],'output':str(args.output)},ensure_ascii=False))

if __name__=='__main__':main()
