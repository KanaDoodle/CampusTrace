"""Additional defense checks in the existing owned API/SQL/graph fixture."""
import json
import uuid


def verify(Client, raw, fixture_url, sql, wait, work):
    checks = []

    def account():
        c = Client()
        credentials = c.register()
        job = c.request('/api/ingest', dict(company='Synthetic Project Defense', title='Go backend', job_type='FULL_TIME',
                         external_id=uuid.uuid4().hex, text='熟悉 Go 并发和后端项目，能解释实现与取舍。', fetch_status='SUCCESS'))
        app = c.request('/api/applications', dict(job_id=job['job_id'], submitted=True,
                         resume_version='synthetic-defense', resume_text='合成简历：Go 与 LangGraph 项目。'))
        return c, credentials, app['id']

    c, _, app = account()
    outsider = Client()
    outsider.register()
    calls = raw(fixture_url + '/fixture/status')['calls']
    catalog = c.request('/api/agent/preparation/defense-topics')
    assert len(catalog['catalog']['topics']) == 2
    before = {t: sql('SELECT COUNT(*) FROM ' + t) for t in ('reviews', 'project_facts', 'agent_memories')}
    base = dict(application_id=app, goal='还不熟悉项目，先核对回答，再安排一个明确的阅读与复述任务。',
                minutes=15, days=1, other_reviews=False, retrieval_mode='off',
                defense=dict(topic='go-langgraph', answer='我还不熟悉这个项目，只知道有 Go 和 LangGraph。'))

    def terminal(identity):
        value = c.request('/api/agent/executions/' + identity)
        return value if value['state'] not in ('RUNNING', 'PENDING') else None

    def start(value):
        review = c.request('/api/agent/preparation/preview', dict(input=value))
        request = dict(input=value, confirm=True, request_key=uuid.uuid4().hex, review_hash=review['review_hash'])
        execution = c.request('/api/agent/preparation', request)
        return wait(lambda: terminal(execution['id'])), request

    preview = c.request('/api/agent/preparation/preview', dict(input=base))
    assert raw(fixture_url + '/fixture/status')['calls'] == calls
    assert preview['defense']['version'] == catalog['version']
    assert all(s['application_id'] == app for s in preview['sources'])
    assert all(not s.get('application_id') for s in preview['defense']['evidence'])
    outsider.request('/api/agent/preparation/preview', dict(input=base), 404)
    checks += ['defense_catalog_and_preview_zero_calls', 'frozen_and_current_evidence_separate', 'defense_owner_isolation']

    first, request = start(base)
    state = first['preparation_agent']
    assert first['state'] == 'COMPLETED', first
    assert state['engine'] == 'langgraph' and state['defense']['review']['next_action'] == 'READ'
    assert state['defense']['evidence_read'] == 1
    assert sum(i['minutes'] for i in state['result']['items']) == 15
    count = raw(fixture_url + '/fixture/status')['calls']
    assert c.request('/api/agent/preparation', request)['id'] == first['id']
    assert raw(fixture_url + '/fixture/status')['calls'] == count
    checks += ['unfamiliar_answer_reads_evidence_then_short_plan', 'defense_start_idempotency']

    pending = c.request('/api/agent/preparation/' + first['id'] + '/save-preview', {})
    assert not c.request('/api/agent/executions/' + first['id'])['preparation_agent'].get('saved_at')
    receipt = c.request('/agent/actions/' + pending['action_id'] + '/confirm', dict(confirm=True))
    assert receipt == c.request('/agent/actions/' + pending['action_id'] + '/confirm', dict(confirm=True))
    assert json.loads(sql("SELECT result FROM action_receipts WHERE id='" + pending['action_id'] + "'")) == receipt
    assert sql("SELECT COUNT(*) FROM action_receipts WHERE id='" + pending['action_id'] + "'") == '1'
    checks.append('defense_plan_confirmed_sql_receipt_and_replay')

    child, _ = start(dict(base, parent_id=first['id'], defense=dict(topic='go-langgraph', answer='受控完整回答：Go处理权限、模型调用和保存，LangGraph衔接步骤。')))
    assert child['state'] == 'COMPLETED', child
    child_state = child['preparation_agent']
    assert child_state['defense']['round'] == 2 and child_state['defense']['question'] == state['defense']['review']['next_question']
    assert child_state['defense']['evidence_read'] == 0 and child_state['model_calls'] == 1
    assert child_state['defense']['review']['next_action'] == 'FINISH' and not child_state['result']['items']
    assert not child_state['input'].get('mastered') and not child_state['input'].get('completed')
    c.request('/api/agent/preparation/' + child['id'] + '/save-preview', {}, 409)
    checks += ['reanswer_reuses_prior_question_and_evidence', 'finish_never_declares_mastery_or_saves_empty_plan']

    short, _ = start(dict(base, parent_id=first['id'], minutes=5, defense=dict(topic='go-langgraph', answer='读完后我还不确定模型调用的位置。')))
    assert short['state'] == 'COMPLETED', short
    assert sum(i['minutes'] for i in short['preparation_agent']['result']['items']) == 5
    assert short['preparation_agent']['defense']['evidence_read'] == 0
    checks.append('five_minute_replan_without_repeated_read')

    for mode in ('defense-invalid-quote', 'defense-bad-read'):
        raw(fixture_url + '/fixture/mode', dict(mode=mode))
        value, _ = start(dict(base, defense=dict(topic='unknown-recovery', answer='我不知道中断后怎么处理。')))
        assert value['state'] == 'COMPLETED', value
        assert any(t['status'] == 'REJECTED' and 'INVALID_DEFENSE' in t.get('detail', '') for t in value['preparation_agent']['trace'])
        assert value['preparation_agent']['defense']['evidence_read'] == 1
        checks.append(mode + '_feedback_and_repair')
    raw(fixture_url + '/fixture/mode', dict(mode='normal'))

    stale = c.request('/api/agent/preparation/' + short['id'] + '/save-preview', {})
    assert len(short['id']) == 32 and all(x in '0123456789abcdef' for x in short['id'])
    sql("UPDATE agent_executions SET body=JSON_SET(body,'$.preparation_agent.defense.version','injected-stale-version') WHERE id='" + short['id'] + "'")
    c.request('/agent/actions/' + stale['action_id'] + '/confirm', dict(confirm=True), 409)
    assert sql("SELECT COUNT(*) FROM action_receipts WHERE id='" + stale['action_id'] + "'") == '0'
    checks.append('injected_evidence_version_rejects_pending_save')
    assert {t: sql('SELECT COUNT(*) FROM ' + t) for t in before} == before
    checks.append('no_changes_to_real_reviews_project_facts_or_memories')
    (work / 'project-defense.json').write_text(json.dumps(dict(status='PASS', evidence='controlled-model-real-Go-Python-SQL',
         external_model_calls=0, checks=checks, semantic_quality='NOT_MEASURED', receipt=receipt), ensure_ascii=False, indent=2) + '\n')
    # Browser uses another owned account, keeping every account below the normal
    # five-starts/minute admission limit instead of disabling the product guard.
    _, credentials, browser_app = account()
    return checks, credentials, browser_app
