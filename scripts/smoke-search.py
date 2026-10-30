#!/usr/bin/env python3
"""Real separate Core, Search, Analysis processes; exclusively synthetic data."""
import json
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

work, evidence, project = Path(sys.argv[1]), Path(sys.argv[2]), sys.argv[3]
assert project.startswith('ct-observer-test-')
compose=['docker','compose','--project-name',project,'--file',str(work/'compose.json')]
address=lambda service,port:'http://'+subprocess.check_output(compose+['port',service,str(port)],text=True).strip()
urls={'api':address('api',8080),'tempo':address('tempo',3200),'prometheus':address('prometheus',9090),'grafana':address('grafana',3000)}
assert all(url.startswith('http://127.0.0.1:') for url in urls.values())
token=''
def request(service,path,body=None):
 headers={'Accept':'application/json'}
 if service=='api' and token:headers['Authorization']='Bearer '+token
 if body is not None:headers['Content-Type']='application/json'
 with urllib.request.urlopen(urllib.request.Request(urls[service]+path, data=None if body is None else json.dumps(body).encode(),headers=headers),timeout=5) as response:
  raw=response.read();return json.loads(raw),response.headers

def eventually(label,fn,seconds=60):
 until=time.monotonic()+seconds;last=''
 while time.monotonic()<until:
  try:
   value=fn()
   if value:return value
  except (urllib.error.URLError,TimeoutError,ValueError,KeyError,AssertionError) as error:last=str(error)
  time.sleep(.3)
 raise AssertionError(label+': '+last)
def identity(service):
 return subprocess.check_output(compose+['ps','-q',service],text=True).strip()
def started(service):
 return subprocess.check_output(['docker','inspect','--format','{{.State.StartedAt}}',identity(service)],text=True).strip()
def search(word,mode=None):
 result,headers=request('api','/api/jobs/search',{'text':word})
 if mode:assert result['mode']==mode,result
 return result,headers

def ingest(marker):
 observation,headers=request('api','/api/ingest',{'company':'Synthetic Search','title':'岗位内容检索验收','job_type':'FULL_TIME','locations':['上海'],'external_id':uuid.uuid4().hex,'text':'C++ Redis 分布式后端开发。 '+marker,'fetch_status':'SUCCESS'})
 def analyzed():
  detail,_=request('api','/api/jobs/'+observation['job_id'])
  return any(o['id']==observation['id'] and o['extraction_status']=='COMPLETE' for o in detail['observations'])
 eventually('existing Analysis still completes',analyzed)
 return observation,headers['X-Trace-ID']

account={'email':'search-'+uuid.uuid4().hex+'@synthetic.invalid','password':'Synthetic2026!'}
request('api','/auth/register',account);login,_=request('api','/auth/login',account);token=login['token']
first,trace_id=ingest('search-never-export-private-marker')
eventually('initial fulltext catchup',lambda:search('Redis','fulltext')[0]['job_ids']==[first['job_id']])
# Read capability is wired into the actual browser entry points.
cap,_=request('api','/api/profile/resume/capabilities');assert cap['job_search']
# Replacing a Search process must not restart Core or either Analysis process.
unchanged={name:started(name) for name in ('api','worker','analysis-1','analysis-2')}
subprocess.run(compose+['stop','-t','5','search'],check=True,stdout=subprocess.DEVNULL)
fallback,_=search('Redis','basic');assert fallback['job_ids']==[first['job_id']]
second,_=ingest('search-offline-synthetic')
fallback,_=search('search-offline-synthetic','basic');assert fallback['job_ids']==[second['job_id']]
subprocess.run(compose+['up','-d','--no-deps','--force-recreate','search'],check=True,stdout=subprocess.DEVNULL)
eventually('Search replacement catches up',lambda:search('search-offline-synthetic','fulltext')[0]['job_ids']==[second['job_id']])
assert {name:started(name) for name in unchanged}==unchanged
# The index is an independent failure domain. Restarting its disposable tmpfs
# also loses the concrete index, so Core must notice a changed UUID and replay.
search_started=started('search')
subprocess.run(compose+['stop','-t','10','opensearch'],check=True,stdout=subprocess.DEVNULL)
search('Redis','basic')
third,_=ingest('index-offline-synthetic')
fallback,_=search('index-offline-synthetic','basic');assert fallback['job_ids']==[third['job_id']]
subprocess.run(compose+['start','opensearch'],check=True,stdout=subprocess.DEVNULL)
eventually('index restart and loss recovery',lambda:len(search('Redis','fulltext')[0]['job_ids'])==3,seconds=100)
assert started('search')==search_started
assert {name:started(name) for name in unchanged}==unchanged
# Admin index replacement is followed by Core replay, without business writes.
subprocess.run(compose+['exec','-T','search','/app/search','rebuild'],check=True,stdout=subprocess.DEVNULL)
eventually('rebuild catches up',lambda:len(search('Redis','fulltext')[0]['job_ids'])==3)
# The separate Search image has neither database credentials nor Core's network.
config=json.loads((work/'compose.json').read_text());search_config=config['services']['search']
assert not any(key.startswith(('MYSQL','REDIS','LLM','JWT','ETCD')) for key in search_config['environment'])
assert 'default' not in search_config['networks']
# Query trace links Core -> Search HTTP -> index operation.
_,headers=search('Redis','fulltext');query_trace=headers['X-Trace-ID']
def check_trace(identifier,required):
 data,_=request('tempo','/api/traces/'+identifier);spans=[]
 for batch in data.get('batches',data.get('resourceSpans',[])):
  attrs={item['key']:item.get('value',{}).get('stringValue') for item in batch.get('resource',{}).get('attributes',[])}
  for scope in batch.get('scopeSpans',batch.get('instrumentationLibrarySpans',[])):
   spans.extend((attrs.get('service.name'),span) for span in scope.get('spans',[]))
 if not required<={span['name'] for _,span in spans}:return False
 assert {'campustrace-api','campustrace-search'}<={service for service,_ in spans}
 by_id={span['spanId']:(service,span) for service,span in spans}
 search_server=next(span for service,span in spans if service=='campustrace-search' and span['name']=='POST /v1/query')
 assert by_id[search_server['parentSpanId']][0]=='campustrace-api'
 assert by_id[search_server['parentSpanId']][1]['name']=='search.http'
 raw=json.dumps(data);assert 'search-never-export-private-marker' not in raw and 'Bearer ' not in raw
 (evidence/('search-trace-'+identifier+'.json')).write_text(json.dumps(data,indent=2))
 return {'trace_id':identifier,'spans':len(spans),'services':sorted({service for service,_ in spans})}
trace=eventually('Search trace',lambda:check_trace(query_trace,{'POST /api/jobs/search','search.http','POST /v1/query','search.index'}))
def prometheus_query(expr):
 data,_=request('prometheus','/api/v1/query?'+urllib.parse.urlencode({'query':expr}));return data['data']['result']
eventually('Search scrape',lambda:any(row['value'][1]=='1' for row in prometheus_query('up{job="campustrace-search"}')))
eventually('backlog metric',lambda:any(row['value'][1]=='0' for row in prometheus_query('campustrace_search_pending')))
dashboard,_=request('grafana','/api/dashboards/uid/campustrace-search');assert len(dashboard['dashboard']['panels'])==9
result={'separate_search_image':True,'independent_search_replacement':True,'core_and_analysis_kept_running':True,'outage_fallback_and_write':True,'recovered_outbox':True,'rebuild_replay':True,'index_outage_and_loss_recovered':True,'database_network_isolated':True,'search_dashboard_panels':9,'query_trace':trace}
(evidence/'search-acceptance.json').write_text(json.dumps(result,indent=2));print(json.dumps(result,ensure_ascii=False))
