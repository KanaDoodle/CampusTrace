"""Scripted defense replies for program acceptance, never a semantic grader."""
def respond(data, messages, reply, mode='normal'):
    defense = data['defense']
    goal = data['goal']
    known = {s['id']: s for s in data['sources']}
    for message in messages[2:]:
        if message.get('role') == 'tool':
            import json
            try:
                value = json.loads(message.get('content', '{}'))
                known.update({s['id']: s for s in value.get('sources', [])})
            except (ValueError, TypeError):
                pass
    wanted = list(dict.fromkeys(p['source_ids'][0] for p in defense['points']))
    missing = [identity for identity in wanted if identity not in known]
    rejected = any('INVALID_DEFENSE' in m.get('content', '') for m in messages[2:])
    if mode == 'defense-bad-read' and not rejected:
        reply('read_project_defense_evidence', {'ids': ['../../.env.model.local']})
        return
    if missing:
        reply('read_project_defense_evidence', {'ids': missing[:3]})
        return
    # The response branch is prescribed by fixture input, not a claim that these
    # keywords assess a person's knowledge or real-model answer quality.
    unfamiliar = '不熟悉' in defense['answer'] or '不知道' in defense['answer']
    finish = '受控完整回答' in defense['answer']
    action = 'FINISH' if finish else ('READ' if unfamiliar and goal['minutes'] >= 15 else 'FOLLOW_UP')
    findings = []
    for point in defense['points']:
        src = known[point['source_ids'][0]]
        quote = next(line.strip() for line in src['text'].splitlines() if line.strip())[:60]
        if mode == 'defense-invalid-quote' and not rejected:
            quote = 'invented-not-in-source'
        findings.append({'point': point['id'], 'verdict': 'SUPPORTED' if finish else 'NOT_COVERED',
                         'answer_quote': defense['answer'] if finish else '',
                         'explanation': '受控反馈：本次原回答尚未展开此点；先核对指定片段。' if not finish else '受控反馈：此项用于测试原回答与证据绑定。',
                         'references': [{'source_id': src['id'], 'quote': quote, 'supports': '该片段供核对对应的执行与状态规则'}]})
    items = []
    if action == 'READ':
        src = known[wanted[0]]
        quote = next(line.strip() for line in src['text'].splitlines() if line.strip())[:60]
        items.append({'id': 'read-one', 'title': '读一个执行片段', 'minutes': 10, 'day': 1,
                      'basis': 'GENERAL', 'action': '展开这条依据，阅读对应函数，记下该函数做的一个动作。',
                      'deliverable': '一句话说明这个动作发生在哪里',
                      'workload': {'mode': 'READ', 'units': 1, 'scope': '1个片段：对应执行函数'},
                      'references': [{'source_id': src['id'], 'quote': quote, 'supports': '用于找到实际阅读入口'}]})
    if action != 'FINISH':
        items.append({'id': 'retell-one', 'title': '复述一个要点', 'minutes': 5, 'day': 1,
                      'basis': 'GENERAL', 'action': '用自己的话解释刚才片段中的一个动作，并标出仍不清楚的地方。',
                      'deliverable': '一段自己的说明或一个具体疑问',
                      'workload': {'mode': 'ORAL', 'units': 1, 'scope': '1个问题：刚才片段中的动作'},
                      'references': []})
    reply('finish_project_defense_review', {'summary': '受控项目答辩反馈，供验证执行与保存流程。',
          'findings': findings, 'next_action': action,
          'next_question': '' if finish else '请用自己的话说明刚才片段中由谁完成哪一步操作？',
          'plan': {'summary': '按当前缺口安排阅读和复述。' if items else '本轮练习结束。', 'items': items}})
