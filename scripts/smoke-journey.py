#!/usr/bin/env python3
"""Exercise only a fresh synthetic journey-demo on loopback; no real accounts."""
import argparse
import json
import os
from pathlib import Path
import urllib.error
import urllib.parse
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('--url', default='http://127.0.0.1:23380')
parser.add_argument('--output', type=Path, default=os.environ.get('JOURNEY_SMOKE_OUTPUT'), help='optional new JSON evidence file')
args = parser.parse_args()
url = urllib.parse.urlparse(args.url)
if url.scheme != 'http' or url.hostname != '127.0.0.1' or url.path not in ('', '/'):
    parser.error('only the loopback synthetic demo is accepted')
client = urllib.request.build_opener()
token = ''
checks = []

def passed(name):
    checks.append(name)
    print('PASS ' + name)

def call(path, body=None, method=None, expect=200):
    request = urllib.request.Request(args.url.rstrip('/') + path,
        data=json.dumps(body, ensure_ascii=False).encode() if body is not None else None,
        headers={'Content-Type': 'application/json', **({'Authorization':'Bearer '+token} if token else {})}, method=method or ('POST' if body is not None else 'GET'))
    try:
        response = client.open(request, timeout=20)
    except urllib.error.HTTPError as error:
        response = error
    assert response.code == expect, (path, response.code, response.read().decode())
    return json.loads(response.read())

token = call('/auth/login', {'email':'demo@campustrace.invalid','password':'Synthetic2026!'})['token']
imported = call('/api/ingest', {'company':'合成验收公司','title':'Go 后端验收岗位','job_type':'FULL_TIME',
    'fetch_status':'SUCCESS','text':'任职要求：熟悉 Go、Redis Streams 和 XAUTOCLAIM。releasesmoke20261031'})
found = call('/api/jobs/search', {'text':'releasesmoke20261031','limit':10})
assert found['mode'] == 'basic' and found['job_ids'] == [imported['job_id']]
passed('owned JD import and authoritative search')
workspace = call('/api/matching/company-workspace', {'company':'合成青杉科技','job_ids':[]})
plan = workspace['plan']
assert len(plan['suggested_job_ids']) == 2, 'requires a fresh, unmodified demo'
body = {'company':'合成青杉科技','job_ids':[], 'selected_job_ids':plan['suggested_job_ids'], 'decision_key':plan['decision_key']}
apps = call('/api/matching/company-plan', body)
assert apps == call('/api/matching/company-plan', body)
passed('atomic company plan and replay')
for app in apps:
    preview = call(f"/api/applications/{app['id']}/context-preview", {'resume_text':'合成简历正文：Go 事务、幂等与恢复。'})
    call('/api/applications/transition', {'application_id':app['id'],'state':'APPLIED','version':1,
        'context_key':preview['context_key'],'resume_text':'合成简历正文：Go 事务、幂等与恢复。','note':'合成演示，模拟官网投递'})
application_id = apps[0]['id']
original = call(f'/api/applications/{application_id}/dossier')['context']
call('/api/profile', {'target_languages':['Python']}, method='PUT')
assert original == call(f'/api/applications/{application_id}/dossier')['context']
passed('reviewed submission snapshots remain fixed after profile changes')
content = (Path(__file__).resolve().parent.parent / 'examples/journey/interview.txt').read_text()
notice = call('/api/recruitment-notices', {'content':content,'format':'text'})
assert notice['ambiguous'] and len(notice['candidates']) == 2
assert notice['notice']['id'] == call('/api/recruitment-notices', {'content':content,'format':'text'})['notice']['id']
a = call(f'/api/applications/{application_id}/dossier')['application']
confirmation = {'version':1,'application_id':application_id,'application_version':a['version'],
    'kind':'INTERVIEW','scheduled_at':notice['notice']['parsed']['scheduled_at'],'round':1,'reference':'SYN-101','note':'合成线上面试'}
confirm_path = f"/api/recruitment-notices/{notice['notice']['id']}/confirm"
call(confirm_path, dict(confirmation, application_version=1), expect=409)
assert call(confirm_path, confirmation) == call(confirm_path, confirmation)
dossier = call(f'/api/applications/{application_id}/dossier')
assert dossier['application']['current_state'] == 'INTERVIEW' and len(dossier['interviews']) == 1
passed('ambiguity, duplicate import, stale confirmation and idempotent confirmation')
content = (Path(__file__).resolve().parent.parent / 'examples/journey/reschedule.eml').read_text()
notice = call('/api/recruitment-notices', {'content':content,'format':'eml'})
assert notice['notice']['parsed'].get('scheduled_at') is None
old = dossier['interviews'][0]
confirmation = {'version':1,'application_id':application_id,'application_version':dossier['application']['version'],
    'kind':'RESCHEDULE','scheduled_at':'2026-11-03T08:00:00Z','round':1,'reference':'SYN-101','note':'确认新时间',
    'interview_id':old['id'],'previous_scheduled_at':old['scheduled_at']}
confirm_path = f"/api/recruitment-notices/{notice['notice']['id']}/confirm"
assert call(confirm_path, confirmation) == call(confirm_path, confirmation)
final = call(f'/api/applications/{application_id}/dossier')
assert len(final['interviews']) == 1 and final['interviews'][0]['id'] == old['id']
assert len(final['notices']) == 2 and final['preparation']['tasks'] and final['context'] == original
passed('EML import, explicit new time, reschedule, provenance and frozen preparation')

interview_id = final['interviews'][0]['id']
call('/api/reviews', {'interview_id':interview_id,
    'actual_questions':['Redis Streams 的 PEL 和 XAUTOCLAIM 如何恢复消费者故障？'],
    'self_evaluation':'合成复盘：需要复习消息重领和去重的边界。',
    'missed_points':['Redis PEL 与 XAUTOCLAIM 的恢复边界'],
    'follow_up_notes':'复习保存后确认及消费者幂等。','weak_topics':[]})
draft = call(f'/api/interviews/{interview_id}/learning-draft')
assert draft['origin']['application_id'] == application_id and 'XAUTOCLAIM' in draft['text']
material = {'source_key':draft['origin']['source_key'],'title':draft['title'],'text':draft['text']}
saved = call(f'/api/interviews/{interview_id}/learning-material', material)
replayed = call(f'/api/interviews/{interview_id}/learning-material', material)
assert saved['id'] == replayed['id'] and replayed['reused']
document = call('/api/documents/' + saved['id'])
assert document['origin']['source_key'] == draft['origin']['source_key']
passed('interview review becomes one learning note with server-bound provenance')

skill_input = {'skill_id':'application-prep','application_id':application_id,'request_key':'release-smoke-preparation-1'}
execution = call('/api/agent/executions', skill_input)
assert execution['state'] == 'COMPLETED', execution
observations = execution['grounded_observations']
assert len(observations) == 1 and observations[0]['tool'] == 'get_application_preparation'
preparation = observations[0]['data']
assert preparation['preparation_scope'] == 'frozen_application' and preparation['review_count'] == 1
assert any(hit['document_id'] == saved['id'] for hit in preparation['knowledge'])
assert call('/api/agent/executions', skill_input)['id'] == execution['id']
assert original == call(f'/api/applications/{application_id}/dossier')['context']
passed('new preparation retrieves the review note while preserving frozen application materials')

if args.output:
    with args.output.open('x') as output:
        json.dump({'status':'PASS','kind':'isolated-synthetic-http-journey','checks':checks,
            'external_model_calls':0,'source_mode':'synthetic-seeded-comparison-and-local-keyword-retrieval'}, output, indent=2)
        output.write('\n')
print('Synthetic smoke complete. Inspect 招聘通知 / 投递进展 in the demo UI.')
