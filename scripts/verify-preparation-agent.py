#!/usr/bin/env python3
"""Owned API/SQL/Redis/Kafka and browser acceptance; no real model or personal DB."""
import argparse,contextlib,http.cookiejar,json,os,pathlib,socket,subprocess,sys,tempfile,time,urllib.error,urllib.request,uuid
from project_defense_acceptance import verify as verify_defense
from project_defense_memory_acceptance import verify as verify_memory
sys.dont_write_bytecode=True

def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--output',type=pathlib.Path,required=True);p.add_argument('--playwright-module');args=p.parse_args()
 work=args.output.resolve();work.mkdir(parents=True,exist_ok=True)
 if any(work.iterdir()):raise SystemExit('use a new empty output directory')
 root=pathlib.Path(__file__).resolve().parents[1];runid=uuid.uuid4().hex[:10];containers=[];processes=[];handles=[];checks=[]
 env=dict(os.environ,GOWORK='off',LLM_URL='',LLM_API_KEY='',LLM_MODEL='',SEARCH_URL='',JOB_EVENTS_MODE='direct',NARRATION_BACKEND='disabled',INFERENCE_RPC_ADDRESS='',INFERENCE_MODEL='',INFERENCE_BACKEND_KIND='',OTEL_EXPORTER_OTLP_TRACES_ENDPOINT='',CAMPUS_LIVE_SOURCES='0',PROFILE_ADDR='',PROFILE_TOKEN='')
 def run(cmd):return subprocess.check_output(cmd,text=True,stderr=subprocess.PIPE,env=env).strip()
 def wait(fn,seconds=20):
  until=time.monotonic()+seconds
  while time.monotonic()<until:
   value=fn()
   if value:return value
   time.sleep(.05)
  raise AssertionError('acceptance condition timed out')
 def launch(name,cmd,child_env=env):
  out=open(work/(name+'.stdout'),'w');err=open(work/(name+'.stderr'),'w');handles.extend([out,err]);proc=subprocess.Popen(cmd,cwd=root,env=child_env,stdout=out,stderr=err);processes.append(proc);return proc
 def ready(name,proc):
  def poll():
   if proc.poll() is not None:raise AssertionError(name+' exited')
   for line in (work/(name+'.stdout')).read_text().splitlines():
    with contextlib.suppress(ValueError):
     v=json.loads(line)
     if v.get('event')=='ready':return v
  return wait(poll)
 def raw(url,body=None):
  req=urllib.request.Request(url,data=None if body is None else json.dumps(body).encode(),headers={'Content-Type':'application/json'})
  with urllib.request.urlopen(req,timeout=10) as r:return json.load(r)
 def container(role,image,port,extra,cmd):
  name='ct-preparation-'+role+'-'+runid;containers.append(name);run(['docker','run','--detach','--name',name,'--label','campustrace.verification='+runid,'-p','127.0.0.1::'+str(port),*extra,image,*cmd]);mapped=run(['docker','port',name,str(port)+'/tcp']).split(':')[-1];return name,mapped
 try:
  run(['go','build','-mod=readonly','-o',str(work/'api'),'./cmd/api'])
  kafka='ct-preparation-kafka-'+runid;containers.append(kafka);brokers=run(['sh',str(root/'scripts/testsupport/kafka.sh'),kafka])
  mysql,mp=container('mysql','mysql:8.4',3306,['--tmpfs','/var/lib/mysql','-e','MYSQL_ROOT_PASSWORD=prep-local-only','-e','MYSQL_DATABASE=ct_prep','-e','MYSQL_USER=prep','-e','MYSQL_PASSWORD=prep-local-only'],[])
  redis,rp=container('redis','redis:7.4-alpine',6379,[],['redis-server','--save','','--appendonly','no'])
  wait(lambda:subprocess.run(['docker','exec',mysql,'mysql','-h127.0.0.1','-uprep','-pprep-local-only','-Dct_prep','-e','SELECT 1'],capture_output=True).returncode==0,90)
  fixture=ready('fixture',launch('fixture',[sys.executable,str(root/'scripts/preparation-model-fixture.py')]))
  fixture_url='http://'+fixture['address']
  with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
  base='http://127.0.0.1:'+str(port)
  with socket.socket() as sock:sock.bind(('127.0.0.1',0));graph_port=sock.getsockname()[1]
  graph_url='http://127.0.0.1:'+str(graph_port)
  graph_env=dict(env,PREPARATION_GO_URL=base,PREPARATION_CHECKPOINT_DB=str(work/'checkpoints.sqlite'))
  graph_cmd=[str(root/'services/preparation/.venv/bin/python'),'-m','uvicorn','app:app','--app-dir',str(root/'services/preparation'),'--host','127.0.0.1','--port',str(graph_port),'--no-access-log']
  graph=launch('langgraph',graph_cmd,graph_env)
  def graph_healthy():
   if graph.poll() is not None:raise AssertionError('LangGraph exited')
   try:return raw(graph_url+'/healthz')
   except (OSError,ValueError):return False
  wait(graph_healthy)

  api_env=dict(env,PREPARATION_GRAPH_URL=graph_url,MYSQL_DSN='prep:prep-local-only@tcp(127.0.0.1:'+mp+')/ct_prep?parseTime=true&loc=UTC',REDIS_ADDR='127.0.0.1:'+rp,KAFKA_BROKERS=brokers,KAFKA_TASK_NAMESPACE='prep:'+runid+':',CAMPUS_REDIS_PREFIX='prep:'+runid+':',JWT_SECRET='preparation-synthetic-secret-at-least-32',HTTP_ADDR='127.0.0.1:'+str(port),LLM_URL=fixture_url+'/v1/chat/completions',LLM_API_KEY='preparation-fixture-key',LLM_MODEL='controlled-preparation')
  api=launch('api',[str(work/'api')],api_env)
  def healthy():
   if api.poll() is not None:raise AssertionError('API exited; inspect owned logs')
   try:return raw(base+'/healthz')
   except (OSError,ValueError):return False
  wait(healthy,60)
  class Client:
   def __init__(self):self.opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
   def request(self,path,body=None,expected=200,method=None):
    req=urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),headers={'Content-Type':'application/json','Origin':base},method=method)
    try:r=self.opener.open(req,timeout=10)
    except urllib.error.HTTPError as e:r=e
    data=r.read();assert r.status==expected,(path,r.status,expected,data.decode());return json.loads(data) if data else None
   def register(self):
    credentials={'email':'prep-'+uuid.uuid4().hex+'@synthetic.invalid','password':'SyntheticPrep2026!'};self.request('/auth/register',credentials);self.request('/auth/login',dict(credentials,web_session=True));return credentials
  c=Client();credentials=c.register()
  observation=c.request('/api/ingest',{'company':'Synthetic Preparation','title':'Go 后端工程师','job_type':'FULL_TIME','locations':['上海'],'external_id':uuid.uuid4().hex,'text':'岗位要求：熟悉 Go 并发、context 取消、MySQL 索引与事务。','fetch_status':'SUCCESS'})
  application=c.request('/api/applications',{'job_id':observation['job_id'],'submitted':True,'resume_version':'synthetic-frozen','resume_text':'合成简历：实现 Go 取消传播、MySQL 事务。'})
  app=application['id'];goal={'application_id':app,'goal':'三天后面试，安排 Go 后端准备计划','minutes':180,'days':3,'other_reviews':True}
  review=c.request('/api/agent/preparation/preview',{'input':goal})
  assert raw(fixture_url+'/fixture/status')['calls']==0
  checks.append('preview_zero_calls')
  req={'input':goal,'confirm':True,'request_key':uuid.uuid4().hex,'review_hash':review['review_hash']}
  c.request('/api/agent/preparation',dict(req,confirm=False),400)
  v=c.request('/api/agent/preparation',req)
  def terminal(id):
   value=c.request('/api/agent/executions/'+id);return value if value['state'] not in ('PENDING','RUNNING') else None
  v=wait(lambda:terminal(v['id']))
  assert v['preparation_agent']['engine']=='langgraph'
  assert v['state']=='COMPLETED' and sum(x['minutes'] for x in v['preparation_agent']['result']['items'])<=180
  calls=raw(fixture_url+'/fixture/status')['calls'];replay=c.request('/api/agent/preparation',req);assert replay['id']==v['id'] and raw(fixture_url+'/fixture/status')['calls']==calls
  checks.extend(['structured_plan_and_literal_sources','start_idempotency','unconfirmed_start_rejected'])
  foreign=Client();foreign.register();foreign.request('/api/agent/executions/'+v['id'],expected=404);foreign.request('/api/agent/preparation/preview',{'input':goal},404)
  checks.append('owner_isolation')
  pending=c.request('/api/agent/preparation/'+v['id']+'/save-preview',{})
  assert not c.request('/api/agent/executions/'+v['id'])['preparation_agent'].get('saved_at')
  first=c.request('/agent/actions/'+pending['action_id']+'/confirm',{'confirm':True});second=c.request('/agent/actions/'+pending['action_id']+'/confirm',{'confirm':True});assert first==second
  receipt_id=pending['action_id'];assert len(receipt_id)==32 and all(ch in '0123456789abcdef' for ch in receipt_id)
  sql=lambda statement:run(['docker','exec',mysql,'mysql','-uprep','-pprep-local-only','-Dct_prep','--default-character-set=utf8mb4','-N','-B','--raw','-e',statement])
  receipt=json.loads(sql("SELECT result FROM action_receipts WHERE id='"+receipt_id+"'"))
  assert receipt==first and receipt['execution_id']==v['id'], {'http':first,'sql':receipt}
  assert sql("SELECT COUNT(*) FROM action_receipts WHERE id='"+receipt_id+"'")=='1'
  assert sql("SELECT JSON_EXTRACT(body,'$.preparation_agent.saved_at') IS NOT NULL FROM agent_executions WHERE id='"+v['id']+"'")=='1'
  (work/'business-receipt.json').write_text(json.dumps({'receipt':receipt,'receipt_rows':1,'saved_at_in_mysql':True},ensure_ascii=False,indent=2)+'\n')
  checks.append('confirmed_save_mysql_state_receipt_and_idempotency')
  if args.playwright_module:
   browser_env=dict(env,PREP_BROWSER_URL=base,PREP_BROWSER_EMAIL=credentials['email'],PREP_BROWSER_PASSWORD=credentials['password'],PREP_BROWSER_APPLICATION=app,PREP_BROWSER_EXECUTION=v['id'],PREP_BROWSER_OUTPUT=str(work),PREP_PLAYWRIGHT_MODULE=args.playwright_module)
   subprocess.run(['node',str(root/'scripts/verify-preparation-browser.cjs')],cwd=root,env=browser_env,check=True)
   checks.append('chromium_plan_feedback_save')
  defense_checks,defense_credentials,defense_app=verify_defense(Client,raw,fixture_url,sql,wait,work)
  checks.extend(defense_checks)
  if args.playwright_module:
   defense_env=dict(env,PREP_BROWSER_URL=base,PREP_BROWSER_EMAIL=defense_credentials['email'],PREP_BROWSER_PASSWORD=defense_credentials['password'],PREP_BROWSER_APPLICATION=defense_app,PREP_BROWSER_OUTPUT=str(work),PREP_PLAYWRIGHT_MODULE=args.playwright_module)
   subprocess.run(['node',str(root/'scripts/verify-project-defense-browser.cjs')],cwd=root,env=defense_env,check=True)
   browser_state=json.loads((work/'project-defense-browser-state.json').read_text());browser_execution=browser_state['execution_id']
   assert len(browser_execution)==32 and all(ch in '0123456789abcdef' for ch in browser_execution)
   assert sql("SELECT JSON_EXTRACT(body,'$.preparation_agent.saved_at') IS NOT NULL FROM agent_executions WHERE id='"+browser_execution+"'")=='1'
   assert sql("SELECT COUNT(*) FROM action_receipts WHERE JSON_UNQUOTE(JSON_EXTRACT(CONVERT(result USING utf8mb4),'$.execution_id'))='"+browser_execution+"'")=='1'
   checks.append('project_defense_browser_reanswer_and_sql_save')
  # Lose a known model response between Go's durable receipt and Python's
  # checkpoint. Resume the pending graph node without paying for it twice.
  outage_review=c.request('/api/agent/preparation/preview',{'input':goal})
  before_outage=raw(fixture_url+'/fixture/status')['calls']
  before_tasks=len(c.request('/api/agent/preparation?application_id='+app))
  graph.terminate();graph.wait(5)
  unavailable=c.request('/api/agent/preparation',{'input':goal,'confirm':True,'request_key':uuid.uuid4().hex,'review_hash':outage_review['review_hash']},503)
  assert unavailable['code']=='PREPARATION_SERVICE_UNAVAILABLE'
  assert raw(fixture_url+'/fixture/status')['calls']==before_outage and len(c.request('/api/agent/preparation?application_id='+app))==before_tasks
  checks.append('service_unavailable_explicit_no_legacy_fallback_or_new_task')
  proxy=ready('callback-proxy',launch('callback-proxy',[sys.executable,str(root/'scripts/preparation-callback-fixture.py')],dict(env,PREP_CALLBACK_TARGET=base)))
  graph=launch('langgraph-known-response',graph_cmd,dict(graph_env,PREPARATION_GO_URL='http://'+proxy['address']));wait(graph_healthy)
  known_client=Client();known_client.register()
  known_observation=known_client.request('/api/ingest',{'company':'Synthetic Known Receipt','title':'Go backend','job_type':'FULL_TIME','external_id':uuid.uuid4().hex,'text':'Go 并发 MySQL 索引','fetch_status':'SUCCESS'})
  known_application=known_client.request('/api/applications',{'job_id':known_observation['job_id'],'submitted':True,'resume_version':'synthetic-known-receipt','resume_text':'Synthetic Go context and MySQL project.'})
  known_goal=dict(goal,application_id=known_application['id'])
  def known_terminal(id):
   value=known_client.request('/api/agent/executions/'+id);return value if value['state'] not in ('PENDING','RUNNING') else None
  known_review=known_client.request('/api/agent/preparation/preview',{'input':known_goal})
  known_req={'input':known_goal,'confirm':True,'request_key':uuid.uuid4().hex,'review_hash':known_review['review_hash']}
  known=known_client.request('/api/agent/preparation',known_req);known=wait(lambda:known_terminal(known['id']))
  assert known['state']=='PAUSED' and known['preparation_agent']['provider_state']=='READY' and known['preparation_agent']['model_calls']==1
  assert known['preparation_agent']['graph_sequence']==1 and known['next_step']==0
  known_client.request('/api/agent/preparation/'+known['id']+'/resume',dict(known_req,request_key=uuid.uuid4().hex))
  known=wait(lambda:known_terminal(known['id']))
  assert known['state']=='COMPLETED' and known['preparation_agent']['model_calls']==2
  checks.append('known_model_receipt_reused_after_lost_callback_response')
  graph.terminate();graph.wait(5)
  graph=launch('langgraph-normal-again',graph_cmd,graph_env);wait(graph_healthy)
  # Missing constraints produces a real LangGraph interrupt. A reviewed child
  # resumes that interrupt with Command, and keeps the new time budget.
  clarify_goal=dict(goal,minutes=0,days=0,goal='缺少准备时间，请先询问')
  clarify_review=c.request('/api/agent/preparation/preview',{'input':clarify_goal})
  clarify_request={'input':clarify_goal,'confirm':True,'request_key':uuid.uuid4().hex,'review_hash':clarify_review['review_hash']}
  question=c.request('/api/agent/preparation',clarify_request);question=wait(lambda:terminal(question['id']))
  assert question['state']=='PAUSED' and question['preparation_agent'].get('question')
  # Wait for the graph interrupt write, rather than merely observing the Go copy.
  time.sleep(.3)
  supplement=dict(goal,parent_id=question['id'],minutes=15,days=1,feedback='只有十五分钟，优先 Go 并发')
  supplement_review=c.request('/api/agent/preparation/preview',{'input':supplement})
  child=c.request('/api/agent/preparation',{'input':supplement,'confirm':True,'request_key':uuid.uuid4().hex,'review_hash':supplement_review['review_hash']})
  child=wait(lambda:terminal(child['id']));(work/'clarification-child.json').write_text(json.dumps(child,ensure_ascii=False,indent=2)+'\n');assert child['state']=='COMPLETED' and sum(x['minutes'] for x in child['preparation_agent']['result']['items'])<=15, {'state':child['state'],'trace':child.get('preparation_agent',{}).get('trace')}
  checks.append('clarification_interrupt_reviewed_child_15_minutes')
  # Cancellation reaches a real in-flight callback and fences late writes.
  raw(fixture_url+'/fixture/mode',{'mode':'hang'})
  cancel_review=c.request('/api/agent/preparation/preview',{'input':goal})
  cancel_task=c.request('/api/agent/preparation',{'input':goal,'confirm':True,'request_key':uuid.uuid4().hex,'review_hash':cancel_review['review_hash']})
  wait(lambda:raw(fixture_url+'/fixture/status')['active']>0)
  c.request('/api/agent/executions/'+cancel_task['id']+'/cancel',{})
  wait(lambda:raw(fixture_url+'/fixture/status')['active']==0)
  assert c.request('/api/agent/executions/'+cancel_task['id'])['state']=='CANCELLED'
  c.request('/api/agent/preparation/'+cancel_task['id']+'/save-preview',{},409)
  raw(fixture_url+'/fixture/mode',{'mode':'normal'})
  checks.append('cancel_propagates_to_provider_and_blocks_save')
  pending_stale=c.request('/api/agent/preparation/'+child['id']+'/save-preview',{})
  c.request('/api/documents/import',{'title':'Synthetic source revision','text':'Go context 协作取消与 MySQL 索引学习资料。'})
  c.request('/agent/actions/'+pending_stale['action_id']+'/confirm',{'confirm':True},409)
  assert not c.request('/api/agent/executions/'+child['id'])['preparation_agent'].get('saved_at')
  assert sql("SELECT COUNT(*) FROM action_receipts WHERE id='"+pending_stale['action_id']+"'")=='0'
  assert sql("SELECT JSON_EXTRACT(body,'$.preparation_agent.saved_at') IS NULL FROM agent_executions WHERE id='"+child['id']+"'")=='1'
  checks.append('source_revision_blocks_final_confirm_no_business_write')
  # Own-process SIGKILL leaves a durable dispatch marker. Expiring only this
  # fixture task's lease is an injected boundary, not a 60-second timing claim.
  recovery=Client();recovery.register()
  recovery_observation=recovery.request('/api/ingest',{'company':'Synthetic Recovery','title':'Go backend recovery','job_type':'FULL_TIME','external_id':uuid.uuid4().hex,'text':'Go 并发与 MySQL 索引。','fetch_status':'SUCCESS'})
  recovery_app=recovery.request('/api/applications',{'job_id':recovery_observation['job_id'],'submitted':True,'resume_version':'synthetic','resume_text':'Synthetic Go worker.'})
  recovery_input={'application_id':recovery_app['id'],'goal':'核对项目答辩回答并安排复习','minutes':15,'days':1,'other_reviews':False,'defense':{'topic':'unknown-recovery','answer':'我还不熟悉中断后的恢复。'}}
  recovery_preview=recovery.request('/api/agent/preparation/preview',{'input':recovery_input})
  recovery_request={'input':recovery_input,'confirm':True,'request_key':uuid.uuid4().hex,'review_hash':recovery_preview['review_hash']}
  raw(fixture_url+'/fixture/mode',{'mode':'hang-after-read'})
  interrupted=recovery.request('/api/agent/preparation',recovery_request)
  def dispatched():
   value=recovery.request('/api/agent/executions/'+interrupted['id']);state=value['preparation_agent']
   return value if state['provider_state']=='DISPATCHED' and state['model_calls']==2 and value['next_step']==1 else None
  before_kill=wait(dispatched)
  assert len(before_kill['grounded_observations'])==1 and before_kill['preparation_agent']['usage']['charged']>0
  # Kill Python only: Go remains alive, cancels the model callback and records UNKNOWN.
  graph.kill();graph.wait();wait(lambda:raw(fixture_url+'/fixture/status')['active']==0)
  def python_stopped():
   value=recovery.request('/api/agent/executions/'+interrupted['id'])
   return value if value['state']=='PAUSED' and value['preparation_agent']['provider_state']=='UNKNOWN' else None
  python_interrupted=wait(python_stopped)
  assert api.poll() is None
  graph=launch('langgraph-restarted',graph_cmd,graph_env);wait(graph_healthy)
  # Inspect the actual persistent LangGraph cursor after Python is dead.
  inspect_code="""import asyncio,json,sys
from langgraph.checkpoint.sqlite.aio import AsyncSqliteSaver
from graph import build_graph
async def main():
 async with AsyncSqliteSaver.from_conn_string(sys.argv[1]) as saver:
  state=await build_graph(saver).aget_state({'configurable':{'thread_id':sys.argv[2]}})
  print(json.dumps({'next':state.next,'values':state.values}))
asyncio.run(main())"""
  checkpoint=json.loads(subprocess.check_output([str(root/'services/preparation/.venv/bin/python'),'-c',inspect_code,str(work/'checkpoints.sqlite'),interrupted['id']],cwd=root/'services/preparation',text=True))
  assert checkpoint['next']==['model'] and checkpoint['values']['view']['sequence']>=2,checkpoint
  assert 'sources' not in checkpoint['values']['view']['preparation'] and 'messages' not in checkpoint['values']['view']['preparation']
  (work/'checkpoint-restart.json').write_text(json.dumps(checkpoint,ensure_ascii=False,indent=2)+'\n')
  checks.append('python_only_sigkill_persistent_checkpoint_reopened')
  # Also retain the historical Go-process boundary, without replaying a model.
  api.kill();api.wait()
  task_id=interrupted['id'];assert len(task_id)==32 and all(c in '0123456789abcdef' for c in task_id)
  run(['docker','exec',mysql,'mysql','-uroot','-pprep-local-only','-Dct_prep','-e',"UPDATE agent_executions SET lease_until=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE id='"+task_id+"'"])
  api=launch('api-restarted',[str(work/'api')],api_env);wait(healthy,60)
  observed=recovery.request('/api/agent/executions/'+task_id);assert observed['can_resume'] and observed['preparation_agent']['provider_state']=='UNKNOWN'
  retry=dict(recovery_request,request_key=uuid.uuid4().hex)
  before_calls=raw(fixture_url+'/fixture/status')['calls']
  recovery.request('/api/agent/preparation/'+task_id+'/resume',retry,409)
  assert raw(fixture_url+'/fixture/status')['calls']==before_calls
  raw(fixture_url+'/fixture/mode',{'mode':'normal'})
  recovery.request('/api/agent/preparation/'+task_id+'/resume',dict(retry,retry_unknown=True))
  def recovered():
   value=recovery.request('/api/agent/executions/'+task_id);return value if value['state']=='COMPLETED' else None
  resumed=wait(recovered)
  assert len(resumed['grounded_observations'])==1 and resumed['preparation_agent']['model_calls']==3
  assert raw(fixture_url+'/fixture/status')['calls']==before_calls+1
  checks.extend(['sigkill_durable_model_dispatch','lease_expiry_injected_not_elapsed','unknown_resume_requires_ack','completed_tool_not_repeated','unknown_budget_reservation_retained'])
  parent_checkpoint=json.loads(subprocess.check_output([str(root/'services/preparation/.venv/bin/python'),'-c',inspect_code,str(work/'checkpoints.sqlite'),question['id']],cwd=root/'services/preparation',text=True))
  assert not parent_checkpoint['next'] and parent_checkpoint['values']['continuation']==child['id']
  (work/'clarification-checkpoint.json').write_text(json.dumps(parent_checkpoint,ensure_ascii=False,indent=2)+'\n')
  checks.append('clarification_command_resumes_parent_to_reviewed_child')
  (work/'recovered.json').write_text(json.dumps(resumed,ensure_ascii=False,indent=2)+'\n')
  (work/'execution.json').write_text(json.dumps(v,ensure_ascii=False,indent=2)+'\n')
  # The expanded suite crosses the real shared 30-calls/minute guard. Wait
  # for its owned Redis window; do not raise or reset production limits.
  rate_key='prep:'+runid+':rate:llm:global'
  ttl=int(run(['docker','exec',redis,'redis-cli','PTTL',rate_key]))
  if ttl>0:
   print('Waiting for the owned model-rate window before memory acceptance',flush=True)
   until=time.monotonic()+ttl/1000+.1
   while time.monotonic()<until:time.sleep(min(.5,max(0,until-time.monotonic())))
  memory_checks,memory_credentials,memory_app=verify_memory(Client,raw,fixture_url,sql,wait,work)
  checks.extend(memory_checks)
  if args.playwright_module:
   memory_env=dict(env,PREP_BROWSER_URL=base,PREP_BROWSER_EMAIL=memory_credentials['email'],PREP_BROWSER_PASSWORD=memory_credentials['password'],PREP_BROWSER_APPLICATION=memory_app,PREP_BROWSER_OUTPUT=str(work),PREP_PLAYWRIGHT_MODULE=args.playwright_module)
   subprocess.run(['node',str(root/'scripts/verify-defense-memory-browser.cjs')],cwd=root,env=memory_env,check=True)
   checks.append('defense_memory_browser_confirm_recall_correct_forget')
  final=raw(fixture_url+'/fixture/status');assert final['active']==0
  (work/'result.json').write_text(json.dumps({'status':'PASS','evidence':'controlled-response-Go-Python-LangGraph','external_model_calls':0,'controlled_http_calls':final['calls'],'checks':checks,'real_model_effect':'NOT_VALIDATED','browser':'PASS' if args.playwright_module else 'NOT_RUN'},ensure_ascii=False,indent=2)+'\n')
  print((work/'result.json').read_text())
 finally:
  for proc in reversed(processes):
   if proc.poll() is None:
    proc.terminate()
    try:proc.wait(timeout=5)
    except subprocess.TimeoutExpired:proc.kill();proc.wait()
  for h in handles:h.close()
  for name in reversed(containers):subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
if __name__=='__main__':main()
