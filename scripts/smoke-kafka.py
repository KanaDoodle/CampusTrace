#!/usr/bin/env python3
"""Independent real consumers, broker outage, replay, and DLQ on owned data."""
import json
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

work,evidence,project=Path(sys.argv[1]),Path(sys.argv[2]),sys.argv[3]
assert project.startswith('ct-observer-test-')
compose=['docker','compose','--project-name',project,'--file',str(work/'compose.json')]
def address(service,port):
 value=subprocess.check_output(compose+['port',service,str(port)],text=True).strip();assert value.startswith('127.0.0.1:');return 'http://'+value
urls={'api':address('api',8080),'tempo':address('tempo',3200),'prometheus':address('prometheus',9090),'grafana':address('grafana',3000)}
token=''
def request(service,path,body=None):
 headers={'Accept':'application/json'}
 if token and service=='api':headers['Authorization']='Bearer '+token
 if body is not None:headers['Content-Type']='application/json'
 with urllib.request.urlopen(urllib.request.Request(urls[service]+path,data=None if body is None else json.dumps(body).encode(),headers=headers),timeout=8) as response:
  return json.loads(response.read()),response.headers

def eventually(label,check,seconds=90):
 until=time.monotonic()+seconds;last=''
 while time.monotonic()<until:
  try:
   value=check()
   if value:return value
  except (urllib.error.URLError,ValueError,TimeoutError,KeyError,AssertionError) as error:last=type(error).__name__+': '+str(error)
  time.sleep(.3)
 raise AssertionError(label+': '+last)
def scalar(query):
 return int(subprocess.check_output(compose+['exec','-T','mysql','sh','-c','MYSQL_PWD="$MYSQL_PASSWORD" mysql --batch --skip-column-names -u"$MYSQL_USER" "$MYSQL_DATABASE" -e "$1"','sh',query],text=True).strip())
def inbox():return request('api','/api/notifications')[0]
def notified(job):
 assert len(job)==32 and all(c in '0123456789abcdef' for c in job)
 latest=scalar("SELECT version FROM search_documents WHERE job_id='"+job+"'")
 applied=scalar("SELECT COALESCE(MAX(version),0) FROM job_notification_versions WHERE job_id='"+job+"'")
 return applied==latest and any(row['entity_id']==job for row in inbox())
def query(marker,mode):
 result,_=request('api','/api/jobs/search',{'text':marker});assert result['mode']==mode, result;return result['job_ids']
def ingest(marker):
 observation,headers=request('api','/api/ingest',{'company':'Synthetic Kafka','title':'岗位事件验收','job_type':'FULL_TIME','locations':['上海'],'external_id':uuid.uuid4().hex,'fetch_status':'SUCCESS','text':'Golang Redis '+marker})
 def analyzed():
  detail,_=request('api','/api/jobs/'+observation['job_id']);return any(row['id']==observation['id'] and row['extraction_status']=='COMPLETE' for row in detail['observations'])
 eventually('Redis/KanaRPC analysis completion',analyzed)
 eventually('existing Redis assessment completion',lambda:scalar("SELECT COUNT(*) FROM jobs WHERE id='"+observation['job_id']+"' AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.current_status'))='NEEDS_VERIFICATION'")==1)
 return observation['job_id'],headers['X-Trace-ID']
def group_lag(group):
 raw=subprocess.check_output(compose+['exec','-T','kafka','/opt/kafka/bin/kafka-consumer-groups.sh','--bootstrap-server','kafka:9092','--describe','--group',group],text=True,stderr=subprocess.DEVNULL)
 lines=[line.split() for line in raw.splitlines() if line.strip()];header=next((line for line in lines if 'CURRENT-OFFSET' in line),None)
 if not header:return None
 total=0
 for row in lines:
  if row[0]!=group:continue
  value=row[header.index('LAG')]
  if value=='-':
   if row[header.index('LOG-END-OFFSET')]!='0':return None
  else:total+=int(value)
 return total
def caught_up():
 return scalar('SELECT COUNT(*) FROM job_event_outbox WHERE published_at IS NULL')==0 and group_lag('campustrace-search-v1')==0 and group_lag('campustrace-notify-v1')==0

# Quiesce earlier acceptance accounts before selecting a trace's first record.
eventually('initial consumer drain',caught_up)
account={'email':'kafka-'+uuid.uuid4().hex+'@synthetic.invalid','password':'Synthetic2026!'}
request('api','/auth/register',account);login,_=request('api','/auth/login',account);token=login['token']
first,trace=ingest('kafkafirst never-export-kafka-marker')
eventually('first event applied',lambda:query('kafkafirst','fulltext')==[first] and notified(first))
notification_count=len(inbox())
subprocess.run(compose+['stop','-t','5','search'],check=True,stdout=subprocess.DEVNULL)
second,_=ingest('kafkasecond')
assert query('kafkasecond','basic')==[second]
eventually('notify independent of stopped Search',lambda:notified(second))
assert scalar('SELECT COUNT(*) FROM search_outbox')>0
notification_count=len(inbox())
subprocess.run(compose+['start','search'],check=True,stdout=subprocess.DEVNULL)
eventually('Search resumes at its offsets',lambda:query('kafkasecond','fulltext')==[second])
subprocess.run(compose+['stop','-t','5','notify'],check=True,stdout=subprocess.DEVNULL)
third,_=ingest('kafkathird')
eventually('Search independent of stopped notify',lambda:query('kafkathird','fulltext')==[third])
assert len(inbox())==notification_count
subprocess.run(compose+['start','notify'],check=True,stdout=subprocess.DEVNULL)
eventually('notification consumer resumes',lambda:notified(third))
notification_count=len(inbox())
subprocess.run(compose+['pause','kafka'],check=True,stdout=subprocess.DEVNULL)
try:
 fourth,_=ingest('kafkafourth')
 assert query('kafkafourth','basic')==[fourth]
 assert scalar('SELECT COUNT(*) FROM job_event_outbox WHERE published_at IS NULL')>0
 assert len(inbox())==notification_count
finally:subprocess.run(compose+['unpause','kafka'],check=True,stdout=subprocess.DEVNULL)
eventually('broker recovery and durable event drain',lambda:query('kafkafourth','fulltext')==[fourth] and notified(fourth) and caught_up())
notification_count=len(inbox())
subprocess.run(compose+['exec','-T','worker','/app/events','replay','--from-version','0','--limit','10000'],check=True,stdout=subprocess.DEVNULL)
eventually('replay consumers caught up',caught_up)
assert len(inbox())==notification_count
# Poison is quarantined with digest/offset diagnostics, never raw JD text.
subprocess.run(compose+['exec','-T','kafka','/opt/kafka/bin/kafka-console-producer.sh','--bootstrap-server','kafka:9092','--topic','campustrace.jobs.v1','--property','parse.key=true','--property','key.separator=|'],input='invalid|{"schema":999,"private":"poison-must-not-be-logged"}\n',text=True,check=True,stdout=subprocess.DEVNULL)
eventually('poison was not allowed to block both consumers',caught_up)
dlq=subprocess.check_output(compose+['exec','-T','kafka','/opt/kafka/bin/kafka-console-consumer.sh','--bootstrap-server','kafka:9092','--topic','campustrace.jobs.dlq.v1','--from-beginning','--max-messages','2','--timeout-ms','15000'],text=True,stderr=subprocess.DEVNULL)
assert 'poison-must-not-be-logged' not in dlq
assert len([line for line in dlq.splitlines() if json.loads(line)['reason']=='INVALID_EVENT'])==2
(evidence/'dlq-diagnostics.jsonl').write_text(dlq)
assert len(inbox())==notification_count
# Trace propagation follows stored context and Kafka record headers.
def trace_ready():
 data,_=request('tempo','/api/traces/'+trace);spans=[]
 for batch in data.get('batches',data.get('resourceSpans',[])):
  service=next((a['value'].get('stringValue') for a in batch.get('resource',{}).get('attributes',[]) if a['key']=='service.name'),'')
  for scope in batch.get('scopeSpans',batch.get('instrumentationLibrarySpans',[])):
   spans.extend((service,span) for span in scope.get('spans',[]))
 if not {'campustrace-api','campustrace-worker','campustrace-search','campustrace-notify'}<={service for service,_ in spans}:return False
 indexed={span['spanId']:(service,span) for service,span in spans}
 consumers=[(service,span) for service,span in spans if span['name']=='job.event.consume']
 if not {'campustrace-search','campustrace-notify'}<={service for service,_ in consumers}:return False
 assert all(indexed.get(span.get('parentSpanId'),('',{}))[1].get('name')=='kafka.publish' for _,span in consumers)
 raw=json.dumps(data);assert 'never-export-kafka-marker' not in raw and 'Bearer ' not in raw
 (evidence/'kafka-trace.json').write_text(json.dumps(data,indent=2))
 return {'trace_id':trace,'spans':len(spans),'services':sorted({service for service,_ in spans})}
trace_result=eventually('Kafka producer/consumer trace',trace_ready)
dashboard,_=request('grafana','/api/dashboards/uid/campustrace-kafka');assert len(dashboard['dashboard']['panels'])==9
results={'independent_search_and_notify_groups':True,'broker_ack_not_index_ack':True,'search_restart_catchup':True,'notify_restart_catchup':True,'broker_outage_core_crud_and_redis_analysis_continued':True,'durable_publish_recovered':True,'sql_receipts_deduplicate_replay':True,'poison_quarantine_without_raw_payload':True,'distinct_job_notifications':notification_count,'kafka_dashboard_panels':9,'trace':trace_result,'external_model_calls':0,'limits':'Single-host synthetic acceptance; single Kafka broker, no cluster HA or production throughput claim.'}
(evidence/'kafka-acceptance.json').write_text(json.dumps(results,indent=2));print(json.dumps(results,ensure_ascii=False))
