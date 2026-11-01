#!/usr/bin/env python3
"""Package existing results for user review, offline; never call or score a model."""
import argparse
import hashlib
import json
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
REVIEW = ROOT / 'scripts/review-preparation-evaluation.py'


def sha(data):
    return hashlib.sha256(data).hexdigest()


def block(value):
    text = value if isinstance(value, str) else json.dumps(value, ensure_ascii=False, indent=2)
    fence = '`' * max(3, max((len(x) for x in re.findall(r'`+', text)), default=0) + 1)
    return f'{fence}\n{text}\n{fence}\n'


def write(path, text):
    with path.open('x', encoding='utf-8') as out:
        out.write(text)
    path.chmod(0o600)


def markdown(parts):
    return re.sub(r'(?<=\|)\n{2,}(?=\|)', '\n', '\n'.join(parts))


def build(selection, output):
    output = output.resolve()
    if output.is_relative_to(ROOT):
        raise ValueError('Private reviews must remain outside the repository')
    rows = json.loads(selection.read_text())
    if not isinstance(rows, list) or not rows:
        raise ValueError('Selection must be a nonempty list')
    prepared = []
    for entry in rows:
        report_path = pathlib.Path(entry['report']).resolve()
        dataset_path = pathlib.Path(entry['dataset']).resolve()
        raw = report_path.read_bytes()
        report = json.loads(raw)
        dataset_raw = dataset_path.read_bytes()
        if sha(dataset_raw) != report['dataset_sha256']:
            raise ValueError('Dataset hash does not match report: ' + str(report_path))
        tasks = {t['id']: t for t in json.loads(dataset_raw)}
        # One report per sheet keeps existing review sidecars directly reusable.
        if len(report['results']) != 1:
            raise ValueError('Select a single-result report for each review sheet')
        result = report['results'][0]
        task = tasks[result['case_id']]
        if entry.get('exercise') and (task['input']['minutes'] != 15 or
                                     result['state'] != 'COMPLETED' or not result.get('plan')):
            raise ValueError('Exercise requires a completed 15-minute task')
        prepared.append((entry, report_path, raw, report, task, result))
    if sum(bool(e.get('exercise')) for e in rows) > 1:
        raise ValueError('Select at most one actual-execution exercise')
    # Exclusive creation prevents reruns from overwriting a user's annotations.
    output.mkdir(parents=True, mode=0o700, exist_ok=False)
    index = ['# 准备计划人工审阅\n',
             '这是已有执行报告的定向审阅包；模型证据类型以每份原报告为准，受控响应不能作为真实模型效果。'
             '全包离线生成，没有新增模型调用。评分尚未填写，不代表已完成人工评测。\n',
             '## 如何填写\n',
             '逐例打开审阅页，对照原任务、JD、计划和引用原文，再填写同目录 scores.json。'
             'reviewer 填本人姓名或固定代号，reviewer_kind 填 TARGET_USER_SELF_REVIEW；'
             '这是目标使用者审阅，不标为独立专家或盲评。\n',
             '评分：0=不满足，1=部分满足，2=满足，N/A=不适用，null=未审阅。'
             '每个分数写具体条目或原句证据。失败没有计划时，计划维度可填 N/A，'
             'notes 写失败对任务完成的影响。不要只审阅成功结果。\n',
             '| 维度 | 审阅问题 |\n| --- | --- |\n',
             '| target_requirement_coverage | 是否优先覆盖 JD 的重要要求，是否有关键遗漏？ |\n',
             '| citation_entailment | 原句能否支持这条行动，而不只是主题相关？ |\n',
             '| feasible_workload | 按我的基础，产物能否在规定时间内完成？是否一项塞入多个主题？ |\n',
             '| feedback_compliance | 是否排除已掌握/完成项，并遵守新的剩余时间？无反馈则 N/A。 |\n',
             '| no_fabricated_experience | 是否把学习资料写成我的失分、经验或生产经历？ |\n',
             '| retrieval_added_value | 补查增加了什么可用信息？零检索填 N/A；只有引用不能证明提升。 |\n',
             '\n先独立填写，再看已有助手审阅；版本与程序结果保留用于归因。不要把不同版本合成成功率。\n',
             '## 审阅列表\n']
    provenance = []
    for number, (entry, original, raw, report, task, result) in enumerate(prepared, 1):
        folder = output / f'{number:02d}'
        folder.mkdir(mode=0o700)
        write(folder / 'report.json', raw.decode())
        write(folder / 'task.json', json.dumps(task, ensure_ascii=False, indent=2) + '\n')
        subprocess.run([sys.executable, str(REVIEW), '--report', str(folder / 'report.json'),
                        '--output', str(folder / 'scores.json')], check=True, capture_output=True)
        sources = {s['id']: s for s in task.get('corpus', [])}
        sources.update({s['id']: s for s in task['sources']})
        text = [f'# {number:02d} · {entry["label"]}\n',
                f'版本：{report["agent_version"]} / {report.get("quality_version", "历史规则，无质量版本字段")}；证据：{report["mode"]}\n',
                f'任务：{result["case_id"]}；执行器：{report.get("dynamic_executor")}；'
                f'终态：{result["state"]}；请求：{result["model_calls"]} 次。\n',
                f'原报告 SHA-256：{sha(raw)}\n',
                '## 任务与材料范围\n', block(task['input']), block(task.get('missing', []))]
        if task.get('previous_plan'):
            text += ['## 反馈前的计划\n', block(task['previous_plan'])]
        if result.get('defense'):
            defense = result['defense']
            text += ['## 跨练习记忆与本轮回答\n', block({'memory_mode': task['input']['defense'].get('memory_mode', 'confirmed'), 'memory_candidates': task.get('memory_candidates', []), 'selected_memories': defense.get('memories', []), 'question': defense['question'], 'answer': defense['answer'], 'review': defense.get('review')}), '审阅反馈是否误判、旧记忆是否被当作当前掌握，以及用户纠正是否被尊重。对照同题另一记忆开关结果，保留无收益或退化。\n']
            sources.update({s['id']: s for s in defense.get('evidence', [])})
        text += ['## 实际输出\n', block(result.get('plan') or result.get('answer') or '未生成计划'),
                 '## 引用核对\n']
        for item in (result.get('plan') or {}).get('items', []):
            text.append(f'### {item["title"]}\n')
            for ref in item.get('references', []):
                src = sources.get(ref['source_id'])
                text += [block(ref), f'来源：{src["kind"]} / {src["title"]}\n' if src else '来源未能在本任务材料中解析，需核查。\n']
                if src:
                    text.append(block(src['text']))
        text += ['## 初始材料（用于检查要求覆盖与虚构经历）\n']
        for src in task['sources']:
            text += [f'### {src["kind"]} · {src["title"]}\n', block(src['text'])]
        text += ['## 检索与失败轨迹\n',
                 '引用核对中的学习材料来自任务语料；是否实际读取，以本节轨迹和原报告为准。\n',
                 block({k: result.get(k) for k in ('retrievals', 'retrieval_new_sources',
                       'retrieval_cited_sources', 'trace', 'answer', 'business_state')}),
                 '## 填写评价\n',
                 '填写 [scores.json](scores.json)。分数与证据留空，等待本人审阅。'
                 '程序通过不自动决定语义分数。\n']
        write(folder / '审阅.md', markdown(text))
        index.append(f'- [{number:02d} · {entry["label"]}]({number:02d}/审阅.md)\n')
        provenance.append({'sheet': f'{number:02d}', 'report': str(original), 'report_sha256': sha(raw),
                           'dataset': entry['dataset'], 'dataset_sha256': report['dataset_sha256'],
                           'case_id': result['case_id'], 'arm': result['arm']})
        if entry.get('exercise'):
            exercise = ['# 15 分钟实际执行记录\n',
                        f'使用 [{number:02d} 的原计划]({number:02d}/审阅.md)，不预先缩小任务。'
                        '到15分钟时停止并记录剩余工作；若继续，额外时间单独记录。\n',
                        '这是一份目标使用者完成记录，不是群体时间研究；观察者暂未执行。\n',
                        '- 执行者：\n- 日期：\n- 开始时间：\n- 实际停止时间：\n- 中断及耗时：\n',
                        '| 计划项 | 预算分钟 | 实际分钟 | 完成/部分/未完成 | 实际产物路径或笔记 | 卡点及剩余工作 |\n'
                        '| --- | --- | --- | --- | --- | --- |\n']
            for item in result['plan']['items']:
                title = item['title'].replace('|', '\\|').replace('\n', ' ')
                exercise.append(f'| {title} | {item["minutes"]} | | | | |\n')
            exercise += ['\n- 15分钟内实际产物：\n- 是否提前掌握：\n- 隐含子任务及额外时间：\n'
                         '- 哪条引用实际帮助完成任务：\n- 准备后仍答不出的一个问题：\n'
                         '- 建议保留/缩小/删除的计划项及原因：\n',
                         '\n完成后据此填写对应 scores.json 的 feasible_workload，其他案例的时间判断仍是估计。\n']
            write(output / '15分钟实际执行.md', markdown(exercise))
    index += ['\n## 校验与后续\n',
              '每例填写后可运行现有脚本：\n', block(
                  f'python3 {REVIEW} --report "<本包>/01/report.json" --validate "<本包>/01/scores.json"'),
              '该命令只检查报告绑定、评分结构和证据填写，不判断评分正确，也不将留空视为完成人工审阅。\n',
              '未来保留验证：在新额度明确授权后，冻结6～10个未参与调试的目标与材料哈希；'
              '固定模型、Prompt、工具、预算与最终版本，单独记录结果。'
              '检索开关对照需至少有一例真实知识缺口，自动组实际检索且得到可用信息；'
              '若两组均不检索或没有增益，如实保留。当前包不是这轮新验证。\n']
    write(output / '开始审阅.md', markdown(index))
    write(output / 'provenance.json', json.dumps(provenance, ensure_ascii=False, indent=2) + '\n')
    return len(prepared)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--selection', type=pathlib.Path, required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    args = parser.parse_args()
    print(json.dumps({'review_sheets': build(args.selection, args.output), 'model_calls': 0,
                      'human_scores': 'UNREVIEWED', 'output': str(args.output)}, ensure_ascii=False))


if __name__ == '__main__':
    main()
