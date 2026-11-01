"""Cross-practice memory checks against owned Go/LangGraph/MySQL, scripted model."""
import json
import uuid


def verify(Client, raw, fixture_url, sql, wait, work):
    checks = []

    def account():
        c = Client()
        credentials = c.register()
        job = c.request('/api/ingest', dict(company='Synthetic Memory Practice', title='Go backend', job_type='FULL_TIME',
                         external_id=uuid.uuid4().hex, text='解释 Go 项目的执行与保存。', fetch_status='SUCCESS'))
        app = c.request('/api/applications', dict(job_id=job['job_id'], submitted=True,
                         resume_version='synthetic-memory', resume_text='合成简历：Go 后端项目。'))
        return c, credentials, app['id']

    c, _, app = account()
    outsider, _, _ = account()
    topic = 'go-langgraph'
    endpoint = '/api/agent/preparation/defense-memories?topic=' + topic
    base = dict(application_id=app, goal='结合本次回答核对项目，安排一个具体复述任务。',
                minutes=15, days=1, other_reviews=False, retrieval_mode='off',
                defense=dict(topic=topic, answer='我还不熟悉这个项目。', memory_mode='confirmed'))

    def preview(value=None):
        value = base if value is None else value
        return c.request('/api/agent/preparation/preview', dict(input=value))

    def start(value):
        review = preview(value)
        request = dict(input=value, confirm=True, request_key=uuid.uuid4().hex, review_hash=review['review_hash'])
        v = c.request('/api/agent/preparation', request)
        def terminal():
            out = c.request('/api/agent/executions/' + v['id'])
            return out if out['state'] not in ('PENDING', 'RUNNING') else None
        return wait(terminal)

    def confirm(proposal):
        return c.request('/agent/actions/' + proposal['pending']['action_id'] + '/confirm', dict(confirm=True))

    first = start(base)
    assert first['state'] == 'COMPLETED'
    assert not c.request(endpoint)
    before_calls = raw(fixture_url + '/fixture/status')['calls']
    path = '/api/agent/preparation/' + first['id'] + '/memory-preview'
    body = dict(note='上次没有讲清模型请求的位置，下次请重新核对。', disputed=False, days=90)
    outsider.request(path, body, 404)
    c.request(path, dict(body, episode=dict(answer='forged')), 400)
    proposal = c.request(path, body)
    duplicate = c.request(path, body)
    assert not c.request(endpoint)
    assert raw(fixture_url + '/fixture/status')['calls'] == before_calls
    assert proposal['memory']['episode']['answer'] == first['preparation_agent']['defense']['answer']
    assert proposal['memory']['episode']['review'] == first['preparation_agent']['defense']['review']
    c.request('/agent/actions/' + proposal['pending']['action_id'] + '/confirm', dict(confirm=False), 400)
    receipt = confirm(proposal)
    assert confirm(proposal) == receipt and confirm(duplicate)['id'] == receipt['id']
    memory_id = receipt['id']
    assert sql("SELECT COUNT(*) FROM agent_memories WHERE id='" + memory_id + "'") == '1'
    assert json.loads(sql("SELECT result FROM action_receipts WHERE id='" + proposal['pending']['action_id'] + "'")) == receipt
    assert not c.request('/api/agent/memories') and not outsider.request(endpoint)
    c.request('/api/agent/memories', dict(kind='DEFENSE', content='伪造已掌握', episode=receipt['episode']), 400)
    c.request('/api/agent/memories', dict(id=memory_id, version=1, kind='PREFERENCE', content='把模拟记录冒充偏好'), 400)
    checks += ['memory_preview_zero_calls_and_no_write', 'memory_server_binds_completed_answer_review_and_sources',
               'memory_owner_isolation_and_ordinary_write_rejection', 'memory_confirmation_sql_receipt_and_business_deduplication']

    job2 = c.request('/api/ingest', dict(company='Synthetic Second Application', title='Go backend', job_type='FULL_TIME', external_id=uuid.uuid4().hex, text='解释 Go 项目的执行与保存。', fetch_status='SUCCESS'))
    app2 = c.request('/api/applications', dict(job_id=job2['job_id'], submitted=True, resume_version='synthetic-memory', resume_text='合成简历：Go 后端项目。'))['id']
    base = dict(base, application_id=app2)
    # A fresh application, not a child execution, recalls the original origin.
    on_preview = preview(base)
    selected = on_preview['defense']['memories']
    assert [m['id'] for m in selected] == [memory_id]
    assert selected[0]['episode']['application_id'] == app
    off = dict(base, defense=dict(base['defense'], memory_mode='off'))
    assert not preview(off)['defense'].get('memories')
    on_result, off_result = start(base), start(off)
    for value in (on_result, off_result):
        assert value['state'] == 'COMPLETED'
        state = value['preparation_agent']
        assert state['defense']['evidence_read'] == 1, 'old memory must not count as current tool read'
        assert not state['input'].get('mastered')
    assert '上次练习' in on_result['preparation_agent']['defense']['review']['next_question']
    assert '上次练习' not in off_result['preparation_agent']['defense']['review']['next_question']
    checks += ['confirmed_memory_crosses_practices_and_applications_with_origin', 'paired_same_question_sources_budget_memory_on_off', 'memory_changes_scripted_followup_not_mastery',
               'old_memory_evidence_cannot_bypass_current_read']

    # Actual source-version exclusion, with explicit SQL injection as a fixture.
    original = json.loads(sql("SELECT body FROM agent_memories WHERE id='" + memory_id + "'"))
    sql("UPDATE agent_memories SET body=JSON_SET(body,'$.episode.evidence_version','old') WHERE id='" + memory_id + "'")
    assert not preview()['defense'].get('memories')
    sql("UPDATE agent_memories SET body=JSON_SET(body,'$.episode.evidence_version','" + original['episode']['evidence_version'] + "') WHERE id='" + memory_id + "'")
    correction_path = '/api/agent/preparation/defense-memories/' + memory_id + '/preview'
    correction = dict(version=1, note='纠正：上次反馈需要核对，不应当作能力判断。', disputed=True, days=90)
    outsider.request(correction_path, correction, 404)
    pending = c.request(correction_path, correction)
    old_pending = c.request(correction_path, dict(correction, note='迟到的更改'))
    corrected = confirm(pending)
    assert corrected['id'] == memory_id and corrected['version'] == 2 and corrected['disputed']
    c.request('/agent/actions/' + old_pending['pending']['action_id'] + '/confirm', dict(confirm=True), 409)
    assert not preview()['defense'].get('memories')
    assert c.request(endpoint)[0]['content'] == correction['note']
    before_calls = raw(fixture_url + '/fixture/status')['calls']
    c.request('/api/agent/preparation', dict(input=base, confirm=True, request_key=uuid.uuid4().hex,
              review_hash=on_preview['review_hash']), 409)
    assert raw(fixture_url + '/fixture/status')['calls'] == before_calls
    checks += ['injected_old_code_not_recalled', 'user_correction_is_versioned_and_dispute_excluded',
               'late_correction_and_old_previews_rejected_without_model_call']

    # Corrected user note reaches the next preview; keep original answer immutable.
    restored = confirm(c.request(correction_path, dict(version=2, note='纠正后保留，下一次只追问调用位置。', disputed=False, days=30)))
    assert restored['version'] == 3
    assert restored['episode'] == original['episode']
    assert preview()['defense']['memories'][0]['note'] == restored['content']
    checks.append('correction_keeps_original_evidence_and_reaches_next_context')

    forgotten_pending = c.request(correction_path, dict(version=3, note='遗忘前的待确认内容', disputed=False, days=30))
    c.request('/api/agent/memories/' + memory_id, dict(version=2), 409, method='DELETE')
    c.request('/api/agent/memories/' + memory_id, dict(version=3), method='DELETE')
    c.request('/agent/actions/' + forgotten_pending['pending']['action_id'] + '/confirm', dict(confirm=True), 404)
    replay = confirm(proposal)
    assert replay['forgotten'] and not replay.get('episode') and not replay['content']
    assert not c.request(endpoint) and not preview()['defense'].get('memories')
    erased = json.loads(sql("SELECT body FROM agent_memories WHERE id='" + memory_id + "'"))
    assert not erased.get('episode') and not erased['content']
    for execution in (first, on_result, off_result):
        cleared = c.request('/api/agent/executions/' + execution['id'])
        assert cleared['state'] == 'STALE' and not cleared.get('preparation_agent')
    assert not sql("SELECT id FROM action_receipts WHERE JSON_UNQUOTE(JSON_EXTRACT(result,'$.id'))='" + memory_id + "' AND JSON_EXTRACT(result,'$.episode')<>CAST('null' AS JSON)")
    checks += ['forget_clears_episode_receipts_and_recalled_contexts', 'forget_prevents_pending_or_confirmed_replay_resurrection']
    (work / 'defense-memory.json').write_text(json.dumps(dict(status='PASS', evidence='controlled-Go-LangGraph-MySQL',
        external_model_calls=0, checks=checks, semantic_quality='NOT_MEASURED',
        paired=dict(on=on_result['preparation_agent']['defense']['review']['next_question'],
                    off=off_result['preparation_agent']['defense']['review']['next_question']),
        note='Paired question differences are prescribed fixture behavior, not model improvement.'), ensure_ascii=False, indent=2) + '\n')
    _, credentials, browser_app = account()
    return checks, credentials, browser_app
